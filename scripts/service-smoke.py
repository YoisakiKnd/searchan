#!/usr/bin/env python3
"""Black-box native service smoke test; all upstreams are local fixtures.

--systemd is restricted to the disposable GitHub runner's CI installation.
The local mode launches only the explicitly supplied binary and private state.
"""
import argparse
import collections
import email.parser
import email.policy
import hashlib
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import stat
import struct
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error


class Fixtures:
    def __init__(self, out):
        self.out = out
        self.lock = threading.Lock()
        self.release = threading.Event()
        self.release.set()
        self.blocked = 0
        self.research_release = threading.Event()
        self.research_release.set()
        self.research_blocked = 0
        self.recovery_notices = collections.Counter()
        self.images = collections.Counter()
        self.accepted = 0
        self.statuses = 0
        self.calls = collections.Counter()
        self.errors = []

    def reply(self, path, data):
        with self.lock:
            self.calls[path] += 1
        if path == "/search":
            query = data["query"]
            paragraph = "Atlas cancellation signals workers; stopping requires cooperative checks. "
            return {"results": [{"title": "Atlas cancellation contract", "url": f"https://fixture.example/atlas/cancellation/{i}", "text": f"Atlas source {i}. {query}.\n\n" + paragraph * 800} for i in range(3)]}
        if path == "/model":
            prompt = data["messages"][0]["content"]
            source = json.loads(data["messages"][-1]["content"])
            if "制定网页搜索计划" in prompt:
                answer = {"intent": "fact", "queries": [], "aspects": ["cancellation contract"]}
            elif "candidates" in source:
                answer = {"answers_query": True, "missing": [], "queries": [], "read_ids": []}
            elif "answer" in source:
                answer = {"answers_query": True, "unsupported": [], "missing": []}
            else:
                with self.lock:
                    if not self.research_release.is_set():
                        self.research_blocked += 1
                assert self.research_release.wait(30), "research response gate expired"
                answer = {"answers_query": True, "title": "Atlas cancellation", "lead": {"text": "Atlas cancellation signals workers; stopping requires cooperative checks.", "evidence_ids": [source["evidence"][0]["id"]]}, "sections": [], "uncertainties": []}
            return {"choices": [{"message": {"content": json.dumps(answer)}, "finish_reason": "stop"}]}
        if path.endswith("/sendMessage"):
            with self.lock:
                self.accepted += data["text"].startswith("开始搜索。")
                self.statuses += "发送结果待核对" in data["text"]
                if data["text"].startswith("服务中断，本次搜索未能完成"):
                    assert data["message_thread_id"] == 5
                    reply = data["reply_parameters"]
                    self.recovery_notices[(data["chat_id"], reply["message_id"])] += 1
            return {"ok": True, "result": {"message_id": self.calls[path] + 100}}
        if path.endswith("/sendPhoto"):
            png = data["photo"]
            assert png.startswith(b"\x89PNG\r\n\x1a\n") and min(struct.unpack(">II", png[16:24])) > 500
            assert "https://fixture.example/" in data["caption"]
            assert data["message_thread_id"] == "5"
            reply = json.loads(data["reply_parameters"])
            key = (data["chat_id"], reply["message_id"])
            with self.lock:
                self.images[key] += 1
                receipt = sum(self.images.values()) + 200
                if not self.release.is_set():
                    self.blocked += 1
                if sum(self.images.values()) == 1:
                    (self.out / "uploaded.png").write_bytes(png)
            assert self.release.wait(30), "fixture response gate expired"
            return {"ok": True, "result": {"message_id": receipt}}
        raise AssertionError("unexpected fixture endpoint")


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        try:
            length = int(self.headers.get("Content-Length", "0"))
            assert 0 < length <= 16 << 20, "fixture request exceeds bound"
            raw = self.rfile.read(length)
            content_type = self.headers.get("Content-Type", "")
            if content_type.startswith("multipart/form-data"):
                message = email.parser.BytesParser(policy=email.policy.default).parsebytes(("Content-Type: " + content_type + "\r\nMIME-Version: 1.0\r\n\r\n").encode() + raw)
                data = {}
                for part in message.iter_parts():
                    name = part.get_param("name", header="content-disposition")
                    payload = part.get_payload(decode=True)
                    data[name] = payload if name == "photo" else payload.decode()
            else:
                data = json.loads(raw)
            result = self.server.fixtures.reply(self.path, data)
            response = json.dumps(result).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response)))
            self.end_headers()
            self.wfile.write(response)
        except (BrokenPipeError, ConnectionResetError):
            pass  # Expected after the service dies with a response outstanding.
        except Exception as exc:
            with self.server.fixtures.lock:
                self.server.fixtures.errors.append(type(exc).__name__ + ": " + str(exc))
            self.send_error(500)


def systemctl(*args):
    return subprocess.check_output(["systemctl", *args, "searchan"], text=True).strip()


def require_ci_systemd():
    if not (sys.platform == "linux" and os.geteuid() == 0 and os.environ.get("GITHUB_ACTIONS") == "true"):
        raise RuntimeError("systemd mode requires the disposable CI runner")


class Service:
    def __init__(self, args, env, out):
        self.args, self.env, self.out = args, env, out
        self.process = None
        self.log = (out / "service.log").open("wb")
        self.previous = None
        self.configured = False
        self.samples = []
        self.restart_baseline = None
        if args.systemd:
            require_ci_systemd()
            self.config = Path("/etc/searchan.env")
            self.previous = self.config.read_bytes()
            if b"MODEL_NAME=ci-model\n" not in self.previous:
                raise RuntimeError("refusing to reconfigure a non-CI installation")
            self.mode = stat.S_IMODE(self.config.stat().st_mode)
            self.data = Path("/var/lib/searchan/tasks")
            if list(self.data.glob("*/task.json")):
                raise RuntimeError("CI service must have no existing tasks")
        else:
            self.data = Path(env["SEARCHAN_DATA_DIR"]) / "tasks"

    def start(self):
        if self.args.systemd:
            if not self.configured:
                systemctl("stop")
                self.configured = True
                self.config.write_text("".join(k + "=" + v + "\n" for k, v in self.env.items()))
                self.config.chmod(self.mode)
            systemctl("start")
            self.restart_baseline = int(systemctl("show", "-p", "NRestarts", "--value"))
        else:
            self.process = subprocess.Popen([str(Path(self.args.binary).resolve()), "serve"], env={"PATH": os.environ["PATH"], "GOMEMLIMIT": "96MiB", **self.env}, stdout=self.log, stderr=self.log)

    def kill_and_restart(self):
        self.sample()
        if self.args.systemd:
            pid = int(systemctl("show", "-p", "MainPID", "--value"))
            assert pid > 1
            os.kill(pid, signal.SIGKILL)
            stop_at = time.monotonic() + 5
            while Path("/proc", str(pid)).exists():
                if time.monotonic() >= stop_at:
                    raise RuntimeError("killed service did not exit before releasing the fixture response")
                time.sleep(0.05)
        else:
            self.process.kill()
            self.process.wait(timeout=5)
            self.start()

    def sample(self):
        if not self.args.systemd:
            return
        control = systemctl("show", "-p", "ControlGroup", "--value")
        if not control:
            return  # The manager can remove it while waiting to restart.
        assert control.startswith("/system.slice/searchan.service"), "unexpected service cgroup"
        root = Path("/sys/fs/cgroup") / control.lstrip("/")
        if not (root / "memory.peak").exists():
            return  # Restart may temporarily remove the cgroup; final sample required.
        try:
            events = dict(line.split() for line in (root / "memory.events").read_text().splitlines())
            sample = {"memory_peak_bytes": int((root / "memory.peak").read_text()), "memory_max_bytes": int((root / "memory.max").read_text()), "tasks_max": int((root / "pids.max").read_text()), "cpu_max": (root / "cpu.max").read_text().strip(), "oom": int(events["oom"]), "oom_kill": int(events["oom_kill"])}
        except FileNotFoundError:
            return  # A sampled old cgroup can disappear during the restart.
        assert sample["memory_max_bytes"] == 384 * 1024 * 1024 and sample["tasks_max"] == 96
        quota, period = map(int, sample["cpu_max"].split())
        assert quota == period * 2 and sample["oom"] == sample["oom_kill"] == 0
        self.samples.append(sample)

    def close(self):
        if self.args.systemd:
            try:
                systemctl("stop")
            finally:
                if self.configured:
                    self.config.write_bytes(self.previous)
                    self.config.chmod(self.mode)
        elif self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=50)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.log.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--systemd", action="store_true")
    parser.add_argument("--binary")
    parser.add_argument("--typst")
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    assert args.systemd or (args.binary and args.typst), "local mode requires explicit binary and Typst"
    if args.systemd:
        require_ci_systemd()
    out = Path(args.out).resolve()
    out.mkdir(mode=0o700)  # Never overwrite a previous acceptance run.
    fixture = Fixtures(out)
    upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    upstream.daemon_threads = True
    upstream.fixtures = fixture
    thread = threading.Thread(target=upstream.serve_forever, daemon=True)
    thread.start()
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    base = "http://127.0.0.1:" + str(upstream.server_port)
    env = {"SEARCHAN_LISTEN": "127.0.0.1:" + str(port), "SEARCHAN_PROVIDERS": "exa", "EXA_API_KEY": "local-fixture", "EXA_ENDPOINT": base + "/search", "MODEL_ENDPOINT": base + "/model", "MODEL_NAME": "local-fixture", "SEARCHAN_MODEL_PLAN": "true", "SEARCHAN_MODEL_VERIFY": "true", "SEARCHAN_WORKERS": "2", "SEARCHAN_QUEUE_SIZE": "32", "TELEGRAM_BOT_TOKEN": "local-fixture", "TELEGRAM_BOT_USERNAME": "ci_bot", "TELEGRAM_WEBHOOK_SECRET": "ci_fixture_secret_01234567890123456789", "TELEGRAM_API_ENDPOINT": base, "SEARCHAN_ALLOWED_CHATS": "telegram:100,telegram:101,telegram:102,telegram:103", "SEARCHAN_DATA_DIR": str(out / "state") if not args.systemd else "/var/lib/searchan"}
    if not args.systemd:
        env["TYPST_BIN"] = str(Path(args.typst).resolve())
    service = None
    deadline = time.monotonic() + 120
    url = "http://127.0.0.1:" + str(port)
    started = time.monotonic()
    try:
        service = Service(args, env, out)
        service.start()

        def wait(predicate, reason, allow_interrupted=False):
            while time.monotonic() < deadline:
                assert not fixture.errors, fixture.errors
                assert not any(r["state"] == "failed" and not (allow_interrupted and r.get("error") == "service interrupted while processing; submit a new request") for r in records()), "service task failed; inspect service log and task state"
                if predicate():
                    return
                service.sample()
                time.sleep(0.1)
            raise AssertionError(reason)

        def ready():
            try:
                with urllib.request.urlopen(url + "/readyz", timeout=1) as response:
                    return response.status == 200
            except (OSError, urllib.error.URLError):
                return False

        def all_records():
            return [json.loads(p.read_text()) for p in service.data.glob("*/task.json")]

        def records():
            return [r for r in all_records() if r["query"]]

        def submit(i, text=None):
            question = text or ("/search " if i % 2 == 0 else "@ci_bot ") + "Atlas cancellation contract task " + str(i)
            body = {"update_id": 1000 + i, "message": {"message_id": 1000 + i, "message_thread_id": 5, "text": question, "entities": [{"type": "mention", "offset": 0, "length": 7}] if question.startswith("@ci_bot") else [], "from": {"id": i + 1}, "chat": {"id": 100 + i % 4, "type": "supergroup"}}}
            request = urllib.request.Request(url + "/webhooks/telegram", data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "X-Telegram-Bot-Api-Secret-Token": env["TELEGRAM_WEBHOOK_SECRET"]})
            for _ in range(2):  # Duplicate webhook must not duplicate the task/delivery.
                with urllib.request.urlopen(request, timeout=5) as response:
                    assert response.status == 200

        wait(ready, "service did not become ready")
        for i in range(8):
            submit(i)
        wait(lambda: len(records()) == 8 and all(r["state"] == "completed" for r in records()), "normal batch did not complete")
        service.sample()
        normal_ms = round((time.monotonic() - started) * 1000)
        fixture.release.clear()
        for i in range(8, 12):
            submit(i)
        wait(lambda: fixture.blocked == 2 and sum(r["state"] == "sending" for r in records()) == 2, "two deliveries did not reach the crash barrier")
        service.kill_and_restart()
        fixture.release.set()
        wait(ready, "service did not recover")
        wait(lambda: len(records()) == 12 and sum(r["state"] == "completed" for r in records()) == 10 and sum(r["state"] == "uncertain" for r in records()) == 2, "restart lost queued work or repeated an ambiguous send")
        service.sample()
        fixture.research_release.clear()
        for i in range(12, 16):
            submit(i)
        wait(lambda: fixture.research_blocked == 2 and sum(r["state"] == "running" for r in records()) == 2, "two research tasks did not reach the crash barrier")
        service.kill_and_restart()
        fixture.research_release.set()
        wait(ready, "service did not recover interrupted research", allow_interrupted=True)
        wait(lambda: len(records()) == 16 and sum(r["state"] == "completed" for r in records()) == 12 and sum(r["state"] == "failed" for r in records()) == 2 and len(fixture.recovery_notices) == 2 and all(r["state"] == "completed" for r in all_records() if not r["query"]), "interrupted research was repeated, not notified, or queued work was lost", allow_interrupted=True)
        service.sample()
        final = records()
        assert fixture.accepted == 16 and len(fixture.images) == 14 and all(n == 1 for n in fixture.images.values())
        assert len(fixture.recovery_notices) == 2 and all(n == 1 for n in fixture.recovery_notices.values())
        assert fixture.calls["/model"] == 62 and fixture.calls["/search"] == 16, "restart repeated research requests"
        for record in final:
            if record["state"] == "failed":
                assert not record.get("output") and not record.get("recovery_notice") and len(record["deliveries"]) == 1
                continue
            assert record["output"]["stats"]["model_calls"] == 4 and len(record["artifacts"]) == 1
            assert record["output"]["stats"]["reader_calls"] == record["output"]["stats"]["fallback_reads"] == 0, "fixture result caused a public Reader request"
            artifact = record["artifacts"][0]
            assert Path(artifact["path"]).read_bytes().startswith(b"\x89PNG\r\n\x1a\n")
            assert len(record["deliveries"]) == 2
        if args.systemd:
            assert service.samples, "cgroup memory peak was not available"
            assert int(systemctl("show", "-p", "NRestarts", "--value")) == service.restart_baseline + 2, "missing deliberate restart or additional unexpected restarts"
        report = {"mode": "native-systemd-local-fixtures" if args.systemd else "native-local-process-fixtures", "tasks": 16, "normal_completed": 8, "normal_batch_ms": normal_ms, "after_restart_completed": 12, "after_restart_uncertain": 2, "after_restart_failed": 2, "mock_recovery_notices": sum(fixture.recovery_notices.values()), "mock_images": sum(fixture.images.values()), "mock_accepted": fixture.accepted, "duplicate_webhooks": 16, "model_adapter_requests": fixture.calls["/model"], "search_adapter_requests": fixture.calls["/search"], "actual_model_search_platform_calls": 0, "cgroup": service.samples[-1] if service.samples else None, "cgroup_max_observed_peak_bytes": max((s["memory_peak_bytes"] for s in service.samples), default=None), "elapsed_ms": round((time.monotonic() - started) * 1000)}
        (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        (out / "tasks.json").write_text(json.dumps(final, indent=2) + "\n")
        print(json.dumps(report))
    except Exception as exc:
        (out / "failure.json").write_text(json.dumps({"error": str(exc), "fixture_errors": fixture.errors}, indent=2) + "\n")
        raise
    finally:
        fixture.release.set()
        fixture.research_release.set()
        if service is not None:
            service.close()
        upstream.shutdown()
        upstream.server_close()


if __name__ == "__main__":
    main()

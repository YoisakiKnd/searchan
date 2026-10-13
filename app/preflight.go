package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"searchan/demo"
	"searchan/internal/fsutil"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Preflight struct {
	Ready  bool    `json:"ready"`
	Scope  string  `json:"scope"`
	Checks []Check `json:"checks"`
}

// Preflight performs local configuration, durable-write and actual font/render
// checks. Online checks are opt-in and only read bot identity/webhook state.
func (r *Runtime) Preflight(ctx context.Context, strict, online bool) Preflight {
	report := Preflight{Ready: true, Scope: "local"}
	if online {
		report.Scope = "local-and-telegram-readonly"
	}
	add := func(name, status, detail string) {
		report.Checks = append(report.Checks, Check{name, status, detail})
		if status == "fail" {
			report.Ready = false
		}
	}
	if len(r.Senders) == 0 {
		add("platform", "fail", "configure a bot before serving")
	} else {
		add("platform", "pass", "bot configured; live credentials require online verification")
	}
	if err := r.validateAccess(); err != nil {
		add("access", "fail", err.Error())
	} else {
		add("access", "pass", "explicit access policy configured")
	}
	if r.Telegram != nil {
		if err := r.Telegram.ValidateSecret(); err != nil {
			add("telegram-secret", "fail", err.Error())
		} else {
			add("telegram-secret", "pass", "secret format valid")
		}
		if r.Telegram.Username == "" {
			add("telegram-username", "warn", "set TELEGRAM_BOT_USERNAME for /command@bot in groups")
		}
	}
	if r.OneBot != nil {
		if err := r.OneBot.Validate(); err != nil {
			add("onebot", "fail", err.Error())
		} else {
			add("onebot", "pass", "HTTP API, signed webhook and bot identity configured; live gateway not verified")
		}
	}
	if r.Engine.HasModel() {
		add("model", "pass", "model configured; live quality not measured")
	} else {
		add("model", "fail", "model required for AI summaries; configure MODEL_ENDPOINT and MODEL_NAME")
	}
	if !r.Config.VerifyWithModel {
		status := "warn"
		if strict {
			status = "fail"
		}
		add("verification", status, "independent support verification is disabled")
	} else {
		add("verification", "pass", "support verification enabled")
	}
	if err := os.MkdirAll(r.Config.DataDir, 0700); err != nil {
		add("storage", "fail", "cannot create data directory")
		return report
	}
	work, err := os.MkdirTemp(r.Config.DataDir, ".preflight-")
	if err != nil {
		add("storage", "fail", "data directory is not writable")
		return report
	}
	defer os.RemoveAll(work)
	if err := fsutil.WriteJSON(filepath.Join(work, "probe.json"), map[string]bool{"probe": true}); err != nil {
		add("storage", "fail", "atomic durable write failed")
	} else {
		add("storage", "pass", "atomic write, file sync and directory sync succeeded")
	}
	versionCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	version, err := probeCommand(versionCtx, r.Typst.Binary, "--version")
	stop()
	if err != nil {
		add("typst", "fail", "Typst executable unavailable")
	} else {
		add("typst", "pass", strings.TrimSpace(string(version[:min(128, len(version))])))
		fontCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		args := []string{"fonts"}
		for _, path := range r.Typst.FontPaths {
			args = append(args, "--font-path", path)
		}
		fonts, err := probeCommand(fontCtx, r.Typst.Binary, args...)
		stop()
		found := false
		for _, family := range []string{"Noto Sans CJK SC", "Heiti SC", "Songti SC"} {
			if bytes.Contains(fonts, []byte(family)) {
				found = true
				break
			}
		}
		if err != nil || !found {
			add("fonts", "fail", "template requires Noto Sans CJK SC, Heiti SC or Songti SC")
		} else {
			add("fonts", "pass", "Chinese template font available")
		}
		if _, err := r.Renderer.Render(ctx, "preflight", demo.Output(), filepath.Join(work, "images")); err != nil {
			add("render", "fail", "actual Chinese card render failed; check fonts/compiler")
		} else {
			add("render", "pass", "actual Chinese card PNG compiled and validated")
		}
	}
	if online && r.Telegram != nil {
		checkCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		bot, err := r.Telegram.GetMe(checkCtx)
		if err != nil {
			add("telegram-online", "fail", "bot identity request failed")
		} else if r.Telegram.Username != "" && !strings.EqualFold(r.Telegram.Username, bot.Username) {
			add("telegram-online", "fail", "configured bot username does not match token")
		} else {
			add("telegram-online", "pass", "bot identity verified")
			if bot.CanReadAllGroupMessages {
				add("telegram-group-mentions", "pass", "privacy mode disabled; existing groups may need the bot re-added; actual group delivery not verified")
			} else {
				// getMe reports a bot-wide privacy setting, not this bot's
				// administrator role or delivery permissions in a particular group.
				add("telegram-group-mentions", "warn", "group delivery of @username questions not verified: privacy mode is enabled or unreported; use /search@username, or configure BotFather privacy and re-add the bot; group administrators receive all messages")
			}
		}
		info, err := r.Telegram.WebhookInfo(checkCtx)
		if err != nil {
			add("webhook-online", "fail", "webhook status request failed")
		} else if info.URL == "" {
			add("webhook-online", "fail", "webhook has not been registered")
		} else {
			add("webhook-online", "pass", "webhook registered; end-to-end delivery still needs a test request")
		}
		stop()
	}
	return report
}

type probeOutput struct{ bytes.Buffer }

func (w *probeOutput) Write(p []byte) (int, error) {
	if remaining := (32 << 10) - w.Len(); remaining > 0 {
		_, _ = w.Buffer.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}
func probeCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var output probeOutput
	command.Stdout = &output
	err := command.Run()
	return output.Bytes(), err
}

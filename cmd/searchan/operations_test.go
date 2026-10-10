package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHealthcheckAndVersionCLI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ready","pending":0}`) }))
	defer srv.Close()
	for _, args := range [][]string{{"healthcheck", "-url", srv.URL}, {"version"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, func(string) string { return "" }); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("operation did not emit JSON")
		}
	}
	if err := run(context.Background(), []string{"healthcheck", "extra"}, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
		t.Fatal("extra argument ignored")
	}
	if err := run(context.Background(), []string{"healthcheck"}, &bytes.Buffer{}, func(k string) string {
		if k == "SEARCHAN_LISTEN" {
			return strings.TrimPrefix(srv.URL, "http://")
		}
		return ""
	}); err != nil {
		t.Fatal("healthcheck did not honor configured listen port", err)
	}
}

func TestTelegramCLIDoesNotPrintCredentials(t *testing.T) {
	var registrations atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"fixture_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			fmt.Fprint(w, `{"ok":true,"result":{"url":"https://bot.example.org/webhooks/telegram"}}`)
		case strings.HasSuffix(r.URL.Path, "setWebhook"):
			registrations.Add(1)
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected call")
		}
	}))
	defer srv.Close()
	env := map[string]string{"TELEGRAM_BOT_TOKEN": "private-token-never-print", "TELEGRAM_WEBHOOK_SECRET": strings.Repeat("private_secret", 4), "TELEGRAM_API_ENDPOINT": srv.URL}
	for _, args := range [][]string{{"telegram", "info"}, {"telegram", "webhook", "-url", "https://bot.example.org/webhooks/telegram"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), env["TELEGRAM_BOT_TOKEN"]) || strings.Contains(out.String(), env["TELEGRAM_WEBHOOK_SECRET"]) {
			t.Fatal("credential in operation output")
		}
	}
	if registrations.Load() != 1 {
		t.Fatal("wrong registration count")
	}
}

func TestStrictDoctorUsesRealLocalRenderWithoutOnlineCalls(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("requires actual Typst")
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not call", 500) }))
	defer srv.Close()
	env := map[string]string{"TELEGRAM_BOT_TOKEN": "fixture-token", "TELEGRAM_BOT_USERNAME": "fixture_bot", "TELEGRAM_WEBHOOK_SECRET": strings.Repeat("s", 32), "TELEGRAM_API_ENDPOINT": srv.URL, "SEARCHAN_ALLOWED_USERS": "telegram:1", "MODEL_ENDPOINT": srv.URL, "MODEL_NAME": "fixture", "TYPST_BIN": binary, "SEARCHAN_DATA_DIR": t.TempDir()}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"doctor", "-strict"}, &out, func(k string) string { return env[k] }); err != nil {
		t.Fatalf("local preflight failed: %s %v", out.String(), err)
	}
	if calls.Load() != 0 || !strings.Contains(out.String(), `"ready": true`) {
		t.Fatal("local doctor called remote interfaces or did not report success")
	}
	delete(env, "MODEL_ENDPOINT")
	delete(env, "MODEL_NAME")
	for _, args := range [][]string{{"doctor", "-strict"}, {"doctor"}} {
		out.Reset()
		if err := run(context.Background(), args, &out, func(k string) string { return env[k] }); err == nil || !strings.Contains(out.String(), "model required") {
			t.Fatal("doctor accepted missing model", out.String())
		}
	}
}

func TestModelCheckUsesConfiguredProtocolWithoutLeakingCredentials(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses", "anthropic"} {
		t.Run(api, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				input, _ := json.Marshal(payload)
				if len(input) < 10000 || payload["model"] != "fixture" {
					t.Error("probe did not exercise representative input")
				}
				body := `{"ok":true,"summary":"Synthetic cancellation reference."}`
				switch api {
				case "chat_completions":
					fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, body)
				case "responses":
					fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, body)
				case "anthropic":
					if r.Header.Get("x-api-key") != "private-never-print" {
						t.Error("missing Anthropic auth")
					}
					fmt.Fprintf(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":%q}]}`, body)
				}
			}))
			defer srv.Close()
			env := map[string]string{"MODEL_ENDPOINT": srv.URL, "MODEL_NAME": "fixture", "MODEL_API": api, "MODEL_API_KEY": "private-never-print", "SEARCHAN_MODEL_TIMEOUT": "1ms"}
			var output bytes.Buffer
			if err := run(context.Background(), []string{"model", "check", "-timeout", "2s"}, &output, func(k string) string { return env[k] }); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || !strings.Contains(output.String(), `"ready": true`) || strings.Contains(output.String(), "private-never-print") {
				t.Fatal("bad model probe output", output.String())
			}
		})
	}
	for _, args := range [][]string{{"model"}, {"model", "wrong"}, {"model", "check", "-timeout", "-1s"}, {"model", "check", "-timeout", "3m"}, {"model", "check", "extra"}, {"model", "check"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
			t.Fatalf("invalid/unconfigured model check accepted: %v", args)
		}
	}
}

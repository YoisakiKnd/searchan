package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOnlinePreflightDoesNotConfuseBotPrivacyWithGroupDelivery(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST for actual local render")
	}
	for _, tc := range []struct{ name, identity, want string }{
		{"disabled", `{"id":1,"is_bot":true,"username":"fixture_bot","can_read_all_group_messages":true}`, "pass"},
		{"enabled", `{"id":1,"is_bot":true,"username":"fixture_bot","can_read_all_group_messages":false}`, "warn"},
		{"unreported", `{"id":1,"is_bot":true,"username":"fixture_bot"}`, "warn"},
		{"wrong-bot", `{"id":1,"is_bot":true,"username":"another_bot","can_read_all_group_messages":true}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch {
				case strings.HasSuffix(r.URL.Path, "/getMe"):
					fmt.Fprintf(w, `{"ok":true,"result":%s}`, tc.identity)
				case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
					fmt.Fprint(w, `{"ok":true,"result":{"url":"https://bot.example/webhooks/telegram"}}`)
				default:
					t.Error("preflight mutated bot settings or called search/model")
					http.Error(w, "unexpected method", 500)
				}
			}))
			defer api.Close()
			env := map[string]string{"TELEGRAM_BOT_TOKEN": "private-fixture-token", "TELEGRAM_BOT_USERNAME": "fixture_bot", "TELEGRAM_WEBHOOK_SECRET": strings.Repeat("s", 32), "TELEGRAM_API_ENDPOINT": api.URL, "SEARCHAN_ALLOWED_USERS": "telegram:1", "MODEL_ENDPOINT": api.URL, "MODEL_NAME": "fixture", "TYPST_BIN": binary, "SEARCHAN_DATA_DIR": t.TempDir()}
			r, err := New(func(k string) string { return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			report := r.Preflight(context.Background(), true, true)
			found := ""
			for _, check := range report.Checks {
				if check.Name == "telegram-group-mentions" {
					found = check.Status
					if !strings.Contains(check.Detail, "not verified") || strings.Contains(check.Detail, env["TELEGRAM_BOT_TOKEN"]) {
						t.Fatal("claimed real group delivery or exposed token", check)
					}
				}
			}
			if found != tc.want || report.Ready != (tc.want != "") || calls.Load() != 2 {
				t.Fatalf("privacy check=%q ready=%v calls=%d", found, report.Ready, calls.Load())
			}
		})
	}
}

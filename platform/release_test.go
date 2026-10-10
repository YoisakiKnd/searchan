package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/render"
)

func TestTelegramManagementAndRegistrationPayload(t *testing.T) {
	var registrations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":123,"is_bot":true,"username":"searchan_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			fmt.Fprint(w, `{"ok":true,"result":{"url":"https://bot.example.org/webhooks/telegram","pending_update_count":2}}`)
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			var payload struct {
				Secret  string   `json:"secret_token"`
				Drop    bool     `json:"drop_pending_updates"`
				Allowed []string `json:"allowed_updates"`
				Max     int      `json:"max_connections"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.Secret != strings.Repeat("s", 32) || payload.Drop || len(payload.Allowed) != 1 || payload.Allowed[0] != "message" || payload.Max != 8 {
				t.Errorf("unsafe registration payload: %+v", payload)
			}
			registrations.Add(1)
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected method")
		}
	}))
	defer server.Close()
	bot := &Telegram{Token: "private-test-token", Endpoint: server.URL, WebhookSecret: strings.Repeat("s", 32), Client: server.Client()}
	if identity, err := bot.GetMe(context.Background()); err != nil || identity.Username != "searchan_bot" {
		t.Fatal(identity, err)
	}
	if info, err := bot.WebhookInfo(context.Background()); err != nil || info.Pending != 2 {
		t.Fatal(info, err)
	}
	if err := bot.SetWebhook(context.Background(), "https://bot.example.org/webhooks/telegram", false); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://example.org/webhooks/telegram", "https://user:pass@example.org/webhooks/telegram", "https://example.org/wrong", "https://example.org/webhooks/telegram?token=secret"} {
		if err := bot.SetWebhook(context.Background(), raw, false); err == nil {
			t.Error("invalid webhook accepted")
		}
	}
	if registrations.Load() != 1 {
		t.Fatal("invalid URL reached platform")
	}
}

func TestTelegramFailureClassification(t *testing.T) {
	for _, test := range []struct {
		status            int
		body              string
		definite, limited bool
	}{
		{429, `{"ok":false,"error_code":429,"parameters":{"retry_after":60}}`, true, true},
		{403, `{"ok":false,"error_code":403}`, true, false},
		{500, `{"ok":false,"error_code":403}`, false, false},
		{200, `{"ok":true,"result":null}`, false, false},
		{200, `broken`, false, false},
	} {
		t.Run(fmt.Sprint(test.status, test.body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }))
			defer server.Close()
			bot := &Telegram{Token: "secret", Endpoint: server.URL, Client: server.Client()}
			_, err := bot.SendText(context.Background(), Target{ChatID: "1"}, "test", 1)
			var limited *RateLimitError
			if err == nil || Definite(err) != test.definite || errors.As(err, &limited) != test.limited {
				t.Fatalf("incorrect delivery classification: %v", err)
			}
			if limited != nil && limited.After != time.Minute {
				t.Fatal("retry_after lost")
			}
		})
	}
}

type sendFunc func() (Receipt, error)

func (f sendFunc) SendText(context.Context, Target, string, int) (Receipt, error) { return f() }
func (f sendFunc) SendImage(context.Context, Target, render.Artifact, string, int) (Receipt, error) {
	return f()
}
func TestRetryOnlyExplicitRateLimits(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("missing receipt"), &RejectedError{Code: 403}, &RateLimitError{After: time.Hour}} {
		calls := 0
		sender := RetrySender{Next: sendFunc(func() (Receipt, error) { calls++; return Receipt{}, failure })}
		if _, err := sender.SendText(context.Background(), Target{}, "", 1); err == nil || calls != 1 {
			t.Fatalf("unknown/non-retryable send repeated: %v calls=%d", err, calls)
		}
	}
	calls := 0
	sender := RetrySender{Next: sendFunc(func() (Receipt, error) {
		calls++
		if calls < 3 {
			return Receipt{}, &RateLimitError{After: time.Millisecond}
		}
		return Receipt{MessageID: "ok"}, nil
	})}
	if got, err := sender.SendText(context.Background(), Target{}, "", 1); err != nil || got.MessageID != "ok" || calls != 3 {
		t.Fatal(got, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	sender.Next = sendFunc(func() (Receipt, error) { calls++; return Receipt{}, &RateLimitError{After: time.Second} })
	if _, err := sender.SendText(ctx, Target{}, "", 1); !Definite(err) || calls != 1 {
		t.Fatal("cancelled backoff lost known refusal")
	}
}

func TestTelegramRejectsCommandsForOtherBots(t *testing.T) {
	bot := &Telegram{WebhookSecret: "webhook", Username: "searchan_bot"}
	for _, test := range []struct {
		text   string
		accept bool
	}{{"/search@other_bot query", false}, {"/search@SEARCHAN_BOT query", true}, {"/search query", true}} {
		raw, _ := json.Marshal(map[string]any{"update_id": 1, "message": map[string]any{"message_id": 1, "text": test.text, "from": map[string]int{"id": 2}, "chat": map[string]int{"id": 3}}})
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
		event, err := bot.DecodeWebhook(req)
		if err != nil || (event.ID != "") != test.accept {
			t.Fatal(test, event, err)
		}
	}
}

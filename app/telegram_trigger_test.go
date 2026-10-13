package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"searchan/platform"
)

func TestTelegramQuotedExamplesNeverCreatePaidWork(t *testing.T) {
	p := &testProvider{}
	sender := &testSender{}
	s, _ := setup(t, p, sender, testRenderer{})
	secret := strings.Repeat("s", 32)
	s.Runtime.Telegram = &platform.Telegram{Username: "fixture_bot", WebhookSecret: secret}
	for i, text := range []string{"/search Go context", "  /deep Go context", "@fixture_bot Go context", "\u3000@fixture_bot Go context"} {
		for _, kind := range []string{"code", "pre", "blockquote", "expandable_blockquote"} {
			body, err := json.Marshal(map[string]any{"update_id": i, "message": map[string]any{"message_id": i + 1, "text": text, "entities": []map[string]any{{"type": kind, "offset": 0, "length": len([]rune(text))}}, "from": map[string]int{"id": 42}, "chat": map[string]int{"id": 123}}})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				req := httptest.NewRequest("POST", "/webhooks/telegram", bytes.NewReader(body))
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
				response := httptest.NewRecorder()
				s.Handler().ServeHTTP(response, req)
				if response.Code != 200 {
					t.Fatal("literal example was not acknowledged", response.Code)
				}
			}
		}
	}
	if s.Store.Pending() != 0 || p.calls.Load() != 0 || len(sender.texts) != 0 || sender.images != 0 {
		t.Fatal("literal examples created tasks, searches or responses")
	}
	if _, exists, err := s.Store.Claim(); exists || err != nil {
		t.Fatal("literal examples left durable work", exists, err)
	}
}

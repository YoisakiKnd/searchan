package platform

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramMentionEntitiesAndLiteralExamples(t *testing.T) {
	entity := func(kind string, offset, length int) map[string]any {
		return map[string]any{"type": kind, "offset": offset, "length": length}
	}
	const username = "searchan_bot"
	const mention = "@" + username
	for _, tc := range []struct {
		name, text, command, argument string
		entities                      []map[string]any
	}{
		{"adjacent-chinese", mention + "帮我查 Spring Data 简介", "search", "帮我查 Spring Data 简介", []map[string]any{entity("mention", 0, len(mention))}},
		{"punctuation", mention + "，Spring Data 是什么？", "search", "，Spring Data 是什么？", []map[string]any{entity("mention", 0, len(mention))}},
		{"adjacent-command", mention + "/deep Go context", "deep", "Go context", []map[string]any{entity("mention", 0, len(mention))}},
		{"wide-whitespace-utf16", "\u3000" + mention + "介绍一下 Go context", "search", "介绍一下 Go context", []map[string]any{entity("mention", 1, len(mention))}},
		{"newline-whitespace", " \n\t" + mention + "查这个 API", "search", "查这个 API", []map[string]any{entity("mention", 3, len(mention))}},
		{"case-and-emoji", "@SEARCHAN_BOT查🐼是什么动物", "search", "查🐼是什么动物", []map[string]any{entity("mention", 0, len(mention))}},
		{"mention-only", mention, "help", "", []map[string]any{entity("mention", 0, len(mention))}},
		{"bold-real-mention", mention + "查 Go", "search", "查 Go", []map[string]any{entity("bold", 0, len(mention)), entity("mention", 0, len(mention))}},
		{"legacy-spaced", mention + " Go context", "search", "Go context", nil},
		{"middle-mention", "讨论 " + mention + "查 Go", "", "", []map[string]any{entity("mention", 3, len(mention))}},
		{"emoji-before-mention", "🐼" + mention + "查 Go", "", "", []map[string]any{entity("mention", 2, len(mention))}},
		{"other-bot", "@other_bot查 Go", "", "", []map[string]any{entity("mention", 0, len("@other_bot"))}},
		{"forged-short-prefix", mention + "_other 查 Go", "", "", []map[string]any{entity("mention", 0, len(mention))}},
		{"byte-offset-is-not-utf16", "\u3000" + mention + "查 Go", "", "", []map[string]any{entity("mention", 3, len(mention))}},
		{"wrong-length", mention + "查 Go", "", "", []map[string]any{entity("mention", 0, len(mention)-1)}},
		{"negative-offset", mention + "查 Go", "", "", []map[string]any{entity("mention", -1, len(mention))}},
		{"excess-length", mention + "查 Go", "", "", []map[string]any{entity("mention", 0, 1<<30)}},
		{"unmarked-adjacent-text", mention + "查 Go", "", "", nil},
		{"code-mention-example", mention + " Go context", "", "", []map[string]any{entity("code", 0, len(mention)+11)}},
		{"pre-command-example", "/search Go context", "", "", []map[string]any{entity("pre", 0, 18)}},
		{"wide-space-code-command", "\u3000/search Go context", "", "", []map[string]any{entity("code", 1, 18)}},
		{"code-includes-leading-space", " \n/search Go context", "", "", []map[string]any{entity("pre", 0, 20)}},
		{"code-before-command", "  /search Go context", "search", "Go context", []map[string]any{entity("code", 0, 2)}},
		{"blockquote-command-example", "/search Go context", "", "", []map[string]any{entity("blockquote", 0, 18)}},
		{"expanded-quote-example", mention + " Go context", "", "", []map[string]any{entity("expandable_blockquote", 0, 24)}},
		{"formatted-command", "/search Go context", "search", "Go context", []map[string]any{entity("bold", 0, 7)}},
		{"mention-before-code-query", mention + " Go context", "search", "Go context", []map[string]any{entity("mention", 0, len(mention)), entity("code", len(mention)+1, 10)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"update_id": 7, "message": map[string]any{"message_id": 8, "message_thread_id": 9, "text": tc.text, "entities": tc.entities, "from": map[string]int{"id": 42}, "chat": map[string]int{"id": -100}}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
			bot := &Telegram{Username: username, WebhookSecret: "webhook"}
			event, err := bot.DecodeWebhook(req)
			cmd, accepted := ParseCommand(event.Text)
			if err != nil || accepted != (tc.command != "") || cmd.Name != tc.command || cmd.Argument != tc.argument {
				t.Fatalf("unexpected trigger: event=%+v command=%+v accepted=%v error=%v", event, cmd, accepted, err)
			}
			if accepted && (event.ID != "telegram:7" || event.Target.ThreadID != 9 || event.Target.ChatID != "-100" || event.Target.UserID != "42" || event.Target.MessageID != "8") {
				t.Fatal("entity trigger lost event identity or topic", event)
			}
		})
	}
}

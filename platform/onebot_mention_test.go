package platform

import (
	"fmt"
	"strings"
	"testing"
)

func TestOneBotLeadingMentionRouting(t *testing.T) {
	q := &OneBot{SelfID: "12345", WebhookSecret: strings.Repeat("s", 32)}
	for _, tc := range []struct {
		name, message, command, query string
	}{
		{"natural", `[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "search", "Go context cancellation"},
		{"whitespace-numeric", `[{"type":"text","data":{"text":" \n\t"}},{"type":"at","data":{"qq":12345}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "search", "Go context cancellation"},
		{"reply-metadata", `[{"type":"reply","data":{"id":"8"}},{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "search", "Go context cancellation"},
		{"mention-only", `[{"type":"at","data":{"qq":"12345"}}]`, "help", ""},
		{"repeated-self", `[{"type":"at","data":{"qq":"12345"}},{"type":"at","data":{"qq":12345}},{"type":"text","data":{"text":" question"}}]`, "search", "question"},
		{"control", `[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" /status abc"}}]`, "status", "abc"},
		{"slash", `[{"type":"text","data":{"text":"/search Go context cancellation"}}]`, "search", "Go context cancellation"},
		{"mention-slash", `[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" /deep Go context cancellation"}}]`, "deep", "Go context cancellation"},
		{"mid-sentence", `[{"type":"text","data":{"text":"hello "}},{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "", ""},
		{"image-first", `[{"type":"image","data":{"file":"untrusted"}},{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "", ""},
		{"face-first", `[{"type":"face","data":{"id":"1"}},{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]`, "", ""},
		{"other-bot", `[{"type":"at","data":{"qq":"67890"}},{"type":"text","data":{"text":" /search question"}}]`, "", ""},
		{"all", `[{"type":"at","data":{"qq":"all"}},{"type":"text","data":{"text":" question"}}]`, "", ""},
		{"literal-at", `[{"type":"text","data":{"text":"@12345 question"}}]`, "", ""},
		{"raw-cq", `"[CQ:at,qq=12345] /search question"`, "", ""},
		{"plain-question", `"Go context cancellation"`, "", ""},
		{"unknown-command", `[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" /unknown question"}}]`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"self_id":12345,"post_type":"message","message_type":"group","group_id":88,"user_id":99,"message_id":10,"message":%s}`, tc.message)
			event, err := q.DecodeWebhook(signedOneBotRequest(q, body))
			if err != nil {
				t.Fatal(err)
			}
			cmd, ok := ParseCommand(event.Text)
			if ok != (tc.command != "") || cmd.Name != tc.command || cmd.Argument != tc.query {
				t.Fatalf("route=%+v ok=%v text=%q", cmd, ok, event.Text)
			}
		})
	}
}

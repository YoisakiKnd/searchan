package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"searchan/platform"
	"searchan/render"
)

// Typst runs locally and the gateway is a local HTTP mock. Search and model use
// fixed fixtures; this verifies delivery and access, not factual answer quality.
func TestOneBotMentionRealPNGAndMergedDelivery(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST for actual Typst end-to-end test")
	}
	for _, tc := range []struct {
		name, scope string
		snippetOnly bool
	}{
		{"group-summary", "group", false},
		{"private-summary", "private", false},
		{"group-brief", "group", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var texts, forwards int
			var uploaded []byte
			var notes strings.Builder
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer local-test" {
					t.Error("missing gateway authentication")
				}
				raw, err := io.ReadAll(io.LimitReader(req.Body, (2<<20)+1))
				if err != nil || len(raw) > 2<<20 || req.ContentLength != int64(len(raw)) {
					t.Error("invalid bounded request length")
				}
				var payload struct {
					GroupID  int64 `json:"group_id"`
					UserID   int64 `json:"user_id"`
					Messages []struct {
						Type string `json:"type"`
						Data struct {
							Uin     int64 `json:"uin"`
							Content []struct {
								Type string            `json:"type"`
								Data map[string]string `json:"data"`
							} `json:"content"`
						} `json:"data"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Error(err)
				}
				method := "send_group"
				if tc.scope == "group" {
					if payload.GroupID != 88 || payload.UserID != 0 {
						t.Error("wrong group destination")
					}
				} else {
					method = "send_private"
					if payload.UserID != 99 || payload.GroupID != 0 {
						t.Error("wrong private destination")
					}
				}
				switch req.URL.Path {
				case "/" + method + "_msg":
					texts++
				case "/" + method + "_forward_msg":
					forwards++
					if len(payload.Messages) != 2 {
						t.Error("expected image and source notes in one forwarded record")
					}
					for _, node := range payload.Messages {
						if node.Type != "node" || node.Data.Uin != 12345 {
							t.Error("invalid custom forward node")
						}
						for _, segment := range node.Data.Content {
							switch segment.Type {
							case "image":
								if uploaded != nil || !strings.HasPrefix(segment.Data["file"], "base64://") {
									t.Error("duplicate image or non-inline upload")
								}
								uploaded, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(segment.Data["file"], "base64://"))
								if err != nil {
									t.Error(err)
								}
								if cfg, err := png.DecodeConfig(bytes.NewReader(uploaded)); err != nil || cfg.Width < 500 || cfg.Height < 300 {
									t.Error("not an actual readable report PNG")
								}
							case "text":
								notes.WriteString(segment.Data["text"])
							default:
								t.Error("unexpected forwarded segment")
							}
						}
					}
				default:
					t.Errorf("unexpected gateway method: %s", req.URL.Path)
				}
				fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{"message_id":456}}`)
			}))
			defer api.Close()
			p := &testProvider{snippetOnly: tc.snippetOnly}
			s, _ := setup(t, p, &testSender{}, testRenderer{})
			q := &platform.OneBot{SelfID: "12345", WebhookSecret: strings.Repeat("s", 32), Endpoint: api.URL, AccessToken: "local-test", Client: api.Client()}
			s.Runtime.OneBot = q
			s.Runtime.Senders["qq"] = platform.RetrySender{Next: q}
			s.Runtime.Renderer = render.New(binary, 1)
			s.Runtime.Config.Timeout = 15 * time.Second
			s.Runtime.Config.AllowPublic = false
			s.Runtime.Config.AllowedChats = map[string]bool{"qq:88": true, "qq:99": true}
			body := fmt.Sprintf(`{"self_id":12345,"post_type":"message","message_type":%q,"group_id":88,"user_id":99,"message_id":10,"message":[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" Go context cancellation"}}]}`, tc.scope)
			post := func(body string, signed bool, want int) {
				t.Helper()
				req := httptest.NewRequest("POST", "/webhooks/onebot", strings.NewReader(body))
				if signed {
					mac := hmac.New(sha1.New, []byte(q.WebhookSecret))
					mac.Write([]byte(body))
					req.Header.Set("X-Signature", "sha1="+hex.EncodeToString(mac.Sum(nil)))
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, req)
				if w.Code != want {
					t.Fatalf("status=%d want=%d", w.Code, want)
				}
			}
			post(body, false, http.StatusUnauthorized)
			denied := strings.ReplaceAll(body, `"group_id":88`, `"group_id":77`)
			denied = strings.ReplaceAll(denied, `"user_id":99`, `"user_id":77`)
			post(denied, true, http.StatusNoContent)
			if s.Store.Pending() != 0 || p.calls.Load() != 0 {
				t.Fatal("untrusted event started search work")
			}
			post(body, true, http.StatusNoContent)
			post(body, true, http.StatusNoContent)
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok || s.Store.Pending() != 0 || rec.Query != "Go context cancellation" {
				t.Fatalf("mention did not create exactly one search: %+v %v", rec, err)
			}
			s.process(context.Background(), rec)
			stored, err := s.Store.Get(rec.ID)
			if err != nil || stored.State != "completed" || stored.Output == nil || len(stored.Output.Sources) == 0 || len(stored.Artifacts) != 1 || len(stored.Deliveries) != 2 {
				t.Fatalf("pipeline failed: %+v %v", stored, err)
			}
			for i, kind := range []string{"accepted", "report"} {
				if d := stored.Deliveries[i]; d.Kind != kind || d.State != "sent" || d.Receipt != "456" {
					t.Fatalf("missing durable receipt: %+v", d)
				}
			}
			post(body, true, http.StatusNoContent)
			mu.Lock()
			defer mu.Unlock()
			if texts != 1 || forwards != 1 || p.calls.Load() != 1 || s.Store.Pending() != 0 {
				t.Fatalf("duplicate or split delivery: text=%d forward=%d search=%d", texts, forwards, p.calls.Load())
			}
			for _, source := range stored.Output.Sources {
				if !strings.Contains(notes.String(), source.URL) {
					t.Fatal("forward missing clickable source")
				}
			}
			if !strings.Contains(notes.String(), rec.ID) || !strings.Contains(notes.String(), stored.Output.Answer.Lead.Text) {
				t.Fatal("forward missing task or answer")
			}
			if tc.snippetOnly && stored.Output.AnswerMode != "brief_summary" {
				t.Fatal("snippet-only fixture did not take brief-summary path")
			}
			rendered, err := os.ReadFile(stored.Artifacts[0].Path)
			if err != nil || !bytes.Equal(uploaded, rendered) {
				t.Fatal("uploaded image differs from rendered artifact")
			}
			if dir := os.Getenv("SEARCHAN_TEST_ONEBOT_ARTIFACT_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				dir = filepath.Join(dir, tc.name)
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "uploaded.png"), uploaded, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(notes.String()), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOneBotMentionHelpDoesNotQueueSearch(t *testing.T) {
	p, sender := &testProvider{}, &testSender{}
	s, _ := setup(t, p, sender, testRenderer{})
	s.Runtime.OneBot = &platform.OneBot{}
	s.Runtime.Senders["qq"] = sender
	event := platform.Event{ID: "help-onebot", Text: "/help", Target: platform.Target{Platform: "qq", Scope: "group", ChatID: "88", UserID: "99"}}
	for range 2 {
		if err := s.Accept(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.texts) != 1 || !strings.Contains(sender.texts[0], "消息开头 @本机器人") || !strings.Contains(sender.texts[0], "合并聊天记录") || p.calls.Load() != 0 || s.Store.Pending() != 0 {
		t.Fatal("help missing, repeated, or started search", sender.texts)
	}
}

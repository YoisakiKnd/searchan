package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/platform"
	"searchan/render"
	"searchan/research"
	"searchan/search"
	"searchan/task"
)

type pausedRenderer struct {
	next             render.Renderer
	started, release chan struct{}
	output           research.Output
}

func (r *pausedRenderer) Render(ctx context.Context, id string, output research.Output, path string) ([]render.Artifact, error) {
	r.output = output
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.next.Render(ctx, id, output, path)
}

func TestTelegramWebhookRealPNGAndGracefulShutdown(t *testing.T) {
	for _, tc := range []struct {
		name, text, query string
		snippetOnly       bool
		entities          []map[string]any
	}{
		{"full-summary", "/search@searchan_test_bot Go context cancellation", "Go context cancellation", false, nil},
		{"brief-summary", "/search@searchan_test_bot Go context cancellation", "Go context cancellation", true, nil},
		{"verified-gap", "/search@searchan_test_bot Go context cancellation", "Go context cancellation", false, nil},
		{"leading-mention-topic", "@searchan_test_bot Go context cancellation", "Go context cancellation", false, nil},
		{"adjacent-chinese-mention", "@searchan_test_bot介绍 Go context cancellation", "介绍 Go context cancellation", false, []map[string]any{{"type": "mention", "offset": 0, "length": len("@searchan_test_bot")}}},
		{"adjacent-slash-command", "@searchan_test_bot/search Go context cancellation", "Go context cancellation", false, []map[string]any{{"type": "mention", "offset": 0, "length": len("@searchan_test_bot")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testTelegramWebhookRealPNGAndGracefulShutdown(t, tc.text, tc.query, tc.snippetOnly, tc.entities, tc.name == "verified-gap")
		})
	}
}

func testTelegramWebhookRealPNGAndGracefulShutdown(t *testing.T, text, query string, snippetOnly bool, entities []map[string]any, verifiedGap bool) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST for actual Typst end-to-end test")
	}
	var images, texts atomic.Int32
	var uploaded []byte
	var caption string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/sendMessage"):
			texts.Add(1)
		case strings.HasSuffix(req.URL.Path, "/sendPhoto"):
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "bad multipart", 400)
				return
			}
			defer req.MultipartForm.RemoveAll()
			if req.FormValue("message_thread_id") != "5" || req.FormValue("chat_id") != "123" {
				t.Error("image was not routed to the original chat topic")
			}
			f, _, err := req.FormFile("photo")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			var header [8]byte
			if _, err := io.ReadFull(f, header[:]); err != nil || !bytes.Equal(header[:], []byte("\x89PNG\r\n\x1a\n")) {
				t.Error("not an actual PNG")
			}
			if verifiedGap {
				rest, err := io.ReadAll(io.LimitReader(f, (10<<20)+1))
				if err != nil || len(rest) > 10<<20 {
					t.Error("invalid bounded upload")
					return
				}
				uploaded = append(append([]byte(nil), header[:]...), rest...)
				caption = req.FormValue("caption")
				if strings.Contains(caption, "SECRET_UNVERIFIED") || !strings.Contains(caption, "部分补充细节尚未确认。") {
					t.Error("caption published verifier prose or lost program status")
				}
			}
			images.Add(1)
		default:
			t.Errorf("unexpected Bot API call: %s", req.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":123}}`)
	}))
	defer api.Close()
	p := &testProvider{snippetOnly: snippetOnly}
	s, _ := setup(t, p, &testSender{}, testRenderer{})
	if verifiedGap {
		e, err := research.New(research.Config{Providers: []search.Provider{p}, Reader: unusedReader{}, MaxSearchCalls: 1, VerifyWithModel: true, Model: testModelFunc(func(ctx context.Context, prompt string, input, output any) error {
			if strings.HasPrefix(prompt, "核对 answer") {
				return json.Unmarshal([]byte(`{"answers_query":true,"unsupported":[],"missing":["SECRET_UNVERIFIED_SCOPE: all workers stop immediately"]}`), output)
			}
			return (testModel{}).Complete(ctx, prompt, input, output)
		})})
		if err != nil {
			t.Fatal(err)
		}
		s.Runtime.Engine = e
	}
	bot := &platform.Telegram{Token: "test-token", WebhookSecret: strings.Repeat("s", 32), Username: "searchan_test_bot", Endpoint: api.URL, Client: api.Client()}
	s.Runtime.Telegram = bot
	s.Runtime.Senders["telegram"] = platform.RetrySender{Next: bot}
	s.Runtime.Config.Workers = 1
	s.Runtime.Config.ShutdownTimeout = 5 * time.Second
	s.Runtime.Config.AllowPublic = false
	s.Runtime.Config.AllowedUsers = map[string]bool{"telegram:42": true}
	renderer := &pausedRenderer{next: render.New(binary, 1), started: make(chan struct{}), release: make(chan struct{})}
	s.Runtime.Renderer = renderer
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, listener) }()
	url := "http://" + listener.Addr().String() + "/webhooks/telegram"
	body, err := json.Marshal(map[string]any{"update_id": 77, "message": map[string]any{"message_id": 7, "message_thread_id": 5, "text": text, "entities": entities, "from": map[string]int{"id": 42}, "chat": map[string]int{"id": 123}}})
	if err != nil {
		t.Fatal(err)
	}
	post := func(secret string, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		client := &http.Client{Timeout: time.Second}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("webhook code=%d want=%d", res.StatusCode, want)
		}
	}
	post("wrong", 401)
	post(bot.WebhookSecret, 200)
	post(bot.WebhookSecret, 200)
	select {
	case <-renderer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("pipeline did not reach rendering")
	}
	stop()
	for start := time.Now(); !s.draining.Load(); {
		if time.Since(start) > time.Second {
			t.Fatal("server did not begin draining")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown cancelled in-flight render: %v", err)
	default:
	}
	close(renderer.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("graceful shutdown stalled")
	}
	if images.Load() != 1 || texts.Load() != 1 || p.calls.Load() != 1 {
		t.Fatalf("images=%d texts=%d searches=%d", images.Load(), texts.Load(), p.calls.Load())
	}
	if renderer.output.Query != query {
		t.Fatal("mention parsing changed the search question", renderer.output.Query)
	}
	if snippetOnly && (renderer.output.AnswerMode != "brief_summary" || !strings.Contains(renderer.output.Text(), "未读取原文")) {
		t.Fatal("brief summary or limitation did not reach the real Typst renderer")
	}
	if verifiedGap {
		files, err := filepath.Glob(filepath.Join(s.Store.Dir, "*", "task.json"))
		if err != nil || len(files) != 1 {
			t.Fatal("verified result was not durably stored")
		}
		raw, err := os.ReadFile(files[0])
		var record task.Record
		if err != nil || json.Unmarshal(raw, &record) != nil || strings.Contains(string(raw), "SECRET_UNVERIFIED") || record.State != "completed" || record.Output == nil || record.Output.Stats.VerificationGaps != 1 || record.Output.Stats.ModelCalls != 2 || len(record.Artifacts) != 1 {
			t.Fatal("durable result retained unchecked verifier assertions or exceeded model budget")
		}
		rendered, err := os.ReadFile(record.Artifacts[0].Path)
		if err != nil || !bytes.Equal(rendered, uploaded) || strings.Contains(renderer.output.Text(), "SECRET_UNVERIFIED") || !strings.Contains(renderer.output.Text(), "部分补充细节尚未确认。") {
			t.Fatal("renderer or uploaded artifact diverged from safe final result")
		}
		if root := os.Getenv("SEARCHAN_TEST_ARTIFACT_DIR"); root != "" {
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "verified-gap")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			output, _ := json.MarshalIndent(record.Output, "", "  ")
			delivery, _ := json.MarshalIndent(map[string]any{"mode": "synthetic-model-local-telegram", "caption": caption, "deliveries": record.Deliveries, "actual_model_calls": 0, "actual_platform_sends": 0, "mock_images": images.Load()}, "", "  ")
			for name, data := range map[string][]byte{"output.json": output, "delivery.json": delivery, "uploaded.png": uploaded} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

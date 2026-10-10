package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/platform"
	"searchan/render"
	"searchan/research"
)

type pausedRenderer struct {
	next             render.Renderer
	started, release chan struct{}
}

func (r *pausedRenderer) Render(ctx context.Context, id string, output research.Output, path string) ([]render.Artifact, error) {
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.next.Render(ctx, id, output, path)
}

func TestTelegramWebhookRealPNGAndGracefulShutdown(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST for actual Typst end-to-end test")
	}
	var images, texts atomic.Int32
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
			images.Add(1)
		default:
			t.Errorf("unexpected Bot API call: %s", req.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":123}}`)
	}))
	defer api.Close()
	p := &testProvider{}
	s, _ := setup(t, p, &testSender{}, testRenderer{})
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
	body := `{"update_id":77,"message":{"message_id":7,"text":"/search@searchan_test_bot Go context cancellation","from":{"id":42},"chat":{"id":123}}}`
	post := func(secret string, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", url, strings.NewReader(body))
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
	if images.Load() != 1 || texts.Load() != 2 || p.calls.Load() != 1 {
		t.Fatalf("images=%d texts=%d searches=%d", images.Load(), texts.Load(), p.calls.Load())
	}
}

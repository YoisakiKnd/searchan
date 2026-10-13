package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/app"
	"searchan/quality"
)

// Opt in explicitly: real search/model and real Typst, but all platform
// credentials are discarded and delivery is restricted to this local server.
func TestLiveSearchToLocalTelegramPNG(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"command", "/search@local_test_bot PostgreSQL 默认事务隔离级别是什么？"},
		{"leading-mention", "@local_test_bot PostgreSQL 默认事务隔离级别是什么？"},
	} {
		t.Run(tc.name, func(t *testing.T) { liveSearchToLocalTelegramPNG(t, tc.text) })
	}
}

func liveSearchToLocalTelegramPNG(t *testing.T, text string) {
	t.Helper()
	path, typst := os.Getenv("SEARCHAN_TEST_REAL_ENV"), os.Getenv("SEARCHAN_TEST_TYPST")
	if path == "" || typst == "" {
		t.Skip("set SEARCHAN_TEST_REAL_ENV and SEARCHAN_TEST_TYPST for real research/local delivery acceptance")
	}
	base, err := quality.Environment(path, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := ""
	if root := os.Getenv("SEARCHAN_TEST_ARTIFACT_DIR"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		artifactDir = filepath.Join(root, filepath.Base(t.Name()))
		// Refuse to overwrite a previous paid run or its review.
		if err := os.Mkdir(artifactDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	type upload struct {
		PNG     []byte
		Caption string
	}
	uploads := make(chan upload, 1)
	var images, texts atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botlocal-test-token/sendMessage":
			var payload struct {
				ChatID   string `json:"chat_id"`
				ThreadID int64  `json:"message_thread_id"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload); err != nil || payload.ChatID != "123" || payload.ThreadID != 5 {
				t.Error("text did not preserve the test chat topic")
			}
			texts.Add(1)
		case "/botlocal-test-token/sendPhoto":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "bad multipart", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("chat_id") != "123" || r.FormValue("message_thread_id") != "5" || !strings.Contains(r.FormValue("caption"), "https://") {
				t.Error("image lost its chat topic or clickable sources")
			}
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			imageBytes, err := io.ReadAll(io.LimitReader(f, (10<<20)+1))
			if err != nil || len(imageBytes) > 10<<20 {
				t.Error("upload exceeds the bounded PNG size")
				return
			}
			if _, err := png.DecodeConfig(bytes.NewReader(imageBytes)); err != nil {
				t.Error("upload is not an actual PNG")
				return
			}
			select {
			case uploads <- upload{PNG: imageBytes, Caption: r.FormValue("caption")}:
			default:
				t.Error("unexpected duplicate image upload")
				return
			}
			images.Add(1)
		default:
			t.Errorf("unexpected local platform call: %s", r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":456}}`)
	}))
	defer api.Close()
	data := t.TempDir()
	overrides := map[string]string{
		"SEARCHAN_DATA_DIR": data, "SEARCHAN_LISTEN": "127.0.0.1:0", "SEARCHAN_WORKERS": "1",
		"SEARCHAN_PUBLIC": "false", "SEARCHAN_ALLOWED_USERS": "telegram:42", "SEARCHAN_ALLOWED_CHATS": "",
		"TELEGRAM_BOT_TOKEN": "local-test-token", "TELEGRAM_BOT_USERNAME": "local_test_bot",
		"TELEGRAM_WEBHOOK_SECRET": strings.Repeat("s", 32), "TYPST_BIN": typst,
	}
	runtime, err := app.New(func(k string) string {
		if v, ok := overrides[k]; ok {
			return v
		}
		return base(k)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Telegram.Endpoint, runtime.Telegram.Client = api.URL, api.Client()
	server, err := app.NewServer(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.Timeout+10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(runtime.Config.ShutdownTimeout + time.Second):
			t.Error("live acceptance server did not shut down")
		}
	}()
	started := time.Now()
	encoded, err := json.Marshal(map[string]any{"update_id": 77, "message": map[string]any{"message_id": 7, "message_thread_id": 5, "text": text, "from": map[string]int{"id": 42}, "chat": map[string]int{"id": 123}}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for range 2 {
		req := httptest.NewRequest("POST", "/webhooks/telegram", strings.NewReader(body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", overrides["TELEGRAM_WEBHOOK_SECRET"])
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("local webhook rejected: %d", w.Code)
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		files, err := filepath.Glob(filepath.Join(data, "tasks", "*", "task.json"))
		if err != nil || len(files) > 1 {
			t.Fatal("duplicate webhook created multiple tasks")
		}
		if len(files) == 1 {
			id := filepath.Base(filepath.Dir(files[0]))
			record, err := server.Store.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			switch record.State {
			case "completed":
				if record.Output == nil || record.Output.Answer.Lead.Text == "" || len(record.Output.Sources) == 0 || images.Load() != 1 || texts.Load() != 1 {
					t.Fatal("real research did not deliver a single cited image and acceptance message")
				}
				if len(record.Artifacts) != 1 {
					t.Fatal("completed task did not retain one rendered artifact")
				}
				var sent upload
				select {
				case sent = <-uploads:
				default:
					t.Fatal("completed task has no captured upload")
				}
				rendered, err := os.ReadFile(record.Artifacts[0].Path)
				if err != nil || !bytes.Equal(rendered, sent.PNG) {
					t.Fatal("Telegram upload differs from the durable rendered artifact")
				}
				if artifactDir != "" {
					// Only final research, the actual upload and synthetic delivery
					// metadata; never archive config, credentials or model drafts.
					writeJSON := func(name string, value any) {
						t.Helper()
						b, err := json.MarshalIndent(value, "", "  ")
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(artifactDir, name), b, 0600); err != nil {
							t.Fatal(err)
						}
					}
					writeJSON("output.json", record.Output)
					writeJSON("delivery.json", map[string]any{
						"version": 1, "mode": "local_telegram", "state": record.State,
						"elapsed_ms": time.Since(started).Milliseconds(), "caption": sent.Caption,
						"deliveries": record.Deliveries, "png_sha256": fmt.Sprintf("%x", sha256.Sum256(sent.PNG)),
						"width": record.Artifacts[0].Width, "height": record.Artifacts[0].Height, "bytes": len(sent.PNG),
					})
					if err := os.WriteFile(filepath.Join(artifactDir, "uploaded.png"), sent.PNG, 0600); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("real research + Typst + local Telegram completed: model=%d search=%d pages=%d", record.Output.Stats.ModelCalls, record.Output.Stats.SearchCalls, record.Output.Stats.PagesRead)
				return
			case "failed", "uncertain", "cancelled":
				// Safe categories only: remote diagnostic strings can contain URLs.
				t.Fatalf("live acceptance ended in state %s; remote error details omitted", record.State)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("live acceptance timed out")
		case <-ticker.C:
		}
	}
}

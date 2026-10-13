package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/render"
	"searchan/research"
	"searchan/search"
)

func TestAllModelProtocolsRecoverAndDeliverCitedSummary(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses", "anthropic"} {
		t.Run(api, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				input, err := io.ReadAll(io.LimitReader(r.Body, 128<<10))
				if err != nil {
					t.Error(err)
					return
				}
				if calls.Add(1) == 1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				body := `{"title":"Go context","lead":{"text":"Workers observe cancellation.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`
				if strings.Contains(string(input), "核对 answer") {
					body = `{"unsupported":[],"missing":[],"answers_query":true}`
				}
				switch api {
				case "chat_completions":
					fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, body)
				case "responses":
					fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, body)
				case "anthropic":
					fmt.Fprintf(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":%q}]}`, body)
				}
			}))
			defer srv.Close()
			env := map[string]string{"MODEL_ENDPOINT": srv.URL, "MODEL_API": api, "MODEL_NAME": "fixture"}
			runtime, err := New(func(k string) string { return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			provider := &testProvider{}
			sender := &testSender{}
			s, event := setup(t, provider, sender, testRenderer{})
			// Keep enough budget for a retry after reserving verification time.
			s.Runtime.Config.Timeout = 15 * time.Second
			s.Runtime.Config.ResearchTimeout = 10 * time.Second
			engine, err := research.New(research.Config{Providers: []search.Provider{provider}, Reader: unusedReader{}, Model: runtime.Model, MaxSearchCalls: 1, VerifyWithModel: true})
			if err != nil {
				t.Fatal(err)
			}
			s.Runtime.Engine = engine
			if binary := os.Getenv("SEARCHAN_TEST_TYPST"); binary != "" {
				s.Runtime.Renderer = render.New(binary, 1)
			}
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok {
				t.Fatal("no queued task", err)
			}
			s.process(context.Background(), rec)
			stored, err := s.Store.Get(rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != "completed" || stored.Output == nil || stored.Output.AnswerMode != "summary" || len(stored.Output.Sources) != 1 || stored.Output.Stats.ModelCalls != 2 || calls.Load() != 3 || sender.images != 1 || len(sender.texts) != 2 {
				t.Fatalf("protocol did not recover and deliver cited PNG: task=%+v requests=%d images=%d texts=%v", stored, calls.Load(), sender.images, sender.texts)
			}
		})
	}
}

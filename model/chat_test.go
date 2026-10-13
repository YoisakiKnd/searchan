package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatBoundedJSON(t *testing.T) {
	for _, tt := range []struct {
		content, finish string
		wantErr         bool
	}{
		{`{"value":"好"}`, "stop", false}, {"```json\n{\"value\":\"好\"}\n```", "stop", false},
		{`{"value":"好"}`, "length", true}, {`{"value":"好"} {}`, "stop", true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("missing key")
			}
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":%q}]}`, tt.content, tt.finish)
		}))
		client := &Chat{Endpoint: srv.URL, APIKey: "secret", Model: "test", Client: srv.Client()}
		var out struct{ Value string }
		err := client.Complete(context.Background(), "return JSON", map[string]string{"query": "q"}, &out)
		srv.Close()
		if (err != nil) != tt.wantErr {
			t.Fatalf("unexpected result: %v", err)
		}
	}
	client := &Chat{Endpoint: "https://example.org", Model: "test"}
	var out any
	if err := client.Complete(context.Background(), "JSON", strings.Repeat("x", 129<<10), &out); err == nil {
		t.Fatal("unbounded input")
	}
}

func TestMalformedModelOutputIsDiagnosedWithoutRetryOrDraftLeak(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, `{"lead":"private-draft"}`)
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Model: "test", Client: srv.Client(), MaxRetries: 1}
	var out struct{ Lead struct{ Text string } }
	err := c.Complete(context.Background(), "JSON", nil, &out)
	var issue *OutputError
	if !errors.As(err, &issue) || issue.Kind() != "type" || calls != 1 || strings.Contains(err.Error(), "private-draft") {
		t.Fatalf("unsafe output handling: calls=%d err=%v", calls, err)
	}
	if d := issue.Diagnostic(); d.Form != "object" || d.Bytes == 0 || d.Offset == 0 {
		t.Fatalf("missing safe diagnostics: %+v", d)
	}
}

func TestOutputFailureIncludesOnlySafeProtocolMetadata(t *testing.T) {
	for _, tt := range []struct {
		api, wire, finish string
		tokens, reasoning int64
	}{
		{"chat_completions", `{"choices":[{"message":{"content":"{\"lead\":"},"finish_reason":"stop"}],"usage":{"completion_tokens":120,"completion_tokens_details":{"reasoning_tokens":20}}}`, "stop", 120, 20},
		{"responses", `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"lead\":"}]}],"usage":{"output_tokens":130,"output_tokens_details":{"reasoning_tokens":30}}}`, "completed", 130, 30},
		{"anthropic", `{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"lead\":"}],"usage":{"output_tokens":140}}`, "end_turn", 140, 0},
		{"chat_completions", `{"choices":[{"message":{"content":"{\"lead\":"}}],"usage":{"completion_tokens":-1,"completion_tokens_details":{"reasoning_tokens":1000001}}}`, "unspecified", 0, 0},
		{"chat_completions", `{"choices":[{"message":{"content":"{\"lead\":"},"finish_reason":"stop"}],"usage":{"completion_tokens":"private-vendor-value"}}`, "stop", 0, 0},
	} {
		t.Run(tt.api+tt.finish+fmt.Sprint(tt.tokens), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tt.wire) }))
			defer srv.Close()
			c := &Client{Endpoint: srv.URL, Model: "fixture", API: tt.api, Client: srv.Client()}
			var out any
			err := c.Complete(context.Background(), "JSON", nil, &out)
			var issue *OutputError
			if !errors.As(err, &issue) {
				t.Fatal("lost typed output error", err)
			}
			d := issue.Diagnostic()
			if d.Cause != "unexpected_end" || d.API != tt.api || d.Finish != tt.finish || d.OutputTokens != tt.tokens || d.ReasoningTokens != tt.reasoning || strings.Contains(err.Error(), "private-") {
				t.Fatalf("unsafe or incorrect metadata: %+v %v", d, err)
			}
		})
	}
}

func TestOptionalUsageCannotRejectValidAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"value\":\"ok\"}"},"finish_reason":"stop"}],"usage":{"completion_tokens":"custom-format"}}`)
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Model: "fixture", Client: srv.Client()}
	var out struct{ Value string }
	if err := c.Complete(context.Background(), "JSON", nil, &out); err != nil || out.Value != "ok" {
		t.Fatal("optional diagnostics changed protocol acceptance", err)
	}
}

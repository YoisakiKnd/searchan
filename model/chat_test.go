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
		{`{"value":"好"}`, "length", true}, {`{"value":"好"} {}`, "stop", false}, {`{"value":`, "stop", true},
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

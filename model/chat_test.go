package model

import (
	"context"
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

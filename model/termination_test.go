package model

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestProtocolTerminationDiagnosticsNeverRetainDraftOrVendorStrings(t *testing.T) {
	for _, tc := range []struct {
		api, wire, kind, finish string
		tokens, reasoning       int64
	}{
		{"chat_completions", `{"choices":[{"finish_reason":"length","message":{"content":"{\"value\":\"TOPSECRET-TEST\"}"}}],"usage":{"completion_tokens":2400,"completion_tokens_details":{"reasoning_tokens":2300}}}`, "incomplete", "length", 2400, 2300},
		{"responses", `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"TOPSECRET-TEST"}]}],"usage":{"output_tokens":2400,"output_tokens_details":{"reasoning_tokens":2200}}}`, "incomplete", "incomplete", 2400, 2200},
		{"anthropic", `{"stop_reason":"max_tokens","content":[{"type":"text","text":"TOPSECRET-TEST"}],"usage":{"output_tokens":2400}}`, "incomplete", "max_tokens", 2400, 0},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"reasoning_content":"TOPSECRET-TEST"}}],"usage":{"completion_tokens":2400}}`, "empty", "stop", 2400, 0},
		{"chat_completions", `{"choices":[{"finish_reason":"content_filter","message":{"content":"TOPSECRET-TEST"}}]}`, "refusal", "content_filter", 0, 0},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"TOPSECRET-TEST"}}]}`, "refusal", "stop", 0, 0},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"TOPSECRET-TEST\"}","refusal":"https://private.example/?key=TOPSECRET-TEST"}}]}`, "refusal", "stop", 0, 0},
		{"chat_completions", `{"choices":[{"message":{"content":"{\"value\":\"TOPSECRET-TEST\"}","refusal":" "}}]}`, "refusal", "unspecified", 0, 0},
		{"responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","text":"TOPSECRET-TEST"}]}]}`, "refusal", "completed", 0, 0},
		{"anthropic", `{"stop_reason":"refusal","content":[{"type":"text","text":"TOPSECRET-TEST"}]}`, "refusal", "refusal", 0, 0},
		{"anthropic", `{"stop_reason":"https://private.example/?key=TOPSECRET-TEST\nprivate log","content":[]}`, "protocol", "unspecified", 0, 0},
		{"chat_completions", `{"choices":[{"finish_reason":"TOPSECRET-TEST","message":{"content":"{}"}}]}`, "protocol", "unspecified", 0, 0},
	} {
		t.Run(tc.api+"/"+tc.finish, func(t *testing.T) {
			calls := 0
			c := &Client{Endpoint: "https://gateway.example/endpoint", Model: "test", API: tc.api, MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return jsonResponse(tc.wire), nil
			})}}
			var out struct{ Value string }
			err := c.Complete(context.Background(), "JSON", nil, &out)
			var detail *OutputError
			if !errors.As(err, &detail) || out.Value != "" || calls != 1 {
				t.Fatal("termination decoded or retried the failed answer", err, out, calls)
			}
			d := detail.Diagnostic()
			if d.Kind != tc.kind || d.API != tc.api || d.Finish != tc.finish || d.OutputTokens != tc.tokens || d.ReasoningTokens != tc.reasoning || d.Form == "" || strings.Contains(err.Error(), "TOPSECRET") || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe or incorrect termination metadata: %+v %v", d, err)
			}
		})
	}
}

func TestChatNullOrEmptyRefusalAllowsNormalAnswer(t *testing.T) {
	for _, field := range []string{``, `,"refusal":null`, `,"refusal":""`} {
		c := &Client{Endpoint: "https://gateway.example/endpoint", Model: "test", API: "chat_completions", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"OK\"}"` + field + `}}]}`), nil
		})}}
		var out struct{ Value string }
		if err := c.Complete(context.Background(), "JSON", nil, &out); err != nil || out.Value != "OK" {
			t.Fatalf("ordinary answer rejected for refusal field %q: %v", field, err)
		}
	}
}

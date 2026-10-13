package model

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestObservedCallsKeepProtocolUsageWithoutDrafts(t *testing.T) {
	for _, tc := range []struct {
		api, wire string
		want      string
		failed    bool
	}{
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"OK\"}"}}],"usage":{"prompt_tokens":90,"completion_tokens":10,"total_tokens":100,"completion_tokens_details":{"reasoning_tokens":0},"prompt_tokens_details":{"cached_tokens":64}}}`, `{"prompt_tokens":90,"completion_tokens":10,"total_tokens":100,"reasoning_tokens":0,"cached_tokens":64}`, false},
		{"responses", `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"value\":\"OK\"}"}]}],"usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100,"output_tokens_details":{"reasoning_tokens":5},"input_tokens_details":{"cached_tokens":32}}}`, `{"input_tokens":80,"output_tokens":20,"total_tokens":100,"reasoning_tokens":5,"cached_tokens":32}`, false},
		{"anthropic", `{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"value\":\"OK\"}"}],"usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":40}}`, `{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":40}`, false},
		{"chat_completions", `{"choices":[{"finish_reason":"length","message":{"content":"TOPSECRET-DRAFT"}}],"usage":{"prompt_tokens":90,"completion_tokens":2400,"total_tokens":2490}}`, `{"prompt_tokens":90,"completion_tokens":2400,"total_tokens":2490}`, true},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{TOPSECRET-DRAFT"}}],"usage":{"prompt_tokens":90,"completion_tokens":10}}`, `{"prompt_tokens":90,"completion_tokens":10}`, true},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"OK\"}"}}],"usage":{"prompt_tokens":null,"completion_tokens":-1,"total_tokens":1000001,"completion_tokens_details":{"reasoning_tokens":-4}}}`, `{}`, false},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"OK\"}"}}],"usage":{"prompt_tokens":"TOPSECRET-REMOTE"}}`, `null`, false},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"value\":\"OK\"}"}}]}`, `null`, false},
	} {
		t.Run(tc.api+tc.want, func(t *testing.T) {
			c := &Client{Endpoint: "https://private.example/?key=TOPSECRET-ENDPOINT", APIKey: "TOPSECRET-KEY", Model: "test", API: tc.api, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(tc.wire), nil })}}
			var got []CallMetrics
			var out struct{ Value string }
			err := c.CompleteObserved(context.Background(), "TOPSECRET-PROMPT", map[string]string{"q": "TOPSECRET-QUERY"}, &out, func(m CallMetrics) { got = append(got, m) })
			if (err != nil) != tc.failed || len(got) != 1 || got[0].API != tc.api || got[0].RequestAttempts != 1 || !got[0].ResponseDecoded || got[0].OutputDecoded == tc.failed || got[0].InputJSONBytes != len(`{"q":"TOPSECRET-QUERY"}`) {
				t.Fatalf("incorrect observation: %+v, error=%v", got, err)
			}
			b, _ := json.Marshal(got[0].Usage)
			var actual, expected any
			if json.Unmarshal(b, &actual) != nil || json.Unmarshal([]byte(tc.want), &expected) != nil || !equalJSON(actual, expected) {
				t.Fatalf("usage=%s want=%s", b, tc.want)
			}
			b, _ = json.Marshal(got)
			if strings.Contains(string(b), "TOPSECRET") {
				t.Fatal("observation leaked source, prompt, credentials or draft")
			}
		})
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestObservedRetryDoesNotAttributePartialResponseUsage(t *testing.T) {
	for _, recover := range []bool{false, true} {
		calls := 0
		c := &Client{Endpoint: "https://model.example/", Model: "test", MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if recover && calls == 2 {
				return jsonResponse(`{"choices":[{"message":{"content":"{\"value\":\"OK\"}"}}],"usage":{"completion_tokens":7}}`), nil
			}
			res := jsonResponse("")
			res.Body = &metricsBrokenBody{body: strings.NewReader(`{"choices":[],"usage":{"completion_tokens":999}}`)}
			return res, nil
		})}}
		var got CallMetrics
		var out struct{ Value string }
		err := c.CompleteObserved(context.Background(), "JSON", nil, &out, func(m CallMetrics) { got = m })
		if calls != 2 || got.RequestAttempts != 2 || got.ResponseDecoded != recover || got.OutputDecoded != recover || (err == nil) != recover {
			t.Fatalf("wrong retry accounting: %+v calls=%d err=%v", got, calls, err)
		}
		if recover {
			if got.Usage == nil || got.Usage.CompletionTokens == nil || *got.Usage.CompletionTokens != 7 {
				t.Fatalf("retry retained earlier usage: %+v", got)
			}
		} else if got.Usage != nil {
			t.Fatal("incomplete wire supplied usage")
		}
	}
}

type metricsBrokenBody struct{ body *strings.Reader }

func (b *metricsBrokenBody) Read(p []byte) (int, error) {
	if b.body.Len() > 0 {
		return b.body.Read(p)
	}
	return 0, syscall.ECONNRESET
}
func (*metricsBrokenBody) Close() error { return nil }

func TestObservedCanceledBackoffAndInvalidInput(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	c := &Client{Endpoint: "https://model.example/", Model: "test", MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		stop()
		return nil, syscall.ECONNRESET
	})}}
	var got []CallMetrics
	var out any
	if c.CompleteObserved(ctx, "JSON", nil, &out, func(m CallMetrics) { got = append(got, m) }) == nil || len(got) != 1 || got[0].RequestAttempts != 1 || got[0].Usage != nil {
		t.Fatalf("cancellation accounting: %+v", got)
	}
	got = nil
	if c.CompleteObserved(context.Background(), "JSON", make(chan int), &out, func(m CallMetrics) { got = append(got, m) }) == nil || len(got) != 1 || got[0].RequestAttempts != 0 || got[0].ResponseDecoded {
		t.Fatalf("preflight accounting: %+v", got)
	}
}

func TestObservedConcurrentCallsAreIsolated(t *testing.T) {
	c := &Client{Endpoint: "https://model.example/", Model: "test", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":4}}`), nil
	})}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got []CallMetrics
			var out any
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := c.CompleteObserved(ctx, "JSON", nil, &out, func(m CallMetrics) { got = append(got, m) }); err != nil || len(got) != 1 || got[0].RequestAttempts != 1 || got[0].Usage == nil || *got[0].Usage.PromptTokens != 4 {
				t.Errorf("concurrent observation: %+v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
}

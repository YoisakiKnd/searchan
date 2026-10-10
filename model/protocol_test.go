package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const chatJSON = `{"choices":[{"message":{"content":"{\"value\":\"ok\"}","reasoning_content":"ignored"},"finish_reason":"stop"}]}`
const responsesJSON = `{"status":"completed","output":[{"type":"reasoning"},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"value\":"},{"type":"output_text","text":"\"ok\"}"}]}]}`
const anthropicJSON = `{"type":"message","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"ignored"},{"type":"text","text":"{\"value\":"},{"type":"text","text":"\"ok\"}"}]}`

func TestProtocolRequestsAndResponses(t *testing.T) {
	for _, tt := range []struct {
		name, endpoint, api, auth, thinking, effort, token, wire string
		budget                                                   int
	}{
		{"openai-official", "https://api.openai.com/v1/chat/completions", "auto", "", "", "low", "", chatJSON, 0},
		{"deepseek-official", "https://api.deepseek.com/chat/completions", "auto", "", "auto", "", "", chatJSON, 0},
		{"generic-gateway", "https://gateway.example/prefix/v1/chat/completions?route=a", "auto", "", "auto", "", "max_completion_tokens", chatJSON, 0},
		{"api-key-gateway", "https://gateway.example/deployments/model/chat/completions?api-version=2024-10-21", "auto", "api-key", "auto", "", "max_completion_tokens", chatJSON, 0},
		{"gateway-deepseek-thinking", "https://gateway.example/chat/completions", "chat_completions", "", "enabled", "low", "", chatJSON, 0},
		{"openai-responses", "https://api.openai.com/v1/responses", "auto", "", "disabled", "", "", responsesJSON, 0},
		{"responses-gateway", "https://gateway.example/custom", "responses", "", "auto", "high", "", responsesJSON, 0},
		{"anthropic-official", "https://api.anthropic.com/v1/messages", "auto", "", "auto", "", "", anthropicJSON, 0},
		{"anthropic-thinking", "https://gateway.example/v1/messages", "auto", "", "enabled", "", "", anthropicJSON, 1200},
		{"anthropic-adaptive", "https://gateway.example/messages/", "auto", "", "adaptive", "low", "", anthropicJSON, 0},
		{"anthropic-bearer-gateway", "https://gateway.example/custom", "anthropic", "bearer", "disabled", "", "", anthropicJSON, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{Endpoint: tt.endpoint, Model: "configured-model", APIKey: "private", API: tt.api, Auth: tt.auth, JSONMode: true, Thinking: tt.thinking, ThinkingBudget: tt.budget, ReasoningEffort: tt.effort, TokenParameter: tt.token}
			c.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != tt.endpoint || r.Method != "POST" {
					t.Fatal("endpoint changed")
				}
				defer r.Body.Close()
				var p map[string]any
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if p["model"] != "configured-model" || p["stream"] != false {
					t.Fatal("invalid model request")
				}
				api, _, _, _ := c.request("", nil)
				if api == "anthropic" {
					if p["system"] != "JSON test" || p["max_tokens"] != float64(2400) || p["response_format"] != nil || p["max_output_tokens"] != nil || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Fatalf("wrong Messages schema: %v", p)
					}
					messages := p["messages"].([]any)
					if len(messages) != 1 || messages[0].(map[string]any)["role"] != "user" {
						t.Fatal("system role sent to Anthropic")
					}
					if tt.auth == "bearer" {
						if r.Header.Get("Authorization") != "Bearer private" || r.Header.Get("x-api-key") != "" {
							t.Fatal("wrong gateway auth")
						}
					} else if r.Header.Get("x-api-key") != "private" || r.Header.Get("Authorization") != "" {
						t.Fatal("wrong Anthropic auth")
					}
					if tt.budget > 0 && p["thinking"].(map[string]any)["budget_tokens"] != float64(tt.budget) {
						t.Fatal("thinking budget omitted")
					}
				} else {
					wantBearer, wantAPIKey := "Bearer private", ""
					if tt.auth == "api-key" {
						wantBearer, wantAPIKey = "", "private"
					}
					if r.Header.Get("Authorization") != wantBearer || r.Header.Get("api-key") != wantAPIKey || r.Header.Get("anthropic-version") != "" || r.Header.Get("x-api-key") != "" {
						t.Fatal("protocol headers leaked")
					}
					if api == "responses" {
						if p["max_output_tokens"] != float64(2400) || p["instructions"] != "JSON test" || p["input"] == nil || p["messages"] != nil || p["max_tokens"] != nil || p["response_format"] != nil || p["store"] != false {
							t.Fatalf("wrong Responses schema: %v", p)
						}
						if p["text"].(map[string]any)["format"].(map[string]any)["type"] != "json_object" {
							t.Fatal("wrong Responses JSON mode")
						}
						if p["thinking"] != nil || p["reasoning_effort"] != nil {
							t.Fatal("chat thinking parameters leaked")
						}
					} else {
						if p["messages"] == nil || p["response_format"].(map[string]any)["type"] != "json_object" || p["max_output_tokens"] != nil {
							t.Fatal("wrong chat schema")
						}
						if tt.name == "openai-official" || tt.token == "max_completion_tokens" {
							if p["max_completion_tokens"] != float64(2400) || p["max_tokens"] != nil {
								t.Fatal("wrong token parameter")
							}
						} else if p["max_tokens"] != float64(2400) {
							t.Fatal("missing max_tokens")
						}
						if tt.name == "deepseek-official" && p["thinking"].(map[string]any)["type"] != "disabled" {
							t.Fatal("DeepSeek thinking still implicit")
						}
						if (tt.name == "openai-official" || tt.name == "generic-gateway") && p["thinking"] != nil {
							t.Fatal("DeepSeek parameter leaked to generic endpoint")
						}
					}
				}
				return jsonResponse(tt.wire), nil
			})}
			var out struct{ Value string }
			if err := c.Complete(context.Background(), "JSON test", map[string]string{"query": "q"}, &out); err != nil || out.Value != "ok" {
				t.Fatalf("protocol failed: %v %+v", err, out)
			}
		})
	}
}

func TestIncompleteOrRefusedOutputNeverSucceeds(t *testing.T) {
	for _, tt := range []struct{ api, wire string }{
		{"responses", `{"status":"incomplete","output":[]}`},
		{"responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","text":"no"}]}]}`},
		{"responses", `{"status":"completed","output":[{"type":"function_call"}]}`},
		{"responses", `{"status":"completed","output":[]}`},
		{"anthropic", `{"stop_reason":"max_tokens","content":[{"type":"text","text":"{}"}]}`},
		{"anthropic", `{"stop_reason":"tool_use","content":[]}`},
		{"anthropic", `{"stop_reason":"end_turn","content":[{"type":"thinking"}]}`},
		{"chat_completions", `{"choices":[{"finish_reason":"content_filter","message":{"content":"{}"}}]}`},
		{"chat_completions", `{"choices":[{"message":{"content":""}}]}`},
		{"chat_completions", `{"error":{"message":"private-upstream-details"}}`},
	} {
		calls := 0
		c := &Client{Endpoint: "https://gateway.example/custom", Model: "m", API: tt.api, MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return jsonResponse(tt.wire), nil })}}
		var out any
		if err := c.Complete(context.Background(), "JSON", nil, &out); err == nil || strings.Contains(err.Error(), "private-upstream-details") || calls != 1 {
			t.Fatalf("invalid response was accepted/retried/leaked: %s, calls=%d, err=%v", tt.wire, calls, err)
		}
	}
}

func TestInvalidProtocolOptionsFailBeforeRequest(t *testing.T) {
	for _, modify := range []func(*Client){
		func(c *Client) { c.API = "unknown" }, func(c *Client) { c.Auth = "unknown" }, func(c *Client) { c.Thinking = "unknown" },
		func(c *Client) { c.MaxRetries = 2 }, func(c *Client) { c.ThinkingBudget = -1 }, func(c *Client) { c.TokenParameter = "unknown" },
		func(c *Client) { c.API = "anthropic"; c.Thinking = "enabled"; c.ThinkingBudget = 2400 },
		func(c *Client) { c.API = "responses"; c.Thinking = "adaptive" },
		func(c *Client) { c.API = "responses"; c.Thinking = "disabled"; c.ReasoningEffort = "high" },
		func(c *Client) { c.Endpoint = "https://api.openai.com/v1/chat/completions"; c.Thinking = "disabled" },
	} {
		c := &Client{Endpoint: "https://gateway.example/custom", Model: "m", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid config caused request"); return nil, nil })}}
		modify(c)
		if err := c.Validate(); err == nil {
			t.Fatalf("invalid options accepted: %+v", c)
		}
	}
}

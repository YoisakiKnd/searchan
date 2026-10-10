package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) request(system string, data []byte) (string, map[string]string, map[string]any, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || c.Model == "" {
		return "", nil, nil, errors.New("model endpoint and name are required")
	}
	api := c.API
	if api == "" || api == "auto" {
		switch {
		case strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/responses"):
			api = "responses"
		case strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/messages"):
			api = "anthropic"
		default:
			api = "chat_completions"
		}
	}
	if api != "chat_completions" && api != "responses" && api != "anthropic" {
		return "", nil, nil, errors.New("MODEL_API must be auto, chat_completions, responses or anthropic")
	}
	if c.MaxRetries < 0 || c.MaxRetries > 1 {
		return "", nil, nil, errors.New("MODEL_RETRIES must be 0 or 1")
	}
	tokens := c.MaxTokens
	if tokens == 0 {
		tokens = 2400
	}
	if tokens < 256 || tokens > 8192 {
		return "", nil, nil, errors.New("MODEL_MAX_TOKENS must be 256-8192")
	}
	thinking := c.Thinking
	if thinking == "auto" || thinking == "" {
		thinking = ""
		if api == "chat_completions" && strings.EqualFold(u.Hostname(), "api.deepseek.com") {
			thinking = "disabled"
		}
	}
	if thinking != "" && thinking != "enabled" && thinking != "disabled" && thinking != "adaptive" {
		return "", nil, nil, errors.New("MODEL_THINKING must be auto, enabled, disabled or adaptive")
	}
	effort := c.ReasoningEffort
	if effort != "" && effort != "none" && effort != "minimal" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max" {
		return "", nil, nil, errors.New("invalid MODEL_REASONING_EFFORT")
	}
	if c.ThinkingBudget != 0 && (api != "anthropic" || thinking != "enabled") {
		return "", nil, nil, errors.New("MODEL_THINKING_BUDGET requires Anthropic enabled thinking")
	}
	auth := c.Auth
	if auth == "" || auth == "auto" {
		auth = "bearer"
		if api == "anthropic" {
			auth = "x-api-key"
		}
	}
	if auth != "bearer" && auth != "x-api-key" && auth != "api-key" {
		return "", nil, nil, errors.New("MODEL_AUTH must be auto, bearer, x-api-key or api-key")
	}
	headers := map[string]string{}
	if c.APIKey != "" {
		if auth == "bearer" {
			headers["Authorization"] = "Bearer " + c.APIKey
		} else {
			headers[auth] = c.APIKey
		}
	}
	payload := map[string]any{"model": c.Model, "stream": false}
	if api != "chat_completions" && c.TokenParameter != "" && c.TokenParameter != "auto" {
		return "", nil, nil, errors.New("MODEL_TOKEN_PARAMETER is only used with chat_completions")
	}
	switch api {
	case "chat_completions":
		payload["messages"] = []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": string(data)}}
		parameter := c.TokenParameter
		if parameter == "" || parameter == "auto" {
			parameter = "max_tokens"
			if strings.EqualFold(u.Hostname(), "api.openai.com") {
				parameter = "max_completion_tokens"
			}
		}
		if parameter != "max_tokens" && parameter != "max_completion_tokens" {
			return "", nil, nil, errors.New("MODEL_TOKEN_PARAMETER must be auto, max_tokens or max_completion_tokens")
		}
		payload[parameter] = tokens
		if c.JSONMode {
			payload["response_format"] = map[string]string{"type": "json_object"}
		}
		if thinking == "adaptive" || (thinking != "" && strings.EqualFold(u.Hostname(), "api.openai.com")) {
			return "", nil, nil, errors.New("this Chat Completions endpoint requires MODEL_REASONING_EFFORT instead of MODEL_THINKING")
		}
		if thinking != "" {
			payload["thinking"] = map[string]string{"type": thinking}
		}
		if effort != "" {
			payload["reasoning_effort"] = effort
		}
	case "responses":
		payload["instructions"], payload["input"], payload["max_output_tokens"] = system, string(data), tokens
		payload["store"] = false
		if c.JSONMode {
			payload["text"] = map[string]any{"format": map[string]string{"type": "json_object"}}
		}
		if thinking == "adaptive" {
			return "", nil, nil, errors.New("adaptive thinking requires Anthropic Messages")
		}
		if thinking == "disabled" {
			if effort != "" && effort != "none" {
				return "", nil, nil, errors.New("disabled thinking conflicts with MODEL_REASONING_EFFORT")
			}
			effort = "none"
		} else if thinking == "enabled" && effort == "" {
			effort = "low"
		}
		if effort != "" {
			payload["reasoning"] = map[string]string{"effort": effort}
		}
	case "anthropic":
		version := c.AnthropicVersion
		if version == "" {
			version = "2023-06-01"
		}
		if len(version) > 32 || strings.ContainsAny(version, "\r\n") {
			return "", nil, nil, errors.New("invalid MODEL_ANTHROPIC_VERSION")
		}
		headers["anthropic-version"] = version
		payload["system"], payload["max_tokens"] = system, tokens
		payload["messages"] = []map[string]string{{"role": "user", "content": string(data)}}
		// Messages has no generic json_object flag. Shared prompts and strict
		// local JSON/citation validation remain required for every response.
		if thinking != "" {
			config := map[string]any{"type": thinking}
			if thinking == "enabled" {
				budget := c.ThinkingBudget
				if budget == 0 {
					budget = 1024
				}
				if budget < 1024 || budget >= tokens {
					return "", nil, nil, errors.New("MODEL_THINKING_BUDGET must be at least 1024 and less than MODEL_MAX_TOKENS")
				}
				config["budget_tokens"] = budget
			}
			payload["thinking"] = config
		}
		if effort != "" {
			payload["output_config"] = map[string]string{"effort": effort}
		}
	}
	return api, headers, payload, nil
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type response struct {
	Error      json.RawMessage `json:"error"`
	Status     string          `json:"status"`
	StopReason string          `json:"stop_reason"`
	Choices    []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Output []struct {
		Type    string         `json:"type"`
		Role    string         `json:"role"`
		Status  string         `json:"status"`
		Content []contentBlock `json:"content"`
	} `json:"output"`
	Content []contentBlock `json:"content"`
}

func (r response) text(api string) (string, error) {
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return "", errors.New("model returned an API error in a successful HTTP response")
	}
	switch api {
	case "chat_completions":
		if len(r.Choices) != 1 {
			return "", errors.New("model returned no unique answer")
		}
		switch r.Choices[0].FinishReason {
		case "", "stop":
		case "length":
			return "", errors.New("model output was truncated; check output token and thinking budgets")
		default:
			return "", errors.New("model did not finish a text answer")
		}
		if strings.TrimSpace(r.Choices[0].Message.Content) == "" {
			return "", errors.New("model returned no answer content; check thinking and output token budgets")
		}
		return r.Choices[0].Message.Content, nil
	case "responses":
		if r.Status != "completed" {
			return "", errors.New("model response incomplete or failed; check output token and thinking budgets")
		}
		var b strings.Builder
		for _, item := range r.Output {
			if item.Type == "reasoning" {
				continue
			}
			if item.Type != "message" || (item.Role != "assistant" && item.Role != "") || (item.Status != "completed" && item.Status != "") {
				return "", errors.New("unexpected Responses output item")
			}
			for _, block := range item.Content {
				if block.Type != "output_text" {
					return "", errors.New("model returned a refusal or unsupported content")
				}
				b.WriteString(block.Text)
			}
		}
		if b.Len() == 0 {
			return "", errors.New("model returned no answer content")
		}
		return b.String(), nil
	case "anthropic":
		if r.StopReason != "end_turn" && r.StopReason != "stop_sequence" {
			return "", fmt.Errorf("Anthropic did not finish a text answer (stop_reason=%.64q)", r.StopReason)
		}
		var b strings.Builder
		for _, block := range r.Content {
			switch block.Type {
			case "thinking", "redacted_thinking":
			case "text":
				b.WriteString(block.Text)
			default:
				return "", errors.New("Anthropic returned unsupported content")
			}
		}
		if b.Len() == 0 {
			return "", errors.New("model returned no answer content")
		}
		return b.String(), nil
	}
	return "", errors.New("unknown model protocol")
}

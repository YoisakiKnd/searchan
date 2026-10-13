package model

import (
	"encoding/json"
	"errors"
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
	effort := c.ReasoningEffort
	if effort != "" && effort != "none" && effort != "minimal" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max" {
		return "", nil, nil, errors.New("invalid MODEL_REASONING_EFFORT")
	}
	deepseekChat := api == "chat_completions" && strings.EqualFold(u.Hostname(), "api.deepseek.com")
	thinking := c.Thinking
	if thinking == "auto" || thinking == "" {
		thinking = ""
		if deepseekChat {
			thinking = "disabled"
			if effort != "" && effort != "none" {
				thinking = "enabled"
			}
		}
	}
	if thinking != "" && thinking != "enabled" && thinking != "disabled" && thinking != "adaptive" {
		return "", nil, nil, errors.New("MODEL_THINKING must be auto, enabled, disabled or adaptive")
	}
	if deepseekChat {
		if thinking == "disabled" && effort != "" && effort != "none" || thinking == "enabled" && effort == "none" {
			return "", nil, nil, errors.New("MODEL_THINKING conflicts with MODEL_REASONING_EFFORT")
		}
		// DeepSeek Chat uses the thinking toggle for none; its effort field
		// controls enabled thinking and does not accept none.
		if effort == "none" {
			effort = ""
		}
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
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Output []struct {
		Type    string         `json:"type"`
		Role    string         `json:"role"`
		Status  string         `json:"status"`
		Content []contentBlock `json:"content"`
	} `json:"output"`
	Content []contentBlock  `json:"content"`
	Usage   json.RawMessage `json:"usage"`
}

type outputUsage struct {
	CompletionTokens  int64 `json:"completion_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// Diagnostics retain only known protocol states and bounded counters. Neither
// arbitrary response strings nor model drafts are copied into task logs/tapes.
func (r response) annotateOutputError(api string, e *OutputError) {
	var usage outputUsage
	// Optional vendor metadata must not make an otherwise valid answer fail.
	if json.Unmarshal(r.Usage, &usage) != nil {
		usage = outputUsage{}
	}
	bounded := func(n int64) int64 {
		if n < 0 || n > 1_000_000 {
			return 0
		}
		return n
	}
	e.finish = "unspecified"
	switch api {
	case "chat_completions":
		e.api = api
		if len(r.Choices) == 1 {
			switch r.Choices[0].FinishReason {
			case "stop", "length", "content_filter", "tool_calls", "function_call":
				e.finish = r.Choices[0].FinishReason
			}
		}
		e.outputTokens = bounded(usage.CompletionTokens)
		e.reasoningTokens = bounded(usage.CompletionDetails.ReasoningTokens)
	case "responses":
		e.api = api
		if r.Status == "completed" || r.Status == "incomplete" || r.Status == "failed" {
			e.finish = r.Status
		}
		e.outputTokens = bounded(usage.OutputTokens)
		e.reasoningTokens = bounded(usage.OutputDetails.ReasoningTokens)
	case "anthropic":
		e.api = api
		switch r.StopReason {
		case "end_turn", "stop_sequence", "max_tokens", "tool_use", "pause_turn", "refusal":
			e.finish = r.StopReason
		}
		e.outputTokens = bounded(usage.OutputTokens)
	}
}

func (r response) text(api string) (string, error) {
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return "", &OutputError{kind: "api", cause: "api_error", form: "unparsed"}
	}
	switch api {
	case "chat_completions":
		if len(r.Choices) != 1 {
			return "", &OutputError{kind: "empty", cause: "answer_count", form: "unparsed"}
		}
		switch r.Choices[0].FinishReason {
		case "", "stop":
		case "length":
			return "", &OutputError{kind: "incomplete", cause: "token_limit", form: "unparsed"}
		case "content_filter":
			return "", &OutputError{kind: "refusal", cause: "content_filter", form: "unparsed"}
		default:
			return "", &OutputError{kind: "protocol", cause: "unsupported_finish", form: "unparsed"}
		}
		// Refusal is independent of finish_reason: a stopped response can
		// still refuse, including gateways returning content alongside it.
		if r.Choices[0].Message.Refusal != "" {
			return "", &OutputError{kind: "refusal", cause: "refusal", form: "unparsed"}
		}
		if strings.TrimSpace(r.Choices[0].Message.Content) == "" {
			return "", &OutputError{kind: "empty", cause: "no_text", form: "empty"}
		}
		return r.Choices[0].Message.Content, nil
	case "responses":
		if r.Status != "completed" {
			return "", &OutputError{kind: "incomplete", cause: "incomplete_status", form: "unparsed"}
		}
		var b strings.Builder
		for _, item := range r.Output {
			if item.Type == "reasoning" {
				continue
			}
			if item.Type != "message" || (item.Role != "assistant" && item.Role != "") || (item.Status != "completed" && item.Status != "") {
				return "", &OutputError{kind: "protocol", cause: "unsupported_output", form: "unparsed"}
			}
			for _, block := range item.Content {
				if block.Type != "output_text" {
					kind := "protocol"
					if block.Type == "refusal" {
						kind = "refusal"
					}
					return "", &OutputError{kind: kind, cause: "unsupported_content", form: "unparsed"}
				}
				b.WriteString(block.Text)
			}
		}
		if b.Len() == 0 {
			return "", &OutputError{kind: "empty", cause: "no_text", form: "empty"}
		}
		return b.String(), nil
	case "anthropic":
		if r.StopReason != "end_turn" && r.StopReason != "stop_sequence" {
			kind, cause := "protocol", "unsupported_finish"
			if r.StopReason == "max_tokens" {
				kind, cause = "incomplete", "token_limit"
			} else if r.StopReason == "refusal" {
				kind, cause = "refusal", "refusal"
			}
			return "", &OutputError{kind: kind, cause: cause, form: "unparsed"}
		}
		var b strings.Builder
		for _, block := range r.Content {
			switch block.Type {
			case "thinking", "redacted_thinking":
			case "text":
				b.WriteString(block.Text)
			default:
				return "", &OutputError{kind: "protocol", cause: "unsupported_content", form: "unparsed"}
			}
		}
		if b.Len() == 0 {
			return "", &OutputError{kind: "empty", cause: "no_text", form: "empty"}
		}
		return b.String(), nil
	}
	return "", errors.New("unknown model protocol")
}

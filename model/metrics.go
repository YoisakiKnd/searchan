package model

import (
	"context"
	"encoding/json"
)

// ObservedCompleter is optional. Normal bot calls use Completer, with no
// collector or additional usage parsing. Observations are synchronous and
// per-call; implementations must not retain the callback after returning.
type ObservedCompleter interface {
	CompleteObserved(context.Context, string, any, any, func(CallMetrics)) error
}

// CallMetrics counts adapter request attempts, not TCP writes, server-received
// requests or billed calls. Transport-internal retries are not observable here.
// Usage belongs only to the last fully decoded response, even if its generated
// answer fails validation. A failed or lost response can still incur charges.
type CallMetrics struct {
	API             string      `json:"api,omitempty"`
	RequestAttempts int         `json:"request_attempts"`
	InputJSONBytes  int         `json:"input_json_bytes"` // Excludes system prompt and protocol envelope.
	ElapsedMS       int64       `json:"elapsed_ms"`
	ResponseDecoded bool        `json:"response_decoded"`
	OutputDecoded   bool        `json:"output_decoded"` // JSON shape only, not factual/citation correctness.
	Usage           *TokenUsage `json:"usage,omitempty"`
}

// TokenUsage preserves protocol-native counters rather than guessing totals or
// prices. Missing/invalid fields stay absent; a reported zero stays present.
// Anthropic input_tokens excludes separately reported cache creation/read tokens.
// OpenAI reasoning/cached details are subsets, not extra total tokens.
// Anthropic cache counters are separate input categories.
type TokenUsage struct {
	PromptTokens             *int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens         *int64 `json:"completion_tokens,omitempty"`
	InputTokens              *int64 `json:"input_tokens,omitempty"`
	OutputTokens             *int64 `json:"output_tokens,omitempty"`
	TotalTokens              *int64 `json:"total_tokens,omitempty"`
	ReasoningTokens          *int64 `json:"reasoning_tokens,omitempty"`
	CachedTokens             *int64 `json:"cached_tokens,omitempty"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens,omitempty"`
}

func reportedCount(n *int64) *int64 {
	if n != nil && (*n < 0 || *n > 1_000_000) {
		return nil
	}
	return n
}

func (r response) tokenUsage(api string) *TokenUsage {
	var wire struct {
		TokenUsage
		CompletionDetails struct {
			ReasoningTokens *int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		OutputDetails struct {
			ReasoningTokens *int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
		PromptDetails struct {
			CachedTokens *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		InputDetails struct {
			CachedTokens *int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	}
	if len(r.Usage) == 0 || string(r.Usage) == "null" || json.Unmarshal(r.Usage, &wire) != nil {
		return nil
	}
	u := &TokenUsage{}
	switch api {
	case "chat_completions":
		u.PromptTokens = reportedCount(wire.PromptTokens)
		u.CompletionTokens = reportedCount(wire.CompletionTokens)
		u.TotalTokens = reportedCount(wire.TotalTokens)
		u.ReasoningTokens = reportedCount(wire.CompletionDetails.ReasoningTokens)
		u.CachedTokens = reportedCount(wire.PromptDetails.CachedTokens)
	case "responses":
		u.InputTokens = reportedCount(wire.InputTokens)
		u.OutputTokens = reportedCount(wire.OutputTokens)
		u.TotalTokens = reportedCount(wire.TotalTokens)
		u.ReasoningTokens = reportedCount(wire.OutputDetails.ReasoningTokens)
		u.CachedTokens = reportedCount(wire.InputDetails.CachedTokens)
	case "anthropic":
		u.InputTokens = reportedCount(wire.InputTokens)
		u.OutputTokens = reportedCount(wire.OutputTokens)
		u.CacheCreationInputTokens = reportedCount(wire.CacheCreationInputTokens)
		u.CacheReadInputTokens = reportedCount(wire.CacheReadInputTokens)
	}
	return u
}

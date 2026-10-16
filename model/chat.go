// Package model implements bounded JSON generation across model protocols.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"searchan/internal/httpx"
)

type Completer interface {
	Complete(context.Context, string, any, any) error
}

type Client struct {
	Endpoint, APIKey, Model string
	API                     string // auto, chat_completions, responses, anthropic
	Auth                    string // auto, bearer, x-api-key, api-key
	AnthropicVersion        string
	MaxTokens               int
	TokenParameter          string // auto, max_tokens, max_completion_tokens (chat only)
	JSONMode                bool
	StructuredOutput        bool   // opt-in native JSON Schema; requires a typed object result
	Thinking                string // auto, enabled, disabled, adaptive (Anthropic only)
	ThinkingBudget          int    // Anthropic enabled mode only
	ReasoningEffort         string // optional; accepted values depend on the model
	MaxRetries              int    // 0 or 1; retries share the original deadline
	Client                  *http.Client
}

// Chat preserves the original library API while Client supports all three protocols.
type Chat = Client

func (c *Client) Validate() error {
	_, _, _, err := c.request("", nil)
	return err
}

func (c *Client) Complete(ctx context.Context, system string, input, out any) error {
	return c.CompleteObserved(ctx, system, input, out, nil)
}

func (c *Client) CompleteObserved(ctx context.Context, system string, input, out any, observe func(CallMetrics)) (result error) {
	var metrics CallMetrics
	var wire response
	if observe != nil {
		started := time.Now()
		defer func() {
			metrics.ElapsedMS = time.Since(started).Milliseconds()
			metrics.OutputDecoded = result == nil
			if metrics.ResponseDecoded {
				metrics.Usage = wire.tokenUsage(metrics.API)
			}
			observe(metrics)
		}()
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, 30*time.Second)
		defer stop()
	}
	data, err := marshalInput(input)
	if err != nil {
		return err
	}
	metrics.InputJSONBytes = len(data)
	if len(data) > 128<<10 {
		return errors.New("model input exceeds 128 KiB")
	}
	var schema *outputSchema
	if c.StructuredOutput {
		schema, err = schemaForOutput(out)
		if err != nil {
			return err
		}
		// Keep optional-content examples from contradicting the native schema's
		// required keys. This does not add facts or relax local validation.
		system += "\n按响应 JSON Schema 输出。顶层必须包含这些字段：" + schema.required + "。无内容的数组填[]，可空字段按schema填null；不省略required字段，不为填字段补充事实。"
	}
	api, headers, payload, err := c.request(system, data)
	if err != nil {
		return err
	}
	metrics.API = api
	if schema != nil {
		applyOutputSchema(api, payload, schema.data)
	}
	client := c.Client
	if client == nil {
		client = defaultClient
	}
	start := time.Now()
	for attempt := 0; ; attempt++ {
		metrics.RequestAttempts++
		err := httpx.JSON(ctx, client, "POST", c.Endpoint, headers, payload, &wire, 256<<10)
		if err == nil {
			metrics.ResponseDecoded = true
			break
		}
		if attempt >= c.MaxRetries || !retryable(ctx, err) || !retryBudget(ctx) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("model HTTP request failed (api=%s, attempts=%d, input_bytes=%d, elapsed=%s): %w", api, attempt+1, len(data), time.Since(start).Round(time.Millisecond), err)
		}
		client.CloseIdleConnections()
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		// A broken response may have partially populated the previous attempt.
		wire = response{}
	}
	content, err := wire.text(api)
	if err != nil {
		var invalid *OutputError
		if errors.As(err, &invalid) {
			wire.annotateOutputError(api, invalid)
		}
		return err
	}
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"))
	}
	err = decodeOutput(content, out)
	if err == nil && schema != nil && !schema.shape.matches(json.RawMessage(content)) {
		cause, field := schema.shape.issue(json.RawMessage(content), "")
		form := "other"
		if strings.HasPrefix(content, "{") {
			form = "object"
		}
		err = &OutputError{kind: "schema", cause: cause, field: allowedOutputField(field), bytes: len(content), form: form}
	}
	var invalid *OutputError
	if errors.As(err, &invalid) {
		wire.annotateOutputError(api, invalid)
	}
	return err
}

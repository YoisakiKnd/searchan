// Package model implements bounded JSON generation across model protocols.
package model

import (
	"context"
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
	if _, bounded := ctx.Deadline(); !bounded {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, 30*time.Second)
		defer stop()
	}
	data, err := marshalInput(input)
	if err != nil {
		return err
	}
	if len(data) > 128<<10 {
		return errors.New("model input exceeds 128 KiB")
	}
	api, headers, payload, err := c.request(system, data)
	if err != nil {
		return err
	}
	client := c.Client
	if client == nil {
		client = defaultClient
	}
	var wire response
	start := time.Now()
	for attempt := 0; ; attempt++ {
		err := httpx.JSON(ctx, client, "POST", c.Endpoint, headers, payload, &wire, 256<<10)
		if err == nil {
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
		return err
	}
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"))
	}
	return httpx.Decode(strings.NewReader(content), 64<<10, out)
}

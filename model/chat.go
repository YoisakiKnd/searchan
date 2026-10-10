// Package model implements a small configurable Chat Completions JSON client.
package model

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"searchan/internal/httpx"
)

type Completer interface {
	Complete(context.Context, string, any, any) error
}
type Chat struct {
	Endpoint, APIKey, Model string
	MaxTokens               int
	JSONMode                bool
	Client                  *http.Client
}

func (c *Chat) Complete(ctx context.Context, system string, input, out any) error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || c.Model == "" {
		return errors.New("model endpoint and name are required")
	}
	var msg strings.Builder
	// Encode with the same strict bounded representation used for test fixtures.
	data, err := marshalInput(input)
	if err != nil {
		return err
	}
	msg.Write(data)
	if msg.Len() > 128<<10 {
		return errors.New("model input exceeds 128 KiB")
	}
	tokens := c.MaxTokens
	if tokens <= 0 {
		tokens = 2400
	}
	payload := map[string]any{"model": c.Model, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": msg.String()}}, "max_tokens": tokens, "stream": false}
	if c.JSONMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	client := c.Client
	if client == nil {
		client = httpx.NewClient(30 * time.Second)
	}
	headers := map[string]string{}
	if c.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.APIKey
	}
	var wire struct {
		Choices []struct {
			Message      struct{ Content string }
			FinishReason string `json:"finish_reason"`
		}
	}
	if err := httpx.JSON(ctx, client, "POST", c.Endpoint, headers, payload, &wire, 256<<10); err != nil {
		return err
	}
	if len(wire.Choices) != 1 || wire.Choices[0].Message.Content == "" {
		return errors.New("model returned no answer")
	}
	if wire.Choices[0].FinishReason == "length" {
		return errors.New("model output was truncated")
	}
	content := strings.TrimSpace(wire.Choices[0].Message.Content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"))
	}
	return httpx.Decode(strings.NewReader(content), 64<<10, out)
}

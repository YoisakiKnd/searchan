package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"searchan/internal/httpx"
)

type telegramEnvelope struct {
	OK         bool            `json:"ok"`
	ErrorCode  int             `json:"error_code"`
	Result     json.RawMessage `json:"result"`
	Parameters struct {
		RetryAfter int64 `json:"retry_after"`
	} `json:"parameters"`
}

func telegramDecode(res *http.Response, out any) error {
	defer res.Body.Close()
	// 5xx and malformed successes have unknown delivery outcomes.
	if res.StatusCode >= 500 {
		return &httpx.StatusError{Code: res.StatusCode}
	}
	var wire telegramEnvelope
	if err := httpx.Decode(res.Body, 64<<10, &wire); err != nil {
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			return platformHTTPError(&httpx.StatusError{Code: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")})
		}
		return err
	}
	if !wire.OK {
		code := wire.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		if code == 429 {
			seconds := wire.Parameters.RetryAfter
			if seconds <= 0 {
				seconds = 1
			}
			seconds = min(seconds, 86400)
			return &RateLimitError{After: time.Duration(seconds) * time.Second}
		}
		if code < 400 || code >= 500 {
			return errors.New("Telegram returned an invalid rejection")
		}
		return &RejectedError{Code: code}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &httpx.StatusError{Code: res.StatusCode}
	}
	if len(wire.Result) == 0 || string(wire.Result) == "null" {
		return errors.New("Telegram response missing result")
	}
	if err := json.Unmarshal(wire.Result, out); err != nil {
		return errors.New("invalid Telegram result")
	}
	return nil
}

func (t *Telegram) call(ctx context.Context, method string, payload, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint(method), bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := t.client().Do(req)
	if err != nil {
		return httpx.TransportError(ctx, err)
	}
	return telegramDecode(res, out)
}

type BotIdentity struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

func (t *Telegram) GetMe(ctx context.Context) (BotIdentity, error) {
	var bot BotIdentity
	err := t.call(ctx, "getMe", struct{}{}, &bot)
	if err == nil && (!bot.IsBot || bot.ID <= 0 || bot.Username == "") {
		err = errors.New("invalid bot identity")
	}
	return bot, err
}

type WebhookInfo struct {
	URL           string `json:"url"`
	Pending       int    `json:"pending_update_count"`
	LastErrorDate int64  `json:"last_error_date,omitempty"`
	LastError     string `json:"last_error_message,omitempty"`
}

func (t *Telegram) WebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	err := t.call(ctx, "getWebhookInfo", struct{}{}, &info)
	return info, err
}
func (t *Telegram) SetWebhook(ctx context.Context, raw string, dropPending bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || !strings.HasSuffix(u.Path, "/webhooks/telegram") {
		return errors.New("webhook must be an HTTPS URL ending in /webhooks/telegram, without credentials, query or fragment")
	}
	if err := t.ValidateSecret(); err != nil {
		return err
	}
	var ok bool
	if err := t.call(ctx, "setWebhook", map[string]any{"url": raw, "secret_token": t.WebhookSecret, "allowed_updates": []string{"message"}, "max_connections": 8, "drop_pending_updates": dropPending}, &ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("Telegram did not confirm webhook registration")
	}
	return nil
}
func (t *Telegram) ValidateSecret() error {
	if len(t.WebhookSecret) < 32 || len(t.WebhookSecret) > 256 {
		return errors.New("TELEGRAM_WEBHOOK_SECRET must contain 32-256 ASCII letters, digits, _ or -")
	}
	for _, r := range t.WebhookSecret {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return errors.New("invalid TELEGRAM_WEBHOOK_SECRET character")
		}
	}
	return nil
}

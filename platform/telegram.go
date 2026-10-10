package platform

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"searchan/internal/httpx"
	"searchan/render"
)

type Telegram struct {
	Token, Endpoint, WebhookSecret string
	Username                       string
	Client                         *http.Client
}

func (t *Telegram) client() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return defaultClient
}
func (t *Telegram) endpoint(method string) string {
	base := t.Endpoint
	if base == "" {
		base = "https://api.telegram.org"
	}
	return strings.TrimRight(base, "/") + "/bot" + t.Token + "/" + method
}

type telegramResponse struct {
	OK        bool
	ErrorCode int `json:"error_code"`
	Result    struct {
		MessageID int64 `json:"message_id"`
	}
}

func telegramReceipt(w telegramResponse) (Receipt, error) {
	if !w.OK {
		return Receipt{}, &RejectedError{Code: w.ErrorCode}
	}
	if w.Result.MessageID == 0 {
		return Receipt{}, errors.New("platform response missing message ID")
	}
	return Receipt{strconv.FormatInt(w.Result.MessageID, 10)}, nil
}

func (t *Telegram) SendText(ctx context.Context, target Target, text string, _ int) (Receipt, error) {
	if t.Token == "" {
		return Receipt{}, &RejectedError{Code: 401}
	}
	if len([]rune(text)) > 3900 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	payload := map[string]any{"chat_id": target.ChatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if target.ThreadID != 0 {
		payload["message_thread_id"] = target.ThreadID
	}
	if id, err := strconv.ParseInt(target.MessageID, 10, 64); err == nil {
		payload["reply_parameters"] = map[string]any{"message_id": id, "allow_sending_without_reply": true}
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	err := t.call(ctx, "sendMessage", payload, &result)
	if err != nil {
		return Receipt{}, platformHTTPError(err)
	}
	return telegramReceipt(telegramResponse{OK: true, Result: result})
}

func platformHTTPError(err error) error {
	var he *httpx.StatusError
	if errors.As(err, &he) && he.Code >= 400 && he.Code < 500 {
		if he.Code == 429 {
			seconds, _ := strconv.Atoi(he.RetryAfter)
			if seconds <= 0 {
				seconds = 1
			}
			seconds = min(seconds, 86400)
			return &RateLimitError{After: time.Duration(seconds) * time.Second}
		}
		return &RejectedError{Code: he.Code}
	}
	return err
}

func (t *Telegram) SendImage(ctx context.Context, target Target, image render.Artifact, caption string, _ int) (Receipt, error) {
	f, err := os.Open(image.Path)
	if err != nil {
		return Receipt{}, &RejectedError{Code: 404}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 8<<20 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	// Prefix + file + suffix streams multipart without a goroutine or whole-image
	// buffer. Only metadata/header bytes are retained in memory.
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)
	_ = writer.WriteField("chat_id", target.ChatID)
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}
	if target.ThreadID != 0 {
		_ = writer.WriteField("message_thread_id", strconv.FormatInt(target.ThreadID, 10))
	}
	if id, err := strconv.ParseInt(target.MessageID, 10, 64); err == nil {
		raw, _ := json.Marshal(map[string]any{"message_id": id, "allow_sending_without_reply": true})
		_ = writer.WriteField("reply_parameters", string(raw))
	}
	if _, err := writer.CreateFormFile("photo", "answer.png"); err != nil {
		return Receipt{}, err
	}
	head := append([]byte(nil), prefix.Bytes()...)
	prefix.Reset()
	if err := writer.Close(); err != nil {
		return Receipt{}, err
	}
	tail := prefix.Bytes()
	body := io.MultiReader(bytes.NewReader(head), io.LimitReader(f, info.Size()), bytes.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint("sendPhoto"), body)
	if err != nil {
		return Receipt{}, &RejectedError{Code: 400}
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = int64(len(head)+len(tail)) + info.Size()
	res, err := t.client().Do(req)
	if err != nil {
		return Receipt{}, httpx.TransportError(ctx, err)
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if err := telegramDecode(res, &result); err != nil {
		return Receipt{}, err
	}
	return telegramReceipt(telegramResponse{OK: true, Result: result})
}

func (t *Telegram) DecodeWebhook(r *http.Request) (Event, error) {
	if t.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(t.WebhookSecret)) != 1 {
		return Event{}, errors.New("invalid Telegram webhook secret")
	}
	var update struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			MessageID int64 `json:"message_id"`
			ThreadID  int64 `json:"message_thread_id"`
			Text      string
			From      struct {
				ID    int64
				IsBot bool `json:"is_bot"`
			}
			Chat struct{ ID int64 }
		} `json:"message"`
	}
	if err := httpx.Decode(r.Body, 64<<10, &update); err != nil {
		return Event{}, err
	}
	if update.Message == nil || update.Message.From.IsBot {
		return Event{}, nil
	}
	m := update.Message
	if m.From.ID == 0 || m.Chat.ID == 0 || m.MessageID <= 0 || update.UpdateID < 0 {
		return Event{}, errors.New("Telegram event missing identity")
	}
	if fields := strings.Fields(m.Text); len(fields) > 0 {
		if _, mention, ok := strings.Cut(fields[0], "@"); ok && (t.Username == "" || !strings.EqualFold(mention, t.Username)) {
			return Event{}, nil
		}
	}
	return Event{ID: fmt.Sprintf("telegram:%d", update.UpdateID), Text: m.Text, Target: Target{Platform: "telegram", ChatID: strconv.FormatInt(m.Chat.ID, 10), UserID: strconv.FormatInt(m.From.ID, 10), MessageID: strconv.FormatInt(m.MessageID, 10), ThreadID: m.ThreadID}}, nil
}

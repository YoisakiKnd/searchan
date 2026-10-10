package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"

	"searchan/internal/httpx"
	"searchan/render"
)

func clipUnits(text string, limit int) string {
	units := 0
	for i, r := range text {
		units += utf16.RuneLen(r)
		if units > limit {
			return text[:i]
		}
	}
	return text
}
func textUnits(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// Link destinations do not consume Telegram's parsed caption limit. Preserve
// EVERY source link; bound labels and repeated prose instead of dropping cites.
func telegramCaption(report Report) string {
	plain := clipUnits(report.Output.Answer.Title, 60) + "\n" + clipUnits(report.Output.Answer.Lead.Text, 220)
	if len(report.Output.Answer.Uncertainties) > 0 {
		plain += "\n" + clipUnits(report.Output.Answer.Uncertainties[0], 100)
	}
	footer := "\n任务：" + clipUnits(report.TaskID, 40)
	var b strings.Builder
	b.WriteString(html.EscapeString(plain))
	sources := report.Output.Sources
	labelBudget := min(32, max(4, (1024-textUnits(plain+footer)-len(sources)*10)/max(1, len(sources))))
	for _, source := range sources {
		fmt.Fprintf(&b, "\n[%d] <a href=\"%s\">%s</a>", source.ID, html.EscapeString(source.URL), html.EscapeString(clipUnits(source.Title, labelBudget)))
	}
	b.WriteString(html.EscapeString(footer))
	return b.String()
}

func (t *Telegram) SupportsReport() bool { return true }
func (t *Telegram) SendReport(ctx context.Context, target Target, report Report, _ int) (Receipt, error) {
	if len(report.Images) < 1 || len(report.Images) > 10 || len(report.Output.Sources) > 24 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	return t.sendPhotos(ctx, target, report.Images, telegramCaption(report), "HTML")
}

func (t *Telegram) sendPhotos(ctx context.Context, target Target, images []render.Artifact, caption, parseMode string) (Receipt, error) {
	if t.Token == "" {
		return Receipt{}, &RejectedError{Code: 401}
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	var sizes []int64
	for _, image := range images {
		f, err := os.Open(image.Path)
		if err != nil {
			return Receipt{}, &RejectedError{Code: 404}
		}
		files = append(files, f)
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return Receipt{}, &RejectedError{Code: 413}
		}
		sizes = append(sizes, info.Size())
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("chat_id", target.ChatID)
	if target.ThreadID != 0 {
		_ = writer.WriteField("message_thread_id", strconv.FormatInt(target.ThreadID, 10))
	}
	if id, err := strconv.ParseInt(target.MessageID, 10, 64); err == nil {
		raw, _ := json.Marshal(map[string]any{"message_id": id, "allow_sending_without_reply": true})
		_ = writer.WriteField("reply_parameters", string(raw))
	}
	method := "sendPhoto"
	if len(images) == 1 {
		_ = writer.WriteField("caption", caption)
		if parseMode != "" {
			_ = writer.WriteField("parse_mode", parseMode)
		}
	} else {
		method = "sendMediaGroup"
		media := make([]map[string]string, len(images))
		for i := range images {
			media[i] = map[string]string{"type": "photo", "media": "attach://photo" + strconv.Itoa(i)}
			if i == 0 {
				media[i]["caption"], media[i]["parse_mode"] = caption, parseMode
			}
		}
		raw, _ := json.Marshal(media)
		_ = writer.WriteField("media", string(raw))
	}
	// Stream file bodies, retaining only multipart headers. No bitmap decoding,
	// base64 copies, pipes, or upload goroutines are needed.
	var readers []io.Reader
	length := int64(0)
	for i, f := range files {
		field := "photo"
		if len(images) > 1 {
			field += strconv.Itoa(i)
		}
		if _, err := writer.CreateFormFile(field, fmt.Sprintf("answer-%d.png", i+1)); err != nil {
			return Receipt{}, err
		}
		head := append([]byte(nil), buf.Bytes()...)
		buf.Reset()
		readers = append(readers, bytes.NewReader(head), io.LimitReader(f, sizes[i]))
		length += int64(len(head)) + sizes[i]
	}
	if err := writer.Close(); err != nil {
		return Receipt{}, err
	}
	readers = append(readers, bytes.NewReader(buf.Bytes()))
	length += int64(buf.Len())
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint(method), io.MultiReader(readers...))
	if err != nil {
		return Receipt{}, &RejectedError{Code: 400}
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = length
	res, err := t.client().Do(req)
	if err != nil {
		return Receipt{}, httpx.TransportError(ctx, err)
	}
	if len(images) == 1 {
		var result struct {
			MessageID int64 `json:"message_id"`
		}
		if err := telegramDecode(res, &result); err != nil {
			return Receipt{}, err
		}
		return telegramReceipt(telegramResponse{OK: true, Result: result})
	}
	var results []struct {
		MessageID int64 `json:"message_id"`
	}
	if err := telegramDecode(res, &results); err != nil {
		return Receipt{}, err
	}
	if len(results) != len(images) {
		return Receipt{}, errors.New("platform album response missing receipts")
	}
	ids := make([]string, len(results))
	for i, r := range results {
		if r.MessageID == 0 {
			return Receipt{}, errors.New("platform album response missing message ID")
		}
		ids[i] = strconv.FormatInt(r.MessageID, 10)
	}
	return Receipt{MessageID: strings.Join(ids, ",")}, nil
}

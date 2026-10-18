package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" // OneBot v11 webhook protocol requires HMAC-SHA1.
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"searchan/internal/httpx"
	"searchan/render"
)

// OneBot uses the v11 HTTP API and the widely implemented custom-node forward
// extension (NapCat/go-cqhttp). It is an explicit alternative to QQ's official API.
type OneBot struct {
	Endpoint, AccessToken, WebhookSecret, SelfID string
	Client                                       *http.Client
}

func positiveID(s string) bool {
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == s
}
func (q *OneBot) Validate() error {
	u, err := url.Parse(q.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("ONEBOT_ENDPOINT must be an HTTP(S) API base URL without credentials or query")
	}
	if q.AccessToken == "" || strings.ContainsAny(q.AccessToken, "\r\n") {
		return errors.New("ONEBOT_ACCESS_TOKEN is required")
	}
	if len(q.WebhookSecret) < 32 || len(q.WebhookSecret) > 256 {
		return errors.New("ONEBOT_WEBHOOK_SECRET must be 32–256 bytes")
	}
	if !positiveID(q.SelfID) {
		return errors.New("ONEBOT_SELF_ID must be the bot's numeric QQ ID")
	}
	return nil
}

type onebotID string

func (id *onebotID) UnmarshalJSON(data []byte) error {
	var s string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	} else {
		s = string(data)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return errors.New("invalid OneBot identity")
	}
	*id = onebotID(s)
	return nil
}

type onebotSegment struct {
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

func textSegment(text string) onebotSegment {
	return onebotSegment{Type: "text", Data: map[string]string{"text": text}}
}

func (q *OneBot) DecodeWebhook(req *http.Request) (Event, error) {
	if len(q.WebhookSecret) < 32 {
		return Event{}, errors.New("OneBot webhook secret is not configured")
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return Event{}, errors.New("invalid OneBot webhook body")
	}
	signature, err := hex.DecodeString(strings.TrimPrefix(req.Header.Get("X-Signature"), "sha1="))
	mac := hmac.New(sha1.New, []byte(q.WebhookSecret))
	mac.Write(raw)
	if err != nil || !strings.HasPrefix(req.Header.Get("X-Signature"), "sha1=") || !hmac.Equal(signature, mac.Sum(nil)) {
		return Event{}, errors.New("invalid OneBot webhook signature")
	}
	var wire struct {
		SelfID      onebotID        `json:"self_id"`
		PostType    string          `json:"post_type"`
		MessageType string          `json:"message_type"`
		MessageID   onebotID        `json:"message_id"`
		GroupID     onebotID        `json:"group_id"`
		UserID      onebotID        `json:"user_id"`
		Message     json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Event{}, errors.New("invalid OneBot event")
	}
	if string(wire.SelfID) != q.SelfID {
		return Event{}, errors.New("OneBot self ID mismatch")
	}
	if wire.PostType != "message" || wire.UserID == wire.SelfID {
		return Event{}, nil
	}
	if !positiveID(string(wire.UserID)) || wire.MessageID == "" || wire.MessageID == "0" {
		return Event{}, errors.New("OneBot event missing identity")
	}
	target := Target{Platform: "qq", UserID: string(wire.UserID), MessageID: string(wire.MessageID), Scope: "c2c", ChatID: string(wire.UserID)}
	switch wire.MessageType {
	case "group":
		if !positiveID(string(wire.GroupID)) {
			return Event{}, errors.New("OneBot group missing identity")
		}
		target.Scope, target.ChatID = "group", string(wire.GroupID)
	case "private":
	default:
		return Event{}, nil
	}
	var text string
	addressed := false
	if len(wire.Message) > 0 && wire.Message[0] == '"' {
		if err := json.Unmarshal(wire.Message, &text); err != nil {
			return Event{}, errors.New("invalid OneBot text")
		}
		// Array format is required for mentions; raw CQ strings are never executed.
		if strings.Contains(text, "[CQ:") {
			return Event{}, nil
		}
	} else {
		var segments []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(wire.Message, &segments); err != nil {
			return Event{}, errors.New("invalid OneBot message segments")
		}
		var b strings.Builder
		mentioned := false
		for _, s := range segments {
			switch s.Type {
			case "at":
				var data struct {
					QQ json.RawMessage `json:"qq"`
				}
				if err := json.Unmarshal(s.Data, &data); err != nil {
					return Event{}, errors.New("invalid OneBot mention")
				}
				var id onebotID
				if err := id.UnmarshalJSON(data.QQ); err != nil || string(id) != q.SelfID {
					return Event{}, nil
				}
				mentioned = true
			case "text":
				var data struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(s.Data, &data); err != nil {
					return Event{}, errors.New("invalid OneBot text segment")
				}
				b.WriteString(data.Text)
			}
		}
		text = b.String()
		addressed = mentioned
	}
	return Event{ID: "qq:onebot:" + q.SelfID + ":" + target.Scope + ":" + target.ChatID + ":" + target.MessageID, Target: target, Text: strings.TrimSpace(text), Addressed: addressed || target.Scope == "c2c"}, nil
}

func (q *OneBot) send(ctx context.Context, method string, body io.Reader, length int64) (Receipt, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(q.Endpoint, "/")+"/"+method, body)
	if err != nil {
		return Receipt{}, &RejectedError{Code: 400}
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+q.AccessToken)
	client := q.Client
	if client == nil {
		client = defaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return Receipt{}, httpx.TransportError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Receipt{}, platformHTTPError(&httpx.StatusError{Code: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")})
	}
	var wire struct {
		Status  string `json:"status"`
		Retcode *int   `json:"retcode"`
		Data    struct {
			MessageID onebotID `json:"message_id"`
		} `json:"data"`
	}
	if err := httpx.Decode(res.Body, 64<<10, &wire); err != nil {
		return Receipt{}, err
	}
	if wire.Status == "async" || wire.Retcode != nil && *wire.Retcode == 1 {
		return Receipt{}, errors.New("OneBot accepted asynchronous delivery without final receipt")
	}
	if wire.Status == "failed" && wire.Retcode != nil {
		// NapCat uses 1200 when sending throws, which can happen after QQ has
		// accepted a message. Only known pre-send refusals permit fallback.
		switch *wire.Retcode {
		case 1400, 1401, 1404:
			return Receipt{}, &RejectedError{Code: *wire.Retcode}
		default:
			return Receipt{}, fmt.Errorf("OneBot delivery outcome unknown (retcode=%d)", *wire.Retcode)
		}
	}
	if wire.Status != "ok" || wire.Retcode == nil || *wire.Retcode != 0 || wire.Data.MessageID == "" || wire.Data.MessageID == "0" {
		return Receipt{}, errors.New("OneBot response missing final receipt")
	}
	return Receipt{MessageID: string(wire.Data.MessageID)}, nil
}

func onebotDestination(target Target, forward bool) (string, map[string]any, error) {
	if !positiveID(target.ChatID) {
		return "", nil, &RejectedError{Code: 400}
	}
	n, _ := strconv.ParseInt(target.ChatID, 10, 64)
	method, key := "send_private", "user_id"
	if target.Scope == "group" {
		method, key = "send_group", "group_id"
	} else if target.Scope != "c2c" {
		return "", nil, &RejectedError{Code: 400}
	}
	if forward {
		method += "_forward_msg"
	} else {
		method += "_msg"
	}
	return method, map[string]any{key: n}, nil
}
func (q *OneBot) SendText(ctx context.Context, target Target, text string, _ int) (Receipt, error) {
	if len([]rune(text)) > 3500 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	method, payload, err := onebotDestination(target, false)
	if err != nil {
		return Receipt{}, err
	}
	payload["message"] = []onebotSegment{textSegment(text)}
	raw, _ := json.Marshal(payload)
	return q.send(ctx, method, bytes.NewReader(raw), int64(len(raw)))
}

func (q *OneBot) SupportsReport() bool { return true }
func (q *OneBot) SendImage(ctx context.Context, target Target, image render.Artifact, caption string, seq int) (Receipt, error) {
	report := Report{Images: []render.Artifact{image}}
	report.Output.Answer.Lead.Text = caption
	return q.SendReport(ctx, target, report, seq)
}

// Fixed-size streaming encoder avoids retaining 4/3-sized image strings or a
// goroutine waiting on an abandoned pipe when a gateway rejects an upload early.
type base64Reader struct {
	source  io.Reader
	raw     [3072]byte
	encoded [4096]byte
	pending []byte
	err     error
}

func (r *base64Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 && r.err == nil {
		n, err := io.ReadFull(r.source, r.raw[:])
		if err == io.ErrUnexpectedEOF {
			err = io.EOF
		}
		r.err = err
		base64.StdEncoding.Encode(r.encoded[:], r.raw[:n])
		r.pending = r.encoded[:base64.StdEncoding.EncodedLen(n)]
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	if n > 0 {
		return n, nil
	}
	return 0, r.err
}

func (q *OneBot) SendReport(ctx context.Context, target Target, report Report, _ int) (Receipt, error) {
	if len(report.Images) < 1 || len(report.Images) > 3 || !positiveID(q.SelfID) {
		return Receipt{}, &RejectedError{Code: 413}
	}
	method, payload, err := onebotDestination(target, true)
	if err != nil {
		return Receipt{}, err
	}
	raw, _ := json.Marshal(payload)
	var readers []io.Reader
	length := int64(0)
	appendText := func(s string) { readers = append(readers, strings.NewReader(s)); length += int64(len(s)) }
	appendText(string(raw[:len(raw)-1]) + `,"messages":[`)
	nodePrefix := fmt.Sprintf(`{"type":"node","data":{"uin":%s,"name":"Searchan","content":`, q.SelfID)
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for i, image := range report.Images {
		f, err := os.Open(image.Path)
		if err != nil {
			return Receipt{}, &RejectedError{Code: 404}
		}
		files = append(files, f)
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return Receipt{}, &RejectedError{Code: 413}
		}
		if i > 0 {
			appendText(",")
		}
		appendText(nodePrefix + `[{"type":"image","data":{"file":"base64://`)
		readers = append(readers, &base64Reader{source: io.LimitReader(f, info.Size())})
		length += int64(base64.StdEncoding.EncodedLen(int(info.Size())))
		appendText(`"}}]}}`)
	}
	var text strings.Builder
	text.WriteString(report.Output.Answer.Lead.Text + "\n")
	for _, s := range report.Output.Sources {
		fmt.Fprintf(&text, "\n[%d] %s\n%s\n", s.ID, s.Title, s.URL)
	}
	for _, s := range report.Output.Answer.Uncertainties {
		text.WriteString("\n" + s)
	}
	text.WriteString("\n任务：" + report.TaskID)
	// Split source notes into bounded nodes inside the SAME forwarded record.
	notes := []rune(text.String())
	for len(notes) > 0 {
		n := min(3000, len(notes))
		content, _ := json.Marshal([]onebotSegment{textSegment(string(notes[:n]))})
		appendText("," + nodePrefix + string(content) + "}}")
		notes = notes[n:]
	}
	appendText("]}")
	return q.send(ctx, method, io.MultiReader(readers...), length)
}

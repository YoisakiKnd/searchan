package platform

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	"sync"
	"time"

	"searchan/internal/httpx"
	"searchan/render"
)

type QQ struct {
	AppID, Secret, Endpoint, TokenEndpoint string
	Client                                 *http.Client
	mu                                     sync.Mutex
	token                                  string
	expires                                time.Time
}

func (q *QQ) client() *http.Client {
	if q.Client != nil {
		return q.Client
	}
	return defaultClient
}
func (q *QQ) accessToken(ctx context.Context) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if time.Now().Before(q.expires.Add(-time.Minute)) && q.token != "" {
		return q.token, nil
	}
	endpoint := q.TokenEndpoint
	if endpoint == "" {
		endpoint = "https://bots.qq.com/app/getAppAccessToken"
	}
	var wire struct {
		Token   string      `json:"access_token"`
		Expires json.Number `json:"expires_in"`
	}
	if err := httpx.JSON(ctx, q.client(), "POST", endpoint, nil, map[string]string{"appId": q.AppID, "clientSecret": q.Secret}, &wire, 64<<10); err != nil {
		return "", err
	}
	if wire.Token == "" {
		return "", errors.New("QQ token response missing token")
	}
	seconds, _ := wire.Expires.Int64()
	if seconds <= 0 {
		seconds = 7200
	}
	q.token = wire.Token
	q.expires = time.Now().Add(time.Duration(seconds) * time.Second)
	return q.token, nil
}
func (q *QQ) path(target Target, kind string) (string, error) {
	if target.ChatID == "" || (target.Scope != "group" && target.Scope != "c2c") {
		return "", &RejectedError{Code: 400}
	}
	segment := "groups"
	if target.Scope == "c2c" {
		segment = "users"
	}
	endpoint := q.Endpoint
	if endpoint == "" {
		endpoint = "https://api.sgroup.qq.com"
	}
	return strings.TrimRight(endpoint, "/") + "/v2/" + segment + "/" + url.PathEscape(target.ChatID) + "/" + kind, nil
}
func (q *QQ) send(ctx context.Context, target Target, payload map[string]any, seq int) (Receipt, error) {
	endpoint, err := q.path(target, "messages")
	if err != nil {
		return Receipt{}, err
	}
	token, err := q.accessToken(ctx)
	if err != nil {
		return Receipt{}, err
	}
	payload["msg_seq"] = seq
	if target.MessageID != "" {
		payload["msg_id"] = target.MessageID
	}
	var wire struct {
		ID   string
		Code int
	}
	if err := httpx.JSON(ctx, q.client(), "POST", endpoint, map[string]string{"Authorization": "QQBot " + token}, payload, &wire, 64<<10); err != nil {
		return Receipt{}, platformHTTPError(err)
	}
	if wire.Code != 0 {
		return Receipt{}, &RejectedError{Code: wire.Code}
	}
	if wire.ID == "" {
		return Receipt{}, errors.New("QQ response missing message ID")
	}
	return Receipt{wire.ID}, nil
}
func (q *QQ) SendText(ctx context.Context, target Target, text string, seq int) (Receipt, error) {
	if len([]rune(text)) > 3500 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	return q.send(ctx, target, map[string]any{"msg_type": 0, "content": text}, seq)
}
func (q *QQ) SendImage(ctx context.Context, target Target, artifact render.Artifact, caption string, seq int) (Receipt, error) {
	endpoint, err := q.path(target, "files")
	if err != nil {
		return Receipt{}, err
	}
	token, err := q.accessToken(ctx)
	if err != nil {
		return Receipt{}, err
	}
	f, err := os.Open(artifact.Path)
	if err != nil {
		return Receipt{}, &RejectedError{Code: 404}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 4<<20 {
		return Receipt{}, &RejectedError{Code: 413}
	}
	// JSON base64 is streamed from the file, avoiding the usual file + encoded
	// string + JSON triple allocation. Chunked uploads are unnecessary for cards.
	pr, pw := io.Pipe()
	finished := make(chan error, 1)
	go func() {
		_, err := io.WriteString(pw, `{"file_type":1,"srv_send_msg":false,"file_data":"`)
		if err == nil {
			enc := base64.NewEncoder(base64.StdEncoding, pw)
			_, err = io.Copy(enc, io.LimitReader(f, info.Size()))
			closeErr := enc.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err == nil {
			_, err = io.WriteString(pw, `"}`)
		}
		pw.CloseWithError(err)
		finished <- err
	}()
	defer func() { pr.Close(); <-finished }()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, pr)
	if err != nil {
		return Receipt{}, err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(`{"file_type":1,"srv_send_msg":false,"file_data":"`)+len(`"}`)) + int64(base64.StdEncoding.EncodedLen(int(info.Size())))
	res, err := q.client().Do(req)
	if err != nil {
		return Receipt{}, httpx.TransportError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Receipt{}, platformHTTPError(&httpx.StatusError{Code: res.StatusCode})
	}
	var upload struct {
		Info string `json:"file_info"`
		Code int
	}
	if err := httpx.Decode(res.Body, 64<<10, &upload); err != nil {
		return Receipt{}, err
	}
	if upload.Code != 0 || upload.Info == "" {
		return Receipt{}, &RejectedError{Code: upload.Code}
	}
	return q.send(ctx, target, map[string]any{"msg_type": 7, "media": map[string]string{"file_info": upload.Info}, "content": caption}, seq)
}

func (q *QQ) privateKey() (ed25519.PrivateKey, error) {
	if q.Secret == "" {
		return nil, errors.New("QQ secret is missing")
	}
	seed := q.Secret
	for len(seed) < 32 {
		seed += seed
	}
	return ed25519.NewKeyFromSeed([]byte(seed[:32])), nil
}

// DecodeWebhook returns a validation/ACK object plus an optional message event.
// Signature seed and payloads follow the official QQ SDK protocol.
func (q *QQ) DecodeWebhook(r *http.Request) (Event, any, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return Event{}, nil, errors.New("oversized QQ event")
	}
	var payload struct {
		ID   string
		Op   int
		Type string          `json:"t"`
		Data json.RawMessage `json:"d"`
	}
	if err := httpx.Decode(bytes.NewReader(raw), 64<<10, &payload); err != nil {
		return Event{}, nil, err
	}
	key, err := q.privateKey()
	if err != nil {
		return Event{}, nil, err
	}
	if payload.Op == 13 {
		var challenge struct {
			Token string `json:"plain_token"`
			TS    string `json:"event_ts"`
		}
		if err := json.Unmarshal(payload.Data, &challenge); err != nil || challenge.Token == "" || challenge.TS == "" {
			return Event{}, nil, errors.New("invalid QQ validation")
		}
		// URL validation is unsigned. Never expose a signing oracle for arbitrary
		// event JSON: platform challenges are short ASCII tokens with numeric time.
		if len(challenge.Token) > 256 || len(challenge.TS) > 20 {
			return Event{}, nil, errors.New("invalid QQ challenge format")
		}
		if seconds, err := strconv.ParseInt(challenge.TS, 10, 64); err != nil || seconds <= 0 {
			return Event{}, nil, errors.New("invalid QQ challenge timestamp")
		}
		for _, c := range challenge.Token {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
				return Event{}, nil, errors.New("invalid QQ challenge token")
			}
		}
		sig := ed25519.Sign(key, []byte(challenge.TS+challenge.Token))
		return Event{}, map[string]string{"plain_token": challenge.Token, "signature": hex.EncodeToString(sig)}, nil
	}
	timestamp := r.Header.Get("X-Signature-Timestamp")
	signature, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	if err != nil || timestamp == "" || !ed25519.Verify(key.Public().(ed25519.PublicKey), append([]byte(timestamp), raw...), signature) {
		return Event{}, nil, errors.New("invalid QQ signature")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return Event{}, nil, errors.New("stale QQ signature")
	}
	ack := map[string]any{"op": 12, "d": 0}
	if payload.Op != 0 {
		return Event{}, ack, nil
	}
	if payload.Type != "GROUP_AT_MESSAGE_CREATE" && payload.Type != "GROUP_MESSAGE_CREATE" && payload.Type != "C2C_MESSAGE_CREATE" {
		return Event{}, ack, nil
	}
	var msg struct {
		ID, Content string
		Group       string `json:"group_openid"`
		Author      struct {
			ID     string
			Member string `json:"member_openid"`
			User   string `json:"user_openid"`
		}
	}
	if err := json.Unmarshal(payload.Data, &msg); err != nil {
		return Event{}, nil, err
	}
	scope, chat, user := "group", msg.Group, msg.Author.Member
	if payload.Type == "C2C_MESSAGE_CREATE" {
		scope = "c2c"
		chat = msg.Author.User
		user = chat
		if chat == "" {
			chat = msg.Author.ID
			user = chat
		}
	}
	if msg.ID == "" || chat == "" {
		return Event{}, nil, fmt.Errorf("QQ event missing message identity")
	}
	return Event{ID: "qq:" + msg.ID, Text: msg.Content, Addressed: payload.Type != "GROUP_MESSAGE_CREATE", Target: Target{Platform: "qq", Scope: scope, ChatID: chat, UserID: user, MessageID: msg.ID}}, ack, nil
}

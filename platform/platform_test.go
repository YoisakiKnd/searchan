package platform

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/render"
)

func TestTelegramStreamAndWebhook(t *testing.T) {
	image := bytes.Repeat([]byte("image"), 30000)
	path := filepath.Join(t.TempDir(), "answer.png")
	if err := os.WriteFile(path, image, 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "sendPhoto") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, image) || r.FormValue("chat_id") != "123" || r.FormValue("message_thread_id") != "5" {
				t.Error("bad multipart payload")
			}
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":456}}`)
	}))
	defer srv.Close()
	bot := &Telegram{Token: "secret", Endpoint: srv.URL, WebhookSecret: "webhook", Client: srv.Client()}
	target := Target{ChatID: "123", MessageID: "1", ThreadID: 5}
	if receipt, err := bot.SendImage(context.Background(), target, render.Artifact{Path: path}, "caption", 1); err != nil || receipt.MessageID != "456" {
		t.Fatalf("upload failed: %+v %v", receipt, err)
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"update_id":11,"message":{"message_id":1,"message_thread_id":5,"text":"/search Go","from":{"id":2},"chat":{"id":123}}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
	event, err := bot.DecodeWebhook(req)
	if err != nil || event.Target.ThreadID != 5 || event.ID != "telegram:11" || event.Addressed {
		t.Fatalf("bad webhook: %+v %v", event, err)
	}
	req = httptest.NewRequest("POST", "/", strings.NewReader(`{"update_id":12,"message":{"message_id":2,"text":"Go context","from":{"id":2},"chat":{"id":2,"type":"private"}}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
	if event, err := bot.DecodeWebhook(req); err != nil || !event.Addressed {
		t.Fatalf("private chat not addressed: %+v %v", event, err)
	}
	req = httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	if _, err := bot.DecodeWebhook(req); err == nil {
		t.Fatal("unauthenticated webhook accepted")
	}
}

func TestQQUploadTokenCacheAndSignature(t *testing.T) {
	var tokenCalls atomic.Int32
	rawImage := []byte("small image payload")
	path := filepath.Join(t.TempDir(), "answer.png")
	_ = os.WriteFile(path, rawImage, 0600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenCalls.Add(1)
			fmt.Fprint(w, `{"access_token":"token","expires_in":7200}`)
		case strings.HasSuffix(r.URL.Path, "/files"):
			if r.Header.Get("Authorization") != "QQBot token" {
				t.Error("wrong authorization")
			}
			var body struct {
				Data string `json:"file_data"`
				Send bool   `json:"srv_send_msg"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			got, err := base64.StdEncoding.DecodeString(body.Data)
			if err != nil || !bytes.Equal(got, rawImage) || body.Send {
				t.Error("invalid streamed base64 JSON")
			}
			fmt.Fprint(w, `{"file_info":"uploaded"}`)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["msg_id"] != "inbound" || body["msg_seq"] != float64(2) {
				t.Errorf("wrong reply identity: %v", body)
			}
			fmt.Fprint(w, `{"id":"outbound"}`)
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	bot := &QQ{AppID: "app", Secret: "secret", Endpoint: srv.URL, TokenEndpoint: srv.URL + "/token", Client: srv.Client()}
	target := Target{Scope: "group", ChatID: "group", MessageID: "inbound"}
	if _, err := bot.SendImage(context.Background(), target, render.Artifact{Path: path}, "caption", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.SendText(context.Background(), target, "text", 2); err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 1 {
		t.Fatal("token was not reused")
	}
	payload := []byte(`{"op":0,"t":"GROUP_AT_MESSAGE_CREATE","d":{"id":"inbound","group_openid":"group","author":{"member_openid":"user"},"content":"<@bot> /search Go"}}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := bot.privateKey()
	sig := ed25519.Sign(key, append([]byte(ts), payload...))
	req := httptest.NewRequest("POST", "/", bytes.NewReader(payload))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	event, ack, err := bot.DecodeWebhook(req)
	if err != nil || event.Target.ChatID != "group" || event.Target.UserID != "user" || ack == nil {
		t.Fatalf("bad signed event: %+v %v", event, err)
	}
	forged := httptest.NewRequest("POST", "/", bytes.NewReader(payload))
	forged.Header.Set("X-Signature-Timestamp", ts)
	forged.Header.Set("X-Signature-Ed25519", strings.Repeat("00", 64))
	if _, _, err := bot.DecodeWebhook(forged); err == nil {
		t.Fatal("forged signature accepted")
	}
	validation := httptest.NewRequest("POST", "/", strings.NewReader(`{"op":13,"d":{"plain_token":"challenge","event_ts":"123"}}`))
	_, response, err := bot.DecodeWebhook(validation)
	if err != nil {
		t.Fatal(err)
	}
	obj := response.(map[string]string)
	signed, _ := hex.DecodeString(obj["signature"])
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), []byte("123challenge"), signed) {
		t.Fatal("invalid validation response")
	}
	attack, _ := json.Marshal(map[string]any{"op": 13, "d": map[string]string{"plain_token": string(payload), "event_ts": ts}})
	if _, _, err := bot.DecodeWebhook(httptest.NewRequest("POST", "/", bytes.NewReader(attack))); err == nil {
		t.Fatal("unsigned validation can sign arbitrary event JSON")
	}
}

func TestCommandParsing(t *testing.T) {
	for _, text := range []string{"/search 中文", "/search@mybot 中文", "<@123> /search 中文"} {
		cmd, ok := ParseCommand(text)
		if !ok || cmd.Name != "search" || cmd.Argument != "中文" {
			t.Fatalf("bad command: %+v", cmd)
		}
	}
	if _, ok := ParseCommand("normal group message"); ok {
		t.Fatal("unsolicited trigger")
	}
}

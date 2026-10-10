package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"searchan/demo"
	"searchan/render"
	"searchan/research"
)

func reportFixture(t *testing.T, pages int) Report {
	t.Helper()
	r := Report{Output: demo.Output(), TaskID: strings.Repeat("a", 32)}
	for i := 0; i < pages; i++ {
		p := filepath.Join(t.TempDir(), "answer.png")
		if err := os.WriteFile(p, bytes.Repeat([]byte{byte(i), 10, 255}, 1100), 0600); err != nil {
			t.Fatal(err)
		}
		r.Images = append(r.Images, render.Artifact{Path: p})
	}
	return r
}

func TestTelegramReportStreamsOnePhotoOrAlbumWithSources(t *testing.T) {
	for _, pages := range []int{1, 3} {
		t.Run(fmt.Sprint(pages), func(t *testing.T) {
			r := reportFixture(t, pages)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if err := req.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer req.MultipartForm.RemoveAll()
				if req.FormValue("chat_id") != "123" || req.FormValue("message_thread_id") != "7" || !strings.Contains(req.FormValue("reply_parameters"), `"message_id":9`) {
					t.Error("routing lost")
				}
				caption, mode := req.FormValue("caption"), req.FormValue("parse_mode")
				if pages > 1 {
					if !strings.HasSuffix(req.URL.Path, "/sendMediaGroup") {
						t.Error("album not sent as one API request")
					}
					var media []map[string]string
					if err := json.Unmarshal([]byte(req.FormValue("media")), &media); err != nil || len(media) != pages {
						t.Error("invalid album metadata")
						return
					}
					caption, mode = media[0]["caption"], media[0]["parse_mode"]
					for i, m := range media {
						if m["media"] != fmt.Sprintf("attach://photo%d", i) || i > 0 && m["caption"] != "" {
							t.Error("bad caption or attachment")
						}
					}
				} else if !strings.HasSuffix(req.URL.Path, "/sendPhoto") {
					t.Error("single photo not used")
				}
				if mode != "HTML" {
					t.Error("linked caption missing HTML mode")
				}
				for _, s := range r.Output.Sources {
					if !strings.Contains(caption, html.EscapeString(s.URL)) {
						t.Error("missing source link")
					}
				}
				for i, image := range r.Images {
					field := "photo"
					if pages > 1 {
						field = fmt.Sprintf("photo%d", i)
					}
					f, _, err := req.FormFile(field)
					if err != nil {
						t.Error(err)
						return
					}
					got, _ := io.ReadAll(f)
					f.Close()
					want, _ := os.ReadFile(image.Path)
					if !bytes.Equal(got, want) {
						t.Error("stream corrupted image")
					}
				}
				if pages == 1 {
					fmt.Fprint(w, `{"ok":true,"result":{"message_id":101}}`)
				} else {
					fmt.Fprint(w, `{"ok":true,"result":[{"message_id":101},{"message_id":102},{"message_id":103}]}`)
				}
			}))
			defer srv.Close()
			bot := RetrySender{Next: &Telegram{Token: "test", Endpoint: srv.URL, Client: srv.Client()}}
			got, err := bot.SendReport(context.Background(), Target{ChatID: "123", MessageID: "9", ThreadID: 7}, r, 2)
			want := "101"
			if pages > 1 {
				want = "101,102,103"
			}
			if err != nil || got.MessageID != want || !bot.SupportsReport() {
				t.Fatalf("receipt=%+v err=%v", got, err)
			}
		})
	}
}

func TestCaptionBoundsEscapingAndAllLinks(t *testing.T) {
	r := reportFixture(t, 1)
	r.Output.Answer.Title = strings.Repeat("🦊<&>", 50)
	r.Output.Answer.Lead.Text = strings.Repeat("🦊中文<&>", 100)
	r.Output.Answer.Uncertainties = []string{strings.Repeat("限制🦊", 100)}
	r.Output.Sources = nil
	for i := 1; i <= 24; i++ {
		r.Output.Sources = append(r.Output.Sources, research.Source{ID: i, Title: strings.Repeat("资料🦊<&>", 30), URL: fmt.Sprintf("https://example.org/%d?a=1&b=2", i)})
	}
	caption := telegramCaption(r)
	plain := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(caption, ""))
	if textUnits(plain) > 1024 || strings.Contains(caption, "🦊<&>") || strings.Count(caption, `<a href=`) != 24 {
		t.Fatalf("bad caption units=%d links=%d", textUnits(plain), strings.Count(caption, `<a href=`))
	}
}

func signedOneBotRequest(q *OneBot, body string) *http.Request {
	req := httptest.NewRequest("POST", "/webhooks/onebot", strings.NewReader(body))
	mac := hmac.New(sha1.New, []byte(q.WebhookSecret))
	mac.Write([]byte(body))
	req.Header.Set("X-Signature", "sha1="+hex.EncodeToString(mac.Sum(nil)))
	return req
}
func TestOneBotSignedEventsAndLiteralSegments(t *testing.T) {
	q := &OneBot{SelfID: "12345", WebhookSecret: strings.Repeat("s", 32)}
	body := `{"self_id":12345,"post_type":"message","message_type":"group","group_id":88,"user_id":"99","message_id":-11,"message":[{"type":"at","data":{"qq":"12345"}},{"type":"text","data":{"text":" /search SpringData简介"}}]}`
	e, err := q.DecodeWebhook(signedOneBotRequest(q, body))
	if err != nil || e.Target.ChatID != "88" || e.Target.UserID != "99" || e.Target.Scope != "group" || e.Text != "/search SpringData简介" {
		t.Fatalf("bad event %+v %v", e, err)
	}
	req := signedOneBotRequest(q, body)
	req.Header.Set("X-Signature", "sha1=00")
	if _, err := q.DecodeWebhook(req); err == nil {
		t.Fatal("invalid signature accepted")
	}
	for _, body := range []string{strings.Replace(body, `"qq":"12345"`, `"qq":"67890"`, 1), strings.Replace(body, `"user_id":"99"`, `"user_id":"12345"`, 1)} {
		if e, err := q.DecodeWebhook(signedOneBotRequest(q, body)); err != nil || e.ID != "" {
			t.Fatalf("wrong mention/self loop accepted: %+v %v", e, err)
		}
	}
	if _, err := q.DecodeWebhook(signedOneBotRequest(q, strings.Replace(body, `"self_id":12345`, `"self_id":67890`, 1))); err == nil {
		t.Fatal("wrong bot ID accepted")
	}
}

func TestOneBotForwardStreamsImagesAndSources(t *testing.T) {
	for _, scope := range []string{"group", "c2c"} {
		t.Run(scope, func(t *testing.T) {
			r := reportFixture(t, 3)
			r.Output.Answer.Lead.Text = `Literal [CQ:at,qq=all] <content>`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				want := "/send_group_forward_msg"
				if scope == "c2c" {
					want = "/send_private_forward_msg"
				}
				if req.URL.Path != want || req.Header.Get("Authorization") != "Bearer token" {
					t.Error("bad forward routing/auth")
				}
				var payload struct {
					Messages []struct {
						Type string
						Data struct {
							Uin     int64
							Name    string
							Content []onebotSegment
						}
					}
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				if len(payload.Messages) != 4 {
					t.Errorf("nodes=%d", len(payload.Messages))
					return
				}
				for i, node := range payload.Messages {
					if node.Type != "node" || node.Data.Uin != 12345 || node.Data.Name != "Searchan" {
						t.Error("invalid sender node")
					}
					if i < 3 {
						got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(node.Data.Content[0].Data["file"], "base64://"))
						want, _ := os.ReadFile(r.Images[i].Path)
						if err != nil || !bytes.Equal(got, want) {
							t.Error("base64 stream corrupted")
						}
					} else {
						if node.Data.Content[0].Type != "text" || !strings.Contains(node.Data.Content[0].Data["text"], "[CQ:at,qq=all]") {
							t.Error("raw CQ data not kept literal")
						}
						for _, source := range r.Output.Sources {
							if !strings.Contains(node.Data.Content[0].Data["text"], source.URL) {
								t.Error("source lost")
							}
						}
					}
				}
				fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{"message_id":-55}}`)
			}))
			defer srv.Close()
			q := &OneBot{Endpoint: srv.URL, AccessToken: "token", SelfID: "12345", Client: srv.Client()}
			if got, err := q.SendReport(context.Background(), Target{Scope: scope, ChatID: "88"}, r, 2); err != nil || got.MessageID != "-55" {
				t.Fatalf("got %+v %v", got, err)
			}
		})
	}
}

func TestBase64ReaderBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 3071, 3072, 3073, 65536} {
		input := bytes.Repeat([]byte{255}, size)
		r := &base64Reader{source: bytes.NewReader(input)}
		var got bytes.Buffer
		buf := make([]byte, 7)
		if _, err := io.CopyBuffer(&got, r, buf); err != nil {
			t.Fatal(err)
		}
		if got.String() != base64.StdEncoding.EncodeToString(input) {
			t.Fatalf("bad base64 size=%d", size)
		}
	}
}

func TestOneBotAmbiguousAndRejectedReceipts(t *testing.T) {
	for _, body := range []string{`{"status":"async","retcode":1}`, `{"status":"ok","retcode":0,"data":{}}`, `{"status":"failed","retcode":1400}`, `{"status":"failed","retcode":1200}`, `{"status":"failed","retcode":100}`, `{"status":"ok","data":{"message_id":55}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		q := &OneBot{Endpoint: srv.URL, Client: srv.Client()}
		_, err := q.SendText(context.Background(), Target{Scope: "group", ChatID: "88"}, "test", 1)
		srv.Close()
		if err == nil || Definite(err) != strings.Contains(body, `1400`) {
			t.Fatalf("wrong outcome %s: %v", body, err)
		}
	}
}

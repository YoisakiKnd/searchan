package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"searchan/platform"
	"searchan/task"
)

type bundledSender struct {
	*testSender
	reports int
	err     error
}

func (*bundledSender) SupportsReport() bool { return true }
func (s *bundledSender) SendReport(_ context.Context, _ platform.Target, r platform.Report, _ int) (platform.Receipt, error) {
	s.reports++
	if len(r.Images) == 0 || len(r.Output.Sources) == 0 {
		return platform.Receipt{}, errors.New("missing report data")
	}
	return platform.Receipt{MessageID: "101,102"}, s.err
}

func TestReportOutboxAndUpgradeResume(t *testing.T) {
	for _, mode := range []string{"ok", "rejected", "ambiguous", "old-page", "already-sent"} {
		t.Run(mode, func(t *testing.T) {
			s, payload := setup(t, &testProvider{}, &testSender{}, testRenderer{})
			sender := &bundledSender{testSender: &testSender{}}
			if mode == "rejected" {
				sender.err = &platform.RejectedError{Code: 400}
			}
			if mode == "ambiguous" {
				sender.err = context.DeadlineExceeded
			}
			s.Runtime.Senders["telegram"] = platform.RetrySender{Next: sender}
			if err := s.Accept(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok {
				t.Fatal(err)
			}
			if mode == "old-page" {
				rec.Deliveries = []task.Delivery{{Kind: "image", Page: 1, State: "sent", Receipt: "1"}}
			}
			if mode == "already-sent" {
				rec.Deliveries = []task.Delivery{{Kind: "report", State: "sent", Receipt: "101,102"}}
			}
			s.process(context.Background(), rec)
			stored, err := s.Store.Get(rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ambiguous" {
				if stored.State != "uncertain" || sender.reports != 1 || len(sender.texts) != 1 {
					t.Fatalf("ambiguous report retried: %+v", stored)
				}
				s.Close()
				reopened, err := task.Open(s.Store.Dir, 4)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if _, ok, _ := reopened.Claim(); ok {
					t.Fatal("uncertain report queued on restart")
				}
				return
			}
			if stored.State != "completed" {
				t.Fatalf("state=%s", stored.State)
			}
			wantReports := 1
			if mode == "old-page" || mode == "already-sent" {
				wantReports = 0
			}
			if sender.reports != wantReports || sender.images != 0 {
				t.Fatalf("duplicate delivery: reports=%d images=%d", sender.reports, sender.images)
			}
			if mode == "ok" && len(sender.texts) != 1 {
				t.Fatal("sources split into another message")
			}
			if mode == "rejected" && len(sender.texts) != 2 {
				t.Fatal("definite rejection did not fall back to text")
			}
		})
	}
}

func TestOneBotWebhookAccessAndDeduplication(t *testing.T) {
	s, _ := setup(t, &testProvider{}, &testSender{}, testRenderer{})
	q := &platform.OneBot{SelfID: "12345", WebhookSecret: strings.Repeat("s", 32)}
	s.Runtime.OneBot = q
	s.Runtime.Senders["qq"] = &testSender{}
	s.Runtime.Config.AllowPublic = false
	s.Runtime.Config.AllowedChats = map[string]bool{"qq:88": true}
	body := `{"self_id":12345,"post_type":"message","message_type":"group","group_id":88,"user_id":99,"message_id":10,"message":[{"type":"text","data":{"text":"/search SpringData简介"}}]}`
	post := func(body string, signed bool, want int) {
		r := httptest.NewRequest("POST", "/webhooks/onebot", strings.NewReader(body))
		if signed {
			mac := hmac.New(sha1.New, []byte(q.WebhookSecret))
			mac.Write([]byte(body))
			r.Header.Set("X-Signature", "sha1="+hex.EncodeToString(mac.Sum(nil)))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("code=%d want=%d", w.Code, want)
		}
	}
	post(body, false, http.StatusUnauthorized)
	post(strings.Replace(body, `"group_id":88`, `"group_id":77`, 1), true, http.StatusNoContent)
	if s.Store.Pending() != 0 {
		t.Fatal("non-allowed group queued paid work")
	}
	post(body, true, http.StatusNoContent)
	post(body, true, http.StatusNoContent)
	if s.Store.Pending() != 1 {
		t.Fatal("duplicate event queued twice")
	}
}

func TestOneBotConfigurationIsExplicitAndValidated(t *testing.T) {
	valid := map[string]string{"QQ_BACKEND": "onebot", "ONEBOT_ENDPOINT": "http://127.0.0.1:3000", "ONEBOT_ACCESS_TOKEN": "token", "ONEBOT_WEBHOOK_SECRET": strings.Repeat("s", 32), "ONEBOT_SELF_ID": "12345"}
	for _, bad := range []string{"", "QQ_BACKEND", "ONEBOT_ENDPOINT", "ONEBOT_ACCESS_TOKEN", "ONEBOT_WEBHOOK_SECRET", "ONEBOT_SELF_ID"} {
		env := map[string]string{}
		for k, v := range valid {
			env[k] = v
		}
		if bad != "" {
			env[bad] = "invalid"
		}
		if bad == "ONEBOT_ACCESS_TOKEN" {
			env[bad] = ""
		}
		r, err := New(func(k string) string { return env[k] })
		if bad == "" {
			if err != nil || r.OneBot == nil || r.QQ != nil {
				t.Fatalf("bad runtime %v", err)
			}
			r.Close()
		} else {
			if r != nil {
				r.Close()
			}
			if err == nil {
				t.Fatalf("invalid %s accepted", bad)
			}
		}
	}
}

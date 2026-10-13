package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"searchan/platform"
)

func TestIncompleteChatCommandsGetDeduplicatedGuidance(t *testing.T) {
	for _, text := range []string{"/search", "/deep", "/search " + strings.Repeat("长", 1400), "/status", "/sources missing", "/cancel invalid"} {
		t.Run(text[:min(len(text), 30)], func(t *testing.T) {
			p := &testProvider{}
			sender := &testSender{}
			s, event := setup(t, p, sender, testRenderer{})
			event.Text = text
			for range 2 {
				s.last = map[string]time.Time{}
				if err := s.Accept(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			if len(sender.texts) != 1 || sender.texts[0] == "" || s.Store.Pending() != 0 || p.calls.Load() != 0 {
				t.Fatalf("silent or repeated invalid command: texts=%v pending=%d", sender.texts, s.Store.Pending())
			}
		})
	}
}

func TestTelegramHelpOffersExplicitCommandWhenGroupMentionIsNotDelivered(t *testing.T) {
	p := &testProvider{}
	sender := &testSender{}
	s, event := setup(t, p, sender, testRenderer{})
	s.Runtime.Telegram = &platform.Telegram{Username: "fixture_bot"}
	event.Text = "/help"
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 1 || !strings.Contains(sender.texts[0], "@fixture_bot 自然语言") || !strings.Contains(sender.texts[0], "若群里的 @提问没有回应") || !strings.Contains(sender.texts[0], "/search@fixture_bot") || p.calls.Load() != 0 || s.Store.Pending() != 0 {
		t.Fatal("missing mention or command guidance", sender.texts)
	}
}

func TestQuotaRejectionCannotBecomeSearchOnWebhookRetry(t *testing.T) {
	for _, fullQueue := range []bool{false, true} {
		p := &testProvider{}
		sender := &testSender{}
		s, event := setup(t, p, sender, testRenderer{})
		s.Runtime.Config.MaxUserTasks = 1
		if fullQueue {
			s.Store.Capacity = 1
		}
		if err := s.Accept(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		rejected := event
		rejected.ID = "rejected-event"
		s.last = map[string]time.Time{}
		if err := s.Accept(context.Background(), rejected); err != nil {
			t.Fatal(err)
		}
		if len(sender.texts) != 1 || (!strings.Contains(sender.texts[0], "正在处理") && !strings.Contains(sender.texts[0], "队列已满")) {
			t.Fatal("missing capacity feedback", sender.texts)
		}
		rec, ok, err := s.Store.Claim()
		if err != nil || !ok {
			t.Fatal(err)
		}
		if err := s.Store.Cancel(rec.ID, event.Target); err != nil {
			t.Fatal(err)
		}
		s.last = map[string]time.Time{}
		if err := s.Accept(context.Background(), rejected); err != nil {
			t.Fatal(err)
		}
		if len(sender.texts) != 1 || s.Store.Pending() != 0 || p.calls.Load() != 0 {
			t.Fatal("rejected update later started paid work")
		}
	}
}

func TestSearchCooldownGetsGuidanceAndCannotStartOnRetryAfterRestart(t *testing.T) {
	for _, recentControl := range []bool{false, true} {
		t.Run(map[bool]string{false: "feedback", true: "quiet-after-control"}[recentControl], func(t *testing.T) {
			p := &testProvider{}
			sender := &testSender{}
			s, event := setup(t, p, sender, testRenderer{})
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if recentControl {
				help := event
				help.ID, help.Text = "help-event", "/help"
				if err := s.Accept(context.Background(), help); err != nil {
					t.Fatal(err)
				}
			}
			rejected := event
			rejected.ID = "cooldown-event"
			if err := s.Accept(context.Background(), rejected); err != nil {
				t.Fatal(err)
			}
			if len(sender.texts) != 1 || (!recentControl && !strings.Contains(sender.texts[0], "请求过快")) || s.Store.Pending() != 1 {
				t.Fatal("cooldown silently lost feedback or created paid work", sender.texts)
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok {
				t.Fatal("initial task missing", err)
			}
			if err := s.Store.Cancel(rec.ID, event.Target); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := NewServer(s.Runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if err := restarted.Accept(context.Background(), rejected); err != nil {
				t.Fatal(err)
			}
			if restarted.Store.Pending() != 0 || len(sender.texts) != 1 || p.calls.Load() != 0 {
				t.Fatal("throttled update became work or repeated a reply after restart")
			}
		})
	}
}

func TestCooldownRedeliveryPreservesAcceptedSearch(t *testing.T) {
	for _, recentControl := range []bool{false, true} {
		t.Run(map[bool]string{false: "reply-budget-free", true: "reply-budget-used"}[recentControl], func(t *testing.T) {
			p := &testProvider{}
			sender := &testSender{}
			s, event := setup(t, p, sender, testRenderer{})
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			wantTexts := 0
			if recentControl {
				help := event
				help.ID, help.Text = "help-before-redelivery", "/help"
				if err := s.Accept(context.Background(), help); err != nil {
					t.Fatal(err)
				}
				wantTexts = 1
			}
			for range 2 {
				if err := s.Accept(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			if s.Store.Pending() != 1 || len(sender.texts) != wantTexts || p.calls.Load() != 0 {
				t.Fatal("redelivery changed accepted work or sent misleading cooldown feedback")
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok || rec.State != "running" || rec.Query != "Go context cancellation" || len(rec.Deliveries) != 0 {
				t.Fatal("accepted search overwritten by rate-limit record", rec, err)
			}
		})
	}
}

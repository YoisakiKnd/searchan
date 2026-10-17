package app

import (
	"context"
	"strings"
	"testing"

	"searchan/platform"
	"searchan/task"
)

func interruptedTask(t *testing.T, s *Server, event platform.Event) task.Record {
	t.Helper()
	r, _, err := s.Store.Submit(event, "private question must not leak")
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	r.Deliveries = []task.Delivery{{Kind: "accepted", State: "sent"}}
	if err := s.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	reopenRecoveryStore(t, s)
	r, err = s.Store.Get(r.ID)
	if err != nil || !r.RecoveryNotice {
		t.Fatal("missing durable marker", err)
	}
	return r
}

func reopenRecoveryStore(t *testing.T, s *Server) {
	t.Helper()
	dir, capacity := s.Store.Dir, s.Store.Capacity
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(dir, capacity)
	if err != nil {
		t.Fatal(err)
	}
	s.Store = store
}

func TestInterruptedResearchNotifiesOnceWithoutResearch(t *testing.T) {
	for _, mode := range []string{"success", "unknown-send", "rejected-send", "intent-before-crash", "access-revoked", "platform-disabled", "failure-already-recorded"} {
		t.Run(mode, func(t *testing.T) {
			provider, sender := &testProvider{}, &testSender{}
			s, event := setup(t, provider, sender, testRenderer{})
			r := interruptedTask(t, s, event)
			wantTexts := 1
			wantState := "completed"
			switch mode {
			case "unknown-send":
				sender.textError = context.DeadlineExceeded
				wantState = "uncertain"
			case "rejected-send":
				sender.textError = &platform.RejectedError{Code: 400}
				wantState = "failed"
			case "intent-before-crash":
				if _, _, err := s.Store.BeginReply(recoveryEvent(r)); err != nil {
					t.Fatal(err)
				}
				reopenRecoveryStore(t, s)
				wantTexts, wantState = 0, "uncertain"
			case "access-revoked":
				s.Runtime.Config.AllowPublic = false
				wantTexts = 0
			case "platform-disabled":
				delete(s.Runtime.Senders, "telegram")
				wantTexts = 0
			case "failure-already-recorded":
				r.Deliveries = append(r.Deliveries, task.Delivery{Kind: "failure", State: "uncertain"})
				if err := s.Store.Save(r); err != nil {
					t.Fatal(err)
				}
				wantTexts = 0
			}
			for i := 0; i < 2; i++ {
				if err := s.notifyRecoveryNotices(context.Background()); err != nil {
					t.Fatal(err)
				}
				reopenRecoveryStore(t, s)
			}
			if len(sender.texts) != wantTexts || sender.images != 0 || provider.calls.Load() != 0 || s.Store.Pending() != 0 {
				t.Fatalf("unexpected work: texts=%v images=%d search=%d", sender.texts, sender.images, provider.calls.Load())
			}
			if wantTexts > 0 && (!strings.Contains(sender.texts[0], "/status "+r.ID) || strings.Contains(sender.texts[0], "private question")) {
				t.Fatal("unsafe or unhelpful notice", sender.texts)
			}
			current, err := s.Store.Get(r.ID)
			if err != nil || current.State != "failed" || current.Output != nil || current.RecoveryNotice {
				t.Fatalf("source state: %+v %v", current, err)
			}
			if wantTexts > 0 || mode == "intent-before-crash" {
				reply, created, err := s.Store.BeginReply(recoveryEvent(r))
				if err != nil || created || reply.State != wantState {
					t.Fatalf("reply state: %+v %v %v", reply, created, err)
				}
				if wantTexts > 0 && reply.Deliveries[0].Page != 2 {
					t.Fatal("recovery reused the accepted reply sequence", reply.Deliveries)
				}
			}
		})
	}
}

func TestRecoveryNotificationStopsWhileDraining(t *testing.T) {
	sender := &testSender{}
	s, event := setup(t, &testProvider{}, sender, testRenderer{})
	r := interruptedTask(t, s, event)
	s.draining.Store(true)
	if err := s.notifyRecoveryNotices(context.Background()); err != context.Canceled {
		t.Fatal(err)
	}
	current, _ := s.Store.Get(r.ID)
	if !current.RecoveryNotice || len(sender.texts) != 0 {
		t.Fatal("drain consumed notice")
	}
}

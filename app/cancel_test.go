package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"searchan/platform"
	"searchan/task"
)

type pausedAcceptanceSender struct {
	*testSender
	accepted chan struct{}
}

func (s *pausedAcceptanceSender) SendText(ctx context.Context, target platform.Target, text string, seq int) (platform.Receipt, error) {
	receipt, err := s.testSender.SendText(ctx, target, text, seq)
	if strings.HasPrefix(text, "开始搜索。") {
		close(s.accepted)
		<-ctx.Done() // The progress message is visible but its HTTP result is pending.
		return platform.Receipt{}, ctx.Err()
	}
	return receipt, err
}

func TestCancelDuringAcceptanceStopsBeforeResearchAndSurvivesRestart(t *testing.T) {
	p := &testProvider{}
	sender := &pausedAcceptanceSender{testSender: &testSender{}, accepted: make(chan struct{})}
	s, event := setup(t, p, sender.testSender, testRenderer{})
	s.Runtime.Senders["telegram"] = sender
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.process(ctx, rec) }()
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("worker did not stop")
		}
	}()
	select {
	case <-sender.accepted:
	case <-time.After(time.Second):
		t.Fatal("acceptance did not start")
	}
	event.ID, event.Text = "cancel-during-acceptance", "/cancel "+rec.ID
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Store.Get(rec.ID)
	if err != nil || stored.State != "cancelled" {
		t.Fatal("progress delivery incorrectly prevented cancellation", stored.State, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("acceptance transport not cancelled")
	}
	if p.calls.Load() != 0 || sender.images != 0 || len(sender.texts) != 2 || !strings.Contains(sender.texts[1], "已取消") {
		t.Fatal("cancelled acknowledgement started research or lacked confirmation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := task.Open(s.Store.Dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err = reopened.Get(rec.ID)
	if err != nil || stored.State != "cancelled" || len(stored.Deliveries) != 1 || stored.Deliveries[0].Kind != "accepted" || stored.Deliveries[0].State != "sending" || reopened.Pending() != 0 {
		t.Fatal("cancelled task or uncertain progress intent lost after restart", err)
	}
}

func TestFirstCancellationConfirmsDespiteRecentStatusAndRepeatedCancelIsQuiet(t *testing.T) {
	sender := &testSender{}
	s, event := setup(t, &testProvider{}, sender, testRenderer{})
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal(err)
	}
	event.ID, event.Text = "status-before-cancel", "/status "+rec.ID
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.ID, event.Text = "first-cancel", "/cancel "+rec.ID
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 2 || !strings.Contains(sender.texts[1], "已取消") {
		t.Fatal("first cancellation hidden by preceding status response", sender.texts)
	}
	for _, id := range []string{"first-cancel", "another-cancel", "yet-another-cancel"} {
		event.ID = id
		if err := s.Accept(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.texts) != 2 || s.Store.Pending() != 0 {
		t.Fatal("repeated cancellation bypassed reply limiter")
	}
}

func TestCancellationConfirmationTimeoutIsNotRetriedAfterRestart(t *testing.T) {
	sender := &testSender{textError: context.DeadlineExceeded}
	s, event := setup(t, &testProvider{}, sender, testRenderer{})
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal(err)
	}
	event.ID, event.Text = "cancel-with-unknown-confirmation", "/cancel "+rec.ID
	if err := s.Accept(context.Background(), event); err != nil {
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
	if err := restarted.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	stored, err := restarted.Store.Get(rec.ID)
	if err != nil || stored.State != "cancelled" || len(sender.texts) != 1 || restarted.Store.Pending() != 0 {
		t.Fatal("unknown cancellation confirmation repeated or cancelled work restarted")
	}
}

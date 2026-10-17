package task

import (
	"context"
	"errors"
	"testing"

	"searchan/platform"
)

func TestInterruptedResearchNoticeIsDurableAndDoesNotUseQueue(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"unknown", "known"} {
		r, _, err := s.Submit(platform.Event{ID: id, Target: platform.Target{Platform: "telegram", ChatID: "chat"}}, "query")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Claim(); err != nil || !ok {
			t.Fatal("claim", err)
		}
		r.State = "running"
		if id == "known" {
			r.Error = "original generation failure"
		}
		if err := s.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Submit(platform.Event{ID: "pending"}, "query"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	visited := 0
	err = s.VisitRecoveryNotices(context.Background(), func(r Record) error {
		visited++
		if r.State != "failed" || r.Error != InterruptedResearch {
			t.Fatalf("unexpected recovery: %+v", r)
		}
		// Callback can write without deadlocking or consuming a queue slot.
		if _, _, err := s.BeginReply(platform.Event{ID: "notice"}); err != nil {
			return err
		}
		r.RecoveryNotice = false
		return s.Save(r)
	})
	if err != nil || visited != 1 || s.Pending() != 1 {
		t.Fatalf("notice/queue: %d %d %v", visited, s.Pending(), err)
	}
	known, err := s.Get(eventID(platform.Event{ID: "known", Target: platform.Target{Platform: "telegram", ChatID: "chat"}}))
	if err != nil || known.Error != "original generation failure" || known.RecoveryNotice {
		t.Fatalf("known failure overwritten: %+v %v", known, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.VisitRecoveryNotices(ctx, func(Record) error { t.Fatal("cancelled scan visited record"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.VisitRecoveryNotices(context.Background(), func(Record) error { t.Fatal("notice repeated"); return nil }); err != nil {
		t.Fatal(err)
	}
}

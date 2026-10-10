package task

import (
	"errors"
	"testing"
	"time"

	"searchan/platform"
)

func TestDurableQueueDedupCancelAndLock(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, 1); err == nil {
		t.Fatal("second process accepted")
	}
	event := platform.Event{ID: "1", Target: platform.Target{Platform: "telegram", ChatID: "chat", UserID: "owner"}}
	rec, created, err := s.Submit(event, "q")
	if err != nil || !created {
		t.Fatal(err)
	}
	duplicate, created, err := s.Submit(event, "q")
	if err != nil || created || duplicate.ID != rec.ID {
		t.Fatal("event was not deduplicated")
	}
	other := event
	other.ID = "2"
	if _, _, err := s.Submit(other, "q"); !errors.Is(err, ErrBusy) {
		t.Fatal("queue capacity not enforced")
	}
	target := event.Target
	target.UserID = "stranger"
	if err := s.Cancel(rec.ID, target); err == nil {
		t.Fatal("other user cancelled task")
	}
	if err := s.Cancel(rec.ID, event.Target); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(rec); err == nil {
		t.Fatal("stale worker undid cancellation")
	}
	s.Close()
	s, err = Open(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok, err := s.Claim(); err != nil || ok {
		t.Fatal("cancelled task was recovered")
	}
}

func TestRecoveryDoesNotRepeatUnknownSends(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 4)
	for i, state := range []string{"sending", "running", "ready"} {
		event := platform.Event{ID: string(rune('a' + i)), Target: platform.Target{Platform: "telegram", ChatID: "chat"}}
		rec, _, _ := s.Submit(event, "q")
		rec.State = state
		_ = s.Save(rec)
	}
	s.Close()
	s, err := Open(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, ok, err := s.Claim()
	if err != nil || !ok || rec.State != "running" {
		t.Fatal("ready task not recovered")
	}
	if _, ok, _ := s.Claim(); ok {
		t.Fatal("unknown send or paid search was automatically repeated")
	}
	if err := s.Prune(time.Hour); err != nil {
		t.Fatal(err)
	}
}

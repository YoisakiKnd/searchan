package task

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"searchan/demo"
	"searchan/platform"
)

func TestClaimRetainsQueuedTaskOnPersistenceFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX permissions as an ordinary user")
	}
	s, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, _, err := s.Submit(platform.Event{ID: "1", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "u"}}, "query")
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Path(r.ID)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if _, ok, err := s.Claim(); ok || err == nil {
		t.Fatal("write failure was not surfaced")
	}
	if s.Pending() != 1 {
		t.Fatal("accepted task disappeared after failed durable claim")
	}
	if s.Probe() == nil {
		t.Fatal("readiness ignored failed durable claim")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Claim(); err != nil || !ok || got.ID != r.ID {
		t.Fatalf("task not recoverable: %+v %v", got, err)
	}
	if err := s.Probe(); err != nil {
		t.Fatal("readiness did not recover after storage repair", err)
	}
}

func TestRedeliveryRestoresRecordLeftByPartialCommit(t *testing.T) {
	s, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event := platform.Event{ID: "1", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "u"}}
	r, _, err := s.Submit(event, "q")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate rename succeeding but the final fsync/submission bookkeeping failing.
	s.pending = s.pending[:0]
	delete(s.live, r.ID)
	if _, created, err := s.Submit(event, "q"); err != nil || created {
		t.Fatal("partial commit was duplicated", err)
	}
	if got, ok, err := s.Claim(); err != nil || !ok || got.ID != r.ID {
		t.Fatal("partial commit not restored", err)
	}
}

func TestTaskQuotasIncludeRunningAndReleaseOnCancelOrCompletion(t *testing.T) {
	s, err := Open(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event := platform.Event{ID: "1", Target: platform.Target{Platform: "telegram", ChatID: "chat", UserID: "owner"}}
	r, _, err := s.SubmitLimited(event, "q", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Claim(); err != nil || !ok {
		t.Fatal(err)
	}
	event.ID = "2"
	if _, _, err := s.SubmitLimited(event, "q", 1, 2); !errors.Is(err, ErrQuota) {
		t.Fatal("running task escaped owner quota")
	}
	r.State = "completed"
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	r, _, err = s.SubmitLimited(event, "q", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(r.ID, r.Target); err != nil {
		t.Fatal(err)
	}
	if s.Pending() != 0 {
		t.Fatal("cancelled queued task retained capacity")
	}
	event.ID = "3"
	if _, _, err := s.SubmitLimited(event, "q", 1, 2); err != nil {
		t.Fatal("cancel did not release quota")
	}
}

func TestCompletedResearchResumesWithoutRepeatingUnknownWork(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _ := s.Submit(platform.Event{ID: "1", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "u"}}, "q")
	r.State = "running"
	output := demo.Output()
	r.Output = &output
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.Claim()
	if err != nil || !ok || got.Output == nil {
		t.Fatalf("completed research discarded: %+v %v", got, err)
	}
}

func TestShortReplyIntentIsDurableAndNeverEnqueued(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	event := platform.Event{ID: "status", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "u"}}
	r, created, err := s.BeginReply(event)
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, created, err := s.BeginReply(event); err != nil || created {
		t.Fatal("reply repeated")
	}
	if s.Pending() != 0 {
		t.Fatal("reply became search work")
	}
	s.Close()
	s, err = Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err = s.Get(r.ID)
	if err != nil || r.State != "uncertain" {
		t.Fatalf("unknown reply outcome not retained: %+v %v", r, err)
	}
	if _, ok, err := s.Claim(); err != nil || ok {
		t.Fatal("reply repeated paid search on restart")
	}
}

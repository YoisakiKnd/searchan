package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"searchan/platform"
)

func TestCancelDoesNotRewriteFailedTaskOrRepeatedCancellation(t *testing.T) {
	s, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event := platform.Event{ID: "failed", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "owner"}}
	rec, _, err := s.Submit(event, "query")
	if err != nil {
		t.Fatal(err)
	}
	rec.State, rec.Error = "failed", "existing failure diagnostic"
	if err := s.Save(rec); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(rec.ID)
	if err := s.Cancel(rec.ID, rec.Target); err == nil {
		t.Fatal("terminal failure changed to cancelled")
	}
	after, _ := s.Get(rec.ID)
	if after.State != "failed" || after.Error != before.Error || !after.Updated.Equal(before.Updated) {
		t.Fatal("failed task diagnostic or timestamp changed")
	}
	event.ID = "active"
	rec, _, err = s.Submit(event, "query")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(rec.ID, rec.Target); err != nil {
		t.Fatal(err)
	}
	before, _ = s.Get(rec.ID)
	if err := s.Cancel(rec.ID, rec.Target); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Get(rec.ID)
	if !after.Updated.Equal(before.Updated) {
		t.Fatal("repeated cancellation refreshed terminal retention")
	}
}

func TestCancelKeepsFinalDeliveryAndFailureBoundaries(t *testing.T) {
	for _, kind := range []string{"report", "image", "text", "sources", "failure", "control"} {
		t.Run(kind, func(t *testing.T) {
			s, err := Open(t.TempDir(), 2)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			event := platform.Event{ID: "event", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "owner"}}
			rec, _, err := s.Submit(event, "query")
			if err != nil {
				t.Fatal(err)
			}
			rec.State = "sending"
			rec.Deliveries = []Delivery{{Kind: kind, State: "sending"}}
			if err := s.Save(rec); err != nil {
				t.Fatal(err)
			}
			before, _ := s.Get(rec.ID)
			if changed, err := s.CancelOnce(rec.ID, rec.Target); changed || !errors.Is(err, ErrNotCancellable) {
				t.Fatal("in-flight result or control delivery was treated as cancellable progress", changed, err)
			}
			after, _ := s.Get(rec.ID)
			if after.State != before.State || !after.Updated.Equal(before.Updated) {
				t.Fatal("rejected cancellation mutated delivery state")
			}
		})
	}
	for _, failure := range []bool{false, true} {
		s, err := Open(t.TempDir(), 2)
		if err != nil {
			t.Fatal(err)
		}
		event := platform.Event{ID: "event", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "owner"}}
		rec, _, err := s.Submit(event, "query")
		if err != nil {
			t.Fatal(err)
		}
		rec.State = "ready"
		if failure {
			rec.Error, rec.Failure = "existing failure", &FailureDiagnostic{ModelCalls: 1}
		} else {
			rec.Deliveries = []Delivery{{Kind: "report", State: "uncertain"}}
		}
		if err := s.Save(rec); err != nil {
			t.Fatal(err)
		}
		if changed, err := s.CancelOnce(rec.ID, rec.Target); changed || !errors.Is(err, ErrNotCancellable) {
			t.Fatal("transitional ready state hid an existing failure or unknown result")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentCancellationTransitionsOnceAndChecksOwnership(t *testing.T) {
	s, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event := platform.Event{ID: "event", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "owner"}}
	rec, _, err := s.Submit(event, "query")
	if err != nil {
		t.Fatal(err)
	}
	stranger := rec.Target
	stranger.UserID = "stranger"
	if changed, err := s.CancelOnce(rec.ID, stranger); changed || err == nil {
		t.Fatal("another user cancelled work")
	}
	var changed atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			fresh, err := s.CancelOnce(rec.ID, rec.Target)
			if err != nil {
				t.Error(err)
			}
			if fresh {
				changed.Add(1)
			}
		})
	}
	wg.Wait()
	if changed.Load() != 1 || s.Pending() != 0 {
		t.Fatal("concurrent cancellation repeated transition or retained queue slot")
	}
	if _, err := s.CancelOnce(rec.ID, stranger); err == nil {
		t.Fatal("idempotency bypassed ownership check")
	}
}

func TestCancellationRetryReconcilesPartialCommitWithoutRewriting(t *testing.T) {
	s, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	event := platform.Event{ID: "event", Target: platform.Target{Platform: "telegram", ChatID: "c", UserID: "owner"}}
	rec, _, err := s.Submit(event, "query")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a persisted rename followed by failed completion bookkeeping.
	rec.State = "cancelled"
	if err := s.save(rec); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(rec.ID)
	if changed, err := s.CancelOnce(rec.ID, rec.Target); changed || err != nil {
		t.Fatal("persisted cancellation treated as a new transition", err)
	}
	after, _ := s.Get(rec.ID)
	if s.Pending() != 0 || len(s.live) != 0 || !after.Updated.Equal(before.Updated) {
		t.Fatal("partial cancellation retained queue/quota or rewrote timestamp")
	}
}

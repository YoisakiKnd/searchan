package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccessPolicyAndStorageReadiness(t *testing.T) {
	s, event := setup(t, &testProvider{}, &testSender{}, testRenderer{})
	s.Runtime.Config.AllowPublic = false
	s.Runtime.Config.AllowedUsers = map[string]bool{"telegram:allowed": true}
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if s.Store.Pending() != 0 {
		t.Fatal("unauthorized user queued paid work")
	}
	event.Target.UserID = "allowed"
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if s.Store.Pending() != 1 {
		t.Fatal("authorized user rejected")
	}
	h := s.Handler()
	check := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s: %d != %d", path, w.Code, want)
		}
	}
	check("/readyz", 200)
	s.draining.Store(true)
	check("/readyz", 503)
	check("/healthz", 200)
	s.draining.Store(false)
	s.Store.Close()
	s.probeAt = time.Time{}
	check("/readyz", 503)
}

func TestCommandRepliesAreDeduplicatedEvenOnTimeout(t *testing.T) {
	for _, sendErr := range []error{nil, context.DeadlineExceeded} {
		sender := &testSender{textError: sendErr}
		s, event := setup(t, &testProvider{}, sender, testRenderer{})
		r, _, err := s.Store.Submit(event, "q")
		if err != nil {
			t.Fatal(err)
		}
		event.ID = "status-event"
		event.Text = "/status " + r.ID
		for i := 0; i < 2; i++ {
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
		}
		if len(sender.texts) != 1 || !strings.Contains(sender.texts[0], r.ID) {
			t.Fatal("retried webhook repeated a command reply")
		}
	}
}

func TestAccessConfigurationMustMatchEnabledPlatform(t *testing.T) {
	s, _ := setup(t, &testProvider{}, &testSender{}, testRenderer{})
	s.Runtime.Config.AllowPublic = false
	s.Runtime.Config.AllowedChats = map[string]bool{"qq:elsewhere": true}
	if err := s.Runtime.validateAccess(); err == nil {
		t.Fatal("unused platform allowlist accepted")
	}
	for _, raw := range []string{"123", "wrong:123", "telegram:", "telegram:12 34"} {
		if _, err := accessList(raw); err == nil {
			t.Errorf("invalid access list accepted: %q", raw)
		}
	}
}

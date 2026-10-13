package app

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"searchan/research"
	"searchan/search"
	"searchan/task"
)

type outageProvider struct{ calls atomic.Int32 }

func (p *outageProvider) Name() string { return "fixture" }
func (p *outageProvider) Search(context.Context, search.Request) (search.Response, error) {
	p.calls.Add(1)
	return search.Response{}, &net.DNSError{Err: "TOPSECRET-TEST", Name: "private.example", IsTimeout: true}
}

func TestFailedSearchPersistsSafeDiagnosticsWithoutResumablePartialOutput(t *testing.T) {
	sender := &testSender{}
	s, event := setup(t, &testProvider{}, sender, testRenderer{})
	p := &outageProvider{}
	engine, err := research.New(research.Config{Providers: []search.Provider{p}, Reader: unusedReader{}, Model: testModel{}, MaxSearchCalls: 6})
	if err != nil {
		t.Fatal(err)
	}
	s.Runtime.Engine = engine
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal("no queued task", err)
	}
	s.process(context.Background(), rec)
	stored, err := s.Store.Get(rec.ID)
	if err != nil || stored.State != "failed" || stored.Output != nil || stored.Failure == nil || stored.Failure.SearchCalls != 2 || stored.Failure.SearchFailures["network_timeout"] != 2 || stored.Failure.PagesRead != 0 || p.calls.Load() != 2 {
		t.Fatalf("missing failure diagnostics or partial output became deliverable: %+v %v", stored.Failure, err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(encoded), "TOPSECRET") || strings.Contains(string(encoded), "private.example") || len(sender.texts) != 2 || sender.images != 0 {
		t.Fatal("raw failure retained or incorrect delivery")
	}
	dir := s.Store.Dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := task.Open(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err = reopened.Get(rec.ID)
	if err != nil || stored.Failure == nil || stored.Failure.SearchFailures["network_timeout"] != 2 || stored.Output != nil || reopened.Pending() != 0 {
		t.Fatal("failure diagnostics lost on restart or failed work requeued")
	}
}

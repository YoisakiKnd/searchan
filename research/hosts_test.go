package research

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"searchan/internal/httpx"
	"searchan/reader"
	"searchan/search"
)

func TestHostHealthBlocksOnlySiteWideRefusals(t *testing.T) {
	h := newHostHealth()
	now := time.Now()
	h.now = func() time.Time { return now }
	for host, err := range map[string]error{
		"login.example":     &httpx.StatusError{Code: 403},
		"challenge.example": &httpx.StatusError{Code: 521},
		"script.example":    reader.ContentError("script_challenge"),
		"missing.example":   &httpx.StatusError{Code: 404},
		"flaky.example":     &httpx.StatusError{Code: 503},
		"slow.example":      context.DeadlineExceeded,
		"empty.example":     reader.ContentError("empty_text"),
		"offline.example":   &net.OpError{Op: "dial", Err: errors.New("i/o timeout")},
		"expired.example":   &tls.CertificateVerificationError{Err: errors.New("certificate has expired")},
	} {
		h.observe(host, err)
	}
	for host, want := range map[string]bool{"login.example": true, "challenge.example": true, "script.example": true, "missing.example": false, "flaky.example": false, "slow.example": false, "empty.example": false, "offline.example": true, "expired.example": true} {
		if h.blocked(host) != want {
			t.Errorf("blocked(%s) != %t", host, want)
		}
	}
	now = now.Add(31 * time.Minute)
	if h.blocked("login.example") {
		t.Fatal("block did not expire")
	}
}

func TestReadingSkipsRefusingSitesAndSeeksSecondSource(t *testing.T) {
	var results []search.Result
	for i := 0; i < 3; i++ {
		results = append(results, search.Result{URL: fmt.Sprintf("https://walled.example/p%d", i), Title: "Atlas walled", Snippet: "Atlas repository"})
	}
	for i := 0; i < 3; i++ {
		results = append(results, search.Result{URL: fmt.Sprintf("https://open%d.example/atlas", i), Title: "Atlas open", Snippet: "Atlas repository"})
	}
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: results}, nil
	}}
	var mu sync.Mutex
	walled := 0
	r := readFunc(func(_ context.Context, u string) (reader.Document, error) {
		if strings.Contains(u, "walled") {
			mu.Lock()
			walled++
			mu.Unlock()
			return reader.Document{}, &httpx.StatusError{Code: 403}
		}
		return reader.MakeDocument(u, "Atlas", "Atlas provides repository abstractions for data access in "+u, "fixture"), nil
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: r, Model: fixtureModel(), MaxSearchCalls: 1})
	out, err := e.Run(context.Background(), "Atlas repository")
	if err != nil || evidencePages(out.Evidence) < 2 {
		t.Fatalf("second source not read: %+v %v", out.Stats.PageReads, err)
	}
	// After the first refusal the site is skipped for the rest of the task.
	if walled > 3 || !e.hosts.blocked("walled.example") {
		t.Fatalf("refusing site kept consuming reads: %d", walled)
	}
	_, err = e.Run(context.Background(), "Atlas repository")
	if err != nil || walled > 3 {
		t.Fatalf("blocked site read again in a later task: %d %v", walled, err)
	}
	if errors.Is(err, ErrNoEvidence) {
		t.Fatal(err)
	}
}

package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ddgEmptyFixture = `<div class="no-results__message">No results</div>`

func TestDDGInstanceSerializesConcurrentRequests(t *testing.T) {
	var active, peak, calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, ddgEmptyFixture)
	}))
	defer srv.Close()
	p, _ := NewDDG(Config{Endpoint: srv.URL})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Search(context.Background(), Request{Query: "shared runtime"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 1 || calls.Load() != 8 {
		t.Fatalf("DDG burst not serialized: peak=%d calls=%d", peak.Load(), calls.Load())
	}
}

func TestDDGChallengeCooldownIsSharedDoesNotExtendAndExpires(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `<form id="challenge-form">PRIVATE_CHALLENGE</form>`)
			return
		}
		fmt.Fprint(w, ddgEmptyFixture)
	}))
	defer srv.Close()
	p, _ := NewDDG(Config{Endpoint: srv.URL})
	if _, err := p.Search(context.Background(), Request{Query: "first"}); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	until := p.coolUntil
	for range 3 {
		_, err := p.Search(context.Background(), Request{Query: "another chat"})
		if !errors.Is(err, ErrBlocked) || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || !p.coolUntil.Equal(until) {
		t.Fatal("cooldown retried upstream or moved deadline", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Search(ctx, Request{Query: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cooldown hid cancellation", err)
	}
	p.coolUntil = time.Now().Add(-time.Second)
	if _, err := p.Search(context.Background(), Request{Query: "after expiry"}); err != nil || calls.Load() != 2 {
		t.Fatal("expired circuit failed to recover", err, calls.Load())
	}
	// New adapters (different runtimes) do not share process-global traffic state.
	other, _ := NewDDG(Config{Endpoint: srv.URL})
	if _, err := other.Search(context.Background(), Request{Query: "independent"}); err != nil || calls.Load() != 3 {
		t.Fatal(err, calls.Load())
	}
}

func TestDDGRefusalRetainsStatusAndHonorsLongerRetryAfter(t *testing.T) {
	for _, code := range []int{403, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(code)
				fmt.Fprint(w, "PRIVATE_BODY")
			}))
			defer srv.Close()
			p, _ := NewDDG(Config{Endpoint: srv.URL})
			before := time.Now()
			for range 2 {
				_, err := p.Search(context.Background(), Request{Query: "refused"})
				var status *HTTPError
				if !errors.As(err, &status) || status.StatusCode != code || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal(err)
				}
			}
			if calls.Load() != 1 || p.coolUntil.Before(before.Add(120*time.Second)) {
				t.Fatal("refusal was retried too early", calls.Load(), p.coolUntil)
			}
		})
	}
	p, _ := NewDDG(Config{})
	future := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	p.rememberDDGFailure(&HTTPError{StatusCode: 429, RetryAfter: future.Format(http.TimeFormat)})
	if !p.coolUntil.Equal(future) {
		t.Fatal("HTTP-date Retry-After ignored", p.coolUntil, future)
	}
	p.rememberDDGFailure(&HTTPError{StatusCode: 429, RetryAfter: "9223372036854775807"})
	if p.coolUntil.Year() != 9999 {
		t.Fatal("large backoff overflowed", p.coolUntil)
	}
}

func TestDDGQueuedCancellationAndDefaultTimeoutDoNotCallUpstream(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, ddgEmptyFixture) }))
	defer srv.Close()
	p, _ := NewDDG(Config{Endpoint: srv.URL})
	p.lane <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Search(ctx, Request{Query: "cancel"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.client.Timeout = 20 * time.Millisecond
	if _, err := p.Search(context.Background(), Request{Query: "wait"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queue wait lacked total bound", err)
	}
	if calls.Load() != 0 {
		t.Fatal("expired waiter reached upstream")
	}
	<-p.lane
	p.client.Timeout = time.Second
	if _, err := p.Search(context.Background(), Request{Query: "next"}); err != nil || calls.Load() != 1 {
		t.Fatal("cancelled waiter consumed permit", err)
	}
}

func TestDDGUnknownLayoutAndServerFailureDoNotOpenCircuit(t *testing.T) {
	for _, code := range []int{200, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(code)
					fmt.Fprint(w, "unknown layout")
					return
				}
				fmt.Fprint(w, ddgEmptyFixture)
			}))
			defer srv.Close()
			p, _ := NewDDG(Config{Endpoint: srv.URL})
			if _, err := p.Search(context.Background(), Request{Query: "first"}); err == nil {
				t.Fatal("failure accepted")
			}
			if _, err := p.Search(context.Background(), Request{Query: "second"}); err != nil || calls.Load() != 2 {
				t.Fatal("transient failure poisoned instance", err)
			}
		})
	}
}

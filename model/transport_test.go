package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type faultReader struct{ err error }

func (r faultReader) Read([]byte) (int, error) { return 0, r.err }

func TestModelRetriesOnlyTransientFailures(t *testing.T) {
	for _, tt := range []struct {
		name               string
		status             int
		body               string
		fault              error
		bodyFault          bool
		retries, wantCalls int
	}{
		{"reset", 0, "", syscall.ECONNRESET, false, 1, 2},
		{"eof", 0, "", io.EOF, false, 1, 2},
		{"broken-body", 200, "", io.ErrUnexpectedEOF, true, 1, 2},
		{"bad-gateway", 502, "", nil, false, 1, 2},
		{"unavailable", 503, "", nil, false, 1, 2},
		{"gateway-timeout", 504, "", nil, false, 1, 2},
		{"auth", 401, "", nil, false, 1, 1},
		{"rate-limit", 429, "", nil, false, 1, 1},
		{"malformed-wire", 200, `{"choices":`, nil, false, 1, 1},
		{"invalid-answer", 200, `{"choices":[{"message":{"content":"not JSON"}}]}`, nil, false, 1, 1},
		{"disabled", 0, "", syscall.ECONNRESET, false, 0, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := &Client{Endpoint: "https://gateway.example/custom", Model: "m", MaxRetries: tt.retries, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					return jsonResponse(chatJSON), nil
				}
				if tt.bodyFault {
					res := jsonResponse("")
					res.Body = io.NopCloser(io.MultiReader(strings.NewReader(chatJSON), faultReader{tt.fault}))
					return res, nil
				}
				if tt.fault != nil {
					return nil, &net.OpError{Op: "read", Net: "tcp", Err: tt.fault}
				}
				res := jsonResponse(tt.body)
				res.StatusCode = tt.status
				return res, nil
			})}}
			var out struct{ Value string }
			err := c.Complete(context.Background(), "JSON", nil, &out)
			if calls != tt.wantCalls || (err == nil) != (tt.wantCalls == 2) || (err == nil && out.Value != "ok") {
				t.Fatalf("calls=%d out=%+v err=%v", calls, out, err)
			}
		})
	}
}

func TestModelRetryCapAndBudget(t *testing.T) {
	for _, tt := range []struct {
		name       string
		limit      time.Duration
		want       int
		retryAfter string
	}{
		{"one-retry", 3 * time.Second, 2, ""},
		{"insufficient-budget", 100 * time.Millisecond, 1, ""},
		{"retry-after-not-overridden", 3 * time.Second, 1, "10"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := &Client{Endpoint: "https://gateway.example/custom", Model: "m", MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				res := jsonResponse("")
				res.StatusCode = 503
				if tt.retryAfter != "" {
					res.Header.Set("Retry-After", tt.retryAfter)
				}
				return res, nil
			})}}
			ctx, cancel := context.WithTimeout(context.Background(), tt.limit)
			defer cancel()
			var out any
			if err := c.Complete(ctx, "JSON", nil, &out); err == nil || calls != tt.want {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestModelRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var calls atomic.Int32
	started := make(chan struct{})
	c := &Client{Endpoint: "https://gateway.example/custom", Model: "m", MaxRetries: 1, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		return nil, syscall.ECONNRESET
	})}}
	done := make(chan error, 1)
	go func() { var out any; done <- c.Complete(ctx, "JSON", nil, &out) }()
	<-started
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("cancellation did not stop retry: %v calls=%d", err, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("retry ignored cancellation")
	}
}

func TestActualClosedConnectionRecoversOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		fmt.Fprint(w, chatJSON)
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Model: "m", MaxRetries: 1, Client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out struct{ Value string }
	if err := c.Complete(ctx, "JSON", nil, &out); err != nil || out.Value != "ok" || calls.Load() != 2 {
		t.Fatalf("closed connection not recovered: %+v calls=%d err=%v", out, calls.Load(), err)
	}
}

func TestModelHTTPBudgetAndDefaultConnectionReuse(t *testing.T) {
	client := NewHTTPClient(100 * time.Millisecond)
	defer client.CloseIdleConnections()
	if client.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
		t.Fatal("hidden response-header deadline still overrides model budget")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer slow.Close()
	c := &Client{Endpoint: slow.URL, Model: "m", Client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out any
	if err := c.Complete(ctx, "JSON", nil, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("request deadline lost", err)
	}
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, chatJSON) }))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	c = &Client{Endpoint: srv.URL, Model: "m"}
	for i := 0; i < 2; i++ {
		if err := c.Complete(context.Background(), "JSON", nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("default client did not reuse connections: %d", connections.Load())
	}
}

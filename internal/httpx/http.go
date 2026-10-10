// Package httpx contains bounded, reusable HTTP primitives.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		Proxy:        http.ProxyFromEnvironment,
		DialContext:  (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, ForceAttemptHTTP2: true,
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type StatusError struct {
	Code       int
	RetryAfter string
}

func (e *StatusError) Error() string { return fmt.Sprintf("remote HTTP status %d", e.Code) }

// Decode consumes one JSON value, with a strict total byte limit and no
// intermediate full-response byte slice. It also rejects trailing JSON.
func Decode(r io.Reader, max int64, out any) error {
	tracked := &readErrorTracker{Reader: r}
	lr := &io.LimitedReader{R: tracked, N: max + 1}
	dec := json.NewDecoder(lr)
	if err := dec.Decode(out); err != nil {
		if lr.N > 0 && tracked.err != nil {
			return fmt.Errorf("read JSON response: %w", tracked.err)
		}
		return errors.New("invalid or oversized JSON response")
	}
	var extra any
	err := dec.Decode(&extra)
	if lr.N <= 0 {
		return errors.New("JSON response exceeds size limit")
	}
	if err != io.EOF {
		if tracked.err != nil {
			return fmt.Errorf("read JSON response: %w", tracked.err)
		}
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

// Distinguish a broken response body from a complete but malformed JSON value.
// The decoder's own unexpected EOF must not make invalid model JSON retryable.
type readErrorTracker struct {
	io.Reader
	err error
}

func (r *readErrorTracker) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func JSON(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, payload, out any, max int64) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("invalid service endpoint")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return TransportError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &StatusError{res.StatusCode, res.Header.Get("Retry-After")}
	}
	if err := Decode(res.Body, max, out); err != nil {
		return TransportError(ctx, err)
	}
	return nil
}

func TransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

package model

import (
	"context"
	"errors"
	"io"
	"net/http"
	"syscall"
	"time"

	"searchan/internal/httpx"
)

var defaultClient = NewHTTPClient(30 * time.Second)

// NewHTTPClient pools model connections separately from search and delivery.
// Response headers may arrive late for non-streaming generation; the request
// context and client timeout bound the entire operation instead.
func NewHTTPClient(timeout time.Duration) *http.Client {
	c := httpx.NewClient(timeout)
	c.Transport.(*http.Transport).ResponseHeaderTimeout = 0
	return c
}

func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status *httpx.StatusError
	if errors.As(err, &status) {
		return status.RetryAfter == "" && (status.Code == 502 || status.Code == 503 || status.Code == 504)
	}
	// Malformed model JSON, missing content, truncation, auth and rate limits
	// are not retried. Only recoverable connection/read failures qualify.
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func retryBudget(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) >= 2*time.Second
}

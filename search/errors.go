package search

import (
	"context"
	"errors"
	"net"
	"strconv"
)

// FailureCode describes an adapter failure without copying URLs, response
// bodies, query text or arbitrary transport details into diagnostics.
func FailureCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrBlocked):
		return "blocked"
	}
	var status *HTTPError
	if errors.As(err, &status) && status.StatusCode >= 100 && status.StatusCode <= 599 {
		return "http_" + strconv.Itoa(status.StatusCode)
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network_timeout"
		}
		return "network_error"
	}
	return "io_error"
}

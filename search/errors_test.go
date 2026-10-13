package search

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestFailureCodeNeverCopiesPrivateRemoteDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "deadline"},
		{ErrBlocked, "blocked"},
		{&HTTPError{Provider: "TOPSECRET-TEST", StatusCode: 503}, "http_503"},
		{&HTTPError{Provider: "TOPSECRET-TEST", StatusCode: 9999}, "io_error"},
		{&net.DNSError{Err: "TOPSECRET-TEST", IsTimeout: true}, "network_timeout"},
		{&net.OpError{Op: "TOPSECRET-TEST", Net: "tcp", Err: errors.New("private")}, "network_error"},
		{errors.New("TOPSECRET-TEST"), "io_error"},
	} {
		err := tc.err
		if err != nil {
			err = fmt.Errorf("TOPSECRET-TEST wrapper: %w", err)
		}
		if got := FailureCode(err); got != tc.want || strings.Contains(got, "TOPSECRET") {
			t.Fatalf("incorrect or unsafe code %q, want %q", got, tc.want)
		}
	}
}

package httpx

import (
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
)

type brokenReader struct{ err error }

func (r brokenReader) Read([]byte) (int, error) { return 0, r.err }

func TestDecodePreservesReadFaultWithoutRetryingInvalidJSON(t *testing.T) {
	for _, err := range []error{syscall.ECONNRESET, io.ErrUnexpectedEOF} {
		for _, prefix := range []string{`{"value":`, `{"value":1}`} {
			var out any
			got := Decode(io.MultiReader(strings.NewReader(prefix), brokenReader{err}), 1024, &out)
			if !errors.Is(got, err) {
				t.Fatalf("body read cause lost: %v", got)
			}
		}
	}
	for _, body := range []string{`{"value":`, `{"value":1} {}`, strings.Repeat(" ", 1025) + `{}`} {
		var out any
		got := Decode(strings.NewReader(body), 1024, &out)
		if got == nil || errors.Is(got, io.ErrUnexpectedEOF) || errors.Is(got, io.EOF) {
			t.Fatalf("invalid/oversized JSON became retryable: %v", got)
		}
	}
	var out any
	if err := Decode(strings.NewReader("\n\n  {\"value\":1}\n"), 1024, &out); err != nil {
		t.Fatal("provider keepalive whitespace rejected", err)
	}
}

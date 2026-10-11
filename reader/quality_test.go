package reader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"searchan/internal/fsutil"
)

func TestContentRejectsInterstitialsWithoutRejectingExplanations(t *testing.T) {
	for _, tt := range []struct {
		title, text string
		bad         bool
	}{
		{"Just a moment...", "Checking your browser before accessing the documentation.", true},
		{"Project Docs", "Verify you are human to continue to this website.", true},
		{"Project Docs", "# 404 Not Found\n\nnginx", true},
		{"Login", "Sign in to continue. Email address and password are required.", true},
		{"项目资料", "请先登录后查看这篇文档。", true},
		{"Captcha", "Complete the challenge before continuing to this website.", true},
		{"Definition", "A repository abstracts data access.", false},
		{"Fix access denied", "The access denied error can be caused by missing filesystem permissions.", false},
		{"HTTP status codes", "404 Not Found responses indicate that a resource was not found.", false},
		{"Captcha integration", "This guide explains how to implement a captcha verification endpoint.", false},
		{"Authentication", "The UI displays ‘sign in to continue’ when authentication is required.", false},
		{"Empty", " \n\t", true},
		{"Invalid", string([]byte{0xff}), true},
	} {
		t.Run(tt.title+tt.text[:min(10, len(tt.text))], func(t *testing.T) {
			if err := CheckContent(tt.title, tt.text); (err != nil) != tt.bad {
				t.Fatalf("content decision: %v", err)
			}
		})
	}
}

func TestTruncationSurvivesReadersAndCache(t *testing.T) {
	for _, kind := range []string{"text/plain", "text/html"} {
		t.Run(kind, func(t *testing.T) {
			body := strings.Repeat("Readable repository reference. ", 4000)
			if kind == "text/html" {
				body = "<title>Reference</title><main><p>" + body + "</p></main>"
			}
			client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			c := &Cache{Dir: t.TempDir(), Next: NewHTTP(client)}
			for range 2 {
				doc, err := c.Read(context.Background(), "https://example.org/reference")
				if err != nil || !doc.Truncated || len(doc.Text) > MaxText || doc.Method != "http" {
					t.Fatalf("truncation lost: length=%d truncated=%v method=%s error=%v", len(doc.Text), doc.Truncated, doc.Method, err)
				}
			}
		})
	}
	_, _, truncated, err := extract(strings.NewReader("<main>Short definition.</main>"))
	if err != nil || truncated {
		t.Fatal("short complete page marked truncated")
	}
}

type qualityReader struct {
	calls int
	bad   bool
}

func TestCacheRefreshesTextFromPreviousExtractor(t *testing.T) {
	u := "https://example.org/reference"
	r := &qualityReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: r}
	legacy := MakeDocument(u, "Legacy", "Repository guide. Recommended article preview with a wrong unrelated assertion.", "http")
	oldHash := sha256.Sum256([]byte(u))
	oldPath := filepath.Join(c.Dir, hex.EncodeToString(oldHash[:])+".json")
	if err := fsutil.WriteJSON(oldPath, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		doc, err := c.Read(context.Background(), u)
		if err != nil || doc.Text == legacy.Text || r.calls != 1 {
			t.Fatalf("legacy text reused or fresh cache missed: %+v calls=%d err=%v", doc, r.calls, err)
		}
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("migration unexpectedly deletes legacy data", err)
	}
}

func (r *qualityReader) Read(_ context.Context, u string) (Document, error) {
	r.calls++
	text := "Atlas provides repository abstractions for accessing data stores in applications."
	if r.bad {
		text = "Verify you are human to access this documentation and continue browsing."
	}
	return MakeDocument(u, "Atlas", text, "fixture"), nil
}

func TestCacheRefreshesLegacyInterstitialAndDoesNotCacheNewOne(t *testing.T) {
	r := &qualityReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: r}
	u := "https://docs.example/atlas"
	if _, err := c.Read(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("missing cache file", err)
	}
	bad := MakeDocument(u, "Atlas", "Verify you are human before accessing this documentation.", "http")
	if err := fsutil.WriteJSON(filepath.Join(c.Dir, entries[0].Name()), bad); err != nil {
		t.Fatal(err)
	}
	doc, err := c.Read(context.Background(), u)
	if err != nil || r.calls != 2 || doc.Text == bad.Text {
		t.Fatal("legacy bad cache not refreshed", err)
	}
	if err := os.Remove(filepath.Join(c.Dir, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	r.bad = true
	for range 2 {
		_, err := c.Read(context.Background(), u)
		var content ContentError
		if !errors.As(err, &content) {
			t.Fatal("bad reader result accepted", err)
		}
	}
	entries, err = os.ReadDir(c.Dir)
	if err != nil || len(entries) != 0 || r.calls != 4 {
		t.Fatal("bad page cached")
	}
}

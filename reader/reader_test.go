package reader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"searchan/internal/fsutil"
)

func TestExtractAndLimits(t *testing.T) {
	raw := `<html><head><title>测试标题</title><script>secret</script></head><body><nav>menu</nav><main><h1>正文</h1><p>` + strings.Repeat("这是需要保留的中文段落，context cancellation。", 10) + `</p><p>第二段<br/>继续</p></main><footer>footer</footer></body></html>`
	title, text, err := Extract(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if title != "测试标题" || strings.Contains(text, "menu") || strings.Contains(text, "secret") || strings.Contains(text, "footer") || !strings.Contains(text, "第二段") {
		t.Fatalf("bad extraction: %s %s", title, text)
	}
	_, text, err = Extract(strings.NewReader("<article>" + strings.Repeat("<p>"+strings.Repeat("文本", 100)+"</p>", 300) + "</article>"))
	if err != nil || len(text) > MaxText || !utf8.ValidString(text) {
		t.Fatalf("text is unbounded or corrupt: %d %v", len(text), err)
	}
	if _, _, err := Extract(strings.NewReader(strings.Repeat("<div>", 600))); err == nil {
		t.Fatal("unbounded nesting")
	}
}

type blockingReader struct{ entered, release chan struct{} }

func (r *blockingReader) Read(ctx context.Context, raw string) (Document, error) {
	close(r.entered)
	select {
	case <-r.release:
		return MakeDocument(raw, "Body", strings.Repeat("body ", 30), "fixture"), nil
	case <-ctx.Done():
		return Document{}, ctx.Err()
	}
}
func TestCacheWaitCanBeCancelledWithoutInterruptingSharedRead(t *testing.T) {
	next := &blockingReader{entered: make(chan struct{}), release: make(chan struct{})}
	c := &Cache{Dir: t.TempDir(), Next: next}
	first := make(chan error, 1)
	go func() { _, err := c.Read(context.Background(), "https://example.org/"); first <- err }()
	<-next.entered
	defer func() {
		close(next.release)
		if err := <-first; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { _, err := c.Read(ctx, "https://example.org/"); second <- err }()
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled cache wait held by shared read")
	}
	select {
	case <-first:
		t.Fatal("waiter cancelled the unrelated active read")
	default:
	}
}

func TestDestinationPolicy(t *testing.T) {
	for _, raw := range []string{"http://localhost/", "http://127.0.0.1/", "http://169.254.169.254/", "http://[::ffff:127.0.0.1]/", "https://example.org:8080/", "file:///etc/passwd", "https://user:pass@example.org/"} {
		if ValidateURL(raw) == nil {
			t.Errorf("accepted unsafe URL %s", raw)
		}
	}
	for _, ip := range []string{"10.1.1.1", "100.100.100.200", "192.0.2.1", "224.0.0.1", "::1", "fc00::1", "2001:db8::1"} {
		if PublicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted reserved IP %s", ip)
		}
	}
	if !PublicIP(netip.MustParseAddr("1.1.1.1")) || ValidateURL("https://go.dev/doc/") != nil {
		t.Fatal("rejected public destination")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPReaderRejectsChallengesAndLargeBodies(t *testing.T) {
	for _, tt := range []struct {
		contentType, body string
		size              int64
		wantErr           bool
	}{
		{"text/html", `<title>Just a moment</title><p>` + strings.Repeat("challenge ", 20) + `</p>`, 0, true},
		{"application/pdf", "pdf", 0, true}, {"text/plain", "tiny", 0, true},
		{"text/html", `<main><p>` + strings.Repeat("usable page content ", 20) + `</p></main>`, 0, false},
		{"text/html", "", MaxBody + 1, true},
	} {
		client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tt.contentType}}, Body: io.NopCloser(strings.NewReader(tt.body)), ContentLength: tt.size, Request: r}, nil
		})}
		_, err := NewHTTP(client).Read(context.Background(), "https://example.org/page")
		if (err != nil) != tt.wantErr {
			t.Fatalf("unexpected page outcome: %v", err)
		}
	}
}

type fakeReader struct{ calls atomic.Int32 }

func (f *fakeReader) Read(ctx context.Context, url string) (Document, error) {
	f.calls.Add(1)
	return MakeDocument(url, "test", strings.Repeat("body ", 50), "fake"), nil
}
func TestCacheCoalesces(t *testing.T) {
	next := &fakeReader{}
	cache := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: next}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Read(context.Background(), "https://example.org/"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if next.calls.Load() != 1 {
		t.Fatalf("duplicate fetches: %d", next.calls.Load())
	}
}

func BenchmarkExtract(b *testing.B) {
	raw := []byte("<html><nav>navigation</nav><article>" + strings.Repeat("<p>Go context cancellation and bounded concurrent search workers. 中文正文。</p>", 300) + "</article></html>")
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Extract(strings.NewReader(string(raw))); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCachePruneRetainsFreshPages(t *testing.T) {
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: &fakeReader{}}
	for _, u := range []string{"https://example.org/old", "https://example.org/new"} {
		if _, err := c.Read(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(c.Dir, "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatal("missing cache")
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(files[0], past, past); err != nil {
		t.Fatal(err)
	}
	if err := c.Prune(); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(c.Dir, "*.json"))
	if len(files) != 1 {
		t.Fatal("expired pages were not pruned")
	}
}

func TestFreshReadDoesNotUseNormalTTL(t *testing.T) {
	next := &fakeReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: next}
	if _, err := c.Read(context.Background(), "https://example.org/"); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(c.Dir, "*.json"))
	doc := MakeDocument("https://example.org/", "test", strings.Repeat("body ", 50), "old")
	doc.FetchedAt = time.Now().Add(-time.Minute)
	if err := fsutil.WriteJSON(files[0], doc); err != nil {
		t.Fatal(err)
	}
	_, _ = c.Read(context.Background(), doc.URL)
	if next.calls.Load() != 1 {
		t.Fatal("normal TTL bypassed")
	}
	_, err := c.ReadFresh(context.Background(), doc.URL)
	if err != nil || next.calls.Load() != 2 {
		t.Fatal("freshness cap not respected")
	}
}

func TestHTTPReaderClassifiesScriptChallenges(t *testing.T) {
	for _, tt := range []struct {
		body string
		want ContentError
	}{
		{`<script>document.cookie='token=1; path=/';window.location.href='/faq/1.html'</script>`, "script_challenge"},
		{`<html><head><title>App</title></head><body><div id="app"></div>` + strings.Repeat(`<script src="/x.js"></script>`, 400) + `</body></html>`, "empty_text"},
	} {
		client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(tt.body)), Request: r}, nil
		})}
		_, err := NewHTTP(client).Read(context.Background(), "https://example.org/page")
		var got ContentError
		if !errors.As(err, &got) || got != tt.want {
			t.Fatalf("got %v, want %s", err, tt.want)
		}
	}
}

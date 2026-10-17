package reader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"searchan/internal/fsutil"
)

func TestCodeOffsetsBelongToVisibleSelectedBody(t *testing.T) {
	raw := `<pre>Outside</pre><main><pre>Chrome</pre><article><pre>Card</pre><div class="article-body"><h2>接口</h2><pre><code>行一
<span>第二行</span></code></pre><pre hidden>Hidden</pre><nav><pre>Navigation</pre></nav><pre>Next block</pre><h2>说明</h2><p>Inline <code>Symbol.Code</code> remains prose.</p></div></article></main>`
	_, text, _, _, headings, blocks, err := extractDocumentPage(strings.NewReader(raw), "")
	if err != nil || len(blocks) != 2 || len(headings) != 2 {
		t.Fatalf("outline lost: %q %+v %+v %v", text, headings, blocks, err)
	}
	for i, want := range []string{"行一\n第二行", "Next block"} {
		if got := text[blocks[i].Start:blocks[i].End]; got != want {
			t.Fatalf("code bytes changed: %q != %q", got, want)
		}
	}
	if strings.Contains(text, "Hidden") || strings.Contains(text, "Chrome") || !strings.Contains(text, "Symbol.Code") {
		t.Fatal("selection or inline handling changed", text)
	}
}

func TestCodeOffsetsAreBoundedTrimmedAndUTF8Safe(t *testing.T) {
	_, text, _, _, _, blocks, err := extractDocumentPage(strings.NewReader("<pre> \n中文\n </pre>"), "")
	if err != nil || text != "中文" || len(blocks) != 1 || blocks[0].Start != 0 || blocks[0].End != len(text) {
		t.Fatalf("trim shifted code: %q %+v %v", text, blocks, err)
	}
	var raw strings.Builder
	raw.WriteString("<main>")
	for i := range 140 {
		fmt.Fprintf(&raw, "<pre>代码%d</pre>", i)
	}
	raw.WriteString("</main>")
	_, text, _, _, _, blocks, err = extractDocumentPage(strings.NewReader(raw.String()), "")
	if err != nil || len(blocks) != 128 {
		t.Fatalf("code count unbounded: %d %v", len(blocks), err)
	}
	for i, b := range blocks {
		if text[b.Start:b.End] != fmt.Sprintf("代码%d", i) {
			t.Fatal("untracked block extended old range", b)
		}
	}
	_, text, truncated, _, _, blocks, err := extractDocumentPage(strings.NewReader("<main><pre>"+strings.Repeat("中文", MaxText/2)+"</pre></main>"), "")
	if err != nil || !truncated || !utf8.ValidString(text) || len(blocks) != 1 || blocks[0].End != len(text) {
		t.Fatalf("truncation invalid: %d %+v %v", len(text), blocks, err)
	}
}

func TestHTTPReaderCarriesCodeOffsetsWithoutNetwork(t *testing.T) {
	raw := `<article><h2>Session</h2><pre>class Session { sharedPool; }</pre><p>A new Session uses the shared pool. Independent pools are optional.</p></article>`
	c := &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(raw)), Request: req}, nil
	})}
	d, err := NewHTTP(c).Read(context.Background(), "https://docs.example/session")
	if err != nil || len(d.CodeBlocks) != 1 || d.Text[d.CodeBlocks[0].Start:d.CodeBlocks[0].End] != "class Session { sharedPool; }" {
		t.Fatalf("HTTP lost offsets: %+v %v", d, err)
	}
}

func TestCodeMetadataRefreshesPreviousCacheNamespace(t *testing.T) {
	u := "https://example.org/code-migration"
	r := &qualityReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: r}
	h := sha256.Sum256([]byte("page-text-v8\x00" + u))
	oldPath := filepath.Join(c.Dir, hex.EncodeToString(h[:])+".json")
	legacy := MakeDocument(u, "Legacy", "API declaration and a contract without any code layout metadata.", "http")
	if err := fsutil.WriteJSON(oldPath, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		d, err := c.Read(context.Background(), u)
		if err != nil || d.Text == legacy.Text || r.calls != 1 {
			t.Fatalf("cache refresh failed: %+v %d %v", d, r.calls, err)
		}
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("old cache removed", err)
	}
}

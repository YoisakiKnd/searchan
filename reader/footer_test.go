package reader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"searchan/internal/fsutil"
)

func TestLegacyChromeCannotSupplyEvidenceOrConsumeReferenceSlots(t *testing.T) {
	for _, marker := range []string{`class="mainFooter"`, `class="footers"`, `class="footer-content"`, `id="site-footer"`, `class="page-footer"`, `id="footer"`, `role="contentinfo"`, `class="nav"`, `id="navigation"`, `class="navbar"`, `class="site-nav"`, `id="main-nav"`} {
		t.Run(marker, func(t *testing.T) {
			var raw strings.Builder
			raw.WriteString(`<div ` + marker + `><h2>Footer navigation</h2>`)
			for i := range 40 {
				fmt.Fprintf(&raw, `<a href="https://noise%d.example/">Footer navigation link</a>`, i)
			}
			raw.WriteString(`</div><article><h1>Taxonomy</h1><p>The animal belongs to the described family.</p><a href="https://source.example/taxonomy">Taxonomy source</a><div ` + marker + `><h2>Footer copyright</h2><a href="/department">Footer department</a></div><h2>Evidence</h2><p>Author's explanation and footer terminology survive.</p></article>`)
			_, text, _, links, headings, err := extractOutlinedPage(strings.NewReader(raw.String()), "https://publisher.example/article")
			if err != nil || strings.Contains(text, "Footer") || len(links) != 1 || links[0].URL != "https://source.example/taxonomy" || len(headings) != 2 {
				t.Fatalf("footer leaked or consumed slots: %q %+v %+v %v", text, links, headings, err)
			}
			for _, h := range headings {
				if got := text[h.Start:h.End]; got != "Taxonomy" && got != "Evidence" {
					t.Fatal("heading offsets changed", got)
				}
			}
			if !strings.Contains(text, "belongs to") || !strings.Contains(text, "footer terminology survive") {
				t.Fatal("body lost", text)
			}
		})
	}
}

func TestFooterNamesAreExactContainerMarkers(t *testing.T) {
	raw := `<article><h1>Footer layout</h1><div class="footer-example nav-example">Example wrapper stays.</div><div class="not-footer not-nav">Negative marker stays.</div><code class="footer nav">Code stays.</code><span id="footer" class="nav">Inline text stays.</span><p>Text discussing mainFooter and footer-content stays.</p><a href="https://source.example/layout">Source stays.</a></article>`
	_, text, _, links, err := extractPage(strings.NewReader(raw), "https://publisher.example/layout")
	if err != nil || len(links) != 1 || strings.Count(text, "stays.") != 6 {
		t.Fatalf("footer rule matched prose or substrings: %q %+v %v", text, links, err)
	}
}

func TestFooterExtractionRefreshesPreviousCacheNamespace(t *testing.T) {
	u := "https://example.org/footer-migration"
	r := &qualityReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: r}
	hash := sha256.Sum256([]byte("page-text-v7\x00" + u))
	oldPath := filepath.Join(c.Dir, hex.EncodeToString(hash[:])+".json")
	legacy := MakeDocument(u, "Legacy", "Footer navigation filled the extracted page body and reference slots.", "http")
	if err := fsutil.WriteJSON(oldPath, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		doc, err := c.Read(context.Background(), u)
		if err != nil || doc.Text == legacy.Text || r.calls != 1 {
			t.Fatalf("footer cache was not refreshed once: %+v %d %v", doc, r.calls, err)
		}
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("migration deleted old data", err)
	}
}

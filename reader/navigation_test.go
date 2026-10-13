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

func TestStructuralNavigationDoesNotBecomeEvidenceOrDiscovery(t *testing.T) {
	var raw strings.Builder
	raw.WriteString(`<main><h1>银羽兽</h1><div class="navbox"><h2>导航标题</h2>`)
	for i := range 40 {
		fmt.Fprintf(&raw, `<a href="https://noise%d.example/">导航噪声</a>`, i)
	}
	raw.WriteString(`</div><table class="vertical-navbox"><tr><td>垂直导航噪声</td></tr></table><div id="catlinks">分类管理噪声</div><div class="mw-normal-catlinks">分类导航噪声</div><div class="mw-hidden-catlinks">隐藏分类噪声</div><h2>科学分类<span class="mw-editsection"><a href="https://edit.example/">编辑噪声</a></span></h2><table class="infobox"><tr><th>科</th><td>银羽科</td></tr></table><p>银羽兽属于银羽科；正文中的navbox和catlinks词语须保留。</p><a href="https://source.example/taxonomy">原始分类资料</a></main>`)
	_, text, _, links, headings, err := extractOutlinedPage(strings.NewReader(raw.String()), "https://wiki.example/animal")
	if err != nil || strings.Contains(text, "噪声") || strings.Contains(text, "导航标题") || !strings.Contains(text, "银羽科") || !strings.Contains(text, "navbox和catlinks") || len(links) != 1 || links[0].URL != "https://source.example/taxonomy" {
		t.Fatalf("navigation contaminated body or displaced sources: %q links=%+v err=%v", text, links, err)
	}
	if len(headings) != 2 || text[headings[1].Start:headings[1].End] != "科学分类" {
		t.Fatal("discarded edit control changed heading offsets", headings)
	}
}

func TestNavigationMarkersAreExactStructuralTokens(t *testing.T) {
	raw := `<article><h1>Navbox examples</h1><div class="not-navbox">前缀词保留</div><div class="catlinks-example">后缀词保留</div><p>正文讨论mw-editsection和catlinks。</p><code class="navbox">代码保留</code><span class="navbox">行内文字保留</span><table class="infobox"><tr><td>属性表保留</td></tr></table><ol class="references"><li><a href="https://source.example/paper">参考原文保留</a></li></ol><table class="NAVBOX extra"><tr><td>导航噪声</td></tr></table></article>`
	_, text, _, links, err := extractPage(strings.NewReader(raw), "https://wiki.example/entry")
	if err != nil || strings.Contains(text, "导航噪声") || len(links) != 1 {
		t.Fatalf("structural navigation retained or references removed: %q %+v %v", text, links, err)
	}
	for _, want := range []string{"前缀词保留", "后缀词保留", "正文讨论mw-editsection和catlinks", "代码保留", "行内文字保留", "属性表保留", "参考原文保留"} {
		if !strings.Contains(text, want) {
			t.Fatal("article content removed", want, text)
		}
	}
}

func TestCacheRefreshesNavigationContaminatedBody(t *testing.T) {
	u := "https://example.org/reference"
	r := &qualityReader{}
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Next: r}
	hash := sha256.Sum256([]byte("page-text-v5\x00" + u))
	oldPath := filepath.Join(c.Dir, hex.EncodeToString(hash[:])+".json")
	legacy := MakeDocument(u, "Legacy", "Category navigation and edit controls filled the extracted body.", "http")
	if err := fsutil.WriteJSON(oldPath, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		doc, err := c.Read(context.Background(), u)
		if err != nil || doc.Text == legacy.Text || r.calls != 1 {
			t.Fatalf("navigation cache retained or fresh cache missed: %+v calls=%d err=%v", doc, r.calls, err)
		}
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("migration removed original cache", err)
	}
}

func BenchmarkExtractStructuralNavigation(b *testing.B) {
	raw := `<main><h1>Runtime reference</h1><div class="navbox">` + strings.Repeat(`<a href="https://noise.example/">Runtime reference navigation</a>`, 300) + `</div><h2>Contract</h2>` + strings.Repeat(`<p>Runtime cancellation is a cooperative signal. 中文正文。</p>`, 300) + `</main>`
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for range b.N {
		if _, text, _, _, err := extractPage(strings.NewReader(raw), "https://wiki.example/reference"); err != nil || text == "" {
			b.Fatal(err)
		}
	}
}

package reader

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutlineBelongsToSelectedVisibleBody(t *testing.T) {
	raw := `<h1>Outside</h1><main><h2>Page chrome</h2><article><h2>Card</h2><div class="article-body"><h1>查询手册</h1><div id="toc"><h2>目录</h2><a href="https://noise.example/">noise</a></div><h2>Proto <code>Wire.Code</code> 与协议</h2><p>正文 mentions toc and table-of-contents literally.</p><nav><h3>Invisible</h3></nav><h3>规则</h3><p>必须保留完整的条件。</p></div></article></main>`
	_, text, _, links, headings, err := extractOutlinedPage(strings.NewReader(raw), "https://docs.example/manual")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"查询手册", "Proto Wire.Code 与协议", "规则"}
	if len(headings) != len(want) || len(links) != 0 || strings.Contains(text, "Page chrome") || strings.Contains(text, "Invisible") || !strings.Contains(text, "mentions toc and table-of-contents literally") {
		t.Fatalf("outline retained discarded page content: %q %+v %v", text, headings, links)
	}
	for i, h := range headings {
		if text[h.Start:h.End] != want[i] {
			t.Fatalf("heading offset lost primary promotion/inline text: %+v %q", h, text[h.Start:h.End])
		}
	}
}

func TestOutlineIsBoundedAndUTF8Safe(t *testing.T) {
	var body strings.Builder
	body.WriteString("<main>")
	for i := range 150 {
		fmt.Fprintf(&body, "<h2>标题%d</h2><p>完整正文。</p>", i)
	}
	body.WriteString("<h2>" + strings.Repeat("中文", MaxText/2) + "</h2></main>")
	_, text, truncated, _, headings, err := extractOutlinedPage(strings.NewReader(body.String()), "")
	if err != nil || !truncated || len(headings) != 128 || !utf8.ValidString(text) {
		t.Fatalf("invalid outline bounds: len=%d headings=%d truncated=%v err=%v", len(text), len(headings), truncated, err)
	}
	for i, h := range headings {
		if h.Start < 0 || h.End > len(text) || !utf8.ValidString(text[h.Start:h.End]) || text[h.Start:h.End] != fmt.Sprintf("标题%d", i) {
			t.Fatal("untracked heading extended a previous range", h)
		}
	}
}

func BenchmarkExtractWithOutline(b *testing.B) {
	var body strings.Builder
	body.WriteString("<main><div id=\"toc\">navigation only</div>")
	for i := range 80 {
		fmt.Fprintf(&body, "<h2>Protocol section %d</h2><p>%s</p>", i, strings.Repeat("A protocol implementation defines bounded data access and explicit cancellation behavior. ", 8))
	}
	body.WriteString("</main>")
	raw := body.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for range b.N {
		_, _, _, _, headings, err := extractOutlinedPage(strings.NewReader(raw), "")
		if err != nil || len(headings) != 80 {
			b.Fatal("outline lost", err, len(headings))
		}
	}
}

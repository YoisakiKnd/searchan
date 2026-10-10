package reader

import (
	"strings"
	"testing"
)

func TestExtractPreservesInlineCodeAndWhitespace(t *testing.T) {
	raw := `<main><p>Use <code>context.<b>Done</b>()</code>, or <em>Err</em>TooLarge.</p><p>中文<strong>标识</strong>符。 A
	 B</p><pre>if ok {
    stop()
}</pre><table><tr><th>Limit</th><th>Value</th></tr><tr><td>Timeout</td><td>8s</td></tr></table></main>`
	_, text, err := Extract(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Use context.Done(), or ErrTooLarge.", "中文标识符。 A B", "if ok {\n    stop()\n}", "Limit\tValue", "Timeout\t8s"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

func TestExtractKeepsShortArticleHeaderAndSkipsHiddenText(t *testing.T) {
	raw := `<header>Global menu</header><div role="navigation">Links</div><p>Outside noise.</p><article><header><h1>Quota</h1></header><p>20 requests.</p><div hidden>Wrong 1000.</div><span aria-hidden="true">Hidden 500.</span><template>Template 300.</template></article>`
	_, text, err := Extract(strings.NewReader(raw))
	if err != nil || text != "Quota\n\n20 requests." {
		t.Fatalf("short primary content corrupted: %q %v", text, err)
	}
}

func TestExtractOptionalEndTagsAndNestedLists(t *testing.T) {
	for _, raw := range []string{
		"<main>" + strings.Repeat("<p>Short fact.", 600) + "</main>",
		"<main><table>" + strings.Repeat("<tr><td>name<td>value", 600) + "</table></main>",
		"<main><ul><li>Outer<ul><li>Inner<li>Another</ul><li>Next</ul></main>",
	} {
		_, text, err := Extract(strings.NewReader(raw))
		if err != nil || text == "" {
			t.Fatalf("valid optional end tags rejected: %v", err)
		}
	}
}

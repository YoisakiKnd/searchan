package reader

import (
	"fmt"
	"strings"
	"testing"
)

func TestDiscoveryLinksAreBoundedVisibleExternalAndPublic(t *testing.T) {
	body := `<nav><a href="https://noise.example/projects/vector-store">ignored navigation</a></nav><article>
<a href="https://vendor.example/projects/vector-store#overview">original</a>
<a href="https://vendor.example/projects/vector-store#other">duplicate</a>
<a href="/projects/vector-store">same site</a>
<a href="http://127.0.0.1/">private</a><a href="file:///etc/passwd">file</a>
<a href="https://vendor.example:444/path">port</a><a hidden href="https://hidden.example/project">hidden</a>
<script><a href="https://script.example/">script</a></script>
<p>Vector Store provides a common data access model for applications.</p>`
	for i := range 60 {
		body += fmt.Sprintf(`<a href="https://source%d.example/project">source</a>`, i)
	}
	body += `</article>`
	_, text, _, links, err := extractPage(strings.NewReader(body), "https://mirror.example/project")
	if err != nil || len(links) != 32 || links[0].URL != "https://vendor.example/projects/vector-store" || links[0].Text != "original" || !strings.Contains(text, "common data access") {
		t.Fatalf("bad extraction: links=%v err=%v", links, err)
	}
	for _, link := range links {
		if strings.Contains(link.URL, "noise") || strings.Contains(link.URL, "mirror") || strings.Contains(link.URL, "hidden") || strings.Contains(link.URL, "script") || ValidateURL(link.URL) != nil {
			t.Fatalf("unsafe or hidden discovery: %+v", link)
		}
	}
}

func TestLanguageMenusDoNotConsumeSourceLinks(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<main><h1>Query Engine</h1><div class="vector-dropdown mw-portlet mw-portlet-lang"><ul>`)
	for i := range 40 {
		fmt.Fprintf(&body, `<li class="interlanguage-link"><a href="https://lang%d.example/wiki/Query_Engine">Alternate language</a></li>`, i)
	}
	body.WriteString(`</ul></div><p>Query Engine is an application framework for processing structured data.</p><a href="https://vendor.example/projects/query-engine">vendor.example/projects/query-engine</a></main>`)
	_, text, _, links, err := extractPage(strings.NewReader(body.String()), "https://wiki.example/wiki/Query_Engine")
	if err != nil || strings.Contains(text, "Alternate language") || len(links) != 1 || links[0].URL != "https://vendor.example/projects/query-engine" {
		t.Fatalf("language UI displaced original: text=%q links=%v err=%v", text, links, err)
	}
}

func BenchmarkExtractWithDiscoveryLinks(b *testing.B) {
	var body strings.Builder
	body.WriteString("<html><nav>navigation</nav><article>")
	for i := range 32 {
		fmt.Fprintf(&body, `<a href="https://source%d.example/project">Original source %d</a>`, i, i)
	}
	body.WriteString(strings.Repeat("<p>Go context cancellation and bounded concurrent search workers. 中文正文。</p>", 300))
	body.WriteString("</article></html>")
	raw := body.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for range b.N {
		if _, _, _, _, err := extractPage(strings.NewReader(raw), "https://mirror.example/project"); err != nil {
			b.Fatal(err)
		}
	}
}

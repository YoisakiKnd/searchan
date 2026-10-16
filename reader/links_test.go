package reader

import (
	"fmt"
	"strings"
	"testing"
)

func TestDiscoveryLinksPrioritizeExternalAndRemainBoundedVisiblePublic(t *testing.T) {
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

func TestSameSiteReferencesRequirePrimaryBodyAndPreserveAnchorText(t *testing.T) {
	raw := `<header><a href="/header">header</a></header>
<a href="/outside">outside</a><nav><a href="/navigation">navigation</a></nav>
<main><a href="/atlas/retry#rules">Atlas <code>retry</code> reference</a>
<a href="/atlas/retry#duplicate">duplicate</a>
<a href="https://DOCS.example/atlas/api">API reference</a>
<a href="/atlas/start#self">self</a><a href="#local">local fragment</a>
<a href="/hidden" hidden>hidden</a><aside><a href="/aside">aside</a></aside>
<a href="https://docs.example:444/private">bad port</a>
<a href="https://sources.example/atlas">Original <em>source</em></a>
<p>Atlas retry contract is defined by the linked API documentation.</p></main>`
	_, text, _, links, err := extractPage(strings.NewReader(raw), "https://docs.example/atlas/start")
	// Host spelling is case-insensitive for deciding whether a link is local.
	if err != nil || len(links) != 3 || links[0].URL != "https://sources.example/atlas" || links[1].URL != "https://docs.example/atlas/retry" || links[1].Text != "Atlas retry reference" || links[2].URL != "https://DOCS.example/atlas/api" || links[2].Text != "API reference" {
		t.Fatalf("same-site reference lost or navigation admitted: %+v %v", links, err)
	}
	if strings.Contains(text, "navigation") || !strings.Contains(text, "linked API documentation") {
		t.Fatal("body changed while collecting links", text)
	}
}

func TestSameSiteReserveCannotDisplaceExternalSources(t *testing.T) {
	for _, external := range []int{0, 28, 32} {
		var body strings.Builder
		body.WriteString("<article>")
		for i := range 50 {
			fmt.Fprintf(&body, `<a href="/reference/%d">Local reference %d</a>`, i, i)
		}
		for i := range external {
			fmt.Fprintf(&body, `<a href="https://source%d.example/atlas">Original reference %d</a>`, i, i)
		}
		body.WriteString("</article>")
		_, _, _, links, err := extractPage(strings.NewReader(body.String()), "https://docs.example/atlas")
		if err != nil || len(links) != external+min(8, 32-external) {
			t.Fatalf("discovery reserve exceeded: external=%d links=%+v err=%v", external, links, err)
		}
		for i := range external {
			if links[i].URL != fmt.Sprintf("https://source%d.example/atlas", i) || links[i].Text != fmt.Sprintf("Original reference %d", i) {
				t.Fatal("same-site links displaced or corrupted external source", links[i])
			}
		}
		for i := external; i < len(links); i++ {
			if links[i].Text != fmt.Sprintf("Local reference %d", i-external) {
				t.Fatal("same-site text associated with wrong link", links[i])
			}
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

func BenchmarkExtractWithSameSiteReferences(b *testing.B) {
	var body strings.Builder
	body.WriteString("<main><nav>navigation</nav>")
	for i := range 8 {
		fmt.Fprintf(&body, `<a href="/reference/%d">Same-site API reference %d</a>`, i, i)
	}
	for i := range 4 {
		fmt.Fprintf(&body, `<a href="https://source%d.example/contract">Original source %d</a>`, i, i)
	}
	body.WriteString(strings.Repeat("<p>Concurrent work must preserve request cancellation and transaction retry conditions.</p>", 300))
	body.WriteString("</main>")
	raw := body.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for range b.N {
		if _, _, _, _, err := extractPage(strings.NewReader(raw), "https://docs.example/start"); err != nil {
			b.Fatal(err)
		}
	}
}

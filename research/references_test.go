package research

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestSameSiteBodyReferenceReachesExistingDiscoveryAndEvidence(t *testing.T) {
	const page = "https://docs.example/atlas/start"
	const contract = "https://docs.example/atlas/retry"
	var visits []string
	r := reader.NewHTTP(&http.Client{Transport: evidenceWireTransport(func(req *http.Request) (*http.Response, error) {
		visits = append(visits, req.URL.String())
		body := `<main><p>Atlas retry examples introduce concurrent transaction conflict handling.</p><a href="/atlas/retry#contract">Atlas retry keywords</a></main>`
		if req.URL.String() == contract {
			body = `<main><h1>Atlas retry contract</h1><p>Atlas retry requires retrying the complete transaction after a serialization conflict.</p><a href="/atlas/another">Atlas retry reference</a></main>`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})})
	doc, err := r.Read(context.Background(), page)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: r})
	out := Output{Plan: Plan{Queries: []string{"Atlas retry contract"}}, Stats: Stats{PagesRead: 1}}
	g := newGathering(e, &out)
	g.reading.docs = []reader.Document{doc}
	g.reading.attempted[page] = true
	pool := g.coverageCandidates()
	if len(pool) != 1 || pool[0].URL != contract || pool[0].ReferencedBy != page || len(out.Evidence) != 0 {
		t.Fatal("same-site path was dropped or became unread evidence", pool, out)
	}
	g.readRelevantReference(context.Background(), 2)
	g.readRelevantReference(context.Background(), 8)
	found := false
	for _, ev := range out.Evidence {
		if ev.URL == contract && strings.Contains(ev.Text, "complete transaction") && ev.Kind == "body" {
			found = true
		}
	}
	if !found || len(visits) != 2 || visits[1] != contract || out.Stats.PagesRead != 2 || out.Stats.DiscoveryReads != 1 || out.Stats.ModelCalls != 0 {
		t.Fatalf("same-site reference did not follow bounded discovery: visits=%v output=%+v", visits, out)
	}
}

func TestReferenceDiscoveryUsesOneHopAndSharedPageBudget(t *testing.T) {
	for _, mode := range []string{"read", "full", "cancelled", "attempted", "unrelated_body"} {
		t.Run(mode, func(t *testing.T) {
			var visits []string
			e, err := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: readFunc(func(_ context.Context, u string) (reader.Document, error) {
				visits = append(visits, u)
				title, text := "Atlas retry contract", "Atlas retry contract requires retrying the complete transaction after a conflict."
				if mode == "unrelated_body" {
					title, text = "Cooking recipes", "Cooking recipes describe ingredients and preparation methods for healthy meals."
				}
				doc := reader.MakeDocument(u, title, text, "fixture")
				doc.Links = []reader.Link{{URL: "https://second.example/atlas/retry", Text: "Atlas retry guidance"}}
				return doc, nil
			}), ReadTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			out := Output{Plan: Plan{Queries: []string{"Atlas retry contract"}}, Stats: Stats{PagesRead: 2, ModelCalls: 2}}
			g := newGathering(e, &out)
			doc := reader.MakeDocument("https://article.example/atlas", "Atlas retry contract", "Atlas retry contract is discussed in this tutorial with examples of conflict handling.", "fixture")
			doc.Links = []reader.Link{
				{URL: "http://127.0.0.1/atlas/retry", Text: "Atlas retry reference"},
				{URL: "https://ads.example/atlas", Text: "Official Atlas docs"},
				{URL: "https://other.example/cooking", Text: "Cooking reference"},
				{URL: "https://manual.example/atlas/retry", Text: "Atlas retry guidance"},
				{URL: "https://manual.example/atlas/retry#same", Text: "Atlas retry guidance"},
			}
			g.reading.docs = []reader.Document{doc}
			if mode == "attempted" {
				g.reading.attempted[doc.Links[3].URL] = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			limit := 3
			if mode == "full" {
				limit = 2
			}
			if mode == "cancelled" {
				cancel()
			}
			g.readRelevantReference(ctx, limit)
			g.readRelevantReference(context.Background(), 8)
			want := 0
			if mode == "read" || mode == "unrelated_body" {
				want = 1
			}
			if len(visits) != want || out.Stats.DiscoveryReads != want || out.Stats.PagesRead != 2+want || out.Stats.ModelCalls != 2 {
				t.Fatalf("reference discovery bypassed boundaries: visits=%v stats=%+v", visits, out.Stats)
			}
			if want == 1 && visits[0] != doc.Links[3].URL {
				t.Fatal("invalid, irrelevant or duplicate link selected", visits)
			}
			if mode == "unrelated_body" {
				for _, ev := range out.Evidence {
					if strings.Contains(ev.Text, "Cooking recipes") {
						t.Fatal("reference label promoted unrelated body to evidence")
					}
				}
			}
		})
	}
}

func TestReferenceTermsDoNotCountBoilerplateNumbersOrAliases(t *testing.T) {
	for _, term := range []string{"", "x", "io", "docs", "reference", "official", "2026", "1.2.3", "retry-contract", "文", "文档"} {
		if substantiveReferenceTerm(term) {
			t.Fatal("non-substantive reference match", term)
		}
	}
	for _, term := range []string{"atlas", "retry", "transaction", "http", "失败", "重试"} {
		if !substantiveReferenceTerm(term) {
			t.Fatal("substantive reference term discarded", term)
		}
	}
}

func TestReferenceDiscoveryDoesNotReplaceNamedModuleWithSibling(t *testing.T) {
	reads := 0
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		reads++
		return reader.Document{}, nil
	})})
	out := Output{Plan: Plan{Queries: []string{"Vector Store Commons 在 Vector Store 中负责什么？"}}}
	g := newGathering(e, &out)
	doc := reader.MakeDocument("https://article.example/commons", "Vector Store Commons", "Vector Store Commons provides shared infrastructure for repository modules.", "fixture")
	doc.Links = []reader.Link{{URL: "https://docs.example/vector-store/jpa", Text: "Vector Store JPA reference"}}
	g.reading.docs = []reader.Document{doc}
	g.readRelevantReference(context.Background(), 8)
	if reads != 0 {
		t.Fatal("shared ecosystem words caused an unrelated module read")
	}
}

func TestReferenceEntitiesKeepAliasesAndComparisonSubjects(t *testing.T) {
	plan := Plan{Queries: []string{"Vector Store Commons 在 Vector Store 中负责什么？"}}
	if got := referenceEntities(plan); len(got) != 1 || got[0] != "Vector Store Commons" {
		t.Fatal("explicit parent context broadened module reference", got)
	}
	plan = Plan{Queries: []string{"PostgreSQL Serializable 事务为什么还会失败，需要怎么处理？"}}
	if !matchesEntities("PostgreSQL’s serialization failure guidance", referenceEntities(plan)) {
		t.Fatal("CamelCase spelling alias was discarded")
	}
	plan = Plan{Intent: "compare", Queries: []string{"Vector Store Commons 和 Vector Store 有什么区别？"}}
	if len(referenceEntities(plan)) != len(relevanceEntities(plan)) {
		t.Fatal("comparison subject was removed")
	}
}

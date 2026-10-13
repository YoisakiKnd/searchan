package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestCoveragePoolContainsOnlyBoundedExistingLeads(t *testing.T) {
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: unusedCandidateReader{}})
	out := Output{Plan: Plan{Queries: []string{"Atlas cancellation guarantees"}}}
	g := newGathering(e, &out)
	for i := range 30 {
		g.retrieval.hits = append(g.retrieval.hits, hit{Result: search.Result{URL: fmt.Sprintf("https://search.example/%d", i), Title: strings.Repeat("Atlas", 100), Snippet: strings.Repeat("cancellation guarantees ", 50)}, fusion: 1 / float64(61+i)})
	}
	g.reading.attempted["https://search.example/0"] = true
	doc := reader.MakeDocument("https://read.example/article", "Atlas", "Atlas reference", "fixture")
	doc.Links = []reader.Link{{URL: "http://127.0.0.1/docs", Text: "Official docs"}, {URL: "https://ads.example/", Text: "Documentation sale"}, {URL: "https://source.example/docs", Text: "Official docs"}, {URL: "https://source.example/docs#same", Text: "Reference"}}
	for i := range 10 {
		doc.Links = append(doc.Links, reader.Link{URL: fmt.Sprintf("https://source.example/spec/%d", i), Text: "specification"})
	}
	g.reading.docs = []reader.Document{doc}
	pool := g.coverageCandidates()
	if len(pool) != 20 {
		t.Fatal("candidate pool is not bounded", len(pool))
	}
	urls := map[string]bool{}
	links := 0
	for i, c := range pool {
		if c.ID != fmt.Sprintf("c%d", i+1) || c.URL == "https://search.example/0" || strings.Contains(c.URL, "127.0.0.1") || urls[c.URL] || len(c.Title) > 200 || len(c.Snippet) > 300 {
			t.Fatal("invalid or duplicate candidate metadata", c)
		}
		urls[c.URL] = true
		if c.Origin == "document_link" {
			links++
		}
	}
	if links != 4 || !urls["https://source.example/docs"] {
		t.Fatal("explicit document references lost")
	}
	// A label is only a lead, not authentication: even misleading labels may
	// be presented, but neither the label nor its URL becomes factual evidence.
	if len(out.Evidence) != 0 || out.Stats.PagesRead != 0 {
		t.Fatal("unread metadata became evidence")
	}
}

func TestCoveragePoolRetainsNumberedRFCLinks(t *testing.T) {
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: unusedCandidateReader{}})
	out := Output{Plan: Plan{Queries: []string{"Atlas default encoding"}}}
	g := newGathering(e, &out)
	doc := reader.MakeDocument("https://library.example/atlas", "Atlas", "Atlas encoding overview", "fixture")
	doc.Links = []reader.Link{
		{URL: "https://standards.example/rfc1234", Text: "RFC 1234"},
		{URL: "https://standards.example/rfc5678", Text: "rfc-5678"},
		{URL: "http://127.0.0.1/rfc9999", Text: "RFC 9999"},
		{URL: "https://ads.example/sale", Text: "RFC sale"},
	}
	g.reading.docs = []reader.Document{doc}
	pool := g.coverageCandidates()
	if len(pool) != 2 || pool[0].URL != doc.Links[0].URL || pool[1].URL != doc.Links[1].URL || pool[0].Origin != "document_link" {
		t.Fatal("numbered reference anchors lost or unsafe link admitted", pool)
	}
	if out.Stats.PagesRead != 0 || len(out.Evidence) != 0 {
		t.Fatal("reference label was treated as evidence")
	}
}

func TestNumberedRFCReferenceLabelsAreStrict(t *testing.T) {
	for _, label := range []string{"RFC 1234", "rfc-1234", "RFC1234", "  RFC\t1234  "} {
		if !referenceLabel(label) {
			t.Fatal("numbered RFC anchor rejected", label)
		}
	}
	for _, label := range []string{"RFC", "RFC sale", "RFC -1", "RFC 12x4", "RFC 1234567", "Some RFC 1234 sale", "https://example.org/rfc1234"} {
		if referenceLabel(label) {
			t.Fatal("non-reference anchor accepted", label)
		}
	}
}

func TestCoveragePoolRetainsQueryLeadersBelowLexicalCutoff(t *testing.T) {
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: unusedCandidateReader{}})
	out := Output{Plan: Plan{Queries: []string{"Atlas 取消是否自动停止工作", "Atlas cancellation API reference"}}}
	g := newGathering(e, &out)
	for i := range 25 {
		g.retrieval.hits = append(g.retrieval.hits, hit{Result: search.Result{URL: fmt.Sprintf("https://tutorial%d.example/article", i), Title: "Atlas 取消是否自动停止工作", Snippet: "Atlas 取消是否自动停止工作"}, fusion: 1, queryRanks: map[string]int{queryKey(out.Plan.Queries[0]): i + 1}})
	}
	api := hit{Result: search.Result{URL: "https://library.example/package", Title: "Package documentation"}, queryRanks: map[string]int{queryKey(out.Plan.Queries[1]): 1}}
	g.retrieval.hits = append(g.retrieval.hits, api)
	for _, h := range rankHits(out.Plan, g.retrieval.hits, g.reading.attempted, 20) {
		if h.URL == api.URL {
			t.Fatal("fixture no longer exercises a low lexical result")
		}
	}
	pool := g.coverageCandidates()
	for _, c := range pool {
		if c.URL == api.URL {
			if len(pool) > 20 || c.Origin != "search" || len(out.Evidence) != 0 {
				t.Fatal("discovery result became evidence or escaped pool bounds")
			}
			return
		}
	}
	t.Fatal("original query leader was lost after fusion")
}

func TestQueryLeadersUseEachQueriesActualRanks(t *testing.T) {
	hits := []hit{
		{Result: search.Result{URL: "https://a.example"}, queryRanks: map[string]int{"english": 7, "chinese": 1}},
		{Result: search.Result{URL: "https://b.example"}, queryRanks: map[string]int{"english": 2}},
		{Result: search.Result{URL: "https://c.example"}, queryRanks: map[string]int{"english": 1}},
		{Result: search.Result{URL: "https://d.example"}, queryRanks: map[string]int{"chinese": 2}},
	}
	got := queryLeaders(hits, "english")
	if len(got) != 2 || got[0].URL != hits[2].URL || got[1].URL != hits[1].URL || len(queryLeaders(hits, "unknown")) != 0 {
		t.Fatal("query-specific ranks were confused", got)
	}
}

type unusedCandidateReader struct{}

func (unusedCandidateReader) Read(context.Context, string) (reader.Document, error) {
	panic("unexpected read")
}

func TestCoverageSelectionCannotEscapeIDsPagesOrOneHop(t *testing.T) {
	for _, remaining := range []int{0, 1, 5} {
		var mu sync.Mutex
		visited := map[string]int{}
		e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: readFunc(func(_ context.Context, u string) (reader.Document, error) {
			mu.Lock()
			visited[u]++
			mu.Unlock()
			doc := reader.MakeDocument(u, "Atlas cancellation", "Atlas cancellation is cooperative and does not automatically stop running work.", "fixture")
			doc.Links = []reader.Link{{URL: "https://unselected.example/docs", Text: "Official docs"}}
			return doc, nil
		}), ReadTimeout: time.Second})
		out := Output{Plan: Plan{Queries: []string{"Atlas cancellation"}}, Stats: Stats{PagesRead: 3}}
		g := newGathering(e, &out)
		for i := range 4 {
			g.retrieval.hits = append(g.retrieval.hits, hit{Result: search.Result{URL: fmt.Sprintf("https://source.example/%d", i), Title: "Atlas cancellation"}})
		}
		pool := g.coverageCandidates()
		changed := g.readCoverageCandidates(context.Background(), pool, []string{"https://evil.example/docs", "c999", "c1", "c1", "c2", "c3"}, 3+remaining)
		want := min(remaining, 2)
		if len(visited) != want || out.Stats.CandidateReads != want || out.Stats.PagesRead != 3+want || changed != (want > 0) {
			t.Fatalf("selection escaped budgets: %+v visits=%v", out.Stats, visited)
		}
		for _, count := range visited {
			if count != 1 {
				t.Fatal("duplicate candidate fetched")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if g.readCoverageCandidates(ctx, pool, []string{"c4"}, 10) || len(visited) != want {
			t.Fatal("cancelled selection read a page")
		}
	}
}

func TestCoverageCandidateReadKeepsModelBudgetAndDefersDuplicateSearch(t *testing.T) {
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		var results []search.Result
		for i := range 4 {
			u := fmt.Sprintf("https://manual.example/%d", i)
			text := "Atlas cancellation documentation introduces the cooperative cancellation mechanism."
			if i == 3 {
				u = "https://manual.example/specification"
				text = "Atlas cancellation guarantees notification only; running work must cooperate before stopping."
			}
			results = append(results, search.Result{URL: u, Title: "Atlas cancellation", Content: text + fmt.Sprintf(" Reference %d.", i)})
		}
		return search.Response{Results: results}, nil
	}}
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		if prompt == coveragePrompt {
			for _, c := range input.(map[string]any)["candidates"].([]coverageCandidate) {
				if strings.HasSuffix(c.URL, "/specification") {
					return assign(output, fmt.Sprintf(`{"answers_query":false,"missing":["cancellation guarantees"],"queries":[],"read_ids":[%q]}`, c.ID))
				}
			}
			t.Fatal("unread specification not offered to coverage check")
		}
		if prompt != plannerPrompt && prompt != verifyPrompt {
			for _, ev := range input.(map[string]any)["evidence"].([]modelEvidence) {
				if strings.HasSuffix(ev.URL, "/specification") {
					return assign(output, fmt.Sprintf(`{"answers_query":true,"title":"Atlas cancellation","lead":{"text":"Cancellation only signals running work.","evidence_ids":[%q]},"sections":[],"uncertainties":[]}`, ev.ID))
				}
			}
			t.Fatal("selected specification was not passed to generation")
		}
		return plannedModel(prompt, output)
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: unusedCandidateReader{}, Model: m, PlanWithModel: true, VerifyWithModel: true})
	out, err := e.Run(context.Background(), "Atlas cancellation")
	if err != nil || out.Stats.CandidateReads != 1 || out.Stats.PagesRead != 4 || out.Stats.ModelCalls != 4 || out.Stats.SearchCalls != 1 || out.Stats.RecoveryRounds != 0 || len(out.Stats.CoverageCandidates) != 1 || !out.Stats.CoverageCandidates[0].Selected {
		t.Fatalf("candidate selection wasted or bypassed the shared budget: %+v %v", out.Stats, err)
	}
}

func TestFailedCoverageReadDoesNotSuppressGapRecovery(t *testing.T) {
	p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
		if req.Query != "Atlas cancellation" {
			return search.Response{Results: []search.Result{{URL: "https://recovery.example/docs", Title: "Atlas cancellation", Content: "Atlas cancellation guarantees notification only; running workers must cooperate with the signal."}}}, nil
		}
		var results []search.Result
		for i := range 3 {
			results = append(results, search.Result{URL: fmt.Sprintf("https://tutorial%d.example/atlas", i), Title: "Atlas cancellation", Content: fmt.Sprintf("Atlas cancellation is introduced in this tutorial for asynchronous background tasks. Example %d.", i)})
		}
		results = append(results, search.Result{URL: "https://unreachable.example/spec", Title: "Atlas API"})
		return search.Response{Results: results}, nil
	}}
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		if prompt != coveragePrompt {
			t.Fatal("unexpected extra model call")
		}
		for _, c := range input.(map[string]any)["candidates"].([]coverageCandidate) {
			if c.URL == "https://unreachable.example/spec" {
				return assign(output, fmt.Sprintf(`{"answers_query":false,"missing":["cancellation guarantees"],"queries":[],"read_ids":[%q]}`, c.ID))
			}
		}
		t.Fatal("missing specification candidate")
		return nil
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		return reader.Document{}, errors.New("fixture source unavailable")
	}), Model: m, PlanWithModel: true})
	out := Output{Query: "Atlas cancellation", Plan: Plan{Queries: []string{"Atlas cancellation"}, Aspects: []string{"cancellation guarantees"}}}
	g := newGathering(e, &out)
	g.gather(context.Background())
	found := false
	for _, ev := range out.Evidence {
		found = found || ev.URL == "https://recovery.example/docs"
	}
	if !found || out.Stats.CandidateReads != 1 || out.Stats.RecoveryRounds != 1 || out.Stats.SearchCalls != 2 || out.Stats.ModelCalls != 1 {
		t.Fatalf("failed candidate read hid a core evidence gap: %+v", out.Stats)
	}
}

func BenchmarkCoverageCandidates(b *testing.B) {
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: unusedCandidateReader{}})
	out := Output{Plan: Plan{Queries: []string{"Go context cancellation", "Golang cancellation documentation", "context cooperative workers"}, Aspects: []string{"cancellation", "deadline"}}}
	g := newGathering(e, &out)
	for i := range 90 {
		ranks := map[string]int{}
		for qi, q := range out.Plan.Queries {
			ranks[queryKey(q)] = (i+qi)%15 + 1
		}
		u := fmt.Sprintf("https://source%d.example/%d", i%8, i)
		g.retrieval.hits = append(g.retrieval.hits, hit{Result: search.Result{URL: u, Title: "Go context cancellation reference", Snippet: strings.Repeat("Context cancellation and deadlines release resources. ", 8)}, pageKey: u, fusion: 1 / float64(61+i%15), queryRanks: ranks})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.coverageCandidates()
	}
}

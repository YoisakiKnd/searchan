package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestShortFactsAndTechnicalIdentifiers(t *testing.T) {
	docs := []reader.Document{
		reader.MakeDocument("https://docs.example/config", "Configuration", "Timeout: 8s.\n\nRetries: 0.", "fixture"),
		reader.MakeDocument("https://noise.example/", "News", strings.Repeat("Weather cooking music news. ", 20), "fixture"),
	}
	ev := SelectEvidence("timeout retries", docs, nil, 4)
	if len(ev) != 1 || !strings.Contains(ev[0].Text, "Retries: 0.") {
		t.Fatalf("short facts dropped or irrelevant source filled slots: %+v", ev)
	}
	for _, pair := range [][2]string{{"C++", "C#"}, {"1.26", "1.25"}, {"context.Done", "context.Background"}} {
		q := "reference " + pair[0]
		if relevance(q, "reference "+pair[0]) <= relevance(q, "reference "+pair[1]) {
			t.Fatalf("technical identity collapsed: %v", pair)
		}
	}
}

func TestEvidencePreservesContradictionsAndNumbers(t *testing.T) {
	for _, bodies := range [][2]string{
		{"Remote access is permitted on port 443.", "Remote access is not permitted on port 443."},
		{"The request quota is 20 per minute.", "The request quota is 100 per minute."},
	} {
		var docs []reader.Document
		for i, text := range bodies {
			docs = append(docs, reader.MakeDocument(fmt.Sprintf("https://source%d.example/", i), "Policy request remote quota", text, "fixture"))
		}
		ev := SelectEvidence("remote access request quota", docs, nil, 2)
		if len(ev) != 2 {
			t.Fatalf("numerical or negated variant suppressed: %+v", ev)
		}
	}
}

func TestAspectSelectionAndPassageBoundaries(t *testing.T) {
	text := strings.Repeat("Context cancellation makes workers release resources. ", 60) + "\n\nShort fact.\n\n" + strings.Repeat("A deadline is the maximum time allowed for a request. ", 20)
	docs := []reader.Document{reader.MakeDocument("https://docs.example/", "Context", text, "fixture")}
	ev := SelectEvidenceForPlan(Plan{Queries: []string{"context cancellation deadline"}, Aspects: []string{"cancellation", "deadline"}}, docs, nil, 3)
	var mask uint8
	for _, e := range ev {
		if !strings.Contains(text, e.Text) || len(e.Text) > 1800 || e.ParagraphEnd < e.Paragraph {
			t.Fatalf("broken original-text location: %+v", e)
		}
		for _, a := range e.MatchedAspects {
			mask |= 1 << a
		}
	}
	if mask != 3 {
		t.Fatalf("lost question aspect: %b %+v", mask, ev)
	}
}

func TestFusionDoesNotCountRepeatedQueriesOrSharedDDG(t *testing.T) {
	makeP := func(name string, engines []string) search.Provider {
		return provider{name, func(context.Context, search.Request) (search.Response, error) {
			return search.Response{Results: []search.Result{{URL: "https://example.org/page#one", Title: "Query", Sources: []search.Source{{Provider: name, Rank: 1, Engines: engines}}}, {URL: "https://example.org/page#two", Title: "Duplicate anchor"}}}, nil
		}}
	}
	e, _ := New(Config{Providers: []search.Provider{makeP("ddg", nil), makeP("searxng", []string{"duckduckgo"})}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil })})
	stats := Stats{}
	s := newRetrieval(e, &stats)
	s.search(context.Background(), []string{"query", "query expansion"}, 4)
	if len(s.hits) != 1 || len(s.hits[0].votes) != 1 || s.hits[0].fusion != 1.0/61 {
		t.Fatalf("correlated votes or fragments counted twice: %+v", s.hits)
	}
	if s.hits[0].Sources[0].Rank != 1 || len(s.hits[0].Sources) != 2 {
		t.Fatalf("missing real source provenance: %+v", s.hits[0].Sources)
	}
	s.search(context.Background(), []string{"query"}, 4)
	if stats.SearchCalls != 4 {
		t.Fatal("identical paid calls repeated")
	}
}

func TestRecoveryUsesUnusedProviderAndUnreadInitialHits(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	var ps []search.Provider
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("p%d", i)
		ps = append(ps, provider{name, func(context.Context, search.Request) (search.Response, error) {
			mu.Lock()
			calls[name]++
			mu.Unlock()
			if name != "p4" {
				return search.Response{}, errors.New("offline")
			}
			return search.Response{Results: []search.Result{{URL: "https://good.example/", Title: "Go context", Content: strings.Repeat("Go context supports bounded cancellation. ", 3)}}}, nil
		}})
	}
	e, _ := New(Config{Providers: ps, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), MaxSearchCalls: 6})
	out, err := e.Run(context.Background(), "Go context")
	if err != nil || len(out.Evidence) == 0 || calls["p4"] != 1 || calls["p0"] != 1 {
		t.Fatalf("recovery repeated broken providers: calls=%v err=%v", calls, err)
	}
	// A recovery returning the same result list still has useful unread pages.
	p := provider{"one", func(context.Context, search.Request) (search.Response, error) {
		var rs []search.Result
		for i := 0; i < 8; i++ {
			rs = append(rs, search.Result{URL: fmt.Sprintf("https://docs.example/%d#fragment", i), Title: "Go context"})
		}
		return search.Response{Results: rs}, nil
	}}
	readCount := map[string]int{}
	r := readFunc(func(_ context.Context, u string) (reader.Document, error) {
		mu.Lock()
		defer mu.Unlock()
		readCount[u]++
		if len(readCount) <= 6 {
			return reader.Document{}, errors.New("unreadable")
		}
		return reader.MakeDocument(u, "Go context", strings.Repeat("Go context readable reference ", 5)+u, "fixture"), nil
	})
	e, _ = New(Config{Providers: []search.Provider{p}, Reader: r, MaxPages: 8, MaxSearchCalls: 2})
	out, err = e.Run(context.Background(), "Go context")
	if err != nil || out.Stats.Documents != 2 || out.Stats.PagesRead != 8 {
		t.Fatalf("unread initial hits lost: %+v %v", out.Stats, err)
	}
	for u, n := range readCount {
		if n != 1 || strings.Contains(u, "#") {
			t.Fatalf("page re-read or fragment sent: %s %d", u, n)
		}
	}
}

func TestSlowProviderLeavesUsableAnswerTime(t *testing.T) {
	slow := provider{"slow", func(ctx context.Context, _ search.Request) (search.Response, error) {
		<-ctx.Done()
		return search.Response{}, ctx.Err()
	}}
	fast := provider{"fast", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://go.dev/", Title: "Go context", Content: strings.Repeat("Go context cancellation reference. ", 5)}}}, nil
	}}
	lm := modelFunc(func(_ context.Context, _ string, _ any, out any) error {
		return assign(out, `{"title":"Go context","lead":{"text":"Context reference","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: []search.Provider{slow, fast}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: lm, MaxSearchCalls: 4, SearchTimeout: 15 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	out, err := e.Run(ctx, "Go context")
	if err != nil || out.Answer.Title != "Go context" || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("slow provider consumed answer deadline: %v %+v", err, out.Stats)
	}
}

func TestPlanNormalization(t *testing.T) {
	p := normalizePlan(Plan{Intent: "howto", Queries: []string{"Go context"}, Aspects: []string{"original"}}, Plan{Intent: "invented", Queries: []string{" GO  context ", "context Done", "context Done"}, Aspects: []string{" ", "deadline", " DEADLINE "}})
	if p.Intent != "howto" || len(p.Queries) != 2 || len(p.Aspects) != 1 {
		t.Fatalf("invalid/duplicate model plan accepted: %+v", p)
	}
}

type freshReader struct{ normal, fresh atomic.Int32 }

func (r *freshReader) Read(context.Context, string) (reader.Document, error) {
	r.normal.Add(1)
	return reader.Document{}, errors.New("stale path")
}
func (r *freshReader) ReadFresh(_ context.Context, u string) (reader.Document, error) {
	r.fresh.Add(1)
	return reader.MakeDocument(u, "Latest release", "The latest release fixture contains newly read current material.", "fresh"), nil
}
func TestRecentQuestionRefreshesInsteadOfTrustingProviderBody(t *testing.T) {
	r := &freshReader{}
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://news.example/", Title: "Latest release", Content: strings.Repeat("Old cached body. ", 10)}}}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: r})
	out, err := e.Run(context.Background(), "latest release")
	if err != nil || r.fresh.Load() != 1 || r.normal.Load() != 0 || !strings.Contains(out.Evidence[0].Text, "newly read") {
		t.Fatalf("stale material used as current: %+v %v", out, err)
	}
}

func TestGatherDeadlinePreservesAlreadyReceivedProviderBody(t *testing.T) {
	slow := provider{"slow", func(ctx context.Context, _ search.Request) (search.Response, error) {
		<-ctx.Done()
		return search.Response{}, ctx.Err()
	}}
	fast := provider{"fast", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://go.dev/", Title: "Go context", Content: strings.Repeat("Go context reference already received. ", 5)}}}, nil
	}}
	lm := modelFunc(func(_ context.Context, _ string, _ any, out any) error {
		return assign(out, `{"title":"Received reference","lead":{"text":"Context reference","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: []search.Provider{slow, fast}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		t.Error("must not start a new fetch")
		return reader.Document{}, nil
	}), Model: lm, MaxSearchCalls: 4, SearchTimeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	out, err := e.Run(ctx, "Go context")
	if err != nil || len(out.Evidence) == 0 || out.Answer.Title != "Received reference" || out.Stats.ReaderCalls != 0 {
		t.Fatalf("received body lost at gathering deadline: %+v %v", out, err)
	}
}

func BenchmarkRankHits(b *testing.B) {
	var hits []hit
	for i := 0; i < 90; i++ {
		u := fmt.Sprintf("https://source%d.example/%d", i%8, i)
		hits = append(hits, hit{Result: search.Result{URL: u, Title: "Go context cancellation reference", Snippet: strings.Repeat("Context cancellation and deadlines release resources. ", 8)}, pageKey: u, fusion: 1 / float64(61+i%15), queries: []string{"context"}})
	}
	plan := Plan{Queries: []string{"Go context cancellation"}, Aspects: []string{"cancellation", "deadline"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rankHits(plan, hits, nil, 8)
	}
}

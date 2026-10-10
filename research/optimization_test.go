package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"searchan/reader"
	"searchan/search"
)

func TestSchedulerPausesBeforeFurtherCallsAndReusesBudget(t *testing.T) {
	for _, failure := range []error{search.ErrBlocked, &search.HTTPError{StatusCode: 401}, &search.HTTPError{StatusCode: 403}, &search.HTTPError{StatusCode: 429}} {
		t.Run(failure.Error(), func(t *testing.T) {
			var bad, good atomic.Int32
			ps := []search.Provider{
				provider{"bad", func(context.Context, search.Request) (search.Response, error) {
					bad.Add(1)
					return search.Response{}, failure
				}},
				provider{"good", func(_ context.Context, req search.Request) (search.Response, error) {
					good.Add(1)
					return search.Response{Results: []search.Result{{URL: "https://example.org/" + req.Query, Title: req.Query}}}, nil
				}},
			}
			e, _ := New(Config{Providers: ps, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil })})
			stats := Stats{}
			s := newRetrieval(e, &stats)
			s.search(context.Background(), []string{"alpha", "beta", "gamma"}, 6)
			s.search(context.Background(), []string{"alpha", "beta", "gamma"}, 6)
			if bad.Load() != 1 || good.Load() != 3 || stats.SearchCalls != 4 || len(s.hits) != 3 {
				t.Fatalf("wasted calls or healthy queries starved: bad=%d good=%d stats=%+v", bad.Load(), good.Load(), stats)
			}
		})
	}
}

func TestCompactResponseBoundsLargeSupplierFields(t *testing.T) {
	large := strings.Repeat("中文", 1<<18)
	var sources []search.Source
	for i := 0; i < 20; i++ {
		sources = append(sources, search.Source{Provider: large, Engines: []string{large}})
	}
	in := search.Response{Provider: large, Warnings: make([]string, 20)}
	for i := range in.Warnings {
		in.Warnings[i] = large
	}
	for i := 0; i < 20; i++ {
		in.Results = append(in.Results, search.Result{URL: "https://example.org/", Title: large, Snippet: large, Content: large, PublishedAt: large, ContentProvider: large, Sources: sources})
	}
	in.Results[0].URL = large
	out := compactResponse(in)
	if len(out.Results) != 15 || out.Results[0].URL != "" || len(out.Warnings) != 8 {
		t.Fatal("response/rank bounds not respected")
	}
	r := out.Results[1]
	for _, pair := range []struct {
		text  string
		limit int
	}{{r.Title, 512}, {r.Snippet, 4096}, {r.Content, reader.MaxText}, {r.PublishedAt, 128}, {r.ContentProvider, 64}, {out.Warnings[0], 256}} {
		if len(pair.text) > pair.limit || !utf8.ValidString(pair.text) {
			t.Fatal("unbounded or corrupt retained field")
		}
	}
	if len(r.Sources) != 8 || len(r.Sources[0].Provider) > 64 || len(r.Sources[0].Engines[0]) > 64 {
		t.Fatal("unbounded supplier metadata")
	}
}

func TestSchedulerSerializesEachProviderAndCancelsWaitingJobs(t *testing.T) {
	var active, peak, calls atomic.Int32
	p := provider{"one", func(ctx context.Context, req search.Request) (search.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		calls.Add(1)
		select {
		case <-time.After(time.Millisecond):
			return search.Response{}, nil
		case <-ctx.Done():
			return search.Response{}, ctx.Err()
		}
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil })})
	s := newRetrieval(e, &Stats{})
	s.search(context.Background(), []string{"a", "b", "c"}, 3)
	if peak.Load() != 1 || calls.Load() != 3 {
		t.Fatalf("same-provider burst: peak=%d calls=%d", peak.Load(), calls.Load())
	}
	// Hold the global slot, simulating another task. A cancelled request must
	// release its dispatch ledger without ever starting an external call.
	for i := 0; i < cap(e.searchSlots); i++ {
		e.searchSlots <- struct{}{}
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	s.search(ctx, []string{"waiting"}, 1)
	for i := 0; i < cap(e.searchSlots); i++ {
		<-e.searchSlots
	}
	if s.attempted[searchPair{0, "waiting"}] || calls.Load() != 3 {
		t.Fatal("unstarted call consumed ledger")
	}
	s.search(context.Background(), []string{"waiting"}, 1)
	if calls.Load() != 4 {
		t.Fatal("unstarted call could not resume")
	}
}

func TestLexicalCoverageRecoveryWithoutUsableModel(t *testing.T) {
	for _, modelFails := range []bool{false, true} {
		t.Run(fmt.Sprint(modelFails), func(t *testing.T) {
			p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
				term := "routing"
				if strings.Contains(req.Query, "retention") {
					term = "retention"
				}
				var rs []search.Result
				for i := 0; i < 2; i++ {
					rs = append(rs, search.Result{URL: fmt.Sprintf("https://docs.example/%s/%d", term, i), Title: "Atlas " + term, Content: strings.Repeat("Atlas "+term+" configuration reference. ", 3) + fmt.Sprint(i)})
				}
				return search.Response{Results: rs}, nil
			}}
			cfg := Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
				t.Error("unnecessary fetch")
				return reader.Document{}, nil
			}), MaxPages: 4, MaxSearchCalls: 3}
			if modelFails {
				cfg.Model = modelFunc(func(context.Context, string, any, any) error { return errors.New("model unavailable") })
			}
			e, _ := New(cfg)
			out := Output{Query: "Atlas configuration", Plan: Plan{Queries: []string{"Atlas configuration"}, Aspects: []string{"routing", "retention"}}, Stats: Stats{StageMS: map[string]int64{}}}
			e.gather(context.Background(), &out)
			if out.Stats.RecoveryRounds != 1 || out.Stats.SearchCalls != 2 || len(uncoveredAspects(out.Plan, out.Evidence)) != 0 {
				t.Fatalf("lexical gap was not recovered: %+v %+v", out.Stats, out.Evidence)
			}
		})
	}
}

func TestUnrelatedBodyDoesNotBecomeEvidence(t *testing.T) {
	docs := []reader.Document{reader.MakeDocument("https://food.example/", "Cooking", "Fresh vegetables and recipes for dinner.", "fixture")}
	if ev := SelectEvidence("context cancellation deadline", docs, nil, 3); len(ev) != 0 {
		t.Fatalf("unrelated fetch treated as evidence: %+v", ev)
	}
	docs[0].Title = "Context cancellation deadline"
	if ev := SelectEvidence("context cancellation deadline", docs, nil, 3); len(ev) != 1 {
		t.Fatal("title-only match prematurely rejected")
	}
}

func TestSnippetFallbackFiltersEmptyLeadsBeforeRanking(t *testing.T) {
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		var results []search.Result
		for i := 0; i < 7; i++ {
			results = append(results, search.Result{URL: fmt.Sprintf("https://example.org/%d", i), Title: "Go cancellation"})
		}
		results = append(results, search.Result{URL: "https://reference.example/", Title: "Go cancellation", Snippet: "Go cancellation reference available as an unverified search lead."})
		return search.Response{Results: results}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		return reader.Document{}, errors.New("blocked")
	}), Model: fixtureModel()})
	out, err := e.Run(context.Background(), "Go cancellation")
	if err != nil || len(out.Evidence) != 1 || out.Evidence[0].Kind != "snippet" || out.Evidence[0].URL != "https://reference.example/" {
		t.Fatalf("usable lead behind empty snippets lost: %+v %v", out.Evidence, err)
	}
}

func TestVerifyAndModelPlanCannotDisableFreshness(t *testing.T) {
	if requiresFreshQuery("Go concurrent workers") || inferIntent("Go concurrent workers") == "recent" {
		t.Fatal("concurrent mistaken for current")
	}
	r := &freshReader{}
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://news.example/", Title: "Latest release", Content: strings.Repeat("Old provider cached body. ", 10)}}}, nil
	}}
	lm := modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
		if strings.Contains(prompt, "制定网页搜索计划") {
			return assign(out, `{"intent":"verify","queries":[],"aspects":[]}`)
		}
		return assign(out, `{"title":"Latest release","lead":{"text":"Newly read current material.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: r, Model: lm, PlanWithModel: true})
	out, err := e.Run(context.Background(), "latest release 真的吗")
	if err != nil || !out.Plan.RequiresFresh || out.Plan.Intent != "verify" || r.fresh.Load() != 1 || r.normal.Load() != 0 {
		t.Fatalf("freshness lost to intent classification: %+v %v", out.Plan, err)
	}
}

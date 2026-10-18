package research

import (
	"context"
	"encoding/json"
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

type provider struct {
	name string
	fn   func(context.Context, search.Request) (search.Response, error)
}

func (p provider) Name() string { return p.name }
func (p provider) Search(ctx context.Context, r search.Request) (search.Response, error) {
	return p.fn(ctx, r)
}

type readFunc func(context.Context, string) (reader.Document, error)

func (f readFunc) Read(ctx context.Context, u string) (reader.Document, error) { return f(ctx, u) }

type modelFunc func(context.Context, string, any, any) error

func (f modelFunc) Complete(ctx context.Context, s string, in, out any) error {
	return f(ctx, s, in, out)
}
func assign(out any, s string) error { return json.Unmarshal([]byte(s), out) }

func fixtures() ([]search.Provider, reader.Reader) {
	p := provider{"fixture", func(ctx context.Context, r search.Request) (search.Response, error) {
		return search.Response{Provider: "fixture", Results: []search.Result{
			{Title: "Go context", URL: "https://go.dev/doc/context", Snippet: "context cancellation", Sources: []search.Source{{Provider: "fixture", Rank: 1}}},
			{Title: "Go concurrency", URL: "https://pkg.go.dev/context", Snippet: "context Done cancellation", Sources: []search.Source{{Provider: "fixture", Rank: 2}}},
		}}, nil
	}}
	r := readFunc(func(ctx context.Context, u string) (reader.Document, error) {
		return reader.MakeDocument(u, "Go context", strings.Repeat("Go context carries cancellation; workers observe Done and stop. ", 3), "fixture"), nil
	})
	return []search.Provider{p}, r
}

func fixtureModel() modelFunc {
	return modelFunc(func(_ context.Context, _ string, _ any, out any) error {
		return assign(out, `{"title":"Go context","lead":{"text":"Workers observe cancellation.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
}

func TestEngineCitationsAndInvalidModelError(t *testing.T) {
	providers, r := fixtures()
	for _, valid := range []bool{true, false} {
		lm := modelFunc(func(ctx context.Context, s string, in, out any) error {
			id := "e1"
			if !valid {
				id = "invented"
			}
			return assign(out, fmt.Sprintf(`{"title":"Go context","lead":{"text":"Workers observe cancellation.","evidence_ids":[%q]},"sections":[],"uncertainties":[]}`, id))
		})
		e, err := New(Config{Providers: providers, Reader: r, Model: lm})
		if err != nil {
			t.Fatal(err)
		}
		out, err := e.Run(context.Background(), "Go context cancellation")
		if !valid {
			if !errors.Is(err, ErrAnswerValidation) || out.Answer.Title != "" || len(out.Sources) != 0 {
				t.Fatalf("invalid citations did not fail cleanly: %+v %v", out, err)
			}
			continue
		}
		if err != nil || ValidateAnswer(out.Answer, out.Evidence) != nil || len(out.Sources) == 0 || out.AnswerMode != "summary" {
			t.Fatalf("uncitable output: %+v %v", out, err)
		}
		if out.Stats.SearchCalls > 6 || out.Stats.PagesRead > 8 {
			t.Fatal("budget exceeded")
		}
	}
}

func TestEngineGlobalConcurrencyAndCancellation(t *testing.T) {
	var active, peak atomic.Int32
	p := provider{"slow", func(ctx context.Context, r search.Request) (search.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return search.Response{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
		return search.Response{Results: []search.Result{{URL: "https://go.dev/", Title: "Go", Content: strings.Repeat("Go context cancellation ", 20), ContentProvider: "slow"}}}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		t.Error("unexpected read")
		return reader.Document{}, nil
	}), Model: fixtureModel(), MaxSearchCalls: 1, SearchConcurrency: 2})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Run(context.Background(), "Go context"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("concurrency cap violated: %d", peak.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.Run(ctx, "query")
	if err == nil {
		t.Fatal("cancelled search succeeded without results")
	}
}

func TestEngineSnippetFallbackAndAllFailure(t *testing.T) {
	p := provider{"snippet", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://example.org/", Title: "Search lead", Snippet: "An unverified snippet containing a possible lead."}}}, nil
	}}
	r := readFunc(func(context.Context, string) (reader.Document, error) {
		return reader.Document{}, errors.New("blocked")
	})
	lm := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		if prompt != briefPrompt || input.(map[string]any)["reason"] != "snippets_only" {
			t.Fatal("snippet was not explicitly identified for brief summarization")
		}
		return assign(output, `{"title":"搜索简报","lead":{"text":"搜索摘要显示一个可能线索，尚不能确认结论。","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: r, Model: lm})
	out, err := e.Run(context.Background(), "query")
	if err != nil || out.Evidence[0].Kind != "snippet" || len(out.Answer.Uncertainties) == 0 || out.AnswerMode != "brief_summary" || !strings.Contains(out.Text(), "未读取原文") {
		t.Fatalf("bad snippet fallback: %+v %v", out, err)
	}
	p.fn = func(context.Context, search.Request) (search.Response, error) {
		return search.Response{}, errors.New("failure")
	}
	e, _ = New(Config{Providers: []search.Provider{p}, Reader: r, Model: lm})
	if _, err := e.Run(context.Background(), "query"); !errors.Is(err, ErrNoEvidence) {
		t.Fatal("all failures were hidden")
	}
}

func TestEvidenceRankingAndDiversity(t *testing.T) {
	docs := []reader.Document{
		reader.MakeDocument("https://a.example/", "Go context", strings.Repeat("Go context cancellation. ", 20)+"\n\n"+strings.Repeat("Go context Done. ", 20), "fixture"),
		reader.MakeDocument("https://b.example/", "Go context", strings.Repeat("Go context workers. ", 20), "fixture"),
		reader.MakeDocument("https://c.example/", "Cooking", strings.Repeat("Cooking recipes ingredients. ", 20), "fixture"),
	}
	ev := SelectEvidence("Go context cancellation", docs, nil, 2)
	if len(ev) != 2 || ev[0].URL != docs[0].URL || ev[1].URL != docs[1].URL {
		t.Fatalf("ranking/diversity failed: %+v", ev)
	}
	if relevance("取消任务", "取消任务需要监听信号") <= relevance("取消任务", "今天适合做菜") {
		t.Fatal("Chinese matching failed")
	}
}

func BenchmarkSelectEvidence(b *testing.B) {
	var docs []reader.Document
	for i := 0; i < 8; i++ {
		docs = append(docs, reader.MakeDocument(fmt.Sprintf("https://example.org/%d", i), "Go context workers", fmt.Sprintf("Document %d. ", i)+strings.Repeat("Go context workers cancel tasks and release resources. 中文取消任务。\n\n", 40), "fixture"))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SelectEvidence("Go context 取消任务", docs, nil, 12)
	}
}

func TestQueryCoverageAndReservedRecoveryPages(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(fmt.Sprintf("layout_repair=%t", repair), func(t *testing.T) {
			var mu sync.Mutex
			queries := map[string]bool{}
			var ps []search.Provider
			for i := 0; i < 6; i++ {
				name := fmt.Sprintf("p%d", i)
				ps = append(ps, provider{name, func(_ context.Context, r search.Request) (search.Response, error) {
					mu.Lock()
					queries[r.Query] = true
					mu.Unlock()
					var results []search.Result
					for n := 0; n < 15; n++ {
						results = append(results, search.Result{URL: fmt.Sprintf("https://example.org/%s/%d", r.Query, n), Title: r.Query, Content: fmt.Sprintf("Document %d. ", n) + strings.Repeat(r.Query+" reliable context reference. ", 5), ContentProvider: name})
					}
					return search.Response{Results: results}, nil
				}})
			}
			lm := modelFunc(func(_ context.Context, prompt string, in, out any) error {
				switch {
				case prompt == briefPrompt && repair:
					return assign(out, `{"title":"Brief","lead":{"text":"Context reference.","evidence_ids":["e1"]},"sections":[{"heading":"Extra","claims":[{"text":"Extra detail.","evidence_ids":["e1"]}]}]}`)
				case prompt == verifyPrompt:
					return assign(out, `{"unsupported":["context reference needs qualification"],"missing":[]}`)
				case strings.Contains(prompt, "制定网页搜索计划"):
					return assign(out, `{"intent":"compare","queries":["context","cancel"],"aspects":["context","cancel"]}`)
				case strings.Contains(prompt, "是否足以回答"):
					return assign(out, `{"missing":["deadline"],"queries":["deadline"]}`)
				default:
					return assign(out, `{"title":"Go context","lead":{"text":"Context reference.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
				}
			})
			e, err := New(Config{Providers: ps, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
				t.Error("provider content should suffice")
				return reader.Document{}, nil
			}), Model: lm, PlanWithModel: true, VerifyWithModel: true, MaxSearchCalls: 6, MaxPages: 8})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Run(context.Background(), "Go")
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"Go", "context", "cancel", "deadline"} {
				if !queries[q] {
					t.Fatalf("query starved of budget: %s", q)
				}
			}
			if out.Stats.SearchCalls != 6 || out.Stats.PagesRead != 8 {
				t.Fatalf("unexpected budget: %+v", out.Stats)
			}
			wantCalls, wantRepairs := 5, 0
			if repair {
				wantCalls, wantRepairs = 6, 1
			}
			if out.Stats.ModelCalls != wantCalls || out.Stats.AnswerRepairs != wantRepairs || out.AnswerMode != "brief_summary" || out.AnswerReason != "verification_failed" {
				t.Fatalf("unexpected model budget or fallback: %+v", out)
			}
			found := false
			for _, ev := range out.Evidence {
				if strings.Contains(ev.URL, "deadline") {
					found = true
				}
			}
			if !found {
				t.Fatal("recovery results were not read")
			}
		})
	}
}

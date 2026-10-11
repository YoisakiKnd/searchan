package research

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func plannedModel(prompt string, output any) error {
	switch prompt {
	case plannerPrompt:
		return assign(output, `{"intent":"fact","queries":[],"aspects":["definition"]}`)
	case coveragePrompt:
		return assign(output, `{"answers_query":true,"missing":[],"queries":[]}`)
	case verifyPrompt:
		return assign(output, `{"answers_query":true,"unsupported":[],"missing":[]}`)
	default:
		return assign(output, `{"answers_query":true,"title":"Atlas","lead":{"text":"Atlas provides repository abstractions.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	}
}

func TestSufficientSingleSourceDoesNotForceRecovery(t *testing.T) {
	var searches atomic.Int32
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		searches.Add(1)
		return search.Response{Results: []search.Result{{URL: "https://docs.example/atlas", Title: "Atlas", Content: "Atlas provides repository abstractions for applications to access data stores."}}}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		t.Error("provided body should suffice")
		return reader.Document{}, nil
	}), Model: modelFunc(func(_ context.Context, prompt string, _ any, output any) error { return plannedModel(prompt, output) }), PlanWithModel: true, VerifyWithModel: true})
	out, err := e.Run(context.Background(), "Atlas introduction")
	if err != nil || searches.Load() != 1 || out.Stats.PagesRead != 1 || out.Stats.CoverageChecks != 1 || out.Stats.ModelCalls != 4 || out.Stats.RecoveryRounds != 0 {
		t.Fatalf("sufficient single source wasted budget: %+v %v", out.Stats, err)
	}
}

func TestSufficientFirstBatchLeavesUnreadPages(t *testing.T) {
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		var results []search.Result
		for i := range 15 {
			results = append(results, search.Result{URL: fmt.Sprintf("https://docs.example/%d", i), Title: "Atlas", Content: fmt.Sprintf("Atlas provides repository abstractions for accessing data stores. Page %d.", i)})
		}
		return search.Response{Results: results}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: modelFunc(func(_ context.Context, prompt string, _ any, output any) error { return plannedModel(prompt, output) }), PlanWithModel: true})
	out, err := e.Run(context.Background(), "Atlas introduction")
	if err != nil || out.Stats.PagesRead != 3 || out.Stats.ReadingBatches != 1 || out.Stats.CoverageChecks != 1 || out.Stats.RecoveryRounds != 0 {
		t.Fatalf("read unnecessary pages: %+v %v", out.Stats, err)
	}
}

func TestSingleAspectSemanticGapProducesTargetedRecovery(t *testing.T) {
	p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
		r := search.Result{URL: "https://docs.example/index", Title: "Atlas", Content: "Atlas reference documentation index: configuration, storage, commands and navigation."}
		if strings.Contains(req.Query, "repository definition") {
			r = search.Result{URL: "https://docs.example/definition", Title: "Atlas definition", Content: "Atlas provides repository abstractions for applications to access data stores."}
		}
		return search.Response{Results: []search.Result{r}}, nil
	}}
	m := modelFunc(func(_ context.Context, prompt string, _ any, output any) error {
		if prompt == coveragePrompt {
			// Missing facts alone must suffice; do not blindly repeat the raw query.
			return assign(output, `{"answers_query":false,"missing":["repository definition"],"queries":[]}`)
		}
		return plannedModel(prompt, output)
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m, PlanWithModel: true, VerifyWithModel: true})
	out, err := e.Run(context.Background(), "Atlas introduction")
	if err != nil || out.Stats.RecoveryRounds != 1 || out.Stats.SearchCalls != 2 || len(out.Stats.RecoveryQueries) != 1 || !strings.Contains(out.Stats.RecoveryQueries[0], "repository definition") {
		t.Fatalf("semantic gap not recovered: %+v %v", out.Stats, err)
	}
	found := false
	for _, ev := range out.Evidence {
		found = found || strings.Contains(ev.URL, "/definition")
	}
	if !found {
		t.Fatal("recovered definition missing from evidence")
	}
}

func TestReadingRejectsBadBodiesAcrossEveryEntryPoint(t *testing.T) {
	for _, mode := range []string{"provider", "direct", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			bad := "Verify you are human to access this documentation and continue browsing."
			good := "Atlas provides repository abstractions for accessing data stores in applications."
			p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
				r := search.Result{URL: "https://docs.example/atlas", Title: "Atlas"}
				if mode == "provider" {
					r.Content = bad
				}
				return search.Response{Results: []search.Result{r}}, nil
			}}
			var direct, fallback atomic.Int32
			r := readFunc(func(_ context.Context, u string) (reader.Document, error) {
				direct.Add(1)
				text := bad
				if mode == "provider" {
					text = good
				}
				return reader.MakeDocument(u, "Atlas", text, "http"), nil
			})
			f := readFunc(func(_ context.Context, u string) (reader.Document, error) {
				fallback.Add(1)
				text := good
				if mode == "fallback" {
					text = bad
				}
				return reader.MakeDocument(u, "Atlas", text, "jina"), nil
			})
			e, _ := New(Config{Providers: []search.Provider{p}, Reader: r, FallbackReader: f, Model: fixtureModel(), MaxPages: 1, MaxSearchCalls: 1})
			out, err := e.Run(context.Background(), "Atlas")
			if direct.Load() != 1 || out.Stats.PagesRead != 1 || len(out.Stats.PageReads) != 1 {
				t.Fatalf("unbounded or missing read trace: %+v", out.Stats)
			}
			trace := out.Stats.PageReads[0]
			if mode == "fallback" {
				if !errors.Is(err, ErrNoEvidence) || len(out.Evidence) != 0 || out.Stats.ModelCalls != 0 || trace.State != "rejected" || trace.Issue != "interstitial" {
					t.Fatalf("bad fallback became evidence: %+v %v", out, err)
				}
				return
			}
			if err != nil || len(out.Evidence) != 1 || out.Evidence[0].Text != good || trace.State != "accepted" {
				t.Fatalf("usable body lost: %+v %v", out, err)
			}
			if mode == "provider" && (fallback.Load() != 0 || trace.ProviderRejected != "interstitial" || out.Evidence[0].ReadMethod != "http") {
				t.Fatal("provider rejection did not lead to direct read")
			}
			if mode == "direct" && (fallback.Load() != 1 || trace.FallbackReason != "interstitial" || out.Evidence[0].ReadMethod != "jina") {
				t.Fatal("successful interstitial did not lead to fallback")
			}
		})
	}
}

func TestProviderTruncationIsVisibleInEvidence(t *testing.T) {
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://docs.example/", Title: "Atlas", Content: strings.Repeat("Atlas repository definition. ", 4000), ContentProvider: "fixture"}}}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: fixtureModel(), MaxPages: 1, MaxSearchCalls: 1})
	out, err := e.Run(context.Background(), "Atlas repository")
	if err != nil || len(out.Evidence) == 0 || !out.Evidence[0].Truncated || out.Evidence[0].ReadMethod != "provider:fixture" || !out.Stats.PageReads[0].Truncated {
		t.Fatalf("supplier truncation lost: %+v %v", out.Stats, err)
	}
}

func TestLateAnswerGapRecoversOnceWithinSharedModelBudget(t *testing.T) {
	for _, verifyFails := range []bool{false, true} {
		t.Run(fmt.Sprint(verifyFails), func(t *testing.T) {
			p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
				r := search.Result{URL: "https://docs.example/index", Title: "Atlas", Content: "Atlas documentation index includes configuration, repositories and storage reference."}
				if strings.Contains(req.Query, "repository definition") {
					r = search.Result{URL: "https://docs.example/definition", Title: "Atlas", Content: "Atlas provides repository abstractions for accessing data stores in applications."}
				}
				return search.Response{Results: []search.Result{r}}, nil
			}}
			answers := 0
			m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
				if prompt == answerPrompt || prompt == introductionPrompt {
					answers++
					if answers == 1 {
						return assign(output, `{"answers_query":false,"missing":["repository definition"],"title":"Unknown","lead":{"text":"The index does not define Atlas.","evidence_ids":["e1"]},"sections":[]}`)
					}
					found := false
					for _, ev := range input.(map[string]any)["evidence"].([]Evidence) {
						found = found || strings.Contains(ev.URL, "/definition")
					}
					if !found {
						t.Fatal("regeneration did not receive new evidence")
					}
				}
				if prompt == verifyPrompt && verifyFails {
					return assign(output, `{"answers_query":true,"unsupported":["claim requires qualification"],"missing":[]}`)
				}
				return plannedModel(prompt, output)
			})
			e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m, PlanWithModel: true, VerifyWithModel: true})
			out, err := e.Run(context.Background(), "Atlas introduction")
			wantCalls, wantMode := 5, "summary"
			if verifyFails {
				wantCalls, wantMode = maxModelCalls, "brief_summary"
			}
			if err != nil || answers != 2 || out.Stats.RecoveryRounds != 1 || out.Stats.RecoveryTrigger != "answer_gap" || out.Stats.SearchCalls != 2 || out.Stats.ModelCalls != wantCalls || out.AnswerMode != wantMode || out.Stats.PagesRead > 8 {
				t.Fatalf("late recovery failed: %+v %v", out.Stats, err)
			}
		})
	}
}

func TestLateRecoverySkipsInsufficientBudgets(t *testing.T) {
	for _, reason := range []string{"model", "time", "recovery", "pages", "no-gap"} {
		t.Run(reason, func(t *testing.T) {
			ps, r := fixtures()
			e, _ := New(Config{Providers: ps, Reader: r, Model: fixtureModel(), VerifyWithModel: true})
			out := Output{Query: "Atlas", Plan: Plan{Queries: []string{"Atlas"}}}
			g := newGathering(e, &out)
			ctx := context.Background()
			gaps := []string{"repository definition"}
			switch reason {
			case "model":
				out.Stats.ModelCalls = 4
			case "time":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			case "recovery":
				out.Stats.RecoveryRounds = 1
			case "pages":
				out.Stats.PagesRead = 8
			case "no-gap":
				gaps = nil
			}
			if g.recoverUnanswered(ctx, gaps) || out.Stats.SearchCalls != 0 || out.Stats.ReaderCalls != 0 {
				t.Fatal("ineligible correction started external work")
			}
		})
	}
}

func TestUnansweredRegenerationDoesNotLoop(t *testing.T) {
	p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://docs.example/" + strings.ReplaceAll(req.Query, " ", "-"), Title: "Atlas", Content: "Atlas reference covers configuration and repositories. Query: " + req.Query}}}, nil
	}}
	answers, briefs := 0, 0
	m := modelFunc(func(_ context.Context, prompt string, _ any, output any) error {
		if prompt == answerPrompt || prompt == introductionPrompt {
			answers++
			return assign(output, `{"answers_query":false,"missing":["repository definition"],"title":"Unknown","lead":{"text":"The material does not define Atlas.","evidence_ids":["e1"]},"sections":[]}`)
		}
		if prompt == briefPrompt {
			briefs++
		}
		return plannedModel(prompt, output)
	})
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m, PlanWithModel: true, VerifyWithModel: true})
	out, err := e.Run(context.Background(), "Atlas introduction")
	if err != nil || answers != 2 || briefs != 1 || out.Stats.RecoveryRounds != 1 || out.AnswerReason != "question_unanswered" || out.Stats.ModelCalls > maxModelCalls {
		t.Fatalf("unanswered correction looped: %+v %v", out.Stats, err)
	}
}

func TestModelBudgetIsEnforcedBeforeExternalCalls(t *testing.T) {
	calls := 0
	e := &Engine{cfg: Config{Model: modelFunc(func(context.Context, string, any, any) error { calls++; return nil })}}
	out := Output{}
	for range maxModelCalls {
		if err := e.complete(context.Background(), &out, "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.complete(context.Background(), &out, "", nil, nil); !errors.Is(err, ErrModelBudget) || calls != maxModelCalls || out.Stats.ModelCalls != maxModelCalls {
		t.Fatal("model budget only measured, not enforced")
	}
}

func TestComparisonCannotBeSatisfiedByOneEntity(t *testing.T) {
	query := "SpringData vs MyBatis 对比"
	p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
		r := search.Result{URL: "https://docs.example/data", Title: "Spring Data", Content: "Spring Data provides repository abstractions for applications to access data stores."}
		if req.Query != query {
			r = search.Result{URL: "https://docs.example/mybatis", Title: "MyBatis", Content: "MyBatis is a persistence framework with support for custom SQL and object mappings."}
		}
		return search.Response{Results: []search.Result{r}}, nil
	}}
	m := modelFunc(func(_ context.Context, prompt string, _ any, output any) error { return plannedModel(prompt, output) })
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m})
	out := Output{Query: query, Plan: Plan{Intent: "compare", Queries: []string{query}, Aspects: []string{"persistence comparison"}}}
	e.gather(context.Background(), &out)
	if out.Stats.RecoveryRounds != 1 || len(missingComparisonEntities(out.Plan, out.Evidence)) != 0 {
		t.Fatalf("comparison stopped after one side: %+v %+v", out.Stats, out.Evidence)
	}
}

func TestNoNewEvidenceKeepsCitationIdentityForBriefSummary(t *testing.T) {
	doc := reader.MakeDocument("https://docs.example/original", "Atlas", "Atlas provides repository abstractions for applications to access data stores.", "fixture")
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://mirror.example/", Title: "Atlas", Content: doc.Text}}}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: fixtureModel()})
	previous := []Evidence{{ID: "original-id", URL: doc.URL, Title: doc.Title, DocumentHash: doc.Hash, Text: doc.Text, Kind: "body"}}
	out := Output{Query: "Atlas", Plan: Plan{Queries: []string{"Atlas"}}, Evidence: previous}
	g := newGathering(e, &out)
	g.reading.docs = []reader.Document{doc}
	g.reading.hashes[doc.Hash] = true
	g.reading.urls[doc.URL] = true
	if g.recoverUnanswered(context.Background(), []string{"repository definition"}) || !reflect.DeepEqual(out.Evidence, previous) || out.Stats.RecoveryRounds != 1 {
		t.Fatalf("duplicate recovery changed citation identity: %+v %+v", out.Evidence, out.Stats)
	}
}

func TestCancelledBatchDoesNotStartFallbackOrLeakSlots(t *testing.T) {
	var active, fallback atomic.Int32
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		var results []search.Result
		for i := range 8 {
			results = append(results, search.Result{URL: fmt.Sprintf("https://docs.example/%d", i), Title: "Atlas"})
		}
		return search.Response{Results: results}, nil
	}}
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(ctx context.Context, u string) (reader.Document, error) {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		return reader.Document{}, ctx.Err()
	}), FallbackReader: readFunc(func(context.Context, string) (reader.Document, error) { fallback.Add(1); return reader.Document{}, nil }), Model: fixtureModel(), ReadConcurrency: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	out := Output{Query: "Atlas", Plan: Plan{Queries: []string{"Atlas"}}}
	e.gather(ctx, &out)
	if active.Load() != 0 || fallback.Load() != 0 || len(e.readSlots) != 0 || out.Stats.PagesRead > 1 {
		t.Fatalf("cancellation left work active: %+v active=%d fallback=%d", out.Stats, active.Load(), fallback.Load())
	}
}

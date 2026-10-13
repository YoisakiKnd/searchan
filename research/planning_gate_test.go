package research

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestPausedSingleProviderSkipsPaidPlanning(t *testing.T) {
	for _, failure := range []error{search.ErrBlocked, &search.HTTPError{StatusCode: 401}, &search.HTTPError{StatusCode: 403}, &search.HTTPError{StatusCode: 429}} {
		t.Run(search.FailureCode(failure), func(t *testing.T) {
			modelCalls, searchCalls := 0, 0
			_, r := fixtures()
			e, err := New(Config{Providers: []search.Provider{provider{"blocked", func(context.Context, search.Request) (search.Response, error) {
				searchCalls++
				return search.Response{}, failure
			}}}, Reader: r, PlanWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
				modelCalls++
				return plannedModel(prompt, out)
			})})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Run(context.Background(), "Atlas cancellation")
			if !errors.Is(err, ErrSearchUnavailable) || modelCalls != 0 || out.Stats.ModelCalls != 0 || searchCalls != 1 || out.Stats.SearchCalls != 1 || len(out.Evidence) != 0 || out.Answer.Title != "" {
				t.Fatalf("paused provider consumed planning or hid failure: model=%d searches=%d output=%+v error=%v", modelCalls, searchCalls, out, err)
			}
		})
	}
}

func TestSeedSearchReusedWithoutLosingPlannedQueries(t *testing.T) {
	for _, firstFailure := range []error{nil, context.DeadlineExceeded, &search.HTTPError{StatusCode: 400}} {
		t.Run(search.FailureCode(firstFailure), func(t *testing.T) {
			var events, queries []string
			_, r := fixtures()
			e, err := New(Config{Providers: []search.Provider{provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
				events = append(events, "search")
				queries = append(queries, req.Query)
				if len(queries) == 1 && firstFailure != nil {
					return search.Response{}, firstFailure
				}
				return search.Response{Results: []search.Result{{URL: "https://docs.example/atlas", Title: "Atlas cancellation", Content: "Atlas cancellation signals running workers, which must cooperate to stop their work."}}}, nil
			}}}, Reader: r, PlanWithModel: true, VerifyWithModel: true, MaxSearchCalls: 6, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
				if prompt == plannerPrompt {
					events = append(events, "planning")
					return assign(out, `{"intent":"fact","queries":["Atlas cancellation documentation","Atlas cancellation limits"],"aspects":["cancellation"]}`)
				}
				return plannedModel(prompt, out)
			})})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Run(context.Background(), "Atlas cancellation")
			if err != nil || len(events) < 2 || events[0] != "search" || events[1] != "planning" || out.Stats.SearchCalls != 3 || len(out.Evidence) == 0 || out.Stats.ModelCalls != 4 {
				t.Fatalf("seed not reused or transient failure blocked planning: events=%v output=%+v error=%v", events, out, err)
			}
			if !reflect.DeepEqual(queries, []string{"Atlas cancellation", "Atlas cancellation documentation", "Atlas cancellation limits"}) {
				t.Fatalf("planned query coverage changed: %v", queries)
			}
		})
	}
}

func TestEmptySeedStillAllowsModelExpansion(t *testing.T) {
	_, r := fixtures()
	searches, plans := 0, 0
	e, _ := New(Config{Providers: []search.Provider{provider{"empty", func(context.Context, search.Request) (search.Response, error) {
		searches++
		return search.Response{}, nil
	}}}, Reader: r, PlanWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
		plans++
		if prompt != plannerPrompt {
			t.Fatal("no evidence should reach generation")
		}
		return assign(out, `{"intent":"fact","queries":["Atlas cancellation documentation"],"aspects":["cancellation"]}`)
	})})
	out, err := e.Run(context.Background(), "Atlas cancellation")
	if !errors.Is(err, ErrNoEvidence) || plans != 1 || searches < 2 || out.Stats.ModelCalls != 1 {
		t.Fatalf("successful empty seed was mistaken for outage: %+v %v", out, err)
	}
}

func TestComparisonAndMultiProviderStillPlanBeforeSearching(t *testing.T) {
	for _, tt := range []struct {
		name, query string
		providers   int
	}{
		{"comparison", "Atlas 与 Beacon 的区别", 1},
		{"multiple-providers", "Atlas cancellation", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			planned := false
			var ps []search.Provider
			for i := range tt.providers {
				ps = append(ps, provider{string(rune('a' + i)), func(context.Context, search.Request) (search.Response, error) {
					if !planned {
						t.Error("search order changed outside single-provider non-comparison path")
					}
					return search.Response{}, search.ErrBlocked
				}})
			}
			e, _ := New(Config{Providers: ps, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), PlanWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
				planned = true
				return plannedModel(prompt, out)
			})})
			out, err := e.Run(context.Background(), tt.query)
			if !errors.Is(err, ErrSearchUnavailable) || !planned || out.Stats.ModelCalls != 1 {
				t.Fatalf("planner unexpectedly skipped: %+v %v", out, err)
			}
		})
	}
}

func TestSeedRespectsInitialAndRecoveryBudgets(t *testing.T) {
	for _, budget := range []int{1, 2, 3, 4, 6} {
		t.Run(strconv.Itoa(budget), func(t *testing.T) {
			var queries []string
			_, r := fixtures()
			e, _ := New(Config{Providers: []search.Provider{provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
				queries = append(queries, req.Query)
				return search.Response{Results: []search.Result{{URL: "https://docs.example/atlas", Title: "Atlas cancellation", Content: "Atlas cancellation signals running workers, which must cooperate to stop their work."}}}, nil
			}}}, Reader: r, PlanWithModel: true, MaxSearchCalls: budget, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
				if prompt == plannerPrompt {
					return assign(out, `{"intent":"fact","queries":["Atlas cancellation documentation","Atlas cancellation limits"],"aspects":["cancellation"]}`)
				}
				return plannedModel(prompt, out)
			})})
			out, err := e.Run(context.Background(), "Atlas cancellation")
			want := min(3, budget-min(2, budget-1))
			if err != nil || len(queries) != want || out.Stats.SearchCalls != want || out.Stats.RecoveryRounds != 0 {
				t.Fatalf("seed spent recovery reserve: budget=%d queries=%v stats=%+v error=%v", budget, queries, out.Stats, err)
			}
		})
	}
}

func TestCancellationDuringSeedNeverCallsModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	models := 0
	_, r := fixtures()
	e, _ := New(Config{Providers: []search.Provider{provider{"cancel", func(callCtx context.Context, _ search.Request) (search.Response, error) {
		if deadline, ok := callCtx.Deadline(); !ok || time.Until(deadline) > 8*time.Second {
			t.Error("seed lost bounded search deadline")
		}
		cancel()
		<-callCtx.Done()
		return search.Response{}, callCtx.Err()
	}}}, Reader: r, PlanWithModel: true, Model: modelFunc(func(context.Context, string, any, any) error {
		models++
		return errors.New("cancelled task reached model")
	})})
	out, err := e.Run(ctx, "Atlas cancellation")
	if !errors.Is(err, context.Canceled) || models != 0 || out.Stats.ModelCalls != 0 || out.Stats.SearchCalls != 1 {
		t.Fatalf("cancelled seed leaked paid work: %+v %v", out, err)
	}
}

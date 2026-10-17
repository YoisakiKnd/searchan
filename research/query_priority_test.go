package research

import (
	"context"
	"reflect"
	"testing"

	"searchan/search"
)

func TestPlannedQueriesPrecedeSpellingOnlyFallback(t *testing.T) {
	query := "VectorStore 怎么配置索引"
	variant := spacedQuery(query)
	first, second := "VectorStore index configuration documentation", "VectorStore index configuration limits"
	p := normalizePlan(Plan{Intent: "howto", Queries: []string{query, variant}}, Plan{Queries: []string{first, second}})
	if want := []string{query, first, second}; !reflect.DeepEqual(p.Queries, want) {
		t.Fatalf("spelling-only query displaced a planned query: got=%v want=%v", p.Queries, want)
	}
}

func TestSearchSpellingQueryKeepsMarkedIdentifiers(t *testing.T) {
	for _, query := range []string{
		"Go 的 context.WithTimeout 用完为什么还要调用 cancel？",
		"WithTimeout 用完为什么还要调用 cancel？", "findByLastname 如何解析",
		"HTTPClient()", "HTTPClient ()", "HTTPClient\n()", "HTTPClient2", "API_Client",
		"/SpringData", "--SpringData", "SpringData/Reference", "https://host.example/SpringData",
		"`SpringData`", `"SpringData"`, "'SpringData'", "name=SpringData", "*HTTPClient",
		"“SpringData”", "‘SpringData’", "「SpringData」", "『SpringData』",
	} {
		if got := searchSpellingQuery(query); got != query {
			t.Fatalf("marked identifier changed: %q => %q", query, got)
		}
	}
	for query, want := range map[string]string{
		"SpringData简介":                         "Spring Data 简介",
		"VectorStore 怎么配置索引":                   "Vector Store 怎么配置索引",
		"SpringData 与 context.WithTimeout 的关系": "Spring Data 与 context.WithTimeout 的关系",
		"“HTTPClient” 和 SpringData":            "“HTTPClient” 和 Spring Data",
	} {
		if got := searchSpellingQuery(query); got != want {
			t.Fatalf("product spelling lost: %q => %q want %q", query, got, want)
		}
	}
	// Entity matching still accepts both written forms, independently of the
	// narrower spelling sent to the search provider.
	if !mentionsEntity("context.WithTimeout", "With Timeout") || !mentionsEntity("SpringData", "Spring Data") {
		t.Fatal("search spelling altered entity matching")
	}
}

func TestQueryPriorityKeepsFallbackAndNormalizesIdempotently(t *testing.T) {
	query := "VectorStore 怎么配置索引"
	for _, expansions := range [][]string{nil, {"VectorStore index configuration documentation"}, {spacedQuery(query), "VectorStore index configuration documentation"}} {
		base := Plan{Intent: "howto", Queries: []string{query, searchSpellingQuery(query)}}
		p := normalizePlan(base, Plan{Queries: expansions})
		if p.Queries[0] != query || p.Queries[len(p.Queries)-1] != searchSpellingQuery(query) || len(p.Queries) > 3 {
			t.Fatal("unused spelling fallback lost or displaced original", p.Queries)
		}
		if again := normalizePlan(base, p); !reflect.DeepEqual(p, again) {
			t.Fatal("recorded plan changed during normalization", p, again)
		}
	}
}

func TestQualifiedAPIKeepsBothPlannedQueriesInActualRetrieval(t *testing.T) {
	query := "Go 的 context.WithTimeout 用完为什么还要调用 cancel？"
	want := []string{query, "Go context.WithTimeout cancellation documentation", "Go context.WithTimeout cancellation limits"}
	var actual []string
	_, r := fixtures()
	e, err := New(Config{Providers: []search.Provider{provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
		actual = append(actual, req.Query)
		return search.Response{Results: []search.Result{{URL: "https://docs.example/context", Title: "context.WithTimeout", Content: "WithTimeout creates a cancellation context. Workers must observe cancellation to stop their work."}}}, nil
	}}}, Reader: r, PlanWithModel: true, VerifyWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, _ any, out any) error {
		if prompt == plannerPrompt {
			return assign(out, `{"intent":"fact","queries":["Go context.WithTimeout cancellation documentation","Go context.WithTimeout cancellation limits"],"aspects":["cancellation"]}`)
		}
		return plannedModel(prompt, out)
	})})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Run(context.Background(), query)
	if err != nil || !reflect.DeepEqual(actual, want) || !reflect.DeepEqual(out.Plan.Queries, want) || out.Stats.SearchCalls != 3 || out.Stats.ModelCalls != 4 {
		t.Fatalf("API retrieval lost planned coverage: queries=%v stats=%+v err=%v", actual, out.Stats, err)
	}
}

var spellingBenchmarkResult string

func BenchmarkSpellingQueries(b *testing.B) {
	for _, tc := range []struct{ name, query string }{
		{"qualified-api", "Go 的 context.WithTimeout 用完为什么还要调用 cancel？"},
		{"project", "SpringData简介"},
		{"ordinary", "Redis Stream 消费者组如何确认消息？"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Run("previous", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					spellingBenchmarkResult = spacedQuery(tc.query)
				}
			})
			b.Run("candidate", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					spellingBenchmarkResult = searchSpellingQuery(tc.query)
				}
			})
		})
	}
}

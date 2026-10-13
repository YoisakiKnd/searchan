package research

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestDiscoveryKeepsOriginalAndTechnicalIdentity(t *testing.T) {
	for _, query := range []string{"SpringData简介", "VectorStore 简介", "Spring Data 与 Spring Framework 的区别"} {
		plan := sourceDiscoveryPlan(Plan{Intent: inferIntent(query), Queries: []string{query, spacedQuery(query), "unrelated planning expansion"}})
		if plan.Queries[0] != query || len(plan.Queries) > 3 || plan.Queries[1] != overviewQuery(discoveryEntities(plan)[0]) || !matchesEntities(plan.Queries[1], queryEntities(query)) {
			t.Fatalf("lost original or entity identity: %+v", plan)
		}
		if !reflect.DeepEqual(plan, sourceDiscoveryPlan(plan)) {
			t.Fatalf("plan not idempotent: %+v", plan)
		}
	}
	for _, query := range []string{"天气简介", "SpringData 最新变化", "SpringData如何配置", "Atlas introduction"} {
		plan := Plan{Intent: inferIntent(query), Queries: []string{query}}
		if !reflect.DeepEqual(plan, sourceDiscoveryPlan(plan)) {
			t.Fatalf("discovery widened unsupported query: %+v", plan)
		}
	}
}

func TestComparisonSelectsBothOverviewsBeforeRepeatedMirrors(t *testing.T) {
	plan := Plan{Intent: "compare", Queries: []string{"Vector Store 和 Query Engine 有什么区别？"}}
	hits := []hit{
		{Result: search.Result{URL: "https://mirror1.example/projects/vector-store", Title: "Vector Store", Snippet: "Vector Store 与 Query Engine 区别"}, fusion: .02},
		{Result: search.Result{URL: "https://mirror2.example/projects/vector-store", Title: "Vector Store", Snippet: "Vector Store 与 Query Engine 区别"}, fusion: .019},
		{Result: search.Result{URL: "https://vendor.example/projects/query-engine", Title: "Query Engine", Snippet: "Query Engine overview"}, fusion: .018},
	}
	got := rankHits(plan, hits, nil, 2)
	if len(got) != 2 || got[0].URL != hits[0].URL || got[1].URL != hits[2].URL {
		t.Fatalf("one entity consumed both slots: %+v", got)
	}
	plan.RequiresFresh = true
	if len(overviewTargets(plan)) != 0 {
		t.Fatal("static overview optimization enabled for fresh query")
	}
}

func TestComparisonSearchOrderPreservesOriginalAndBothSides(t *testing.T) {
	plan := sourceDiscoveryPlan(Plan{Intent: "compare", Queries: []string{"Vector Store 和 Query Engine 的区别"}})
	original := append([]string(nil), plan.Queries...)
	got := initialSearchQueries(plan)
	if !reflect.DeepEqual(got, []string{original[1], original[2], original[0]}) || !reflect.DeepEqual(plan.Queries, original) {
		t.Fatalf("comparison queries lost or mutated: %v %+v", got, plan)
	}
	plan.Intent = "howto"
	if !reflect.DeepEqual(initialSearchQueries(plan), plan.Queries) {
		t.Fatal("reordered unrelated query")
	}
}

func TestOverviewEvidenceRetainsDefinitionAmongRepeatedReferences(t *testing.T) {
	definition := "Query Engine is an application framework and provides an inversion of control container for applications."
	text := definition + "\n\n" + strings.Repeat("Background history explains earlier releases of the framework. ", 35)
	for i := range 20 {
		text += "\n\n" + fmt.Sprintf("References %d. Query Engine overview. Query Engine core. Query Engine comparison. Query Engine documentation.", i)
	}
	doc := reader.MakeDocument("https://wiki.example/wiki/Query_Engine", "Query Engine", text, "fixture")
	plan := Plan{Intent: "compare", Queries: []string{"Vector Store 和 Query Engine 有什么区别？"}, Aspects: []string{"Query Engine overview", "Vector Store overview"}}
	evidence := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3)
	found := false
	for _, ev := range evidence {
		found = found || strings.Contains(ev.Text, definition)
	}
	if !found {
		t.Fatalf("references displaced real definition: %+v", evidence)
	}
}

func TestComparisonLinkedOverviewsShareTwoPageBudget(t *testing.T) {
	var mu sync.Mutex
	visited := map[string]bool{}
	e := &Engine{cfg: Config{ReadTimeout: time.Second, MaxEvidence: 12, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
		mu.Lock()
		visited[raw] = true
		mu.Unlock()
		return reader.MakeDocument(raw, "Vector Store / Query Engine", "Vector Store and Query Engine have distinct roles in data processing applications.", "fixture"), nil
	})}, readSlots: make(chan struct{}, 2)}
	out := Output{Plan: Plan{Intent: "compare", Queries: []string{"Vector Store 和 Query Engine 的区别"}}}
	g := newGathering(e, &out)
	g.reading.hints = []reader.Link{
		{URL: "https://mirror1.example/projects/vector-store", Text: "Vector Store"},
		{URL: "https://mirror2.example/projects/vector-store", Text: "Vector Store"},
		{URL: "https://vendor.example/projects/query-engine", Text: "Query Engine"},
	}
	g.readLinkedOverviews(context.Background(), 8)
	if len(visited) != 2 || !visited[g.reading.hints[0].URL] || !visited[g.reading.hints[2].URL] || out.Stats.DiscoveryReads != 2 || out.Stats.PagesRead != 2 {
		t.Fatalf("comparison discovery unbalanced or over budget: %+v visited=%v", out.Stats, visited)
	}
}

func TestLinkedOverviewFollowsOnlyOneBoundedHop(t *testing.T) {
	var visited []string
	e := &Engine{cfg: Config{MaxEvidence: 12, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
		visited = append(visited, raw)
		doc := reader.MakeDocument(raw, "Vector Store", "Vector Store provides a common data access model for applications.", "fixture")
		doc.Links = []reader.Link{{URL: "https://third.example/projects/vector-store", Text: "Vector Store"}}
		return doc, nil
	})}, readSlots: make(chan struct{}, 1)}
	out := Output{Plan: Plan{Queries: []string{"VectorStore简介"}}}
	g := newGathering(e, &out)
	g.reading.hints = []reader.Link{{URL: "http://127.0.0.1/projects/vector-store", Text: "Vector Store"}, {URL: "https://integration.example/projects/vector-store", Text: "OtherDB"}, {URL: "https://vendor.example/projects/vector-store", Text: "Vector Store"}, {URL: "https://vendor.example/projects/vector-store#same", Text: "Vector Store"}, {URL: "https://other.example/projects/vector-store-client", Text: "Vector Store Client"}}
	g.readLinkedOverviews(context.Background(), 1)
	if len(visited) != 1 || visited[0] != "https://vendor.example/projects/vector-store" || out.Stats.DiscoveryReads != 1 || out.Stats.PagesRead != 1 {
		t.Fatalf("wrong link/page budget: %+v visits=%v", out.Stats, visited)
	}
}

func TestSparsePageMetadataIsNeverEvidence(t *testing.T) {
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", func(context.Context, search.Request) (search.Response, error) { return search.Response{}, nil }}}, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
		doc := reader.MakeDocument(raw, "Vector Store", "Vector Store", "fixture")
		doc.Links = []reader.Link{{URL: "https://vendor.example/projects/vector-store", Text: "Vector Store"}}
		return doc, reader.ContentError("insufficient_text")
	})})
	stats := Stats{}
	r := newReading(e, &stats)
	r.read(context.Background(), Plan{Queries: []string{"VectorStore简介"}}, []hit{{Result: search.Result{URL: "https://sparse.example/vector-store", Title: "Vector Store"}}}, 1)
	if len(r.docs) != 0 || len(r.hints) != 1 || stats.PagesRead != 1 || stats.PageReads[0].State != "rejected" {
		t.Fatalf("sparse page promoted to evidence: docs=%v hints=%v", r.docs, r.hints)
	}
}

func TestOverviewAnchorRejectsNamedIntegrations(t *testing.T) {
	for _, text := range []string{"Vector Store OtherDB", "Vector Store Client", "Vector Store Connector"} {
		if overviewAnchor(text, "Vector Store") {
			t.Fatal("integration mistaken for umbrella project", text)
		}
	}
	for _, text := range []string{"Vector Store", "official Vector Store website", "https://vendor.example/projects/vector-store", "projects.vendor.example/vector-store", "项目主页"} {
		if !overviewAnchor(text, "Vector Store") {
			t.Fatal("useful project link rejected", text)
		}
	}
}

func TestRecordedDiscoveryPlanSurvivesNormalization(t *testing.T) {
	base := Plan{Intent: "fact", Queries: []string{"VectorStore简介", "Vector Store 简介"}, Aspects: []string{"definition"}}
	first := normalizePlan(base, Plan{Intent: "fact", Queries: []string{"Vector Store 项目概览"}, Aspects: []string{"definition"}})
	replayed := normalizePlan(base, first)
	if !reflect.DeepEqual(first, replayed) {
		t.Fatalf("recorded plan changed during replay: before=%+v after=%+v", first, replayed)
	}
}

func TestOverviewFitSelectsEntityPageWithoutTrustingOfficialTitle(t *testing.T) {
	plan := Plan{Intent: "fact", Queries: []string{"VectorStore简介"}}
	good := hit{Result: search.Result{URL: "https://vendor.example/projects/vector-store", Title: "Vector Store", Snippet: "Vector Store overview and purpose."}, fusion: .01}
	noise := hit{Result: search.Result{URL: "https://mirror.example/post/42", Title: "Vector Store 官方文档", Snippet: "Vector Store overview and purpose."}, fusion: .01}
	wrong := hit{Result: search.Result{URL: "https://vendor.example/projects/vector-storage", Title: "Vector Storage 官方", Snippet: "Vector Storage overview and purpose."}, fusion: .01}
	submodule := hit{Result: search.Result{URL: "https://vendor.example/projects/vector-store-client", Title: "Vector Store Client"}, fusion: .01}
	got := rankHits(plan, []hit{noise, wrong, submodule, good}, nil, 1)
	if len(got) != 1 || got[0].URL != good.URL {
		t.Fatalf("focused overview not selected: %+v", got)
	}
	for _, h := range []hit{noise, wrong, submodule} {
		if overviewFit(overviewTargets(plan)[0], h.URL, h.Title) != 0 {
			t.Fatalf("title authority or wrong scope rewarded: %+v", h)
		}
	}
	plan.Queries[0] = "VectorStore Client 配置"
	if len(overviewTargets(plan)) != 0 {
		t.Fatal("intro heuristic applied to configuration request")
	}
}

func TestDiscoveryRestoresOnlyExplicitLowercaseEntities(t *testing.T) {
	plan := sourceDiscoveryPlan(Plan{Intent: "fact", Queries: []string{"spring data 简介", "Spring Data 概览", "Vector Store"}})
	if plan.Queries[1] != overviewQuery("Spring Data") || strings.Contains(plan.Queries[1], "Vector Store") {
		t.Fatalf("entity restoration lost scope: %+v", plan)
	}
}

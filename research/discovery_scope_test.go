package research

import (
	"reflect"
	"strings"
	"testing"

	"searchan/reader"
	"searchan/search"
)

func TestDiscoveryPreservesFullNamesAndAcronymSpelling(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"Spring Data MongoDB 是什么？", []string{"Spring Data MongoDB"}},
		{"Vector Store MixedCaseDB 简介", []string{"Vector Store MixedCaseDB"}},
		{"MixedCaseDB 简介", []string{"MixedCaseDB"}},
		{"PostgreSQL 简介", []string{"PostgreSQL"}},
		{"SpringData简介", []string{"Spring Data"}},
	} {
		p := sourceDiscoveryPlan(Plan{Intent: "fact", Queries: []string{tc.query}})
		if got := discoveryEntities(p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v want %v", tc.query, got, tc.want)
		}
		if len(p.Queries) > 3 || p.Queries[0] != tc.query || !strings.Contains(p.Queries[1], `"`+tc.want[0]+`"`) {
			t.Fatalf("discovery replaced original or split name: %+v", p)
		}
		if !reflect.DeepEqual(p, sourceDiscoveryPlan(p)) {
			t.Fatal("discovery changed on repeated normalization", p)
		}
	}
	if !mentionsEntity("Mixed Case DB", "MixedCaseDB") || mentionsEntity("Mixed Case DB Warehouse", "MixedCaseDBWare") {
		t.Fatal("raw acronym names do not preserve complete-word matching")
	}
}

func TestExplicitRelationshipsKeepBothNamedSides(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"Spring Data JPA 和 Hibernate 是什么关系？", []string{"Spring Data JPA", "Hibernate"}},
		{"Vector Store 与 Beacon 的关系", []string{"Vector Store", "Beacon"}},
		{"MixedCaseDB 跟 queryEngine 有什么关系？", []string{"MixedCaseDB", "queryEngine"}},
		{"What is the relationship between Vector Store and Beacon?", []string{"Vector Store", "Beacon"}},
	} {
		if inferIntent(tc.query) != "compare" {
			t.Fatal("relationship treated as an isolated fact", tc.query)
		}
		p := normalizePlan(Plan{Queries: []string{tc.query}}, Plan{Intent: "fact", Queries: []string{"unrelated expansion"}})
		if p.Intent != "compare" || !reflect.DeepEqual(overviewTargets(p), tc.want) || len(p.Queries) != 3 {
			t.Fatalf("planner discarded explicit two-sided scope: %+v", p)
		}
		if got := initialSearchQueries(p); got[0] != p.Queries[1] || got[1] != p.Queries[2] || got[2] != tc.query {
			t.Fatal("comparison order lost a side or the original", got)
		}
	}
	for _, q := range []string{"关系型数据库简介", "什么是关系代数？", "Vector Store 如何配置关系型存储", "Atlas introduction", "Explain why workers are related"} {
		if relationshipQuery(q) {
			t.Error("ordinary prose turned into comparison", q)
		}
	}
	for _, q := range []string{"Vector Store 和 一个好用的库 的关系", "Vector Store 和 Beacon 和 Other 的关系", "What is the relationship between Vector Store and a search service?"} {
		if len(comparisonNames(q)) != 0 {
			t.Error("unnamed prose widened into technical entities", q)
		}
	}
}

func TestRelationshipKeepsBothOverviewDefinitions(t *testing.T) {
	p := normalizePlan(Plan{Queries: []string{"Vector Store 和 Beacon 是什么关系？"}}, Plan{Intent: "fact"})
	definition := "Beacon implements the storage protocol used by Vector Store's repository abstraction."
	doc := reader.MakeDocument("https://vendor.example/projects/beacon", "Beacon", definition+"\n\n"+strings.Repeat("Beacon references Vector Store integrations and examples. ", 200), "fixture")
	evidence := SelectEvidenceForPlan(p, []reader.Document{doc}, nil, 3)
	for _, ev := range evidence {
		if strings.Contains(ev.Text, definition) {
			return
		}
	}
	t.Fatal("relationship definition displaced by repetitive references", evidence)
}

func TestRelationshipDoesNotFilterSingleWordSecondSide(t *testing.T) {
	p := normalizePlan(Plan{Queries: []string{"Vector Store 和 Beacon 是什么关系？"}}, Plan{Intent: "fact"})
	left := reader.MakeDocument("https://vendor.example/projects/vector-store", "Vector Store", "Vector Store supplies repositories over the persistence protocol.", "fixture")
	right := reader.MakeDocument("https://engine.example/runtime/manual/start", "Beacon", "Beacon implements the persistence protocol for object mapping and query execution.", "fixture")
	evidence := SelectEvidenceForPlan(p, []reader.Document{left, right}, nil, 6)
	seen := map[string]bool{}
	for _, ev := range evidence {
		seen[ev.URL] = true
	}
	if !seen[left.URL] || !seen[right.URL] || len(missingComparisonEntities(p, evidence)) != 0 {
		t.Fatal("one explicit side was filtered out", evidence)
	}
	if missing := missingComparisonEntities(p, evidence[:1]); len(missing) != 1 {
		t.Fatal("missing side was invisible to recovery", missing)
	}
	if anchoredQuery(p.Queries[0], "Beacon persistence protocol documentation") != "Beacon persistence protocol documentation" {
		t.Fatal("a query about the second side was forced back to the first")
	}
	hits := []hit{
		{Result: search.Result{URL: left.URL, Title: left.Title, Snippet: left.Text}, fusion: .02},
		{Result: search.Result{URL: "https://mirror.example/projects/vector-store", Title: left.Title, Snippet: left.Text}, fusion: .019},
		{Result: search.Result{URL: right.URL, Title: right.Title, Snippet: right.Text}, fusion: .018},
	}
	selected := rankHits(p, hits, nil, 2)
	if len(selected) != 2 || selected[1].URL != right.URL {
		t.Fatal("URL shape hid a second-side manual behind repeated first-side pages", selected)
	}
}

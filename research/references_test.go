package research

import (
	"context"
	"strings"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

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

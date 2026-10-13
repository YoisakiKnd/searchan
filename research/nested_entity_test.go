package research

import (
	"context"
	"errors"
	"strings"
	"testing"

	"searchan/reader"
	"searchan/search"
)

func TestNestedCompoundNamesDoNotBecomeAlternativeEntities(t *testing.T) {
	for _, query := range []string{"Atlas Cluster OrionDB 简介", "AtlasCluster OrionDB 简介", "Atlas Cluster OrionDB 官方模块简介"} {
		entities := queryEntities(query)
		for _, text := range []string{"OrionDB is an unrelated database.", "Atlas Cluster offers database integrations.", "OrionDatabase"} {
			if matchesEntities(text, entities) {
				t.Errorf("component %q replaced full entity in %q: %v", text, query, entities)
			}
		}
		for _, text := range []string{"Atlas Cluster OrionDB provides integration.", "AtlasCluster OrionDB", "atlas-cluster-orion-db"} {
			if !matchesEntities(text, entities) {
				t.Errorf("full entity alias %q rejected for %q: %v", text, query, entities)
			}
		}
	}
	// Separate comparison subjects remain alternatives for selecting each
	// subject's direct page; containment must not turn this into an AND filter.
	entities := relevanceEntities(Plan{Intent: "compare", Queries: []string{"Atlas Cluster OrionDB 和 OrionDB 有什么区别？"}})
	if len(entities) != 2 || !matchesEntities("OrionDB reference", entities) || !matchesEntities("Atlas Cluster OrionDB reference", entities) {
		t.Fatal("comparison subject coverage changed", entities)
	}
	if !matchesEntities("ErrOverLimit reference", queryEntities("Zen ErrOverLimit response")) {
		t.Fatal("single context word became a mandatory API namespace")
	}
}

func TestParentOrChildNameCannotSupplyMissingModuleEvidence(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-substitution", true: "matching-module"}[found], func(t *testing.T) {
			p := provider{"fixture", func(_ context.Context, _ search.Request) (search.Response, error) {
				results := []search.Result{
					{URL: "https://docs.example/cluster", Title: "Atlas Cluster", Content: strings.Repeat("Atlas Cluster offers database integrations and repository interfaces. ", 3)},
					{URL: "https://docs.example/orion", Title: "OrionDB", Content: strings.Repeat("OrionDB is a database for storing application records. ", 3)},
				}
				if found {
					results = append(results, search.Result{URL: "https://docs.example/module", Title: "Atlas Cluster OrionDB", Content: strings.Repeat("Atlas Cluster OrionDB provides repository integration for the database. ", 3)})
				}
				return search.Response{Results: results}, nil
			}}
			calls := 0
			m := modelFunc(func(_ context.Context, _ string, _ any, output any) error {
				calls++
				if !found {
					t.Fatal("unrelated evidence reached the summary model")
				}
				return assign(output, `{"answers_query":true,"title":"Atlas Cluster OrionDB","lead":{"text":"Atlas Cluster OrionDB provides repository integration.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
			})
			e, err := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Run(context.Background(), "Atlas Cluster OrionDB 官方模块简介")
			if found {
				if err != nil || len(out.Evidence) != 1 || out.Evidence[0].URL != "https://docs.example/module" || calls != 1 {
					t.Fatal("matching module lost", err, out.Evidence, calls)
				}
			} else if !errors.Is(err, ErrNoEvidence) || calls != 0 || out.Answer.Lead.Text != "" {
				t.Fatal("missing module was replaced with a component", err, calls, out.Answer)
			}
			if out.Stats.SearchCalls > 6 || out.Stats.PagesRead > 8 {
				t.Fatal("entity recovery exceeded shared budget", out.Stats)
			}
		})
	}
}

package research

import (
	"strings"
	"testing"

	"searchan/reader"
)

func TestGenericHeadingCannotDisplaceMoreRelevantContract(t *testing.T) {
	contract := "The builder derives a store-specific query from the method name. Entity properties are validated before the query is created. The lookupByEmail method selects the matching email property."
	text := "Query Creation\n\n" + contract + " " + strings.Repeat("The storage adapter validates mapped fields using the declared domain model. ", 12) + "\n\nRepository Query Methods\n\nEach container can be an Iterable, List or Set. Adapt the returned collection to the caller's needs."
	manual := outlinedFixture(text, "Query Creation", "Repository Query Methods")
	manual.URL = "https://manual.example/beacon/lookupBy"
	manual.Title = "Beacon lookupBy manual"
	// A real reference corpus also contains many nonmatching sections. They
	// affect corpus frequencies, but must not consume a supporting passage slot.
	background := reader.MakeDocument("https://manual.example/beacon/maintenance", "Beacon lookupBy maintenance", strings.Repeat("Persistence adapters retain mapped fields within declared domain models. ", 400), "fixture")
	article := reader.MakeDocument("https://article.example/beacon/lookupBy", "Beacon lookupBy query methods", strings.Repeat("Beacon lookupBy 方法名查询原理，Repository 查询查找策略，属性与关键字如何映射。Derived query method name parsing mechanism and repository query lookup strategy. ", 8), "fixture")
	plan := Plan{Intent: "howto", Queries: []string{"Beacon lookupBy 方法名查询原理", "Beacon derived query method name parsing mechanism", "Beacon lookupBy repository query lookup strategy"}, Aspects: []string{"lookupBy 方法名如何解析", "解析环节 Repository 查询查找策略", "属性与关键字如何映射"}}
	ev := SelectEvidenceForPlan(plan, []reader.Document{article, manual, background}, nil, 2)
	found := false
	for _, e := range ev {
		if e.URL == manual.URL {
			if !strings.Contains(e.Text, contract) {
				t.Fatalf("generic heading displaced contract: %+v", ev)
			}
			if !strings.Contains(manual.Text, e.Text) || e.DocumentHash != manual.Hash {
				t.Fatal("ranking changed source text or provenance")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("relevant manual lost its evidence slot", ev)
	}
}

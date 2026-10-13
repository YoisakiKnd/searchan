package research

import (
	"reflect"
	"strings"
	"testing"

	"searchan/reader"
)

func outlinedFixture(text string, headings ...string) reader.Document {
	doc := reader.MakeDocument("https://docs.example/runtime/manual/start", "Beacon manual", text, "fixture")
	offset := 0
	for _, title := range headings {
		i := strings.Index(text[offset:], title) + offset
		doc.Headings = append(doc.Headings, reader.Heading{Start: i, End: i + len(title), Level: 2})
		offset = i + len(title)
	}
	return doc
}

func TestSectionPassagesKeepDefinitionAndOriginalPositions(t *testing.T) {
	heading := "Beacon and Persistence API"
	definition := "Beacon implements the persistence specification used by Vector Store repositories. This defines the relationship between the two projects."
	text := "Preface\n\n" + strings.Repeat("Background notes unrelated to persistence. ", 30) + "\n\n" + heading + "\n\n" + definition + "\n\nConfiguration\n\n" + strings.Repeat("Beacon uses Vector Store persistence settings. Beacon repository configuration and Vector Store persistence configuration. ", 300)
	doc := outlinedFixture(text, heading, "Configuration")
	pieces := documentPassages(doc)
	var joined []string
	for _, p := range pieces {
		if !strings.Contains(text, p.text) {
			t.Fatal("passage invented text", p)
		}
		if strings.Contains(p.text, definition) {
			if p.heading != heading || p.first != strings.Count(text[:strings.Index(text, heading)], "\n\n")+1 {
				t.Fatal("section heading or paragraph lost", p)
			}
		}
		joined = append(joined, strings.Fields(p.text)...)
	}
	if strings.Join(joined, " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatal("section boundaries dropped body text")
	}
	plan := Plan{Intent: "compare", Queries: []string{"Vector Store 和 Beacon 是什么关系？"}, Aspects: []string{"persistence specification relationship"}}
	ev := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3)
	for _, e := range ev {
		if strings.Contains(e.Text, definition) {
			return
		}
	}
	t.Fatal("repeated configuration displaced section definition", ev)
}

func TestInvalidOutlineKeepsLegacySplit(t *testing.T) {
	text := "中文 Preface\n\nBeacon and Persistence API\n\nBeacon implements the persistence specification."
	for _, headings := range [][]reader.Heading{
		{{Start: -1, End: 3, Level: 2}}, {{Start: 1, End: 3, Level: 2}}, {{Start: 0, End: len(text) + 1, Level: 2}}, {{Start: 0, End: 6, Level: 7}}, {{Start: 0, End: 6, Level: 2}, {Start: 0, End: 6, Level: 2}}, make([]reader.Heading, 129),
	} {
		doc := reader.Document{Text: text, Headings: headings}
		if got := documentPassages(doc); !reflect.DeepEqual(got, splitPassages(text)) {
			t.Fatal("untrusted offsets changed evidence", got)
		}
	}
}

func TestIntroductionRetainsEarlySectionWithoutRepeatedEntity(t *testing.T) {
	definition := "Vector Store provides a consistent data access model while retaining storage-specific features."
	features := "Features\n\nRepository abstractions and object mapping. Queries are derived from method names, with auditing and extension points."
	modules := "Modules\n\nVector Store Relational supports tables. Vector Store Document supports documents. Vector Store Graph supports graphs."
	releases := "Release train\n\nVector Store contains independently released projects. Vector Store release train provides curated Vector Store modules."
	text := definition + "\n\n" + features + "\n\n" + modules + "\n\n" + releases
	doc := outlinedFixture(text, "Features", "Modules", "Release train")
	doc.Title, doc.URL = "Vector Store", "https://vendor.example/projects/vector-store"
	plan := Plan{Intent: "fact", Queries: []string{"VectorStore简介", `"Vector Store" project overview official website`}, Aspects: []string{"Vector Store的定义和核心目标", "主要子模块及用途"}}
	evidence := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3)
	found := false
	for _, ev := range evidence {
		if !strings.Contains(doc.Text, ev.Text) {
			t.Fatal("section context rewrote evidence", ev)
		}
		found = found || strings.Contains(ev.Text, "Repository abstractions")
	}
	if !found {
		t.Fatal("entity-free core section discarded despite focused overview page", evidence)
	}
	// An unrelated document must not gain relevance from its first sections.
	doc.Title, doc.URL = "Cooking Recipes", "https://food.example/recipes"
	doc.Text = strings.ReplaceAll(text, "Vector Store", "Cooking Recipes")
	if ev := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3); len(ev) != 0 {
		t.Fatal("unrelated title inherited overview context", ev)
	}
}

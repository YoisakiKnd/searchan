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

func TestAPIQueryRetainsSpecificContractWithProjectHeavyHeadings(t *testing.T) {
	contract := "The loadBy query validates the entity property and translates the predicate; an unknown property cannot define a valid query."
	text := "Vector Store Query Methods\n\nVector Store exposes various query methods through its repository API.\n\nUsing Vector Store Named Queries\n\n" + strings.Repeat("Vector Store named queries belong to Vector Store repository configuration. ", 25) + "\n\nQuery Creation\n\n" + contract + "\n\nVector Store Query Sorting\n\n" + strings.Repeat("Vector Store query sorting uses Vector Store sort configuration. ", 25)
	doc := outlinedFixture(text, "Vector Store Query Methods", "Using Vector Store Named Queries", "Query Creation", "Vector Store Query Sorting")
	doc.Title = "Vector Store Query Methods"
	plan := Plan{Intent: "howto", Queries: []string{"Vector Store loadBy 查询原理", "Vector Store derived query method creation mechanism"}, Aspects: []string{"查询生成过程"}}
	ev := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3)
	found := false
	for _, item := range ev {
		if !strings.Contains(doc.Text, item.Text) || item.DocumentHash != doc.Hash {
			t.Fatal("heading adjustment changed the original passage", item)
		}
		found = found || strings.Contains(item.Text, contract)
	}
	if !found {
		t.Fatal("project-heavy headings displaced API contract", ev)
	}
}

func TestIdentifierHeadingScopeUsesLiteralLowerCamelCase(t *testing.T) {
	for _, query := range []string{"Vector Store loadBy 查询原理", "readNext() returns what?", "怎样使用 parseHTTPResponse？"} {
		if !identifierTopicQuery(query) {
			t.Fatal("literal lower-camel symbol was missed", query)
		}
	}
	for _, query := range []string{"Vector Store Commons 负责什么？", "SpringData简介", "PostgreSQL Serializable 原理", "最新 1.2.3 版本", "Go context.Value 是否传参数？"} {
		if identifierTopicQuery(query) {
			t.Fatal("project, version or plain topic entered identifier heading mode", query)
		}
	}
}

func TestGeneratedOverviewDoesNotDisplaceArticleBody(t *testing.T) {
	prefix := "Vector Store query internals\n\n"
	widget := "AI Summary\n\n" + strings.Repeat("Vector Store query parser uses InventedClause. ", 12) + "\n\n"
	body := "Query parsing\n\n" + strings.Repeat("Vector Store parses method names into property predicates. ", 12)
	doc := outlinedFixture(prefix+widget+body, "Vector Store query internals", "Query parsing")
	doc.Title = "Vector Store query internals"
	pieces := documentPassages(doc)
	for _, p := range pieces {
		if strings.Contains(p.text, "InventedClause") {
			t.Fatal("separately labeled generated overview admitted as article evidence")
		}
		if !strings.Contains(doc.Text, p.text) {
			t.Fatal("article evidence was rewritten")
		}
		if strings.Contains(p.text, "property predicates") && p.first != strings.Count((prefix+widget), "\n\n")+1 {
			t.Fatal("omitted widget shifted original paragraph positions", p)
		}
	}
	ev := SelectEvidence("Vector Store query parser", []reader.Document{doc}, nil, 3)
	if len(ev) == 0 || strings.Contains(ev[0].Text, "InventedClause") {
		t.Fatal("generated keyword repetition displaced substantive body", ev)
	}
}

func TestGeneratedOverviewFilteringRequiresBoundedSeparateBlock(t *testing.T) {
	body := "Details\n\n" + strings.Repeat("Vector Store query documentation and examples. ", 12)
	for _, label := range []string{"AI Summary", "AI-generated summary", "AI 生成摘要", "人工智能生成摘要"} {
		text := "Article\n\n" + label + "\n\nGenerated overview.\n\n" + body
		doc := outlinedFixture(text, "Article", label, "Details")
		for _, p := range documentPassages(doc) {
			if strings.Contains(p.text, "Generated overview.") {
				t.Fatal("separate generated heading retained", label, p)
			}
		}
	}
	for _, label := range []string{"Summary", "AI Summary is a feature explained below.", "Example: AI Summary", "人工智能摘要算法"} {
		doc := outlinedFixture(label+"\n\n"+body, "Details")
		got := documentPassages(doc)
		if len(got) == 0 || !strings.Contains(got[0].text, label) {
			t.Fatal("ordinary summary or article sentence removed", label, got)
		}
	}
	for _, doc := range []reader.Document{
		{Text: "AI Summary\n\nVector Store parses methods."},                        // no validated section boundary
		outlinedFixture("AI Summary\n\nVector Store parses methods.", "AI Summary"), // whole page
		outlinedFixture("AI Summary\n\n"+strings.Repeat("Long article body. ", 300)+"\n\n"+body, "Details"),
	} {
		if got := documentPassages(doc); len(got) == 0 || !strings.Contains(got[0].text, "AI Summary") {
			t.Fatal("ambiguous or unbounded content removed", got)
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

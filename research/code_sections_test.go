package research

import (
	"reflect"
	"strings"
	"testing"

	"searchan/reader"
)

func codeSectionFixture() (reader.Document, string, string) {
	heading := "Session API"
	code := "class Session {\n" + strings.Repeat("  Option unrelatedConfiguration;\n", 90) + "}"
	contract := "A Session uses a shared connection pool by default. Sessions are safe for concurrent use; a new Session does not require a new pool."
	text := heading + "\n\n" + code + "\n\n" + contract + "\n\nOther API\n\n" + strings.Repeat("Session API options describe request configuration and setup. ", 30)
	d := outlinedFixture(text, heading, "Other API")
	d.Title = "Session API reference"
	start := strings.Index(text, code)
	d.CodeBlocks = []reader.CodeBlock{{Start: start, End: start + len(code)}}
	return d, code, contract
}

func TestLeadingDeclarationKeepsFollowingContractAndOriginalCoordinates(t *testing.T) {
	d, _, contract := codeSectionFixture()
	parts := documentPassages(d)
	var joined []string
	found := false
	for _, p := range parts {
		if !strings.Contains(d.Text, p.text) {
			t.Fatal("passage invented bytes", p)
		}
		joined = append(joined, strings.Fields(p.text)...)
		if strings.Contains(p.text, contract) {
			found = true
			want := strings.Count(d.Text[:strings.Index(d.Text, contract)], "\n\n") + 1
			if p.heading != "Session API" || p.first != want {
				t.Fatalf("contract lost context or coordinate: %+v want=%d", p, want)
			}
		}
	}
	if !found || strings.Join(joined, " ") != strings.Join(strings.Fields(d.Text), " ") {
		t.Fatal("declaration or prose dropped")
	}
	plan := Plan{Queries: []string{"Session API connection pool reuse"}}
	ev := SelectEvidenceForPlan(plan, []reader.Document{d}, nil, 1)
	if len(ev) != 1 || !strings.Contains(ev[0].Text, contract) || ev[0].DocumentHash != d.Hash {
		t.Fatal("declaration displaced contract", ev)
	}
}

func TestCodeMetadataCannotSplitTextAtUnvalidatedOffsets(t *testing.T) {
	d, _, _ := codeSectionFixture()
	d.CodeBlocks = nil
	want := documentPassages(d)
	for _, blocks := range [][]reader.CodeBlock{
		{{Start: -1, End: 3}}, {{Start: 0, End: len(d.Text) + 1}}, {{Start: 4, End: 3}}, {{Start: 0, End: 20}, {Start: 10, End: 30}}, make([]reader.CodeBlock, 129),
	} {
		d.CodeBlocks = blocks
		if got := documentPassages(d); !reflect.DeepEqual(got, want) {
			t.Fatal("invalid metadata changed boundaries", blocks)
		}
	}
	unicodeDoc := outlinedFixture("接口\n\n中文代码\n\n说明内容必须保持完整。", "接口")
	unicodeDoc.CodeBlocks = nil
	base := documentPassages(unicodeDoc)
	unicodeDoc.CodeBlocks = []reader.CodeBlock{{Start: 1, End: 10}}
	if got := documentPassages(unicodeDoc); !reflect.DeepEqual(got, base) {
		t.Fatal("UTF8 offset admitted")
	}
}

func TestLaterExamplesDoNotReceiveAnotherOpeningBoost(t *testing.T) {
	d, code, contract := codeSectionFixture()
	d.Text = "Session API\n\n" + contract + "\n\n" + code + "\n\nFinal example discussion."
	d.Headings = []reader.Heading{{Start: 0, End: len("Session API"), Level: 2}}
	start := strings.Index(d.Text, code)
	d.CodeBlocks = []reader.CodeBlock{{Start: start, End: start + len(code)}}
	base := d
	base.CodeBlocks = nil
	if got := documentPassages(d); !reflect.DeepEqual(got, documentPassages(base)) {
		t.Fatal("late example changed ordinary section")
	}
}

func TestConsecutiveCodeBlocksDoNotMasqueradeAsContract(t *testing.T) {
	d, code, contract := codeSectionFixture()
	second := strings.Repeat("  Unrelated config;\n", 150)
	d.Text = strings.Replace(d.Text, code, code+"\n\n"+second, 1)
	for i := range d.Headings {
		if i > 0 {
			d.Headings[i].Start += len(second) + 2
			d.Headings[i].End += len(second) + 2
		}
	}
	start := strings.Index(d.Text, second)
	d.CodeBlocks = append(d.CodeBlocks, reader.CodeBlock{Start: start, End: start + len(second)})
	found := false
	for _, p := range documentPassages(d) {
		if strings.Contains(p.text, contract) {
			found = true
			if p.heading != "Session API" {
				t.Fatal("second code block displaced actual contract", p)
			}
		}
	}
	if !found {
		t.Fatal("contract dropped")
	}
}

func TestSpecificFieldContractStillRanksInsideDeclaration(t *testing.T) {
	d, code, _ := codeSectionFixture()
	field := "// Deadline includes response body reads.\n  Deadline Duration;"
	replacement := strings.TrimSuffix(code, "}") + field + "\n}"
	d.Text = strings.Replace(d.Text, code, replacement, 1)
	for i := range d.Headings {
		if i > 0 {
			d.Headings[i].Start += len(replacement) - len(code)
			d.Headings[i].End += len(replacement) - len(code)
		}
	}
	d.CodeBlocks[0].End += len(replacement) - len(code)
	ev := SelectEvidenceForPlan(Plan{Queries: []string{"Session.Deadline includes response body reads"}}, []reader.Document{d}, nil, 1)
	if len(ev) != 1 || !strings.Contains(ev[0].Text, field) {
		t.Fatal("prose opening displaced requested field", ev)
	}
}

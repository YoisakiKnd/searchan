package quality

import (
	"path/filepath"
	"strings"
	"testing"

	"searchan/internal/fsutil"
	"searchan/research"
)

func reviewScopeFixture(t *testing.T) (string, ReviewFile) {
	t.Helper()
	dir := t.TempDir()
	c := Case{ID: "scope", Query: "Does Widget guarantee completion?", Checks: []string{"Preserves the condition"}}
	suite := Suite{Version: 1, Cases: []Case{c}}
	o := research.Output{Answer: research.Answer{
		Title:         "Widget always completes",
		Lead:          research.Claim{Text: "Widget may complete under condition A.", EvidenceIDs: []string{"e1"}},
		Sections:      []research.Section{{Heading: "Unconditional guarantees", Claims: []research.Claim{{Text: "Condition A is required.", EvidenceIDs: []string{"e1"}}}}},
		Table:         &research.Table{Columns: []string{"Mode", "Guaranteed result"}, Rows: []research.TableRow{{Cells: []string{"A", "May complete"}, EvidenceIDs: []string{"e1"}}}},
		Uncertainties: []string{"Different versions explain the discrepancy.", "The source does not specify a deadline."},
	}}
	r := newReview(c, o, hashJSON(o))
	yes, no := true, false
	r.Reviewer, r.CoreAnswered, r.EntityCorrect, r.UnnecessaryRefusal = "synthetic-test", &yes, &yes, &no
	r.FactualVerdict, r.Checks[0].Verdict = "fail", "pass"
	for i := range r.Claims {
		r.Claims[i].Verdict = "supported"
	}
	for i := range r.UncitedText {
		r.UncitedText[i].Verdict = "pass"
	}
	r.UncitedText[0].Verdict = "fail"
	r.UncitedText[len(r.UncitedText)-2].Verdict = "unclear"
	file := ReviewFile{Version: 2, Cases: []Review{r}}
	report := Report{Version: 1, SuiteHash: hashJSON(suite), Cases: []Result{{ID: c.ID, AnswerHash: hashJSON(o), State: "completed"}}}
	for path, data := range map[string]any{"report.json": report, "review.json": file, "suite.json": suite, "scope/output.json": o} {
		if err := fsutil.WriteJSON(filepath.Join(dir, path), data); err != nil {
			t.Fatal(err)
		}
	}
	markdown := reviewMarkdown(c, o, report.Cases[0])
	for _, text := range r.UncitedText {
		if !strings.Contains(markdown, text.Location+": "+text.Text) {
			t.Fatal("displayed text missing from review instructions", text)
		}
	}
	return dir, file
}

func TestReviewCoversUncitedAssertionsWithoutInflatingCitationCounts(t *testing.T) {
	dir, file := reviewScopeFixture(t)
	summary, err := SummarizeReview(dir)
	if err != nil || summary.Reviewed != 1 || summary.FullTextReviewed != 1 || summary.FactuallyIncorrect != 1 || summary.SupportedStatements != 3 || summary.UncitedTextPassed != 4 || summary.UncitedTextFailed != 1 || summary.UncitedTextUnclear != 1 {
		t.Fatal("review scope or separate counts lost", summary, err)
	}
	file.Cases[0].UncitedText[0].Verdict = "pending"
	if err := fsutil.WriteJSON(filepath.Join(dir, "review.json"), file); err != nil {
		t.Fatal(err)
	}
	summary, err = SummarizeReview(dir)
	if err != nil || summary.Pending != 1 || summary.Reviewed != 0 || summary.SupportedStatements != 0 || summary.FullTextReviewed != 0 {
		t.Fatal("partial review counted", summary, err)
	}
}

func TestReviewRejectsChangedUncitedText(t *testing.T) {
	for _, mutation := range []string{"delete", "text", "location", "order", "verdict", "version"} {
		t.Run(mutation, func(t *testing.T) {
			dir, file := reviewScopeFixture(t)
			texts := file.Cases[0].UncitedText
			switch mutation {
			case "delete":
				file.Cases[0].UncitedText = texts[:len(texts)-1]
			case "text":
				texts[0].Text = "A weaker replacement title"
			case "location":
				texts[0].Location = "uncertainties[0]"
			case "order":
				texts[0], texts[1] = texts[1], texts[0]
			case "verdict":
				texts[0].Verdict = "supported"
			case "version":
				file.Version = 1
			}
			if err := fsutil.WriteJSON(filepath.Join(dir, "review.json"), file); err != nil {
				t.Fatal(err)
			}
			if _, err := SummarizeReview(dir); err == nil {
				t.Fatal("stale or malformed annotation accepted")
			}
		})
	}
}

func TestLegacyReviewRemainsExplicitlyBodyOnly(t *testing.T) {
	dir, file := reviewScopeFixture(t)
	file.Version, file.Cases[0].UncitedText = 1, nil
	if err := fsutil.WriteJSON(filepath.Join(dir, "review.json"), file); err != nil {
		t.Fatal(err)
	}
	summary, err := SummarizeReview(dir)
	if err != nil || summary.Reviewed != 1 || summary.LegacyBodyOnly != 1 || summary.FullTextReviewed != 0 || summary.SupportedStatements != 3 || summary.UncitedTextFailed != 0 {
		t.Fatal("legacy scope silently upgraded", summary, err)
	}
}

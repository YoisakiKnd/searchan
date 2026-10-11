package quality

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"searchan/research"
)

type CheckReview struct {
	Question string `json:"question"`
	Verdict  string `json:"verdict"` // pending, pass, fail
	Note     string `json:"note,omitempty"`
}
type ClaimReview struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
	Verdict     string   `json:"verdict"` // pending, supported, unsupported, unclear
	Note        string   `json:"note,omitempty"`
}
type Review struct {
	ID                 string        `json:"id"`
	AnswerHash         string        `json:"answer_hash"`
	Reviewer           string        `json:"reviewer"`
	CoreAnswered       *bool         `json:"core_answered"`
	EntityCorrect      *bool         `json:"entity_correct"`
	FactualVerdict     string        `json:"factual_verdict"` // pending, pass, fail, unclear
	UnnecessaryRefusal *bool         `json:"unnecessary_refusal"`
	Checks             []CheckReview `json:"checks"`
	Claims             []ClaimReview `json:"claims"`
	Note               string        `json:"note,omitempty"`
}
type ReviewFile struct {
	Version int      `json:"version"`
	Cases   []Review `json:"cases"`
}

func claims(o research.Output) []research.Claim {
	var out []research.Claim
	if o.Answer.Lead.Text != "" {
		out = append(out, o.Answer.Lead)
	}
	for _, s := range o.Answer.Sections {
		out = append(out, s.Claims...)
	}
	if o.Answer.Table != nil {
		for _, row := range o.Answer.Table.Rows {
			out = append(out, research.Claim{Text: strings.Join(row.Cells, " | "), EvidenceIDs: row.EvidenceIDs})
		}
	}
	return out
}
func newReview(c Case, o research.Output, hash string) Review {
	r := Review{ID: c.ID, AnswerHash: hash, FactualVerdict: "pending", Checks: []CheckReview{}, Claims: []ClaimReview{}}
	for _, q := range c.Checks {
		r.Checks = append(r.Checks, CheckReview{Question: q, Verdict: "pending"})
	}
	for _, claim := range claims(o) {
		r.Claims = append(r.Claims, ClaimReview{Text: claim.Text, EvidenceIDs: claim.EvidenceIDs, Verdict: "pending"})
	}
	return r
}

func reviewMarkdown(c Case, o research.Output, r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nQuery: %s\n\nState: %s; error: %s; mode: %s\n\n", c.ID, c.Query, r.State, r.Error, r.AnswerMode)
	b.WriteString("## Review rubric\n\n")
	for _, q := range c.Checks {
		fmt.Fprintf(&b, "- [ ] %s\n", q)
	}
	b.WriteString("\nReference entry points (not injected into search/model input):\n\n")
	for _, ref := range c.References {
		fmt.Fprintf(&b, "- %s\n", ref)
	}
	fmt.Fprintf(&b, "\n## Generated answer\n\n%s\n\n", o.Answer.Title)
	for i, c := range claims(o) {
		fmt.Fprintf(&b, "%d. %s [%s]\n\n", i+1, c.Text, strings.Join(c.EvidenceIDs, ", "))
	}
	for _, u := range o.Answer.Uncertainties {
		fmt.Fprintf(&b, "Uncertainty: %s\n\n", u)
	}
	b.WriteString("## Evidence supplied to the model\n\n")
	for _, e := range o.Evidence {
		fmt.Fprintf(&b, "### %s — %s\n\nURL: %s\n\nKind: %s; read: %s; truncated: %t; paragraphs: %d–%d\n\n%s\n\n", e.ID, e.Title, e.URL, e.Kind, e.ReadMethod, e.Truncated, e.Paragraph, e.ParagraphEnd, e.Text)
	}
	b.WriteString("## Review instructions\n\nEdit ../review.json after checking the answer, cited passages and reference sources. A statement is supported only if all its factual parts are supported by its cited evidence. Number/ID validity is not factual support. Do not mark a failed or incomplete run as a correct answer. Record important missing facts, wrong entities, unnecessary refusal and unsupported additions. Recheck changing facts as of this run's timestamp. No automatic factual score has been assigned.\n")
	return b.String()
}

type ReviewSummary struct {
	Total                 int `json:"total"`
	Reviewed              int `json:"reviewed"`
	Pending               int `json:"pending"`
	CoreAnswered          int `json:"core_answered"`
	WrongEntity           int `json:"wrong_entity"`
	FactuallyCorrect      int `json:"factually_correct"`
	FactuallyIncorrect    int `json:"factually_incorrect"`
	FactuallyUnclear      int `json:"factually_unclear"`
	UnnecessaryRefusal    int `json:"unnecessary_refusal"`
	RubricPassed          int `json:"rubric_passed"`
	RubricTotal           int `json:"rubric_total"`
	SupportedStatements   int `json:"supported_statements"`
	UnsupportedStatements int `json:"unsupported_statements"`
	UnclearStatements     int `json:"unclear_statements"`
}

// SummarizeReview counts fully reviewed cases only. No missing review is scored
// as a failure or success; hashes and claim alignment reject stale annotations.
func SummarizeReview(dir string) (ReviewSummary, error) {
	var report Report
	var file ReviewFile
	var suite Suite
	var summary ReviewSummary
	if err := readJSON(filepath.Join(dir, "report.json"), 4<<20, &report); err != nil {
		return summary, err
	}
	if err := readJSON(filepath.Join(dir, "review.json"), 4<<20, &file); err != nil {
		return summary, err
	}
	if err := readJSON(filepath.Join(dir, "suite.json"), 1<<20, &suite); err != nil {
		return summary, err
	}
	if hashJSON(suite) != report.SuiteHash {
		return summary, errors.New("suite does not match run")
	}
	cases := map[string]Case{}
	for _, c := range suite.Cases {
		cases[c.ID] = c
	}
	if report.Version != 1 || file.Version != 1 || len(report.Cases) > 100 || len(file.Cases) != len(report.Cases) {
		return summary, errors.New("invalid review/report versions or case count")
	}
	seen := map[string]bool{}
	for i, r := range file.Cases {
		result := report.Cases[i]
		if !caseID.MatchString(r.ID) || seen[r.ID] || r.ID != result.ID || r.AnswerHash != result.AnswerHash {
			return summary, errors.New("review does not match run")
		}
		seen[r.ID] = true
		if len(r.Checks) != len(cases[r.ID].Checks) {
			return summary, errors.New("review rubric does not match suite")
		}
		for j, c := range r.Checks {
			if c.Question != cases[r.ID].Checks[j] {
				return summary, errors.New("review rubric changed")
			}
		}
		var o research.Output
		if err := readJSON(filepath.Join(dir, r.ID, "output.json"), 2<<20, &o); err != nil {
			return summary, err
		}
		expected := claims(o)
		if hashJSON(o) != r.AnswerHash || len(expected) != len(r.Claims) {
			return summary, errors.New("review answer or claims changed")
		}
		complete := r.Reviewer != "" && r.CoreAnswered != nil && r.EntityCorrect != nil && r.UnnecessaryRefusal != nil && len(r.Checks) > 0
		if r.FactualVerdict == "pending" {
			complete = false
		} else if r.FactualVerdict != "pass" && r.FactualVerdict != "fail" && r.FactualVerdict != "unclear" {
			return summary, errors.New("invalid factual verdict")
		}
		for _, c := range r.Checks {
			if c.Verdict == "pending" {
				complete = false
			} else if c.Verdict != "pass" && c.Verdict != "fail" {
				return summary, errors.New("invalid rubric verdict")
			}
		}
		for j, c := range r.Claims {
			if c.Text != expected[j].Text || strings.Join(c.EvidenceIDs, "\x00") != strings.Join(expected[j].EvidenceIDs, "\x00") {
				return summary, errors.New("review claim does not match answer")
			}
			if c.Verdict == "pending" {
				complete = false
			} else if c.Verdict != "supported" && c.Verdict != "unsupported" && c.Verdict != "unclear" {
				return summary, errors.New("invalid citation verdict")
			}
		}
		summary.Total++
		if !complete {
			summary.Pending++
			continue
		}
		if result.State != "completed" && *r.CoreAnswered {
			return summary, errors.New("failed/incomplete case cannot be marked answered")
		}
		summary.Reviewed++
		switch r.FactualVerdict {
		case "pass":
			summary.FactuallyCorrect++
		case "fail":
			summary.FactuallyIncorrect++
		case "unclear":
			summary.FactuallyUnclear++
		}
		if *r.CoreAnswered {
			summary.CoreAnswered++
		}
		if !*r.EntityCorrect {
			summary.WrongEntity++
		}
		if *r.UnnecessaryRefusal {
			summary.UnnecessaryRefusal++
		}
		for _, c := range r.Checks {
			summary.RubricTotal++
			if c.Verdict == "pass" {
				summary.RubricPassed++
			}
		}
		for _, c := range r.Claims {
			switch c.Verdict {
			case "supported":
				summary.SupportedStatements++
			case "unsupported":
				summary.UnsupportedStatements++
			case "unclear":
				summary.UnclearStatements++
			}
		}
	}
	return summary, nil
}

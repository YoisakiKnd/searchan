// Package research turns search results into a bounded, citable answer.
package research

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"searchan/model"
)

type Plan struct {
	Intent        string   `json:"intent"`
	Queries       []string `json:"queries"`
	Aspects       []string `json:"aspects"`
	RequiresFresh bool     `json:"requires_fresh,omitempty"`
	focus         []string
}
type Evidence struct {
	ID             string    `json:"id"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Text           string    `json:"text"`
	DocumentHash   string    `json:"document_hash"`
	Paragraph      int       `json:"paragraph"`
	ParagraphEnd   int       `json:"paragraph_end,omitempty"`
	RelevanceScore float64   `json:"relevance_score,omitempty"`
	MatchedAspects []int     `json:"matched_aspects,omitempty"`
	Kind           string    `json:"kind"` // body or snippet; snippet is never silently promoted.
	PublishedAt    string    `json:"published_at,omitempty"`
	FetchedAt      time.Time `json:"fetched_at,omitempty"`
	ReadMethod     string    `json:"read_method,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
}
type Claim struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Section struct {
	Heading string  `json:"heading"`
	Claims  []Claim `json:"claims"`
}
type TableRow struct {
	Cells       []string `json:"cells"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Table struct {
	Columns []string   `json:"columns"`
	Rows    []TableRow `json:"rows"`
}
type Answer struct {
	AnswersQuery  *bool     `json:"answers_query,omitempty"`
	Missing       []string  `json:"missing,omitempty"`
	Title         string    `json:"title"`
	Lead          Claim     `json:"lead"`
	Sections      []Section `json:"sections"`
	Table         *Table    `json:"table,omitempty"`
	Uncertainties []string  `json:"uncertainties"`
}
type Source struct {
	ID          int      `json:"id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Output struct {
	Query        string         `json:"query"`
	Plan         Plan           `json:"plan"`
	Answer       Answer         `json:"answer"`
	AnswerMode   string         `json:"answer_mode,omitempty"`
	AnswerReason string         `json:"answer_reason,omitempty"`
	Evidence     []Evidence     `json:"evidence"`
	Sources      []Source       `json:"sources"`
	Warnings     []string       `json:"warnings,omitempty"`
	GeneratedAt  time.Time      `json:"generated_at"`
	Stats        Stats          `json:"stats"`
	Retrieval    []RetrievedHit `json:"retrieval,omitempty"`
}
type RetrievedHit struct {
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Providers []string `json:"providers"`
}
type Stats struct {
	SearchCalls        int                     `json:"search_calls"`
	PagesRead          int                     `json:"pages_read"`
	ModelCalls         int                     `json:"model_calls"`
	AnswerRepairs      int                     `json:"answer_repairs,omitempty"`
	OutputRetries      int                     `json:"output_retries,omitempty"`
	VerificationIssue  string                  `json:"verification_issue,omitempty"`
	VerificationGaps   int                     `json:"verification_gaps,omitempty"`
	VerificationOutput *model.OutputDiagnostic `json:"verification_output,omitempty"`
	ElapsedMS          int64                   `json:"elapsed_ms"`
	StageMS            map[string]int64        `json:"stage_ms"`
	Candidates         int                     `json:"candidates"`
	Documents          int                     `json:"documents"`
	RecoveryRounds     int                     `json:"recovery_rounds"`
	Calls              []SearchCall            `json:"calls,omitempty"`
	ReaderCalls        int                     `json:"reader_calls"`
	FallbackReads      int                     `json:"fallback_reads"`
	RecoveryQueries    []string                `json:"recovery_queries,omitempty"`
	RecoveryTrigger    string                  `json:"recovery_trigger,omitempty"`
	CoverageChecks     int                     `json:"coverage_checks,omitempty"`
	ReadingBatches     int                     `json:"reading_batches,omitempty"`
	DiscoveryReads     int                     `json:"discovery_reads,omitempty"`
	CandidateReads     int                     `json:"candidate_reads,omitempty"`
	CoverageCandidates []CoverageCandidate     `json:"coverage_candidates,omitempty"`
	PageReads          []PageRead              `json:"page_reads,omitempty"`
}

// CoverageCandidate records the bounded menu offered to the coverage model.
// Selected means an existing ID passed selection and page-budget checks;
// Fallback distinguishes a program-chosen alternative after a failed read.
// page_reads records whether the read actually started and its outcome.
type CoverageCandidate struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Origin   string `json:"origin"`
	Selected bool   `json:"selected,omitempty"`
	Fallback bool   `json:"fallback,omitempty"`
}

// PageRead retains bounded provenance and outcome metadata, never page text.
type PageRead struct {
	URL              string `json:"url"`
	Method           string `json:"method,omitempty"`
	State            string `json:"state"`
	Issue            string `json:"issue,omitempty"`
	ProviderRejected string `json:"provider_rejected,omitempty"`
	FallbackReason   string `json:"fallback_reason,omitempty"`
	Truncated        bool   `json:"truncated,omitempty"`
	ElapsedMS        int64  `json:"elapsed_ms"`
}

type SearchCall struct {
	Provider     string `json:"provider"`
	Query        string `json:"query"`
	Results      int    `json:"results"`
	NewDocuments int    `json:"new_documents"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	Error        string `json:"error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

// Typed failures expose a fixed category without including draft text or an
// untrusted citation ID in logs and quality artifacts.
type answerValidationError struct{ code, message string }

func (e *answerValidationError) Error() string { return e.message }

func AnswerValidationCode(err error) string {
	var validation *answerValidationError
	if errors.As(err, &validation) {
		return validation.code
	}
	var layout *briefLayoutError
	if errors.As(err, &layout) {
		return "brief_layout"
	}
	return ""
}

func ValidateAnswer(a Answer, ev []Evidence) error {
	if strings.TrimSpace(a.Title) == "" || len(a.Title) > 240 {
		return &answerValidationError{"title", "answer title missing or too long"}
	}
	if len(a.Sections) > 6 || len(a.Uncertainties) > 6 {
		return &answerValidationError{"sections", "answer has too many sections"}
	}
	if len(a.Missing) > 4 {
		return &answerValidationError{"gaps", "too many answer gaps"}
	}
	for _, gap := range a.Missing {
		if len(gap) > 400 {
			return &answerValidationError{"gap_size", "oversized answer gap"}
		}
	}
	known := map[string]bool{}
	for _, e := range ev {
		known[e.ID] = true
	}
	total := 0
	check := func(c Claim) error {
		total += len(c.Text)
		if strings.TrimSpace(c.Text) == "" || len(c.Text) > 1800 {
			return &answerValidationError{"claim_size", "empty or oversized claim"}
		}
		if len(ev) > 0 && len(c.EvidenceIDs) == 0 {
			return &answerValidationError{"missing_citation", "factual claim is missing evidence"}
		}
		if len(c.EvidenceIDs) > 8 {
			return &answerValidationError{"citation_limit", "too many citations for one claim"}
		}
		for _, id := range c.EvidenceIDs {
			if !known[id] {
				return &answerValidationError{"unknown_citation", "unknown evidence ID"}
			}
		}
		return nil
	}
	if err := check(a.Lead); err != nil {
		return err
	}
	for _, s := range a.Sections {
		if len(s.Heading) > 160 || len(s.Claims) > 6 {
			return &answerValidationError{"section_size", "oversized answer section"}
		}
		for _, c := range s.Claims {
			if err := check(c); err != nil {
				return err
			}
		}
	}
	if a.Table != nil {
		if len(a.Table.Columns) < 2 || len(a.Table.Columns) > 4 || len(a.Table.Rows) > 8 {
			return &answerValidationError{"table_shape", "table exceeds layout limits"}
		}
		for _, col := range a.Table.Columns {
			if len(col) > 120 {
				return &answerValidationError{"table_heading_size", "table heading too long"}
			}
		}
		for _, row := range a.Table.Rows {
			if len(row.Cells) != len(a.Table.Columns) {
				return &answerValidationError{"table_columns", "table column mismatch"}
			}
			for _, cell := range row.Cells {
				if len(cell) > 360 {
					return &answerValidationError{"table_cell_size", "table cell too long"}
				}
			}
			if err := check(Claim{strings.Join(row.Cells, " "), row.EvidenceIDs}); err != nil {
				return err
			}
		}
	}
	for _, s := range a.Uncertainties {
		total += len(s)
		if len(s) > 600 {
			return &answerValidationError{"uncertainty_size", "uncertainty text too long"}
		}
	}
	if total > 12000 {
		return &answerValidationError{"answer_size", "answer exceeds output budget"}
	}
	return nil
}

func Sources(a Answer, ev []Evidence) []Source {
	wanted := map[string]bool{}
	visit := func(ids []string) {
		for _, id := range ids {
			wanted[id] = true
		}
	}
	visit(a.Lead.EvidenceIDs)
	for _, s := range a.Sections {
		for _, c := range s.Claims {
			visit(c.EvidenceIDs)
		}
	}
	if a.Table != nil {
		for _, r := range a.Table.Rows {
			visit(r.EvidenceIDs)
		}
	}
	var sources []Source
	indices := map[string]int{}
	for _, e := range ev {
		if !wanted[e.ID] {
			continue
		}
		idx, ok := indices[e.URL]
		if !ok {
			idx = len(sources)
			indices[e.URL] = idx
			sources = append(sources, Source{ID: idx + 1, URL: e.URL, Title: e.Title})
		}
		sources[idx].EvidenceIDs = append(sources[idx].EvidenceIDs, e.ID)
	}
	return sources
}

func (o Output) Text() string {
	var b strings.Builder
	ids := map[string]int{}
	for _, s := range o.Sources {
		for _, id := range s.EvidenceIDs {
			ids[id] = s.ID
		}
	}
	refs := func(evidence []string) string {
		seen := map[int]bool{}
		var b strings.Builder
		for _, id := range evidence {
			if n := ids[id]; n > 0 && !seen[n] {
				fmt.Fprintf(&b, "[%d]", n)
				seen[n] = true
			}
		}
		return b.String()
	}
	b.WriteString(o.Answer.Title + "\n\n" + o.Answer.Lead.Text + refs(o.Answer.Lead.EvidenceIDs) + "\n")
	for _, s := range o.Answer.Sections {
		b.WriteString("\n" + s.Heading + "\n")
		for _, c := range s.Claims {
			b.WriteString("• " + c.Text + refs(c.EvidenceIDs) + "\n")
		}
	}
	if o.Answer.Table != nil {
		b.WriteString("\n" + strings.Join(o.Answer.Table.Columns, " | ") + "\n")
		for _, r := range o.Answer.Table.Rows {
			b.WriteString(strings.Join(r.Cells, " | ") + refs(r.EvidenceIDs) + "\n")
		}
	}
	for _, s := range o.Answer.Uncertainties {
		b.WriteString("\n待确认：" + s + "\n")
	}
	for _, s := range o.Sources {
		b.WriteString(fmt.Sprintf("\n[%d] %s\n%s\n", s.ID, s.Title, s.URL))
	}
	return b.String()
}

func domain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

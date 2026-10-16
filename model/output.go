package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OutputError exposes bounded JSON/protocol categories and an allowlisted
// schema field, never drafts, vendor stop strings or decoder descriptions.
type OutputError struct {
	kind, field                   string
	cause                         string
	offset                        int64
	form                          string
	bytes                         int
	api, finish                   string
	outputTokens, reasoningTokens int64
}

type OutputDiagnostic struct {
	Kind            string `json:"kind"`
	Cause           string `json:"cause,omitempty"`
	Field           string `json:"field,omitempty"`
	Offset          int64  `json:"offset,omitempty"`
	Form            string `json:"form"`
	Bytes           int    `json:"bytes"`
	API             string `json:"api,omitempty"`
	Finish          string `json:"finish,omitempty"`
	OutputTokens    int64  `json:"output_tokens,omitempty"`
	ReasoningTokens int64  `json:"reasoning_tokens,omitempty"`
}

func (e *OutputError) Diagnostic() OutputDiagnostic {
	return OutputDiagnostic{Kind: e.kind, Cause: e.cause, Field: e.field, Offset: e.offset, Form: e.form, Bytes: e.bytes, API: e.api, Finish: e.finish, OutputTokens: e.outputTokens, ReasoningTokens: e.reasoningTokens}
}

func (e *OutputError) Kind() string  { return e.kind }
func (e *OutputError) Field() string { return e.field }
func (e *OutputError) Error() string {
	return fmt.Sprintf("model output invalid (kind=%s, cause=%s, field=%s, offset=%d, bytes=%d, api=%s, finish=%s, output_tokens=%d, reasoning_tokens=%d)", e.kind, e.cause, e.field, e.offset, e.bytes, e.api, e.finish, e.outputTokens, e.reasoningTokens)
}

func decodeOutput(content string, out any) (result error) {
	defer func() {
		if e, ok := result.(*OutputError); ok {
			e.bytes = len(content)
			e.form = "other"
			s := strings.TrimSpace(content)
			switch {
			case s == "":
				e.form = "empty"
			case strings.HasPrefix(s, "```"):
				e.form = "fence"
			case s[0] == '{':
				e.form = "object"
			case s[0] == '[':
				e.form = "array"
			}
		}
	}()
	if len(content) > 64<<10 {
		return &OutputError{kind: "oversized"}
	}
	// The protocol adapter already holds content in memory. Unmarshal enforces
	// one complete value without a second full-size decoder buffer.
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		cause := "invalid_token"
		if syntax.Error() == "unexpected end of JSON input" {
			cause = "unexpected_end"
		}
		return &OutputError{kind: "syntax", cause: cause, offset: syntax.Offset}
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		return &OutputError{kind: "type", field: allowedOutputField(mismatch.Field), offset: mismatch.Offset}
	}
	return &OutputError{kind: "schema"}
}

func allowedOutputField(field string) string {
	switch field {
	case "answers_query", "title", "lead", "lead.text", "lead.evidence_ids", "sections", "sections.heading", "sections.claims", "sections.claims.text", "sections.claims.evidence_ids", "uncertainties", "missing", "table", "table.columns", "table.rows", "table.rows.cells", "table.rows.evidence_ids", "intent", "queries", "aspects", "requires_fresh", "read_ids", "unsupported", "ok", "summary":
		return field
	}
	return ""
}

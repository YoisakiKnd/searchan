package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OutputError exposes a bounded category and an allowlisted schema field, never
// the model draft or the decoder's arbitrary value/field descriptions.
type OutputError struct {
	kind, field string
	offset      int64
	form        string
	bytes       int
}

type OutputDiagnostic struct {
	Kind   string `json:"kind"`
	Field  string `json:"field,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Form   string `json:"form"`
	Bytes  int    `json:"bytes"`
}

func (e *OutputError) Diagnostic() OutputDiagnostic {
	return OutputDiagnostic{e.kind, e.field, e.offset, e.form, e.bytes}
}

func (e *OutputError) Kind() string  { return e.kind }
func (e *OutputError) Field() string { return e.field }
func (e *OutputError) Error() string {
	return fmt.Sprintf("model JSON output invalid (kind=%s, field=%s, offset=%d)", e.kind, e.field, e.offset)
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
		return &OutputError{kind: "syntax", offset: syntax.Offset}
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		field := ""
		switch mismatch.Field {
		case "answers_query", "title", "lead", "lead.text", "lead.evidence_ids", "sections", "sections.heading", "sections.claims", "sections.claims.text", "sections.claims.evidence_ids", "uncertainties", "missing", "table", "table.columns", "table.rows", "table.rows.cells", "table.rows.evidence_ids":
			field = mismatch.Field
		}
		return &OutputError{kind: "type", field: field, offset: mismatch.Offset}
	}
	return &OutputError{kind: "schema"}
}

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
	// Models without a native JSON mode wrap the object in fences or prose, and
	// some emit raw control characters inside strings. Recover only the first
	// complete object; anything still invalid reports the original error.
	if candidate, ok := extractObject(content); ok {
		if json.Unmarshal(candidate, out) == nil {
			return nil
		}
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return &OutputError{kind: "syntax", offset: syntax.Offset}
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		// Newer Go versions include array indices (sections.0.claims.1.text).
		var path []string
		for part := range strings.SplitSeq(mismatch.Field, ".") {
			if strings.Trim(part, "0123456789") != "" {
				path = append(path, part)
			}
		}
		field := ""
		switch strings.Join(path, ".") {
		case "answers_query", "title", "lead", "lead.text", "lead.evidence_ids", "sections", "sections.heading", "sections.claims", "sections.claims.text", "sections.claims.evidence_ids", "uncertainties", "missing", "table", "table.columns", "table.rows", "table.rows.cells", "table.rows.evidence_ids":
			field = strings.Join(path, ".")
		}
		return &OutputError{kind: "type", field: field, offset: mismatch.Offset}
	}
	return &OutputError{kind: "schema"}
}

// extractObject returns the first top-level JSON object in content. Raw
// control characters inside strings are escaped, and missing closing brackets
// are inserted where the structure makes them unambiguous: a key inside an
// array, a mismatched closer, or closers omitted after the final value. String
// contents are never changed; the caller still validates the decoded value.
func extractObject(content string) ([]byte, bool) {
	start := strings.IndexByte(content, '{')
	if start < 0 {
		return nil, false
	}
	b := make([]byte, 0, len(content)-start+16)
	var stack []byte
	inString, escaped, afterString := false, false, false
	comma := -1 // position in b of the latest comma outside strings
	closer := map[byte]byte{'{': '}', '[': ']'}
	closeTop := func() {
		b = append(b, closer[stack[len(stack)-1]])
		stack = stack[:len(stack)-1]
	}
	for i := start; i < len(content); i++ {
		c := content[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString, afterString = false, true
			case c == '\n':
				b = append(b, '\\', 'n')
				continue
			case c == '\r':
				b = append(b, '\\', 'r')
				continue
			case c == '\t':
				b = append(b, '\\', 't')
				continue
			case c < 0x20:
				b = append(b, fmt.Sprintf("\\u%04x", c)...)
				continue
			}
			b = append(b, c)
			continue
		}
		switch c {
		case ' ', '\n', '\r', '\t':
			b = append(b, c)
			continue
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			// Close inner containers the model forgot, e.g. "}]}" for "}]}]".
			for len(stack) > 0 && closer[stack[len(stack)-1]] != c {
				closeTop()
			}
			if len(stack) == 0 {
				return nil, false
			}
			stack = stack[:len(stack)-1]
			if b = append(b, c); len(stack) == 0 {
				return b, true
			}
			afterString = false
			continue
		case ',':
			comma = len(b)
		case ':':
			// An object key inside an array: the arrays before the last comma
			// were never closed.
			if afterString && stack[len(stack)-1] == '[' && comma >= 0 {
				var closers []byte
				for len(stack) > 0 && stack[len(stack)-1] == '[' {
					closers = append(closers, ']')
					stack = stack[:len(stack)-1]
				}
				if len(stack) == 0 {
					return nil, false
				}
				b = append(b[:comma], append(closers, b[comma:]...)...)
			}
		}
		afterString = false
		b = append(b, c)
	}
	// Token-limit truncation is rejected by each protocol adapter before
	// decoding, so open containers here are closers the model omitted. Only
	// complete them after a complete value; never mid-string or after ',' / ':'.
	end := strings.TrimRight(string(b), " \n\r\t")
	if inString || len(stack) == 0 || end == "" || strings.ContainsAny(end[len(end)-1:], ",:{[") {
		return nil, false
	}
	b = []byte(end)
	for len(stack) > 0 {
		closeTop()
	}
	return b, true
}

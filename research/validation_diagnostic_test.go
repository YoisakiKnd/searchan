package research

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidationDiagnosticDoesNotExposeUntrustedCitation(t *testing.T) {
	secret := "private-draft https://endpoint.invalid/?key=SECRET\nforged log line"
	a := Answer{Title: "Summary", Lead: Claim{Text: "Fact", EvidenceIDs: []string{secret}}}
	err := ValidateAnswer(a, []Evidence{{ID: "e1"}})
	wrapped := fmt.Errorf("%w: %w", ErrAnswerValidation, err)
	if !errors.Is(wrapped, ErrAnswerValidation) || AnswerValidationCode(wrapped) != "unknown_citation" || strings.Contains(wrapped.Error(), "SECRET") || strings.Contains(FailureMessage(wrapped), "SECRET") {
		t.Fatal("unsafe or missing validation diagnostic", wrapped)
	}
	if AnswerValidationCode(errors.New(secret)) != "" {
		t.Fatal("untyped remote error became a diagnostic")
	}
}

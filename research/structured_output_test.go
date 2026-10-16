package research

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"searchan/model"
	"searchan/search"
)

func TestIgnoredNativeSchemaCannotPublishDraft(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// The gateway accepts native schema but omits required fields. Ordinary
		// typed decoding alone would accept this otherwise usable answer.
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"answers_query":true,"title":"SECRET_DRAFT","lead":{"text":"Supported fact","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`}}}})
	}))
	defer s.Close()
	c := &model.Client{Endpoint: s.URL, Model: "fixture", StructuredOutput: true, MaxRetries: 1, Client: s.Client()}
	e, err := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: unusedCandidateReader{}, Model: c, VerifyWithModel: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Synthesize(context.Background(), SynthesisInput{Query: "Atlas query contract", Plan: Plan{Intent: "fact", Queries: []string{"Atlas query contract"}}, GeneratedAt: time.Now(), Evidence: []Evidence{{ID: "e1", URL: "https://source.example/atlas", Kind: "body", Text: "Supported fact"}}})
	encoded, _ := json.Marshal(out)
	if !errors.Is(err, ErrAnswerGeneration) || calls != 1 || out.Answer.Title != "" || len(out.Sources) != 0 || strings.Contains(string(encoded)+FailureMessage(err), "SECRET_DRAFT") {
		t.Fatalf("schema violation published or retried a draft: calls=%d err=%v", calls, err)
	}
}

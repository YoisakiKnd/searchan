package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"searchan/model"
	"searchan/research"
)

func TestTerminationSurvivesModelTapeWithoutRawVendorState(t *testing.T) {
	for _, tc := range []struct{ api, wire, code, finish string }{
		{"chat_completions", `{"choices":[{"finish_reason":"length","message":{"content":"TOPSECRET-TEST"}}],"usage":{"completion_tokens":2400}}`, "output_incomplete", "length"},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{}","refusal":"TOPSECRET-TEST"}}]}`, "output_refused", "stop"},
		{"anthropic", `{"stop_reason":"TOPSECRET-TEST","content":[]}`, "output_protocol", "unspecified"},
		{"responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","text":"TOPSECRET-TEST"}]}]}`, "output_refused", "completed"},
	} {
		t.Run(tc.api, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.wire) }))
			defer s.Close()
			tape := Record()
			cfg := research.Config{Model: &model.Client{Endpoint: s.URL, Model: "fixture", API: tc.api, Client: s.Client()}}
			tape.Wrap(&cfg)
			var out research.Answer
			err := cfg.Model.Complete(context.Background(), "JSON", nil, &out)
			codes, issues := tape.ModelErrors(), tape.ModelOutputIssues()
			if err == nil || ErrorCode(err) != tc.code || len(codes) != 1 || codes[0] != tc.code || len(issues) != 1 || issues[0].Finish != tc.finish || out.Title != "" || tape.LiveModelCalls() != 1 {
				t.Fatalf("termination lost through tape: %+v %+v %v", codes, issues, err)
			}
			if ErrorCode(fmt.Errorf("%w: %w", research.ErrAnswerGeneration, err)) != "generation" {
				t.Fatal("typed model details hid the public generation failure")
			}
			encoded, marshalErr := json.Marshal(issues)
			if marshalErr != nil || strings.Contains(string(encoded), "TOPSECRET") || strings.Contains(err.Error(), "TOPSECRET") {
				t.Fatal("raw stop/refusal/draft leaked into diagnostics")
			}
		})
	}
}

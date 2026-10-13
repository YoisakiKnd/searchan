package research

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"searchan/model"
)

func TestTerminatedModelAnswerDoesNotEnterJSONRegeneration(t *testing.T) {
	for _, wire := range []string{
		`{"choices":[{"finish_reason":"length","message":{"content":"{\"title\":\"private truncated draft\"}"}}]}`,
		`{"choices":[{"finish_reason":"content_filter","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`,
	} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Write([]byte(wire))
		}))
		c := &model.Client{Endpoint: s.URL, Model: "fixture", MaxRetries: 1, Client: s.Client()}
		e := &Engine{cfg: Config{Model: c, ModelTimeout: 5 * time.Second}}
		out := Output{Evidence: []Evidence{{ID: "e1"}}}
		a, err := e.generate(context.Background(), &out, answerPrompt, nil, false)
		s.Close()
		if !errors.Is(err, ErrAnswerGeneration) || a.Title != "" || calls != 1 || out.Stats.ModelCalls != 1 || out.Stats.OutputRetries != 0 {
			t.Fatalf("terminated answer exposed or retried: %+v %+v %d %v", a, out.Stats, calls, err)
		}
	}
}

func TestTruncatedVerificationKeepsSafeDiagnosticAndFreshSummary(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"private verification draft"}}],"usage":{"completion_tokens":2400,"completion_tokens_details":{"reasoning_tokens":2390}}}`))
	}))
	defer s.Close()
	c := &model.Client{Endpoint: s.URL, Model: "fixture", Client: s.Client()}
	lm := modelFunc(func(ctx context.Context, prompt string, input, output any) error {
		if prompt == verifyPrompt {
			return c.Complete(ctx, prompt, input, output)
		}
		text := "Original draft"
		if prompt == briefPrompt {
			text = "Fresh conservative summary"
		}
		return assign(output, `{"title":"Protocol","lead":{"text":"`+text+`","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
	})
	e := &Engine{cfg: Config{Model: lm, VerifyWithModel: true, ModelTimeout: 5 * time.Second}}
	out := Output{Query: "Protocol behavior", Evidence: []Evidence{{ID: "e1", Kind: "body"}}}
	err := e.summarize(context.Background(), &out, nil)
	d := out.Stats.VerificationOutput
	if err != nil || out.AnswerMode != "brief_summary" || out.AnswerReason != "verification_unavailable" || out.Answer.Lead.Text != "Fresh conservative summary" || out.Stats.ModelCalls != 3 || out.Stats.OutputRetries != 0 || d == nil || d.Kind != "incomplete" || d.Finish != "length" || d.OutputTokens != 2400 || d.ReasoningTokens != 2390 || strings.Contains(out.Text(), "private") || strings.Contains(out.Text(), "Original draft") {
		t.Fatalf("verification truncation hidden or draft exposed: %+v %v", out, err)
	}
}

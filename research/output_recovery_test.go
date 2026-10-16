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
)

func modelOutputFailure(t *testing.T, content string) error {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
	}))
	defer s.Close()
	c := model.Client{Endpoint: s.URL, Model: "fixture"}
	var a Answer
	err := c.Complete(context.Background(), "fixture", nil, &a)
	var detail *model.OutputError
	if !errors.As(err, &detail) {
		t.Fatalf("expected typed model output failure: %v", err)
	}
	return err
}

func TestOutputRecoveryIsFreshValidatedAndBounded(t *testing.T) {
	for _, malformed := range []string{`{"lead":`, `{"lead":{"text":42}}`} {
		failure := modelOutputFailure(t, malformed)
		for _, second := range []string{"valid", "invalid_json", "invalid_citation"} {
			t.Run(malformed+second, func(t *testing.T) {
				calls := 0
				lm := modelFunc(func(_ context.Context, prompt string, input, output any) error {
					calls++
					if calls == 1 {
						// A decoder may partially fill the destination before failing.
						output.(*Answer).Sections = []Section{{Heading: "private partial draft"}}
						return failure
					}
					if output.(*Answer).Sections != nil || !strings.Contains(prompt, "生成完整 JSON") || !strings.Contains(prompt, "sections=[]") {
						t.Fatal("partial draft or missing regeneration instructions")
					}
					if strings.Contains(prompt, "结构上限6节") || !strings.Contains(prompt, answerEvidenceRules) {
						t.Fatal("minimal regeneration inherited the full layout or lost evidence rules")
					}
					if second == "invalid_json" {
						return failure
					}
					id := "e1"
					if second == "invalid_citation" {
						id = "invented"
					}
					return assign(output, `{"title":"Summary","lead":{"text":"Supported fact","evidence_ids":["`+id+`"]}}`)
				})
				e := &Engine{cfg: Config{Model: lm, ModelTimeout: 5 * time.Second}}
				out := Output{Evidence: []Evidence{{ID: "e1"}}}
				a, err := e.generate(context.Background(), &out, answerPrompt, map[string]any{"evidence": out.Evidence}, false)
				if calls != 2 || out.Stats.OutputRetries != 1 || out.Stats.ModelCalls != 2 {
					t.Fatalf("unexpected recovery budget: %+v calls=%d", out.Stats, calls)
				}
				if (err == nil) != (second == "valid") || (err != nil && a.Title != "") {
					t.Fatalf("unsafe recovered answer: %+v %v", a, err)
				}
				// Later generation in the same task cannot repeat JSON recovery.
				calls = 0
				_, err = e.generate(context.Background(), &out, answerPrompt, nil, false)
				if err == nil || calls != 1 || out.Stats.OutputRetries != 1 {
					t.Fatal("repeated recovery in same task", err)
				}
			})
		}
	}
}

func TestOutputRecoveryPreservesTimeAndVerificationBudget(t *testing.T) {
	failure := modelOutputFailure(t, `{"title":`)
	for _, tt := range []struct {
		name    string
		calls   int
		timeout time.Duration
		reserve bool
	}{
		{"verification_reserved", 4, 5 * time.Second, true},
		{"call_limit", 5, 5 * time.Second, false},
		{"time_limit", 0, time.Second, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			e := &Engine{cfg: Config{ModelTimeout: tt.timeout, Model: modelFunc(func(context.Context, string, any, any) error { calls++; return failure })}}
			out := Output{Stats: Stats{ModelCalls: tt.calls}}
			_, err := e.generate(context.Background(), &out, answerPrompt, nil, tt.reserve)
			if !errors.Is(err, ErrAnswerGeneration) || calls != 1 || out.Stats.OutputRetries != 0 || out.Stats.ModelCalls > maxModelCalls {
				t.Fatalf("recovery exceeded budget: %+v %v", out.Stats, err)
			}
		})
	}
}

func TestSpecializedOutputRecoveryKeepsScopeRules(t *testing.T) {
	failure := modelOutputFailure(t, `{"lead":`)
	for _, prompt := range []string{factPrompt, introductionPrompt, comparisonPrompt, briefPrompt} {
		calls := 0
		e := &Engine{cfg: Config{ModelTimeout: 5 * time.Second, Model: modelFunc(func(_ context.Context, got string, input, output any) error {
			calls++
			if calls == 1 {
				return failure
			}
			if !strings.HasPrefix(got, prompt) || !strings.Contains(got, syntaxCorrection) {
				t.Fatal("specialized writing scope lost during format recovery")
			}
			return assign(output, `{"title":"Summary","lead":{"text":"Supported fact","evidence_ids":["e1"]}}`)
		})}}
		out := Output{Evidence: []Evidence{{ID: "e1"}}}
		if _, err := e.generate(context.Background(), &out, prompt, nil, false); err != nil || calls != 2 {
			t.Fatalf("specialized recovery failed: calls=%d err=%v", calls, err)
		}
	}
}

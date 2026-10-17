package research

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVerifierMissingCannotPublishUnverifiedAssertions(t *testing.T) {
	in := fixedSynthesisInput()
	ps, r := fixtures()
	calls := 0
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		calls++
		if prompt == verifyPrompt {
			return assign(output, `{"answers_query":true,"unsupported":[],"missing":["SECRET_UNVERIFIED_SCOPE: default timeout is exactly 42 seconds","SECRET_UNVERIFIED_RECOMMENDATION: all workers stop immediately"]}`)
		}
		return assign(output, `{"answers_query":true,"title":"Atlas cancellation","lead":{"text":"Atlas signals cancellation; workers must cooperate to stop early.","evidence_ids":["stable-id"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
	out, err := e.Synthesize(context.Background(), in)
	if err != nil || calls != 2 || out.AnswerMode != "summary" || strings.Contains(out.Text(), "SECRET_UNVERIFIED") || len(out.Answer.Uncertainties) != 1 {
		t.Fatalf("unverified verifier prose became public assertions: calls=%d output=%+v error=%v", calls, out, err)
	}
}

func TestVerificationGapStatusPreservesCheckedConditionsAndLimits(t *testing.T) {
	for _, mode := range []string{"none", "many", "full", "already_noticed"} {
		t.Run(mode, func(t *testing.T) {
			in := fixedSynthesisInput()
			gaps := []string{}
			if mode != "none" {
				for range 9 {
					gaps = append(gaps, "SECRET_NEW_SCOPE")
				}
			}
			conditions := []string{"Workers must cooperate to stop early."}
			if mode == "full" {
				for len(conditions) < 6 {
					conditions = append(conditions, "The source describes cooperative workers.")
				}
			}
			if mode == "already_noticed" {
				conditions = append(conditions, verificationGapNotice)
			}
			ps, r := fixtures()
			m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
				var value any
				if prompt == verifyPrompt {
					value = map[string]any{"answers_query": true, "unsupported": []string{}, "missing": gaps}
				} else {
					value = map[string]any{"answers_query": true, "title": "Atlas cancellation", "lead": Claim{Text: "Atlas signals cancellation; workers must cooperate to stop early.", EvidenceIDs: []string{"stable-id"}}, "sections": []Section{}, "uncertainties": conditions}
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return assign(output, string(raw))
			})
			e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
			out, err := e.Synthesize(context.Background(), in)
			want := len(conditions)
			if mode == "many" {
				want++
			}
			if err != nil || out.Stats.ModelCalls != 2 || out.Stats.VerificationGaps != len(gaps) || strings.Contains(out.Text(), "SECRET_NEW_SCOPE") || len(out.Answer.Uncertainties) != want {
				t.Fatalf("verification status bypassed existing layout/call limits: output=%+v error=%v", out, err)
			}
			for i, text := range conditions {
				if out.Answer.Uncertainties[i] != text {
					t.Fatal("checked condition was edited or removed")
				}
			}
		})
	}
}

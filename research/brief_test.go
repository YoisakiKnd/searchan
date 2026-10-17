package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBriefLayoutIsRegeneratedOnceFromEvidence(t *testing.T) {
	for _, invalid := range []string{
		`{"title":"Brief","lead":{"text":"` + strings.Repeat("字", 301) + `","evidence_ids":["e1"]}}`,
		`{"title":"Brief","lead":{"text":"Fact","evidence_ids":["e1"]},"sections":[{"heading":"Extra","claims":[{"text":"Fact","evidence_ids":["e1"]}]}]}`,
		`{"title":"Brief","lead":{"text":"Fact","evidence_ids":["e1"]},"table":{"columns":["A","B"],"rows":[{"cells":["A","B"],"evidence_ids":["e1"]}]}}`,
		`{"title":"Brief","lead":{"text":"Fact","evidence_ids":["e1"]},"sections":[],"uncertainties":["An uncited stronger condition must never pass here."]}`,
	} {
		ps, r := fixtures()
		calls := 0
		lm := modelFunc(func(_ context.Context, prompt string, in, output any) error {
			calls++
			input := in.(map[string]any)
			if input["evidence"] == nil || input["reason"] != "snippets_only" || input["brief_limits"].(map[string]any)["model_max_chars"] != 300-utf8.RuneCountInString("Notice") || input["brief_limits"].(map[string]any)["max_uncertainties"] != 0 {
				t.Fatal("evidence or reserved notice budget missing")
			}
			if calls == 1 {
				if prompt != briefPrompt {
					t.Fatal("wrong initial prompt")
				}
				return assign(output, invalid)
			}
			if calls != 2 || prompt != briefRepairPrompt || input["brief_correction"] == nil || input["previous_answer"] != nil {
				t.Fatal("unbounded repair or invalid draft reused")
			}
			return fixtureModel().Complete(context.Background(), prompt, in, output)
		})
		e, _ := New(Config{Providers: ps, Reader: r, Model: lm})
		out := Output{Evidence: []Evidence{{ID: "e1", Text: "Workers observe cancellation."}}}
		err := e.briefSummary(context.Background(), &out, "snippets_only", "Notice")
		if err != nil || calls != 2 || out.Stats.ModelCalls != 2 || out.Stats.AnswerRepairs != 1 || out.AnswerMode != "brief_summary" || out.AnswerReason != "snippets_only" || out.Answer.Uncertainties[0] != "Notice" {
			t.Fatalf("layout did not recover: %+v calls=%d err=%v", out, calls, err)
		}
	}
}

func TestBriefCannotPublishUncitedConditionsAfterOneFreshRepair(t *testing.T) {
	ps, r := fixtures()
	calls := 0
	m := modelFunc(func(_ context.Context, _ string, in, output any) error {
		calls++
		if fields := in.(map[string]any); fields["previous_answer"] != nil {
			t.Fatal("unsupported brief used as repair evidence")
		}
		return assign(output, `{"title":"Brief","lead":{"text":"Atlas signals cancellation.","evidence_ids":["e1"]},"sections":[],"uncertainties":["SECRET_UNCITED_CONDITION"]}`)
	})
	e, _ := New(Config{Providers: ps, Reader: r, Model: m})
	out := Output{Evidence: []Evidence{{ID: "e1", Text: "Atlas signals cancellation."}}}
	err := e.briefSummary(context.Background(), &out, "verification_failed", "Notice")
	if !errors.Is(err, ErrAnswerValidation) || calls != 2 || out.Stats.AnswerRepairs != 1 || out.Answer.Title != "" || len(out.Sources) != 0 || strings.Contains(err.Error(), "SECRET_UNCITED_CONDITION") {
		t.Fatalf("uncited brief condition was salvaged or retried indefinitely: output=%+v calls=%d err=%v", out, calls, err)
	}
}

func TestBriefInputExcludesPlannerAndVerifierAssertions(t *testing.T) {
	in := fixedSynthesisInput()
	in.Plan.Aspects = []string{"SECRET_PLAN_SCOPE"}
	ps, r := fixtures()
	calls := 0
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		calls++
		if prompt == verifyPrompt {
			return assign(output, `{"answers_query":true,"unsupported":["SECRET_VERIFIER_ASSERTION"],"missing":[]}`)
		}
		if prompt == briefPrompt {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"SECRET_PLAN_SCOPE", "SECRET_VERIFIER_ASSERTION", "SECRET_DRAFT", `"aspects"`, `"problems"`, `"previous_answer"`} {
				if strings.Contains(string(raw), value) {
					t.Fatal("model-authored scope or assertions reached independent brief input", value)
				}
			}
			fields := input.(map[string]any)
			if fields["query"] != in.Query || fields["current_date"] != "2026-08-01" || fields["reason"] != "verification_failed" {
				t.Fatal("original question, reference date or program status was lost", fields)
			}
			assertWireEvidence(t, input, in.Evidence)
			return assign(output, `{"title":"Atlas cancellation","lead":{"text":"Atlas signals cancellation; workers must cooperate to stop early.","evidence_ids":["stable-id"]},"sections":[],"uncertainties":[]}`)
		}
		return assign(output, `{"title":"Draft","lead":{"text":"SECRET_DRAFT","evidence_ids":["stable-id"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
	out, err := e.Synthesize(context.Background(), in)
	if err != nil || calls != 3 || out.AnswerMode != "brief_summary" || strings.Contains(out.Text(), "SECRET_") {
		t.Fatalf("independent brief did not preserve its input boundary: calls=%d output=%+v error=%v", calls, out, err)
	}
}

func TestBriefInvalidCitationsNeverEnterLayoutRepair(t *testing.T) {
	for _, ids := range []string{`[]`, `["invented"]`} {
		ps, r := fixtures()
		calls := 0
		lm := modelFunc(func(_ context.Context, _ string, _, output any) error {
			calls++
			return assign(output, `{"title":"Brief","lead":{"text":"`+strings.Repeat("字", 301)+`","evidence_ids":`+ids+`}}`)
		})
		e, _ := New(Config{Providers: ps, Reader: r, Model: lm})
		out := Output{Evidence: []Evidence{{ID: "e1"}}}
		err := e.briefSummary(context.Background(), &out, "snippets_only", "Notice")
		if !errors.Is(err, ErrAnswerValidation) || calls != 1 || out.Stats.AnswerRepairs != 0 || out.Answer.Title != "" {
			t.Fatalf("invalid citations were repaired/published: %+v calls=%d err=%v", out, calls, err)
		}
	}
}

func TestBriefNoticeIncludedInExactCharacterLimit(t *testing.T) {
	for _, length := range []int{300, 301} {
		notice := "关键事实待确认。"
		a := Answer{Title: "T", Lead: Claim{Text: strings.Repeat("字", length-1-utf8.RuneCountInString(notice)), EvidenceIDs: []string{"e1"}}}
		got, err := validateBrief(a, []Evidence{{ID: "e1"}}, notice)
		if length == 300 {
			if err != nil || len(got.Uncertainties) != 1 || got.Lead.Text != a.Lead.Text {
				t.Fatal("exact limit rejected or text truncated", err)
			}
			continue
		}
		var layout *briefLayoutError
		if !errors.As(err, &layout) || layout.chars != 301 || !strings.Contains(err.Error(), "total_chars=301") || !strings.Contains(err.Error(), "sections=0, has_table=false") || got.Title != "" {
			t.Fatal("missing precise safe diagnostic", err)
		}
	}
}

func TestBriefRepairUsesRemainingOriginalBudget(t *testing.T) {
	for _, name := range []string{"shared-stage", "too-little-time", "call-cap", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ps, r := fixtures()
			var deadlines []time.Time
			lm := modelFunc(func(ctx context.Context, prompt string, in, output any) error {
				d, ok := ctx.Deadline()
				if !ok {
					t.Fatal("model call unbounded")
				}
				deadlines = append(deadlines, d)
				if len(deadlines) == 1 {
					if name == "shared-stage" {
						time.Sleep(30 * time.Millisecond)
					}
					return assign(output, `{"title":"Brief","lead":{"text":"`+strings.Repeat("字", 301)+`","evidence_ids":["e1"]}}`)
				}
				return fixtureModel().Complete(ctx, prompt, in, output)
			})
			e, _ := New(Config{Providers: ps, Reader: r, Model: lm, ModelTimeout: 3 * time.Second})
			out := Output{Evidence: []Evidence{{ID: "e1"}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			switch name {
			case "too-little-time":
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, time.Second)
				defer stop()
			case "call-cap":
				out.Stats.ModelCalls = maxModelCalls - 1
			case "canceled":
				cancel()
			}
			err := e.briefSummary(ctx, &out, "snippets_only", "Notice")
			if name == "shared-stage" {
				if err != nil || len(deadlines) != 2 || deadlines[1].After(deadlines[0].Add(10*time.Millisecond)) {
					t.Fatal("repair received a fresh stage budget", deadlines, err)
				}
				return
			}
			if err == nil || out.Answer.Title != "" || out.Stats.ModelCalls > maxModelCalls || out.Stats.AnswerRepairs != 0 || len(deadlines) > 1 {
				t.Fatalf("budget/cancellation bypassed: %+v deadlines=%v err=%v", out, deadlines, err)
			}
		})
	}
}

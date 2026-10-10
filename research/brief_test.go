package research

import (
	"context"
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
	} {
		ps, r := fixtures()
		calls := 0
		lm := modelFunc(func(_ context.Context, prompt string, in, output any) error {
			calls++
			input := in.(map[string]any)
			if input["evidence"] == nil || input["reason"] != "snippets_only" || input["brief_limits"].(map[string]any)["model_max_chars"] != 300-utf8.RuneCountInString("Notice") {
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
		err := e.briefSummary(context.Background(), &out, map[string]any{"evidence": out.Evidence}, "snippets_only", "Notice")
		if err != nil || calls != 2 || out.Stats.ModelCalls != 2 || out.Stats.AnswerRepairs != 1 || out.AnswerMode != "brief_summary" || out.AnswerReason != "snippets_only" || out.Answer.Uncertainties[0] != "Notice" {
			t.Fatalf("layout did not recover: %+v calls=%d err=%v", out, calls, err)
		}
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
		err := e.briefSummary(context.Background(), &out, map[string]any{}, "snippets_only", "Notice")
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
			err := e.briefSummary(ctx, &out, map[string]any{}, "snippets_only", "Notice")
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

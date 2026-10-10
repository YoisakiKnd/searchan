package research

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestMissingModelRejectsBeforeSearching(t *testing.T) {
	ps, r := fixtures()
	ps[0] = provider{"unused", func(context.Context, search.Request) (search.Response, error) {
		t.Fatal("unconfigured model must not incur search calls")
		return search.Response{}, nil
	}}
	e, _ := New(Config{Providers: ps, Reader: r})
	out, err := e.Run(context.Background(), "Go context")
	if !errors.Is(err, ErrModelRequired) || out.Stats.SearchCalls != 0 || out.Answer.Title != "" {
		t.Fatalf("missing model did not fail early: %+v %v", out, err)
	}
}

func TestGenerationFailureNeverReturnsExtracts(t *testing.T) {
	for _, fail := range []error{errors.New("upstream credential-private"), context.DeadlineExceeded} {
		ps, r := fixtures()
		e, _ := New(Config{Providers: ps, Reader: r, Model: modelFunc(func(context.Context, string, any, any) error { return fail })})
		out, err := e.Run(context.Background(), "Go context")
		if !errors.Is(err, ErrAnswerGeneration) || !errors.Is(err, fail) || out.Answer.Title != "" || len(out.Sources) != 0 || out.Stats.ModelCalls != 1 {
			t.Fatalf("generation failure concealed: %+v %v", out, err)
		}
		if strings.Contains(FailureMessage(err), fail.Error()) {
			t.Fatal("upstream details exposed to user")
		}
	}
}

func TestVerificationFallbackUsesFreshBriefSummary(t *testing.T) {
	for _, reason := range []string{"verification_failed", "verification_unavailable", "verification_incomplete", "verified"} {
		t.Run(reason, func(t *testing.T) {
			ps, r := fixtures()
			calls := 0
			lm := modelFunc(func(_ context.Context, prompt string, input, output any) error {
				calls++
				switch prompt {
				case verifyPrompt:
					switch reason {
					case "verification_unavailable":
						return errors.New("unavailable")
					case "verification_incomplete":
						return assign(output, `{}`)
					case "verified":
						return assign(output, `{"unsupported":[],"missing":["deadline behavior remains unconfirmed"]}`)
					default:
						return assign(output, `{"unsupported":["Unsupported conclusion"],"missing":[]}`)
					}
				case briefPrompt:
					in := input.(map[string]any)
					if _, ok := in["previous_answer"]; ok {
						t.Fatal("failed answer reused")
					}
					return assign(output, `{"title":"简短总结","lead":{"text":"资料提及取消信号；更具体的行为仍需确认。","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
				default:
					return assign(output, `{"title":"Original","lead":{"text":"Unsupported conclusion","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
				}
			})
			e, _ := New(Config{Providers: ps, Reader: r, Model: lm, VerifyWithModel: true})
			out, err := e.Run(context.Background(), "Go context")
			if err != nil || ValidateAnswer(out.Answer, out.Evidence) != nil || len(out.Sources) == 0 {
				t.Fatalf("invalid output: %+v %v", out, err)
			}
			if reason == "verified" {
				if out.AnswerMode != "summary" || calls != 2 || !strings.Contains(out.Text(), "deadline behavior") {
					t.Fatalf("verified answer lost: %+v", out)
				}
				return
			}
			want := reason
			if reason == "verification_incomplete" {
				want = "verification_unavailable"
			}
			if out.AnswerMode != "brief_summary" || out.AnswerReason != want || calls != 3 || strings.Contains(out.Text(), "Unsupported conclusion") || !strings.Contains(out.Text(), "关键事实待确认") {
				t.Fatalf("bad verification fallback: %+v calls=%d", out, calls)
			}
		})
	}
}

func TestInvalidBriefSummaryFailsWithoutPublishingOriginal(t *testing.T) {
	for _, invalid := range []string{
		`{"title":"Brief","lead":{"text":"Fact","evidence_ids":["invented"]}}`,
		`{"title":"Brief","lead":{"text":"Fact","evidence_ids":["e1"]},"sections":[{"heading":"Long","claims":[]}]}`,
		`{"title":"Brief","lead":{"text":"` + strings.Repeat("字", 301) + `","evidence_ids":["e1"]}}`,
		`not json`,
	} {
		ps, r := fixtures()
		lm := modelFunc(func(_ context.Context, prompt string, _ any, output any) error {
			switch prompt {
			case verifyPrompt:
				return assign(output, `{"unsupported":["bad assertion"],"missing":[]}`)
			case briefPrompt, briefRepairPrompt:
				return assign(output, invalid)
			default:
				return fixtureModel().Complete(context.Background(), prompt, nil, output)
			}
		})
		e, _ := New(Config{Providers: ps, Reader: r, Model: lm, VerifyWithModel: true})
		out, err := e.Run(context.Background(), "Go context")
		if (!errors.Is(err, ErrAnswerValidation) && !errors.Is(err, ErrAnswerGeneration)) || out.Answer.Title != "" || out.AnswerMode != "" || len(out.Sources) != 0 {
			t.Fatalf("invalid short answer leaked original or extracts: %+v %v", out, err)
		}
	}
}

func TestNoUsableEvidenceDoesNotCallGenerator(t *testing.T) {
	ps, _ := fixtures()
	ps[0] = provider{"empty", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://example.org/", Title: "Unrelated"}}}, nil
	}}
	e, _ := New(Config{Providers: ps, Reader: readFunc(func(context.Context, string) (reader.Document, error) {
		return reader.Document{}, errors.New("blocked")
	}), Model: modelFunc(func(context.Context, string, any, any) error {
		t.Fatal("generator called without evidence")
		return nil
	})})
	_, err := e.Run(context.Background(), "query")
	if !errors.Is(err, ErrNoEvidence) {
		t.Fatal(err)
	}
}

func TestVerificationTimeoutLeavesBudgetForBriefSummary(t *testing.T) {
	ps, r := fixtures()
	lm := modelFunc(func(ctx context.Context, prompt string, input, output any) error {
		if prompt == verifyPrompt {
			<-ctx.Done()
			return ctx.Err()
		}
		return fixtureModel().Complete(ctx, prompt, input, output)
	})
	e, _ := New(Config{Providers: ps, Reader: r, Model: lm, VerifyWithModel: true})
	ctx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	out, err := e.Run(ctx, "Go context")
	if err != nil || out.AnswerReason != "verification_unavailable" || out.Stats.ModelCalls != 3 {
		t.Fatalf("short summary budget consumed: %+v %v", out, err)
	}
}

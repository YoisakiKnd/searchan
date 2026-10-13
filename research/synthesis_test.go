package research

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func fixedSynthesisInput() SynthesisInput {
	return SynthesisInput{Query: "Atlas cancellation", GeneratedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Plan: Plan{Intent: "fact", Queries: []string{"Atlas cancellation"}, Aspects: []string{"cancellation"}}, Evidence: []Evidence{{ID: "stable-id", URL: "https://manual.example/atlas", Title: "Atlas cancellation", Text: "Atlas cancellation signals workers; the work must cooperate to stop early.", Kind: "body", Truncated: true, Paragraph: 7, ParagraphEnd: 9, MatchedAspects: []int{0}}}}
}

func TestSynthesizeFreezesInputsWithoutRetrievalOrRenumbering(t *testing.T) {
	in := fixedSynthesisInput()
	modelCalls := 0
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		modelCalls++
		if prompt == plannerPrompt || prompt == coveragePrompt {
			t.Fatal("fixed evidence called retrieval planning or coverage")
		}
		got := input.(map[string]any)
		assertWireEvidence(t, input, in.Evidence)
		if got["query"] != in.Query {
			t.Fatal("frozen evidence changed")
		}
		if prompt == verifyPrompt {
			return assign(output, `{"answers_query":false,"unsupported":[],"missing":["worker completion conditions"]}`)
		}
		if got["current_date"] != "2026-08-01" {
			t.Fatal("reference date changed to rerun date")
		}
		if prompt == briefPrompt {
			return assign(output, `{"title":"Atlas cancellation","lead":{"text":"Atlas cancellation signals workers.","evidence_ids":["stable-id"]},"sections":[],"uncertainties":[]}`)
		}
		return assign(output, `{"answers_query":true,"title":"Atlas cancellation","lead":{"text":"Atlas cancellation signals workers.","evidence_ids":["stable-id"]},"sections":[],"uncertainties":[]}`)
	})
	e, _ := New(Config{Providers: []search.Provider{provider{"forbidden", func(context.Context, search.Request) (search.Response, error) { panic("fixed synthesis searched") }}}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { panic("fixed synthesis read a page") }), Model: m, PlanWithModel: true, VerifyWithModel: true})
	out, err := e.Synthesize(context.Background(), in)
	if err != nil || modelCalls != 3 || out.Stats.ModelCalls != 3 || out.Stats.SearchCalls != 0 || out.Stats.PagesRead != 0 || out.AnswerReason != "question_unanswered" || !reflect.DeepEqual(out.Evidence, in.Evidence) || !out.GeneratedAt.Equal(in.GeneratedAt) || len(out.Sources) != 1 {
		t.Fatalf("fixed synthesis changed input or escaped into retrieval: %+v %v", out, err)
	}
	// Result ownership must not let later annotation mutate the source input.
	out.Evidence[0].MatchedAspects[0] = 3
	out.Plan.Aspects[0] = "changed"
	if in.Evidence[0].MatchedAspects[0] != 0 || in.Plan.Aspects[0] != "cancellation" {
		t.Fatal("output aliases source input slices")
	}
}

func TestSynthesizeRejectsMalformedInputsBeforeModelRequests(t *testing.T) {
	for _, mutate := range []func(*SynthesisInput){
		func(in *SynthesisInput) { in.Evidence = append(in.Evidence, in.Evidence[0]) },
		func(in *SynthesisInput) { in.Evidence[0].ID = "" },
		func(in *SynthesisInput) { in.Evidence[0].Kind = "unknown" },
		func(in *SynthesisInput) { in.Evidence[0].URL = "http://127.0.0.1/private" },
		func(in *SynthesisInput) { in.Evidence[0].Text = strings.Repeat("x", 4097) },
		func(in *SynthesisInput) { in.GeneratedAt = time.Time{} },
		func(in *SynthesisInput) { in.Plan.Queries = nil },
		func(in *SynthesisInput) { in.Plan.Intent = "unknown" },
	} {
		in := fixedSynthesisInput()
		mutate(&in)
		ps, r := fixtures()
		e, _ := New(Config{Providers: ps, Reader: r, Model: modelFunc(func(context.Context, string, any, any) error {
			t.Fatal("invalid fixed input reached model")
			return nil
		})})
		if _, err := e.Synthesize(context.Background(), in); !errors.Is(err, ErrInvalidEvidenceInput) {
			t.Fatal("invalid input accepted", err)
		}
	}
	ps, r := fixtures()
	e, _ := New(Config{Providers: ps, Reader: r, Model: modelFunc(func(context.Context, string, any, any) error {
		t.Fatal("cancelled synthesis reached model")
		return nil
	})})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Synthesize(ctx, fixedSynthesisInput()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled fixed synthesis did work", err)
	}
}

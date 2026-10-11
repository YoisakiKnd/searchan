package research

import (
	"context"
	"errors"
	"testing"
)

func TestIntroductionDoesNotOverrideCompoundOrExplicitRequests(t *testing.T) {
	for _, q := range []string{"SpringData简介", "spring data 简介", "介绍SpringData", "Spring Data 是什么？", "什么是Go字典", "汉字简介", "Spring Data overview", "What is Spring Data?", "Brief intro to PostgreSQL", "伽利略简介"} {
		if !introductoryQuery(q) {
			t.Errorf("simple introduction not recognized: %s", q)
		}
	}
	for _, q := range []string{"Spring Data 和 Spring Framework 有什么区别？", "Spring Data JPA 和 Hibernate 是什么关系？", "详细介绍 SpringData", "SpringData 简介及优缺点", "介绍 Spring Data 的使用方法", "Spring Data 是什么原理？", "SpringData简介，500字", "用五百字介绍SpringData", "SpringData简介，不少于500字", "SpringData简介以及代码示例", "What is Spring Data and how does it work?", "Spring Data introduction in 500 words", "Spring Data versus Hibernate overview", "Go http.Client 请求后需要关闭 Body 吗？"} {
		if introductoryQuery(q) {
			t.Errorf("explicit scope compressed: %s", q)
		}
	}
}

func TestIntroductionKeepsVerificationAndCallBudget(t *testing.T) {
	for _, q := range []string{"Atlas简介", "Atlas 简介及用法"} {
		ps, r := fixtures()
		intro := introductoryQuery(q)
		generatorCalls, verifierCalls := 0, 0
		m := modelFunc(func(_ context.Context, prompt string, input, result any) error {
			if prompt == verifyPrompt {
				verifierCalls++
				return assign(result, `{"answers_query":true,"unsupported":[],"missing":["An important condition is not covered."]}`)
			}
			generatorCalls++
			if (prompt == introductionPrompt) != intro {
				t.Fatal("wrong writing scope")
			}
			_, hasLimits := input.(map[string]any)["introduction_limits"]
			if hasLimits != intro {
				t.Fatal("writing budget not propagated")
			}
			return assign(result, `{"answers_query":true,"title":"Atlas","lead":{"text":"Atlas offers repository abstractions.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
		})
		e, err := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
		if err != nil {
			t.Fatal(err)
		}
		out := Output{Query: q, Evidence: []Evidence{{ID: "e1", Kind: "body", URL: "https://example.org/atlas", Text: "Atlas offers repository abstractions."}}}
		if err := e.summarize(context.Background(), &out, nil); err != nil || generatorCalls != 1 || verifierCalls != 1 || out.Stats.ModelCalls != 2 || out.AnswerMode != "summary" || len(out.Answer.Uncertainties) != 1 {
			t.Fatalf("verification or limitation lost: %+v %v", out, err)
		}
	}
}

func TestIntroductionStillRejectsInvalidCitationsAndUsesBriefForSnippets(t *testing.T) {
	for _, kind := range []string{"body", "snippet"} {
		ps, r := fixtures()
		calls := 0
		m := modelFunc(func(_ context.Context, prompt string, _ any, result any) error {
			calls++
			if kind == "snippet" {
				if prompt != briefPrompt {
					t.Fatal("snippet promoted to full introduction")
				}
				return assign(result, `{"title":"Atlas","lead":{"text":"搜索摘要提及 Atlas；用途待确认。","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
			}
			return assign(result, `{"title":"Atlas","lead":{"text":"Atlas offers repositories.","evidence_ids":["invented"]},"sections":[],"uncertainties":[]}`)
		})
		e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
		out := Output{Query: "Atlas简介", Evidence: []Evidence{{ID: "e1", Kind: kind, URL: "https://example.org/atlas", Text: "Atlas offers repository abstractions."}}}
		err := e.summarize(context.Background(), &out, nil)
		if calls != 1 {
			t.Fatal("extra repair call introduced")
		}
		if kind == "body" {
			if !errors.Is(err, ErrAnswerValidation) || out.Answer.Title != "" {
				t.Fatal("invalid citation published", err)
			}
		} else if err != nil || out.AnswerReason != "snippets_only" {
			t.Fatal("snippet fallback changed", err)
		}
	}
}

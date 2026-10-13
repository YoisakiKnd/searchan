package research

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIntroductionDoesNotOverrideCompoundOrExplicitRequests(t *testing.T) {
	for _, q := range []string{"SpringData简介", "spring data 简介", "介绍SpringData", "Spring Data 是什么？", "什么是Go字典", "汉字简介", "Spring Data overview", "What is Spring Data?", "Brief intro to PostgreSQL", "伽利略简介"} {
		if !introductoryQuery(q) {
			t.Errorf("simple introduction not recognized: %s", q)
		}
	}
	for _, q := range []string{"Spring Data 和 Spring Framework 有什么区别？", "Spring Data JPA 和 Hibernate 是什么关系？", "详细介绍 SpringData", "SpringData 简介及优缺点", "介绍 Spring Data 的使用方法", "Spring Data 是什么原理？", "SpringData简介，500字", "用五百字介绍SpringData", "SpringData简介，不少于500字", "SpringData简介以及代码示例", "What is Spring Data and how does it work?", "Spring Data introduction in 500 words", "Spring Data versus Hibernate overview", "Go http.Client 请求后需要关闭 Body 吗？", "PostgreSQL 默认事务隔离级别是什么？", "Widget Service 的默认端口是什么？", "API 返回值是什么？"} {
		if introductoryQuery(q) {
			t.Errorf("explicit scope compressed: %s", q)
		}
	}
}

func TestFocusedFactsPreserveExplicitExplanationAndMultiQuestionScope(t *testing.T) {
	for _, q := range []string{"Go context 取消后 goroutine 会自动退出吗？", "Go http.Client 请求完成后需要关闭 resp.Body 吗？", "PostgreSQL 默认事务隔离级别是什么？", "Redis Pub/Sub 会保证消息送达并支持重放吗？", "JWT 的 payload 默认是加密的吗？", "大熊猫属于熊科还是浣熊科？", "北京故宫和台北故宫是同一个博物院吗？", "Does Atlas cancel work automatically?", "What is the default Atlas port?"} {
		if !focusedFactQuery(q) {
			t.Error("narrow fact missed", q)
		}
	}
	for _, q := range []string{"详细说明默认隔离级别与设置方法", "Go context 会自动退出吗？为什么？", "为什么 JWT 默认不加密", "Redis 是否重放？请举例", "JWT 是加密的吗，解释其原理", "PostgreSQL 默认隔离级别以及代码示例", "Go context 是否自动退出\n再介绍 cancel 的用法", "JWT默认加密吗，500字", "Does Atlas cancel work? Explain why.", "Atlas cancellation in 500 words", "Is Atlas available? List its versions.", "What is the latest Atlas release?", "Atlas如何配置默认端口", "Atlas与Zephyr详细比较"} {
		if focusedFactQuery(q) {
			t.Error("explicit writing scope compressed", q)
		}
	}
}

func TestFocusedFactsKeepVerificationAndFreshBriefFallback(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		ps, r := fixtures()
		calls := 0
		m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
			calls++
			switch calls {
			case 1:
				if prompt != factPrompt || input.(map[string]any)["fact_limits"] == nil {
					t.Fatal("focused fact scope missing")
				}
				return assign(output, `{"answers_query":true,"title":"Atlas","lead":{"text":"Atlas signals work.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
			case 2:
				if prompt != verifyPrompt {
					t.Fatal("focused answer skipped independent verification")
				}
				if unsupported {
					return assign(output, `{"answers_query":true,"unsupported":["FAILED-DRAFT-ASSERTION-2099 is too broad"],"missing":[]}`)
				}
				return assign(output, `{"answers_query":true,"unsupported":[],"missing":[]}`)
			default:
				got := input.(map[string]any)
				if !strings.HasPrefix(prompt, briefPrompt) || !strings.Contains(prompt, focusedFactRules) || got["answer"] != nil || got["problems"] != nil || got["fact_limits"] == nil {
					t.Fatal("brief reused draft or discarded question scope")
				}
				return assign(output, `{"title":"Atlas","lead":{"text":"Atlas provides a signal.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
			}
		})
		e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
		out := Output{Query: "Atlas是否自动取消工作？", Evidence: []Evidence{{ID: "e1", Kind: "body", URL: "https://example.org/atlas", Text: "Atlas provides a cancellation signal."}}}
		err := e.summarize(context.Background(), &out, nil)
		want := 2
		if unsupported {
			want++
		}
		if err != nil || calls != want || out.Stats.ModelCalls != want || (out.AnswerMode == "brief_summary") != unsupported {
			t.Fatalf("focused scope changed model budget or failed fallback: %+v %v", out, err)
		}
	}
}

func TestCompactComparisonPreservesExplicitWritingScope(t *testing.T) {
	for _, q := range []string{"Spring Data 和 Spring Framework 有什么区别？", "Redis 和 Memcached 对比", "Vector Store vs Query Engine"} {
		if !conciseComparisonQuery(q) {
			t.Error("simple comparison missed", q)
		}
	}
	for _, q := range []string{"SpringData简介", "详细比较 Spring Data 和 Spring Framework", "比较 Redis 和 Memcached，至少800字", "Redis vs Memcached detailed comparison", "Redis 和 Memcached 对比以及代码示例", "如何比较 PostgreSQL 隔离级别"} {
		if conciseComparisonQuery(q) {
			t.Error("explicit scope compressed", q)
		}
	}
	ps, r := fixtures()
	calls := 0
	m := modelFunc(func(_ context.Context, prompt string, input, result any) error {
		calls++
		if calls == 1 {
			if prompt != comparisonPrompt || input.(map[string]any)["comparison_limits"] == nil {
				t.Fatal("comparison writing scope not propagated")
			}
			return assign(result, `{"answers_query":true,"title":"Comparison","lead":{"text":"The systems have different scopes.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
		}
		if prompt != verifyPrompt {
			t.Fatal("comparison skipped independent verification")
		}
		return assign(result, `{"answers_query":true,"unsupported":[],"missing":[]}`)
	})
	e, _ := New(Config{Providers: ps, Reader: r, Model: m, VerifyWithModel: true})
	out := Output{Query: "Vector Store 和 Query Engine 的区别", Evidence: []Evidence{{ID: "e1", Kind: "body", Text: "The systems have different scopes."}}}
	if err := e.summarize(context.Background(), &out, nil); err != nil || calls != 2 || out.AnswerMode != "summary" {
		t.Fatalf("wrong comparison path: %+v %v", out, err)
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

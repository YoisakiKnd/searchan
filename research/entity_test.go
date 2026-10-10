package research

import (
	"context"
	"errors"
	"strings"
	"testing"

	"searchan/reader"
	"searchan/search"
)

func TestCompoundEntityAliasesDoNotCollapseNeighbors(t *testing.T) {
	for _, query := range []string{"SpringData简介", "Spring Data简介", "介绍SpringData"} {
		entities := queryEntities(query)
		if len(entities) == 0 {
			t.Fatalf("lost entity in %q", query)
		}
		for _, text := range []string{"SpringData", "Spring Data", "spring-data-jpa", "介绍SpringData是一个项目"} {
			if !matchesEntities(text, entities) {
				t.Fatalf("%q does not match %q", text, query)
			}
		}
		for _, text := range []string{"Spring Framework Data Access", "SpringDatabase", "springdatawarehouse"} {
			if matchesEntities(text, entities) {
				t.Fatalf("unrelated %q matches %q", text, query)
			}
		}
	}
	if got := spacedQuery("SpringData简介"); got != "Spring Data 简介" {
		t.Fatal(got)
	}
	if relevance("SpringData", "Spring Data repositories") <= relevance("SpringData", "Spring Framework documentation") {
		t.Fatal("compound name did not improve ranking")
	}
}

func TestEntityEvidenceSurvivesBroadPlanning(t *testing.T) {
	docs := []reader.Document{
		reader.MakeDocument("https://docs.example/framework", "Spring Framework Documentation", "Spring Framework includes Data Access, Transactions, JDBC, Testing, Web and Integration.", "fixture"),
		reader.MakeDocument("https://docs.example/data", "Spring Data", "Spring Data provides a common programming model for accessing data stores with repository abstractions.", "fixture"),
	}
	plan := Plan{Queries: []string{"SpringData简介", "Spring Framework documentation"}, Aspects: []string{"Spring Framework Data Access", "子模块"}}
	ev := SelectEvidenceForPlan(plan, docs, nil, 12)
	if len(ev) != 1 || ev[0].URL != docs[1].URL {
		t.Fatalf("wrong entity entered evidence: %+v", ev)
	}
	plan = normalizePlan(Plan{Queries: []string{"SpringData简介", "Spring Data 简介"}}, Plan{Queries: []string{"Spring Framework docs", ""}})
	if len(plan.Queries) != 3 || !strings.HasPrefix(plan.Queries[2], "Spring Data ") {
		t.Fatalf("planning lost original entity: %+v", plan)
	}
}

func TestWrongEntityRecoversBeforeGeneratingAnswer(t *testing.T) {
	for _, found := range []bool{true, false} {
		t.Run(map[bool]string{true: "recovery", false: "no-unrelated-fallback"}[found], func(t *testing.T) {
			p := provider{"fixture", func(_ context.Context, req search.Request) (search.Response, error) {
				r := search.Result{URL: "https://docs.example/framework", Title: "Spring Framework Documentation", Snippet: "Spring Framework Data Access", Content: strings.Repeat("Spring Framework documentation Data Access overview. ", 3)}
				if found && strings.Contains(req.Query, "overview official") {
					r = search.Result{URL: "https://docs.example/data", Title: "Spring Data", Content: strings.Repeat("Spring Data offers repository abstractions for accessing different data stores. ", 3)}
				}
				return search.Response{Results: []search.Result{r}}, nil
			}}
			var calls int
			m := modelFunc(func(ctx context.Context, prompt string, input, output any) error {
				calls++
				if !found {
					t.Fatal("model called with unrelated evidence")
				}
				if prompt == verifyPrompt {
					return assign(output, `{"answers_query":true,"unsupported":[],"missing":[]}`)
				}
				return assign(output, `{"answers_query":true,"title":"Spring Data 简介","lead":{"text":"Spring Data offers repository abstractions for accessing different data stores.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
			})
			e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(context.Context, string) (reader.Document, error) { return reader.Document{}, nil }), Model: m, VerifyWithModel: true})
			out, err := e.Run(context.Background(), "SpringData简介")
			if found {
				if err != nil || len(out.Evidence) != 1 || out.Evidence[0].Title != "Spring Data" || out.Stats.RecoveryRounds != 1 || !strings.Contains(out.Answer.Lead.Text, "Spring Data offers repository abstractions") {
					t.Fatalf("failed entity recovery: %+v %v", out, err)
				}
			} else if !errors.Is(err, ErrNoEvidence) || calls != 0 {
				t.Fatalf("unrelated summary accepted: %v calls=%d", err, calls)
			}
			if out.Stats.SearchCalls > 6 || out.Stats.PagesRead > 8 {
				t.Fatal("recovery exceeded budget")
			}
		})
	}
}

func TestNonAnswerDoesNotBecomeLongReport(t *testing.T) {
	for _, stage := range []string{"generation", "verification"} {
		t.Run(stage, func(t *testing.T) {
			providers, r := fixtures()
			m := modelFunc(func(ctx context.Context, prompt string, input, output any) error {
				if prompt == answerPrompt && stage == "generation" {
					return assign(output, `{"answers_query":false,"title":"资料不足","lead":{"text":"目录没有回答问题。","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`)
				}
				if prompt == verifyPrompt {
					return assign(output, `{"answers_query":false,"unsupported":[],"missing":["核心定义"]}`)
				}
				return fixtureModel().Complete(ctx, prompt, input, output)
			})
			e, _ := New(Config{Providers: providers, Reader: r, Model: m, VerifyWithModel: true, MaxSearchCalls: 1})
			out, err := e.Run(context.Background(), "Go context")
			if err != nil || out.AnswerMode != "brief_summary" || out.AnswerReason != "question_unanswered" || len(out.Answer.Sections) != 0 || out.Answer.Table != nil {
				t.Fatalf("non-answer published: %+v %v", out, err)
			}
		})
	}
}

package research

import (
	"context"
	"errors"
	"strings"
	"testing"

	"searchan/search"
)

func TestUnavailableSearchIsNotReportedAsNoResults(t *testing.T) {
	for _, tt := range []struct {
		name     string
		failures []error
		want     error
	}{
		{"challenge", []error{search.ErrBlocked}, ErrSearchUnavailable},
		{"rate-limited", []error{&search.HTTPError{Provider: "one", StatusCode: 429}}, ErrSearchUnavailable},
		{"timeout", []error{context.DeadlineExceeded}, ErrSearchUnavailable},
		{"empty-success", []error{nil}, ErrNoEvidence},
		{"partial-empty-success", []error{search.ErrBlocked, nil}, ErrNoEvidence},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ps []search.Provider
			for i, failure := range tt.failures {
				ps = append(ps, provider{string(rune('a' + i)), func(context.Context, search.Request) (search.Response, error) { return search.Response{}, failure }})
			}
			_, r := fixtures()
			e, err := New(Config{Providers: ps, Reader: r, Model: fixtureModel(), MaxSearchCalls: 2})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Run(context.Background(), "Go context")
			if !errors.Is(err, tt.want) || out.Answer.Title != "" || len(out.Sources) != 0 {
				t.Fatalf("incorrect failure classification: %+v %v", out, err)
			}
			if strings.Contains(strings.Join(out.Warnings, " "), "使用搜索摘要生成") {
				t.Fatal("claimed a summary without evidence")
			}
			if tt.want == ErrSearchUnavailable && strings.Contains(FailureMessage(err), "调整关键词") {
				t.Fatal("provider outage blamed on question")
			}
		})
	}
}

func TestBlockedProviderCanFailOverWithinOriginalBudget(t *testing.T) {
	ps, r := fixtures()
	ps = append([]search.Provider{provider{"blocked", func(context.Context, search.Request) (search.Response, error) {
		return search.Response{}, search.ErrBlocked
	}}}, ps...)
	e, _ := New(Config{Providers: ps, Reader: r, Model: fixtureModel(), MaxSearchCalls: 2})
	out, err := e.Run(context.Background(), "Go context")
	if err != nil || out.AnswerMode != "summary" || out.Stats.SearchCalls != 2 || out.Stats.RecoveryRounds != 1 {
		t.Fatalf("fallback lost or exceeded budget: %+v %v", out, err)
	}
}

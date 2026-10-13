package research

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"searchan/search"
)

func TestConsecutiveProviderOutagesPauseAcrossRecovery(t *testing.T) {
	for _, outage := range []error{context.DeadlineExceeded, &net.DNSError{IsTimeout: true}, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("unreachable")}, &search.HTTPError{StatusCode: 503}} {
		t.Run(fmt.Sprintf("%T", outage), func(t *testing.T) {
			calls := 0
			_, reader := fixtures()
			e, err := New(Config{Providers: []search.Provider{provider{"outage", func(context.Context, search.Request) (search.Response, error) {
				calls++
				return search.Response{}, outage
			}}}, Reader: reader, Model: fixtureModel(), MaxSearchCalls: 6})
			if err != nil {
				t.Fatal(err)
			}
			var stats Stats
			r := newRetrieval(e, &stats)
			r.search(context.Background(), []string{"first"}, 1)
			if calls != 1 || r.paused[0] {
				t.Fatal("single transient failure prematurely paused provider")
			}
			r.search(context.Background(), []string{"second", "third", "fourth"}, 5)
			r.search(context.Background(), []string{"recovery"}, 4)
			if calls != 2 || stats.SearchCalls != 2 || !r.paused[0] {
				t.Fatalf("unavailable provider continued during recovery: calls=%d stats=%+v", calls, stats)
			}
			// The pause is per task, never a process-wide outage assertion.
			newRetrieval(e, &Stats{}).search(context.Background(), []string{"next task"}, 1)
			if calls != 3 {
				t.Fatal("new task inherited old provider pause")
			}
		})
	}
}

func TestProviderSuccessIncludingEmptyResultsResetsOutageStreak(t *testing.T) {
	_, reader := fixtures()
	calls := 0
	e, _ := New(Config{Providers: []search.Provider{provider{"intermittent", func(context.Context, search.Request) (search.Response, error) {
		calls++
		if calls%2 == 1 {
			return search.Response{}, context.DeadlineExceeded
		}
		return search.Response{}, nil
	}}}, Reader: reader, Model: fixtureModel()})
	stats := Stats{}
	r := newRetrieval(e, &stats)
	r.search(context.Background(), []string{"a", "b", "c", "d", "e", "f"}, 6)
	if calls != 6 || stats.SearchCalls != 6 || r.paused[0] {
		t.Fatal("successful empty queries treated as outage or streak not reset")
	}
}

func TestProviderOutageReassignsBudgetAndRetainsHealthyResults(t *testing.T) {
	good, reader := fixtures()
	badCalls := 0
	ps := append([]search.Provider{provider{"outage", func(context.Context, search.Request) (search.Response, error) {
		badCalls++
		return search.Response{}, context.DeadlineExceeded
	}}}, good...)
	e, _ := New(Config{Providers: ps, Reader: reader, Model: fixtureModel(), MaxSearchCalls: 6, SearchConcurrency: 1})
	stats := Stats{}
	r := newRetrieval(e, &stats)
	r.search(context.Background(), []string{"a", "b", "c", "d", "e", "f"}, 6)
	if badCalls != 2 || stats.SearchCalls != 6 || len(r.hits) == 0 || !r.paused[0] || r.paused[1] {
		t.Fatalf("outage lost healthy results/budget: calls=%d stats=%+v", badCalls, stats)
	}
}

func TestQueryErrorsDoNotPauseHealthyProvider(t *testing.T) {
	_, reader := fixtures()
	calls := 0
	e, _ := New(Config{Providers: []search.Provider{provider{"query-specific", func(context.Context, search.Request) (search.Response, error) {
		calls++
		return search.Response{}, &search.HTTPError{StatusCode: 400}
	}}}, Reader: reader, Model: fixtureModel()})
	r := newRetrieval(e, &Stats{})
	r.search(context.Background(), []string{"a", "b", "c"}, 3)
	if calls != 3 || r.paused[0] {
		t.Fatal("query-specific rejection disabled provider")
	}
}

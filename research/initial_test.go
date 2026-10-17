package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func initialReferenceFixture() (Plan, []hit, string) {
	plan := Plan{Queries: []string{"Atlas retry contract", "Atlas transaction retry mechanism documentation"}, Aspects: []string{"retry contract"}}
	var hits []hit
	for i := range 3 {
		hits = append(hits, hit{Result: search.Result{URL: fmt.Sprintf("https://article%d.example/atlas/retry", i), Title: "Atlas retry contract transaction mechanism", Snippet: strings.Repeat("Atlas retry contract transaction mechanism ", 4)}, fusion: .05, queryRanks: map[string]int{queryKey(plan.Queries[0]): i + 1}})
	}
	ref := "https://vendor.example/reference/atlas/retry"
	hits = append(hits, hit{Result: search.Result{URL: ref, Title: "Atlas retry contract documentation"}, fusion: .001, queryRanks: map[string]int{queryKey(plan.Queries[1]): 1}})
	return plan, hits, ref
}

func TestInitialReferenceOverlapsSlowPagesWithoutDisplacingThem(t *testing.T) {
	plan, hits, ref := initialReferenceFixture()
	ranked := rankHits(plan, hits, nil, 3)
	if len(ranked) != 3 {
		t.Fatal("fixture must have three original pages")
	}
	for _, h := range ranked {
		if h.URL == ref {
			t.Fatal("fixture reference must not already be ranked into the batch")
		}
	}
	started := make(chan string, 4)
	releaseFast, releaseAll := make(chan struct{}), make(chan struct{})
	var active, peak atomic.Int32
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, PlanWithModel: true, ReadConcurrency: 3, Reader: readFunc(func(ctx context.Context, raw string) (reader.Document, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- raw
		gate := releaseAll
		if raw == ranked[0].URL {
			gate = releaseFast
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return reader.Document{}, ctx.Err()
		}
		return reader.MakeDocument(raw, "Atlas retry contract", "Atlas retry requires restarting the complete transaction after a serialization conflict. "+raw, "fixture"), nil
	})})
	out := Output{Query: plan.Queries[0], Plan: plan}
	g := newGathering(e, &out)
	g.retrieval.hits = hits
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	done := make(chan struct{})
	go func() { g.readInitial(ctx, 6); close(done) }()
	seen := map[string]bool{}
	for range 3 {
		select {
		case raw := <-started:
			seen[raw] = true
		case <-ctx.Done():
			t.Fatal("original pages did not start together")
		}
	}
	if seen[ref] {
		t.Fatal("ahead reference displaced an original page")
	}
	close(releaseFast)
	select {
	case raw := <-started:
		if raw != ref {
			t.Fatal("unexpected fourth page", raw)
		}
	case <-ctx.Done():
		t.Fatal("reference waited for all three original pages")
	}
	close(releaseAll)
	<-done
	if active.Load() != 0 || peak.Load() > 3 || len(e.readSlots) != 0 || out.Stats.PagesRead != 4 || out.Stats.DiscoveryReads != 1 || out.Stats.ReadingBatches != 1 || out.Stats.ModelCalls != 0 || !g.referencesChecked || len(g.reading.docs) != 4 {
		t.Fatalf("parallel reference bypassed shared limits: stats=%+v peak=%d", out.Stats, peak.Load())
	}
	for i, doc := range g.reading.docs {
		want := ref
		if i < 3 {
			want = ranked[i].URL
		}
		if doc.URL != want {
			t.Fatal("completion order changed document/citation order", i, doc.URL, want)
		}
	}
}

func TestInitialReferenceLeavesRoomForFailureRecovery(t *testing.T) {
	for _, limit := range []int{4, 6} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			plan, hits, ref := initialReferenceFixture()
			const bodyRef = "https://body.example/atlas/retry"
			e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, PlanWithModel: true, ReadConcurrency: 3, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
				if raw == ref {
					return reader.Document{}, errors.New("unavailable")
				}
				d := reader.MakeDocument(raw, "Atlas retry contract", "Atlas retry requires restarting the complete transaction after a serialization conflict.", "fixture")
				// Distinct bodies prevent hash deduplication hiding source links.
				d.Text += " " + raw
				d.Hash = ""
				if raw != bodyRef {
					d.Links = []reader.Link{{URL: bodyRef, Text: "Atlas retry reference"}}
				}
				return d, nil
			})})
			out := Output{Query: plan.Queries[0], Plan: plan}
			g := newGathering(e, &out)
			g.retrieval.hits = hits
			g.readInitial(context.Background(), limit)
			if g.referencesChecked {
				t.Fatal("failed or unscheduled ahead read blocked body reference")
			}
			g.readRelevantReference(context.Background(), limit)
			want := 4
			if limit == 6 {
				want = 5
			}
			if out.Stats.PagesRead != want || !g.reading.attempted[bodyRef] || out.Stats.PagesRead > limit {
				t.Fatalf("failed reference exhausted recovery budget: %+v", out.Stats)
			}
			if limit == 4 && g.reading.attempted[ref] {
				t.Fatal("ahead read started without reserved recovery space")
			}
		})
	}
}

func TestInitialReferenceRequiresPlanExpansionAndParallelCapacity(t *testing.T) {
	for _, mode := range []string{"unplanned", "canonical_only", "serial", "scheduled_reference", "intro", "compare", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			plan, hits, ref := initialReferenceFixture()
			parallel := 3
			if mode == "serial" {
				parallel = 1
			}
			if mode == "canonical_only" {
				plan.Queries = []string{plan.Queries[0], spacedQuery(plan.Queries[0])}
			}
			if mode == "scheduled_reference" {
				hits[0].URL = "https://article0.example/reference/atlas/retry"
			}
			if mode == "intro" {
				plan.Queries[0] += " 简介"
			}
			if mode == "compare" {
				plan.Intent = "compare"
			}
			var unexpected atomic.Bool
			e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, PlanWithModel: mode != "unplanned", ReadConcurrency: parallel, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
				if raw == ref {
					unexpected.Store(true)
				}
				return reader.MakeDocument(raw, "Atlas retry contract", "Atlas retry requires restarting the complete transaction after a serialization conflict.", "fixture"), nil
			})})
			out := Output{Query: plan.Queries[0], Plan: plan}
			g := newGathering(e, &out)
			g.retrieval.hits = hits
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			if mode == "cancelled" {
				stop()
			}
			g.readInitial(ctx, 6)
			if unexpected.Load() || out.Stats.DiscoveryReads != 0 || out.Stats.PagesRead > 3 || len(e.readSlots) != 0 {
				t.Fatalf("ineligible initial reference ran: mode=%s stats=%+v", mode, out.Stats)
			}
		})
	}
}

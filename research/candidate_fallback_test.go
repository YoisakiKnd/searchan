package research

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"searchan/reader"
	"searchan/search"
)

func TestFailedCoverageReferenceFindsExistingAlternativeWithoutGap(t *testing.T) {
	const query = "Atlas retry contract"
	const failed = "https://mirror.example/reference/atlas/retry"
	const alternative = "https://vendor.example/reference/atlas/retry"
	p := provider{"fixture", func(context.Context, search.Request) (search.Response, error) {
		var results []search.Result
		for i := range 3 {
			results = append(results, search.Result{URL: fmt.Sprintf("https://tutorial%d.example/atlas/retry", i), Title: query, Snippet: strings.Repeat(query+" ", 5), Content: fmt.Sprintf("Atlas retry contract is introduced in this tutorial for transactions and conflict recovery. Example %d.", i)})
		}
		results = append(results, search.Result{URL: failed, Title: "Atlas retry reference"}, search.Result{URL: alternative, Title: "Atlas retry reference"})
		return search.Response{Results: results}, nil
	}}
	m := modelFunc(func(_ context.Context, prompt string, input, output any) error {
		if prompt != coveragePrompt {
			t.Fatal("unexpected model call")
		}
		for _, c := range input.(map[string]any)["candidates"].([]coverageCandidate) {
			if c.URL == failed {
				return assign(output, fmt.Sprintf(`{"answers_query":true,"missing":[],"queries":[],"read_ids":[%q]}`, c.ID))
			}
		}
		t.Fatal("reference candidate missing")
		return nil
	})
	var reads []string
	e, _ := New(Config{Providers: []search.Provider{p}, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
		reads = append(reads, raw)
		if raw == failed {
			return reader.Document{}, reader.ContentError("empty_text")
		}
		if raw != alternative {
			t.Error("invented or unrelated alternative", raw)
		}
		return reader.MakeDocument(raw, "Atlas retry reference", "Atlas retry contract requires restarting the entire transaction after a serialization conflict.", "fixture"), nil
	}), Model: m, PlanWithModel: true})
	out := Output{Query: query, Plan: Plan{Queries: []string{query}, Aspects: []string{"retry contract"}}}
	g := newGathering(e, &out)
	g.gather(context.Background())
	found := false
	for _, ev := range out.Evidence {
		found = found || ev.URL == alternative
	}
	if !found || len(reads) != 2 || reads[0] != failed || reads[1] != alternative || out.Stats.CandidateReads != 2 || out.Stats.PagesRead != 5 || out.Stats.SearchCalls != 1 || out.Stats.ModelCalls != 1 || out.Stats.RecoveryRounds != 0 {
		t.Fatalf("failed reference was silently abandoned: reads=%v stats=%+v", reads, out.Stats)
	}
}

func TestCoverageReferenceFallbackRemainsBoundedAndTopical(t *testing.T) {
	for _, mode := range []string{"success", "duplicate", "no_reference", "wrong_entity", "no_topic", "unsafe", "one_page", "two_selected", "alternative_failed", "cancelled", "unknown_id"} {
		t.Run(mode, func(t *testing.T) {
			const selectedURL = "https://mirror.example/reference/atlas/retry"
			const altURL = "https://vendor.example/reference/atlas/retry"
			urls := []string{selectedURL, altURL, "https://another.example/reference/atlas/retry"}
			titles := []string{"Atlas retry reference", "Atlas retry reference", "Atlas retry reference"}
			if mode == "no_reference" {
				urls[0], titles[0] = "https://tutorial.example/atlas/retry", "Atlas retry article"
			}
			if mode == "wrong_entity" {
				urls[1], titles[1] = "https://vendor.example/reference/other/retry", "Other retry reference"
			}
			if mode == "no_topic" {
				urls[1], titles[1] = "https://vendor.example/reference/atlas", "Atlas reference"
			}
			if mode == "unsafe" {
				urls[1] = "http://127.0.0.1/reference/atlas/retry"
			}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var reads atomic.Int32
			doc := reader.MakeDocument(selectedURL, "Atlas retry reference", "Atlas retry contract requires restarting the entire transaction after a serialization conflict.", "fixture")
			e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
				reads.Add(1)
				if mode == "success" || mode == "duplicate" {
					return doc, nil
				}
				if mode == "cancelled" {
					stop()
				}
				return reader.Document{}, reader.ContentError("empty_text")
			})})
			out := Output{Plan: Plan{Queries: []string{"Atlas retry contract"}}}
			g := newGathering(e, &out)
			var pool []coverageCandidate
			count := 2
			if mode == "two_selected" || mode == "alternative_failed" {
				count = 3
			}
			for i := range count {
				pool = append(pool, coverageCandidate{ID: fmt.Sprintf("c%d", i+1), URL: urls[i], Title: titles[i], hit: hit{Result: search.Result{URL: urls[i], Title: titles[i]}, pageKey: urls[i]}})
				out.Stats.CoverageCandidates = append(out.Stats.CoverageCandidates, CoverageCandidate{ID: pool[i].ID, URL: urls[i]})
			}
			if mode == "duplicate" {
				g.reading.docs = append(g.reading.docs, doc)
				g.reading.hashes[doc.Hash] = true
			}
			limit := 6
			if mode == "one_page" {
				limit = 1
			}
			ids := []string{"c1"}
			if mode == "two_selected" {
				ids = append(ids, "c2")
			}
			if mode == "unknown_id" {
				ids = []string{"invented"}
			}
			g.readCoverageCandidates(ctx, pool, ids, limit)
			wantReads := 1
			if mode == "alternative_failed" || mode == "two_selected" {
				wantReads = 2
			}
			if mode == "unknown_id" {
				wantReads = 0
			}
			if reads.Load() != int32(wantReads) || out.Stats.PagesRead != wantReads || out.Stats.CandidateReads != wantReads || out.Stats.ModelCalls != 0 || out.Stats.SearchCalls != 0 || len(e.readSlots) != 0 {
				t.Fatalf("fallback exceeded eligibility or budget: reads=%d stats=%+v", reads.Load(), out.Stats)
			}
			for i, c := range out.Stats.CoverageCandidates {
				if c.Fallback != (mode == "alternative_failed" && i == 1) {
					t.Fatal("fallback provenance incorrect", c)
				}
			}
		})
	}
}

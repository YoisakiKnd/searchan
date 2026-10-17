package research

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"searchan/reader"
	"searchan/search"
)

func TestReleaseDateReferenceUsesOriginalQuestionAndExistingLeaders(t *testing.T) {
	plan := Plan{Queries: []string{"Atlas 最新稳定版本是多少？什么时候发布的？", "Atlas latest stable release date"}, RequiresFresh: true}
	const raw = "https://vendor.example/history"
	h := hit{Result: search.Result{URL: raw, Title: "Release History - Atlas"}, queryRanks: map[string]int{queryKey(plan.Queries[1]): 1}}
	if got := searchReference(plan, []hit{h}, nil, nil); got.URL != raw {
		t.Fatal("existing release history was ignored", got)
	}
	for _, mode := range []string{"unasked", "planner_only", "wrong_subject", "unnamed", "late_rank", "attempted", "already_read", "comparison", "unsafe", "subject_in_url_only"} {
		t.Run(mode, func(t *testing.T) {
			p, candidate := plan, h
			var docs []reader.Document
			attempted := map[string]bool{}
			switch mode {
			case "unasked":
				p.Queries = []string{"Atlas cancellation guarantee", plan.Queries[1]}
			case "planner_only":
				p.Queries = []string{"JWT 默认加密吗？", "Keycloak release date"}
				candidate.Title = "Keycloak Release History"
				candidate.queryRanks = map[string]int{queryKey(p.Queries[1]): 1}
			case "wrong_subject":
				candidate.Title = "Other Release History"
			case "unnamed":
				p.Queries = []string{"What is the latest stable version and release date?"}
				candidate.queryRanks = map[string]int{queryKey(p.Queries[0]): 1}
			case "late_rank":
				candidate.queryRanks = map[string]int{queryKey(p.Queries[1]): 3}
				// Populate the actual first two positions too.
			case "attempted":
				attempted[raw] = true
			case "already_read":
				docs = []reader.Document{{URL: raw, Title: candidate.Title}}
			case "comparison":
				p.Intent = "compare"
			case "unsafe":
				candidate.URL = "http://127.0.0.1/history"
			case "subject_in_url_only":
				candidate.URL, candidate.Title = "https://atlas.example/history", "Release History"
			}
			hits := []hit{candidate}
			if mode == "late_rank" {
				for i := 1; i <= 2; i++ {
					hits = append(hits, hit{Result: search.Result{URL: "https://tutorial.example/atlas"}, queryRanks: map[string]int{queryKey(p.Queries[1]): i}})
				}
			}
			if got := searchReference(p, hits, docs, attempted); got.URL != "" {
				t.Fatal("unrelated or unasked history consumed discovery slot", mode, got)
			}
		})
	}
}

func TestReleaseDateHintsDoNotMatchGenericHistoryOrImplementationQuestions(t *testing.T) {
	for _, q := range []string{"Atlas 发布日期", "Atlas 什么时候发布的？", "When was Atlas released?", "Atlas latest stable release date"} {
		if !releaseDateQuery(q) {
			t.Fatal("explicit date request lost", q)
		}
	}
	for _, q := range []string{"Atlas cancellation guarantee", "Go 何时释放内存？", "Atlas 最新版本是什么？", "JWT 默认加密吗？"} {
		if releaseDateQuery(q) {
			t.Fatal("unasked release-date retrieval", q)
		}
	}
	for _, title := range []string{"Atlas Release History", "Atlas version history", "Atlas Changelog", "Atlas 发布历史", "Atlas Release Notes"} {
		if releasePageKind(title) == 0 {
			t.Fatal("publication reference label lost", title)
		}
	}
	for _, title := range []string{"Atlas Release Dashboard", "Atlas release historical fiction", "Changelogsale", "All releases", "Atlas VERSION time"} {
		if releasePageKind(title) != 0 {
			t.Fatal("ambiguous metadata classified as publication reference", title)
		}
	}
}

func TestFreshReleaseEvidenceRetainsExplicitRecentRows(t *testing.T) {
	plan := Plan{Queries: []string{"Atlas 当前最新稳定版本是多少？发布日期？", "Atlas latest stable release date"}, RequiresFresh: true}
	text := "Release History\n\n" + strings.Repeat("Atlas latest stable version and release date documentation. ", 35)
	headings := []reader.Heading{{Start: 0, End: len("Release History"), Level: 1}}
	for _, section := range []struct{ title, body string }{
		{"Old stable version", "atlas1.0 (released 2024-01-02) " + strings.Repeat("Atlas latest stable version release date. ", 40)},
		{"Release policy", strings.Repeat("Atlas latest stable release version policy. ", 40)},
		{"Minor revisions", "atlas3.2 (released 2026-10-08) fixes compiler defects and network handling. This is a maintenance release."},
	} {
		start := len(text) + 2
		text += "\n\n" + section.title + "\n\n" + section.body
		headings = append(headings, reader.Heading{Start: start, End: start + len(section.title), Level: 2})
	}
	doc := reader.MakeDocument("https://vendor.example/history", "Atlas Release History", text, "fixture")
	doc.Headings = headings
	doc.FetchedAt = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	found := false
	for _, ev := range SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 3) {
		found = found || strings.Contains(ev.Text, "atlas3.2 (released 2026-10-08)")
		if ev.DocumentHash != doc.Hash || !strings.Contains(doc.Text, ev.Text) {
			t.Fatal("ranking manufactured release facts or changed provenance", ev)
		}
	}
	if !found {
		t.Fatal("newly read history still omitted the recent explicit release row")
	}
}

func TestExplicitReleaseDatesDoNotPromoteBuildOrUpdateTimestamps(t *testing.T) {
	fetched := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		text string
		want int
	}{
		{"atlas3.2 (released 2026-10-08)", 20261008},
		{"Release date: 2026-10-17", 20261017},
		{"发布日期：2026-10-01", 20261001},
		{"atlas3.2 (released 2026-02-29)", 0},
		{"atlas3.2 (unreleased 2026-10-08)", 0},
		{"atlas3.2 (released 2027-01-01)", 0},
		{"time 2026-10-02T20:28:03Z", 0},
		{"Build date: 2026-10-15", 0},
		{"Updated: 2026-10-16", 0},
		{"Published: 2026-10-16", 0},
	} {
		if got := explicitReleaseDay(tc.text, fetched); got != tc.want {
			t.Errorf("%q: got %d want %d", tc.text, got, tc.want)
		}
	}
}

func TestInitialReleaseReadPrefersHistoryOverExpansionSpecificNotes(t *testing.T) {
	const history = "https://vendor.example/history"
	const notes = "https://vendor.example/old-notes"
	plan := Plan{Queries: []string{"Atlas 当前最新稳定版本是多少？什么时候发布？", "Atlas release date documentation"}, RequiresFresh: true}
	var hits []hit
	for i := range 3 {
		hits = append(hits, hit{Result: search.Result{URL: fmt.Sprintf("https://article%d.example/atlas", i), Title: "Atlas 当前最新稳定版本 发布日期 发布时间", Snippet: strings.Repeat("Atlas 当前最新稳定版本 发布日期 发布时间 ", 20)}, fusion: .05, queryRanks: map[string]int{queryKey(plan.Queries[0]): i + 3}})
	}
	hits = append(hits,
		hit{Result: search.Result{URL: history, Title: "Atlas Release History"}, fusion: .001, queryRanks: map[string]int{queryKey(plan.Queries[0]): 1}},
		hit{Result: search.Result{URL: notes, Title: "Atlas 1.0 Release Notes"}, fusion: .001, queryRanks: map[string]int{queryKey(plan.Queries[1]): 1}})
	for _, h := range rankHits(plan, hits, nil, 3) {
		if h.URL == history || h.URL == notes {
			t.Fatal("fixture must leave publication references outside original three")
		}
	}
	reads := make(chan string, 5)
	e, _ := New(Config{Providers: []search.Provider{provider{"fixture", nil}}, PlanWithModel: true, ReadConcurrency: 3, Reader: readFunc(func(_ context.Context, raw string) (reader.Document, error) {
		reads <- raw
		return reader.MakeDocument(raw, "Atlas Release History", "Atlas release notes describe maintenance updates and explicitly label release dates. "+raw, "fixture"), nil
	})})
	out := Output{Query: plan.Queries[0], Plan: plan}
	g := newGathering(e, &out)
	g.retrieval.hits = hits
	g.readInitial(context.Background(), 6)
	close(reads)
	seen := map[string]bool{}
	for raw := range reads {
		seen[raw] = true
	}
	if !seen[history] || seen[notes] || out.Stats.PagesRead != 4 || out.Stats.DiscoveryReads != 1 || out.Stats.ModelCalls != 0 {
		t.Fatalf("specific expansion notes displaced the existing release history: reads=%v stats=%+v", seen, out.Stats)
	}
}

func TestReleaseDateRankingKeepsNumericRowsAndExplicitVersionScope(t *testing.T) {
	text := "Release policy\n\n" + strings.Repeat("Compatibility and maintenance rules are described here. ", 40)
	oldStart := len(text) + 2
	text += "\n\nOld branch\n\natlas1.0 (released 2024-01-02) " + strings.Repeat("Maintenance information for release branch 1.0. ", 40)
	newStart := len(text) + 2
	text += "\n\nNew branch\n\natlas3.2 (released 2026-10-08) has maintenance fixes."
	doc := reader.MakeDocument("https://vendor.example/history", "Atlas Release History", text, "fixture")
	doc.Headings = []reader.Heading{{Start: 0, End: len("Release policy"), Level: 2}, {Start: oldStart, End: oldStart + len("Old branch"), Level: 2}, {Start: newStart, End: newStart + len("New branch"), Level: 2}}
	doc.FetchedAt = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ query, want string }{
		{"Atlas 当前最新稳定版本发布日期？", "atlas3.2 (released 2026-10-08)"},
		{"Atlas 1.0 当前最新版本发布日期？", "atlas1.0 (released 2024-01-02)"},
	} {
		plan := Plan{Queries: []string{tc.query}, RequiresFresh: true}
		evs := SelectEvidenceForPlan(plan, []reader.Document{doc}, nil, 1)
		if len(evs) != 1 || !strings.Contains(evs[0].Text, tc.want) {
			t.Fatal("release ranking lost sparse rows or crossed an explicit version scope", tc.query, evs)
		}
	}
}

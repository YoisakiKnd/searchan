package research

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
	"searchan/search"
)

type coverageCandidate struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Origin  string `json:"origin"`
	// A fetched page explicitly links to this candidate. This describes the
	// discovery path only; neither anchor text nor the referring page proves it.
	ReferencedBy   string `json:"referenced_by,omitempty"`
	ReferenceLabel string `json:"reference_label,omitempty"`
	hit            hit
}

// Present existing leads, never generated URLs. Reserve a few slots for
// explicit reference links in already-read documents; they remain untrusted
// leads until fetched. This pool is built once, so linked pages cannot recurse.
func (g *gathering) coverageCandidates() []coverageCandidate {
	pool := make([]coverageCandidate, 0, 20)
	seen := map[string]bool{}
	add := func(h hit, origin string) int {
		key := search.DocumentURL(h.URL)
		if key == "" || len(key) > 2048 || g.reading.attempted[key] || reader.ValidateURL(key) != nil {
			return -1
		}
		if seen[key] {
			for i := range pool {
				if pool[i].URL == key {
					return i
				}
			}
		}
		if len(pool) >= 20 {
			return -1
		}
		seen[key] = true
		h.URL, h.pageKey = key, key
		pool = append(pool, coverageCandidate{ID: fmt.Sprintf("c%d", len(pool)+1), URL: key, Title: reader.Clip(h.Title, 200), Snippet: reader.Clip(h.Snippet, 300), Origin: origin, hit: h})
		return len(pool) - 1
	}
	// Preserve discovery paths too: a generic API reference can rank below
	// keyword-dense tutorials after fusion. The top two actual results of each
	// original query get a chance to be assessed, without declaring them trusted.
	// Add them before the lexical fill so all six can fit while retaining four
	// reference slots. Adding them after twelve ranked hits can consume those
	// slots before document links are considered.
	for _, q := range g.out.Plan.Queries[:min(3, len(g.out.Plan.Queries))] {
		for _, h := range queryLeaders(g.retrieval.hits, queryKey(q)) {
			add(h, "search")
		}
	}
	ranked := rankHits(g.out.Plan, g.retrieval.hits, g.reading.attempted, 20)
	for _, h := range ranked {
		add(h, "search")
		if len(pool) >= 12 {
			break
		}
	}
	type references struct {
		from  string
		links []reader.Link
	}
	linkSets := make([]references, 0, len(g.reading.docs)+1)
	for _, doc := range g.reading.docs {
		linkSets = append(linkSets, references{from: doc.URL, links: doc.Links[:min(32, len(doc.Links))]})
	}
	// A successfully read page can provide the actual reference path. Hints
	// retained from rejected/short pages must not mask it through deduplication.
	linkSets = append(linkSets, references{links: g.reading.hints})
	linked := 0
	referencesSeen := map[string]bool{}
links:
	for _, refs := range linkSets {
		for _, link := range refs.links {
			if referenceLabel(link.Text) {
				i := add(hit{Result: search.Result{URL: link.URL, Title: link.Text}}, "document_link")
				if i < 0 || referencesSeen[pool[i].URL] {
					continue
				}
				referencesSeen[pool[i].URL] = true
				linked++
				if refs.from != "" && len(refs.from) <= 2048 && reader.ValidateURL(refs.from) == nil {
					pool[i].ReferencedBy = refs.from
					pool[i].ReferenceLabel = reader.Clip(link.Text, 160)
				}
				if linked == 4 {
					break links
				}
			}
		}
	}
	for _, h := range ranked {
		add(h, "search")
	}
	return pool
}

func queryLeaders(hits []hit, query string) []hit {
	var top []hit
	for _, h := range hits {
		rank := h.queryRanks[query]
		if rank == 0 {
			continue
		}
		i := 0
		for i < len(top) && top[i].queryRanks[query] <= rank {
			i++
		}
		if i >= 2 {
			continue
		}
		top = append(top, h)
		copy(top[i+1:], top[i:])
		top[i] = h
		top = top[:min(2, len(top))]
	}
	return top
}

func referenceLabel(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	// Numbered RFC anchors often contain no "reference" or "specification"
	// word. Recognize the visible identifier as a lead, without guessing a URL
	// or asserting anything about the publisher or the document's authority.
	if strings.HasPrefix(label, "rfc") {
		number := strings.TrimSpace(strings.TrimPrefix(label[3:], "-"))
		digits := 0
		for digits < len(number) && digits < 7 && number[digits] >= '0' && number[digits] <= '9' {
			digits++
		}
		if digits > 0 && digits <= 6 {
			if digits == len(number) {
				return true
			}
			// A visible identifier may be followed by its title. Require a
			// separator, so RFC7519x and RFC7519.2 are not numbered RFC labels.
			next, _ := utf8.DecodeRuneInString(number[digits:])
			if unicode.IsSpace(next) || strings.ContainsRune(":：-–—", next) {
				return true
			}
		}
	}
	for _, marker := range []string{"官方文档", "原始论文", "参考文档", "原文", "规范"} {
		if strings.Contains(label, marker) {
			return true
		}
	}
	for _, word := range strings.FieldsFunc(label, func(r rune) bool { return r < 'a' || r > 'z' }) {
		switch word {
		case "documentation", "docs", "reference", "specification", "paper", "guidance", "keywords":
			return true
		}
	}
	return false
}

// The model may choose IDs only. Unknown IDs, duplicates, attempted documents
// and excess selections cannot introduce requests or bypass the shared budget.
func (g *gathering) readCoverageCandidates(ctx context.Context, pool []coverageCandidate, ids []string, pageLimit int) bool {
	budget := min(2, pageLimit-g.out.Stats.PagesRead)
	if budget <= 0 || ctx.Err() != nil {
		return false
	}
	var selected []hit
	seen := map[string]bool{}
	for _, id := range ids[:min(8, len(ids))] {
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, c := range pool {
			if c.ID == id && !g.reading.attempted[c.hit.pageKey] {
				selected = append(selected, c.hit)
				for i := range g.out.Stats.CoverageCandidates {
					if g.out.Stats.CoverageCandidates[i].ID == id {
						g.out.Stats.CoverageCandidates[i].Selected = true
						break
					}
				}
				break
			}
		}
		if len(selected) == budget {
			break
		}
	}
	if len(selected) == 0 {
		return false
	}
	before := g.out.Stats.PagesRead
	beforeDocs := len(g.reading.docs)
	beforeTraces := len(g.out.Stats.PageReads)
	g.out.Warnings = append(g.out.Warnings, g.reading.read(ctx, g.out.Plan, selected, budget)...)
	// A coverage model may select a reference while considering the tutorials
	// sufficient. A failed read must not silently discard that discovery path.
	// Use at most one existing alternative, inside the same two-page allowance;
	// do not search, call a model again, or follow links from the new body.
	if len(selected) < budget && ctx.Err() == nil {
		if alternative := g.failedReferenceAlternative(pool, g.out.Stats.PageReads[beforeTraces:]); alternative.URL != "" {
			for i := range g.out.Stats.CoverageCandidates {
				if g.out.Stats.CoverageCandidates[i].URL == alternative.URL {
					g.out.Stats.CoverageCandidates[i].Fallback = true
					break
				}
			}
			g.out.Warnings = append(g.out.Warnings, g.reading.read(ctx, g.out.Plan, []hit{alternative}, 1)...)
		}
	}
	g.out.Stats.CandidateReads += g.out.Stats.PagesRead - before
	g.out.Evidence = SelectEvidenceForPlan(g.out.Plan, g.reading.docs, g.reading.published, g.engine.cfg.MaxEvidence)
	for _, doc := range g.reading.docs[beforeDocs:] {
		for _, ev := range g.out.Evidence {
			if ev.URL == doc.URL {
				return true
			}
		}
	}
	return false
}

func (g *gathering) failedReferenceAlternative(pool []coverageCandidate, traces []PageRead) hit {
	anyFailure := false
	for _, trace := range traces {
		anyFailure = anyFailure || trace.State == "failed" || trace.State == "rejected"
	}
	if !anyFailure {
		return hit{}
	}
	scorer := newReferenceMatcher(g.out.Plan)
	failed := false
	for _, trace := range traces {
		for _, c := range pool {
			if c.URL != trace.URL || scorer.score(c.Title, c.URL) == 0 {
				continue
			}
			if trace.State == "accepted" || trace.State == "duplicate" {
				return hit{}
			}
			failed = failed || trace.State == "failed" || trace.State == "rejected"
		}
	}
	if !failed {
		return hit{}
	}
	var best hit
	bestScore := 0.0
	for _, c := range pool {
		key := search.DocumentURL(c.URL)
		if key == "" || len(key) > 2048 || g.reading.attempted[key] || reader.ValidateURL(key) != nil {
			continue
		}
		if weight := scorer.score(c.Title, key); weight > bestScore {
			bestScore, best = weight, c.hit
			best.URL, best.pageKey = key, key
		}
	}
	return best
}

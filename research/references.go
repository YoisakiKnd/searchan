package research

import (
	"context"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
	"searchan/search"
)

// Read one explicit, query-related reference before asking a model whether
// existing tutorials are sufficient. A model can otherwise skip the very page
// needed to check them. This is discovery, not a declaration of source trust.
// Prefer links in already-read bodies. Otherwise consider only the first two
// actual search results per query that look like a topical reference. Neither
// path labels nor search rank authenticate a publisher; no second hop is taken.
func (g *gathering) readRelevantReference(ctx context.Context, pageLimit int) {
	if g.referencesChecked {
		return
	}
	g.referencesChecked = true
	if ctx.Err() != nil || g.out.Stats.PagesRead >= pageLimit || len(g.out.Plan.Queries) == 0 {
		return
	}
	var l *lexical
	var scratch []uint16
	seen := map[string]bool{}
	var best hit
	bestScore := 0.0
	entities := referenceEntities(g.out.Plan)
	for _, doc := range g.reading.docs {
		for _, link := range doc.Links[:min(32, len(doc.Links))] {
			label := reader.Clip(link.Text, 160)
			if !referenceLabel(label) {
				continue
			}
			key := search.DocumentURL(link.URL)
			if key == "" || len(key) > 2048 || seen[key] || g.reading.attempted[key] || reader.ValidateURL(key) != nil {
				continue
			}
			seen[key] = true
			if !matchesEntities(label+" "+key, entities) {
				continue
			}
			if l == nil {
				l = newLexical(strings.Join(g.out.Plan.Queries[:min(3, len(g.out.Plan.Queries))], " "), g.out.Plan.Aspects, nil)
				scratch = make([]uint16, len(l.terms))
			}
			// Lowercase the link before tokenization so CamelCase aliases cannot
			// make a single entity name count as several independent matches.
			address := strings.TrimPrefix(strings.TrimPrefix(key, "https://"), "http://")
			v := l.vector(strings.ToLower(label+" "+address), scratch)
			matched, score := 0, 0.0
			for _, f := range v.matches {
				term := l.terms[f.term]
				if substantiveReferenceTerm(term.text) {
					matched++
					score += term.weight
				}
			}
			if matched >= 2 && score > bestScore {
				bestScore = score
				best = hit{Result: search.Result{URL: key, Title: label}, pageKey: key}
			}
		}
	}
	if best.URL == "" {
		best = searchReference(g.out.Plan, g.retrieval.hits, g.reading.docs, g.reading.attempted)
		if best.URL == "" {
			return
		}
	}
	before := g.out.Stats.PagesRead
	g.out.Warnings = append(g.out.Warnings, g.reading.read(ctx, g.out.Plan, []hit{best}, 1)...)
	g.out.Stats.DiscoveryReads += g.out.Stats.PagesRead - before
	g.out.Evidence = SelectEvidenceForPlan(g.out.Plan, g.reading.docs, g.reading.published, g.engine.cfg.MaxEvidence)
}

// Shared metadata fit for discovery only; it never authenticates a publisher.
// Keep lazy tokenization state on the caller's stack, rather than capturing it
// in a returned closure on every normal reference check.
type referenceMatcher struct {
	plan        Plan
	entities    []string
	entityTerms map[string]bool
	lexical     *lexical
	scratch     []uint16
}

func newReferenceMatcher(plan Plan) referenceMatcher {
	return referenceMatcher{plan: plan, entities: referenceEntities(plan)}
}

func (s *referenceMatcher) score(title, raw string) float64 {
	if len(s.plan.Queries) == 0 || !referencePage(title, raw) || !matchesEntities(title+" "+raw, s.entities) {
		return 0
	}
	if s.lexical == nil {
		s.entityTerms = make(map[string]bool)
		for _, entity := range s.entities {
			scanTerms(spacedQuery(entity), func(term string) { s.entityTerms[term] = true })
		}
		s.lexical = newLexical(strings.Join(s.plan.Queries[:min(3, len(s.plan.Queries))], " "), s.plan.Aspects, nil)
		s.scratch = make([]uint16, len(s.lexical.terms))
	}
	address := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	v := s.lexical.vector(strings.ToLower(title+" "+address), s.scratch)
	matched, topic, weight := 0, false, 0.0
	for _, f := range v.matches {
		term := s.lexical.terms[f.term]
		if substantiveReferenceTerm(term.text) {
			matched++
			weight += term.weight
			topic = topic || !s.entityTerms[term.text]
		}
	}
	if matched < 2 || !topic {
		return 0
	}
	return weight
}

// searchReference fills the same single discovery-read slot, not a separate
// stage. It cannot introduce a URL absent from the actual provider results.
func searchReference(plan Plan, hits []hit, docs []reader.Document, attempted map[string]bool) hit {
	if len(plan.Queries) == 0 {
		return hit{}
	}
	scorer := newReferenceMatcher(plan)
	// Avoid another search-prefetch when a matching reference is already read.
	// Explicit body links above can still refine a reference within the one slot.
	for _, doc := range docs {
		if scorer.score(reader.Clip(doc.Title, 200), doc.URL) > 0 {
			return hit{}
		}
	}
	seen := map[string]bool{}
	var best hit
	bestScore := 0.0
	for _, q := range plan.Queries[:min(3, len(plan.Queries))] {
		for _, h := range queryLeaders(hits, queryKey(q)) {
			key := search.DocumentURL(h.URL)
			if key == "" || len(key) > 2048 || seen[key] || attempted[key] || reader.ValidateURL(key) != nil {
				continue
			}
			seen[key] = true
			if weight := scorer.score(reader.Clip(h.Title, 200), key); weight > bestScore {
				bestScore, best = weight, h
				best.URL, best.pageKey = key, key
			}
		}
	}
	return best
}

func referencePage(title, raw string) bool {
	if referenceLabel(title) {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	// Inspect path components, never a hostname containing "docs". A reference
	// path is a discovery clue only; its body still needs reading and selection.
	for _, component := range strings.Split(strings.ToLower(u.Path), "/") {
		switch component {
		case "reference", "api", "docs", "documentation", "manual", "specification", "spec", "rfc":
			return true
		}
	}
	return false
}

// "Child Module in Parent Project" names a specific module and its surrounding
// ecosystem. A separate parent mention must not admit a sibling module as the
// one automatic reference read. Keep comparison subjects and CamelCase aliases.
func referenceEntities(plan Plan) []string {
	entities := relevanceEntities(plan)
	if len(entities) < 2 || plan.Intent == "compare" || len(plan.Queries) == 0 {
		return entities
	}
	query := strings.ToLower(spacedQuery(reader.Clip(plan.Queries[0], 1024)))
	var focused []string
	for _, parent := range entities {
		keep := true
		for _, child := range entities {
			if child != parent && mentionsEntity(child, parent) {
				withoutChild := strings.ReplaceAll(query, strings.ToLower(spacedQuery(child)), " ")
				if mentionsEntity(withoutChild, parent) {
					keep = false
					break
				}
			}
		}
		if keep {
			focused = append(focused, parent)
		}
	}
	return focused
}

func substantiveReferenceTerm(term string) bool {
	if utf8.RuneCountInString(term) < 2 || len(term) < 4 && term[0] < utf8.RuneSelf || strings.ContainsAny(term, "._-") {
		return false
	}
	switch term {
	case "documentation", "docs", "reference", "specification", "official", "website", "guidance", "source", "paper", "overview", "tutorial", "project", "article", "guide", "manual", "chapter", "https", "www", "com", "org", "html", "latest", "current", "文档", "官方", "原文", "指南", "参考":
		return false
	}
	for _, r := range term {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

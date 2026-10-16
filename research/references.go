package research

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
	"searchan/search"
)

// Read one explicit, query-related reference before asking a model whether
// existing tutorials are sufficient. A model can otherwise skip the very page
// needed to check them. This is discovery, not a declaration of source trust.
// Only links in already-read bodies are eligible, and no second hop is taken.
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
		return
	}
	before := g.out.Stats.PagesRead
	g.out.Warnings = append(g.out.Warnings, g.reading.read(ctx, g.out.Plan, []hit{best}, 1)...)
	g.out.Stats.DiscoveryReads += g.out.Stats.PagesRead - before
	g.out.Evidence = SelectEvidenceForPlan(g.out.Plan, g.reading.docs, g.reading.published, g.engine.cfg.MaxEvidence)
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

package research

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
	"searchan/search"
)

type passage struct {
	text        string
	first, last int
	heading     string
}

// Merge short adjacent paragraphs instead of dropping short facts/headings.
// All output remains a contiguous slice of the original document.
func splitPassages(text string) []passage {
	var out []passage
	paragraph, first, start := 1, 1, 0
	for start < len(text) {
		end := start + min(1800, len(text)-start)
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		if end < len(text) {
			window := text[start:end]
			if cut := strings.LastIndex(window, "\n\n"); cut >= 400 {
				end = start + cut + 2
			} else {
				// Prefer sentence or word boundaries, retaining every byte in the tail.
				for i := end - 1; i >= start+900; i-- {
					if text[i] == ' ' || text[i] == '\n' || text[i] == '.' || text[i] == ';' {
						end = i + 1
						break
					}
				}
			}
		}
		part := text[start:end]
		breaks := strings.Count(part, "\n\n")
		last := paragraph + breaks
		if strings.HasSuffix(part, "\n\n") {
			last--
		}
		paragraph += breaks
		if body := strings.TrimSpace(part); body != "" {
			out = append(out, passage{text: body, first: first, last: max(first, last)})
		}
		start = end
		first = paragraph
	}
	return out
}

// Fixed-size bottom-k shingle sketches avoid retaining a word set per passage.
type sketch struct {
	values          [16]uint64
	n               int
	exact, critical uint64
}

func hashTerm(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
func makeSketch(text string) sketch {
	s := sketch{exact: hashTerm(text)}
	var previous, older uint64
	count := 0
	scanTokens(text, func(term string) {
		h := hashTerm(term)
		critical := false
		for _, r := range term {
			if unicode.IsDigit(r) {
				critical = true
				break
			}
		}
		switch term {
		case "not", "no", "never", "cannot", "without", "can", "may", "must", "only", "except", "before", "after", "unless", "if", "t", "禁止", "不能", "不", "未", "无", "至少", "最多", "仅", "只", "必须", "可能", "如果", "除非":
			critical = true
		}
		if critical {
			s.critical = (s.critical ^ h) * 1099511628211
		}
		if count >= 2 {
			value := h ^ bits.RotateLeft64(previous, 21) ^ bits.RotateLeft64(older, 42)
			i := sort.Search(s.n, func(i int) bool { return s.values[i] >= value })
			if i < s.n && s.values[i] == value {
			} else if i < len(s.values) {
				if s.n < len(s.values) {
					s.n++
				}
				copy(s.values[i+1:s.n], s.values[i:s.n-1])
				s.values[i] = value
			}
		}
		older, previous = previous, h
		count++
	}, false)
	return s
}
func similarity(a, b sketch) float64 {
	if a.critical != b.critical {
		return 0
	} // Preserve numerical/negated variants.
	if a.exact == b.exact {
		return 1
	}
	if a.n == 0 || b.n == 0 {
		return 0
	}
	matched, i, j := 0, 0, 0
	for i < a.n && j < b.n {
		if a.values[i] == b.values[j] {
			matched++
			i++
			j++
		} else if a.values[i] < b.values[j] {
			i++
		} else {
			j++
		}
	}
	return float64(matched) / float64(a.n+b.n-matched)
}

type scoredEvidence struct {
	evidence     Evidence
	vector       lexicalVector
	score        float64
	sketch       sketch
	domain       string
	opening      bool
	publication  bool
	headingScore float64
}

func SelectEvidence(query string, docs []reader.Document, published map[string]string, max int) []Evidence {
	return SelectEvidenceForPlan(Plan{Queries: []string{query}}, docs, published, max)
}

// SelectEvidenceForPlan combines corpus BM25, aspect novelty, source diversity,
// and a small MMR-style redundancy penalty. Lexical matches are not fact checks.
func SelectEvidenceForPlan(plan Plan, docs []reader.Document, published map[string]string, limit int) []Evidence {
	if limit <= 0 || len(docs) == 0 {
		return nil
	}
	limit = min(24, limit)
	query := ""
	if len(plan.Queries) > 0 {
		query = plan.Queries[0]
	}
	query += " " + strings.Join(plan.focus, " ")
	overviews := overviewTargets(plan)
	intro := len(plan.Queries) > 0 && introductoryQuery(plan.Queries[0])
	l := newLexical(query, plan.Aspects, plan.Queries)
	l.expandProseForms(query, plan.Aspects, plan.Queries)
	scratch := make([]uint16, len(l.terms))
	items := make([]scoredEvidence, 0, len(docs)*16)
	vectors := make([]lexicalVector, 0, len(docs)*16)
	hashes := map[string]bool{}
	urls := map[string]bool{}
	entities := relevanceEntities(plan)
	var publicationSubjects []string
	if len(plan.Queries) > 0 && plan.Intent != "compare" && needsFresh(plan) && requiresFreshQuery(plan.Queries[0]) && !strings.ContainsAny(plan.Queries[0], "0123456789") && releaseDateQuery(plan.Queries[0]) {
		publicationSubjects = releaseSubjects(plan)
	}
	latestReleaseDay := 0
	var publicationIndices []int
	var entityHeadingTerms map[string]bool
	if !intro && plan.Intent != "compare" && len(plan.Queries) > 0 && len(entities) > 0 && identifierTopicQuery(plan.Queries[0]) {
		entityHeadingTerms = make(map[string]bool)
		for _, entity := range entities {
			scanTerms(spacedQuery(entity), func(term string) { entityHeadingTerms[term] = true })
		}
	}
	for _, doc := range docs {
		key := search.DocumentURL(doc.URL)
		if key == "" || urls[key] || (doc.Hash != "" && hashes[doc.Hash]) {
			continue
		}
		urls[key] = true
		if doc.Hash != "" {
			hashes[doc.Hash] = true
		}
		titleVector := l.vector(doc.Title, scratch)
		titleBoost := 0.0
		for _, overview := range overviews {
			titleBoost = math.Max(titleBoost, overviewFit(overview, doc.URL, doc.Title))
		}
		focusedOverview := titleBoost > 0
		for _, f := range titleVector.matches {
			titleBoost += l.terms[f.term].weight * .12
		}
		docFocused := matchesEntities(doc.Title+" "+doc.URL, entities)
		publicationPage := len(publicationSubjects) > 0 && releaseTitleKind(reader.Clip(doc.Title, 200), publicationSubjects) > 0
		for pi, p := range documentPassages(doc) {
			if !docFocused && !matchesEntities(p.text, entities) {
				continue
			}
			v := l.vector(p.text, scratch)
			headingScore := 0.0
			if p.heading != "" {
				hv := l.vector(p.heading, scratch)
				for _, f := range hv.matches {
					if !docFocused || !entityHeadingTerms[l.terms[f.term].text] {
						headingScore += l.terms[f.term].weight
					}
				}
				headingScore /= math.Sqrt(float64(max(1, hv.length)))
			}
			opening := false
			if pi < 2 {
				// An overview's early outlined section may describe capabilities
				// without repeating its entity name. Preserve that page context;
				// ranking hints never alter the actual quoted passage or prove trust.
				opening = intro && focusedOverview && docFocused && p.heading != ""
				for _, overview := range overviews {
					opening = opening || overviewFit(overview, doc.URL, doc.Title) > 0 && mentionsEntity(p.text, overview)
				}
			}
			if publicationPage {
				if day := explicitReleaseDay(p.text, doc.FetchedAt); day > 0 && day >= latestReleaseDay {
					if day > latestReleaseDay {
						latestReleaseDay = day
						publicationIndices = publicationIndices[:0]
					}
					publicationIndices = append(publicationIndices, len(items))
				}
			}
			items = append(items, scoredEvidence{evidence: Evidence{URL: doc.URL, Title: doc.Title, Text: p.text, DocumentHash: doc.Hash, Paragraph: p.first, ParagraphEnd: p.last, Kind: "body", PublishedAt: published[doc.URL], FetchedAt: doc.FetchedAt, ReadMethod: reader.Clip(doc.Method, 64), Truncated: doc.Truncated}, vector: v, score: titleBoost, domain: domain(doc.URL), opening: opening, headingScore: headingScore})
			vectors = append(vectors, v)
		}
	}
	if len(items) == 0 {
		return nil
	}
	scores := l.scores(vectors)
	bestBody, bestHeading := 0.0, 0.0
	for i := range items {
		items[i].score += scores[i]
		bestBody = math.Max(bestBody, scores[i])
		bestHeading = math.Max(bestHeading, items[i].headingScore)
	}
	publicationScale := bestBody
	if len(publicationIndices) > 0 && publicationScale == 0 {
		// A release table may name the project only in its page title. Keep
		// explicit dated rows even when their version identifiers have no
		// literal query words. This fallback is not used for ordinary queries.
		for _, item := range items {
			publicationScale = math.Max(publicationScale, item.score)
		}
	}
	for _, i := range publicationIndices {
		// Preserve an explicit recent release row against keyword-heavy
		// policy/overview text, including its heading bonus. This ranks
		// source text; it does not certify a release or infer a date.
		items[i].publication = true
		items[i].score += 1.8 * publicationScale
	}
	// Definition/overview questions need the opening in context. Repeated entity
	// names in references and footers must not consume all per-page slots. Scale
	// against this corpus rather than adding a negligible fixed BM25 offset.
	for i := range items {
		// The first passage of a query-relevant section carries its definition
		// or contract before repeated examples. Headings affect rank only; all
		// evidence remains a contiguous, unchanged slice of the fetched text.
		if bestHeading > 0 {
			items[i].score += .7 * bestBody * items[i].headingScore / bestHeading
		}
		if items[i].opening {
			// Give introductory openings at least the strongest body relevance.
			// Otherwise an entity-free capabilities section is admitted, but
			// repeated names in release lists still consume all per-page slots.
			boost := .8 * bestBody
			if intro {
				boost = math.Max(boost, bestBody-scores[i])
			}
			items[i].score += boost
		}
	}
	// Do not fill diversity slots with unrelated text merely because its domain differs.
	if bestBody > 0 {
		filtered := items[:0]
		for i, item := range items {
			if item.opening || item.publication || scores[i] >= .04*bestBody {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	} else if len(l.terms) > 0 {
		// A fetched unrelated page is not usable evidence merely because the
		// fetch succeeded. Keep title matches (e.g. a terse answer without query
		// words), otherwise let gathering recover or return search leads.
		filtered := items[:0]
		for _, item := range items {
			if item.score > 0 {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	// Bound the expensive diversity pass, reserving room per document first.
	pool := make([]scoredEvidence, 0, min(96, len(items)))
	perDoc := map[string]int{}
	for _, item := range items {
		if perDoc[item.evidence.URL] >= 8 {
			continue
		}
		perDoc[item.evidence.URL]++
		item.sketch = makeSketch(item.evidence.Text)
		pool = append(pool, item)
		if len(pool) >= 96 {
			break
		}
	}
	if len(pool) == 0 {
		return nil
	}
	peak := math.Max(pool[0].score, 1e-9)
	used := make([]bool, len(pool))
	perURL := map[string]int{}
	perDomain := map[string]int{}
	selected := make([]int, 0, limit)
	covered := uint8(0)
	for len(selected) < limit {
		best, utility := -1, math.Inf(-1)
		for i, item := range pool {
			if used[i] || perURL[item.evidence.URL] >= 3 {
				continue
			}
			redundancy := 0.0
			exactDuplicate := false
			for _, j := range selected {
				if item.sketch.exact == pool[j].sketch.exact {
					exactDuplicate = true
					break
				}
				redundancy = math.Max(redundancy, similarity(item.sketch, pool[j].sketch))
			}
			if exactDuplicate {
				continue
			}
			novel := bits.OnesCount8(item.vector.mask &^ covered)
			score := item.score/peak + .3*float64(novel) - .45*redundancy - .07*float64(perDomain[item.domain]) - .12*float64(perURL[item.evidence.URL])
			if score > utility {
				best, utility = i, score
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		selected = append(selected, best)
		item := pool[best]
		covered |= item.vector.mask
		perURL[item.evidence.URL]++
		perDomain[item.domain]++
	}
	out := make([]Evidence, 0, len(selected))
	for _, i := range selected {
		ev := pool[i].evidence
		ev.ID = fmt.Sprintf("e%d", len(out)+1)
		ev.RelevanceScore = pool[i].score
		for aspect := range min(4, len(plan.Aspects)) {
			if pool[i].vector.mask&(1<<aspect) != 0 {
				ev.MatchedAspects = append(ev.MatchedAspects, aspect)
			}
		}
		out = append(out, ev)
	}
	return out
}

// Literal lower-camel symbols provide a narrower API topic than a
// capitalized project name. Do not apply entity-heading discount to role or
// overview questions, where the project/module name is itself the topic.
func identifierTopicQuery(query string) bool {
	for _, word := range strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_')
	}) {
		if len(word) == 0 {
			continue
		}
		if word[0] >= 'a' && word[0] <= 'z' && len(camelWords(word)) > 1 {
			return true
		}
	}
	return false
}

package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"searchan/reader"
	"searchan/search"
)

type hit struct {
	search.Result
	votes      map[string]float64
	fusion     float64
	queries    []string
	queryRanks map[string]int
	pageKey    string
}
type searchPair struct {
	provider int
	query    string
}
type retrieval struct {
	engine    *Engine
	attempted map[searchPair]bool
	counts    []int
	paused    []bool
	failures  []uint8
	hits      []hit
	indices   map[string]int
	stats     *Stats
}

func newRetrieval(e *Engine, stats *Stats) *retrieval {
	return &retrieval{engine: e, stats: stats, attempted: map[searchPair]bool{}, counts: make([]int, len(e.cfg.Providers)), paused: make([]bool, len(e.cfg.Providers)), failures: make([]uint8, len(e.cfg.Providers)), indices: map[string]int{}}
}

func queryKey(q string) string { return strings.ToLower(strings.Join(strings.Fields(q), " ")) }

// The ledger spans initial search and recovery. Prefer unused providers and
// never repeat an identical provider/query pair within a task.
func (s *retrieval) search(ctx context.Context, queries []string, budget int) []string {
	type job struct {
		query    string
		key      string
		provider int
	}
	keys := make([]string, len(queries))
	for i, q := range queries {
		keys[i] = queryKey(q)
	}
	type outcome struct {
		response search.Response
		err      error
		elapsed  int64
		started  bool
	}
	type completion struct {
		index  int
		result outcome
	}
	concurrency := min(s.engine.cfg.SearchConcurrency, len(s.engine.cfg.Providers))
	completed := make(chan completion, concurrency)
	inflight := make([]bool, len(s.counts))
	var jobs []job
	var results []outcome
	active, nextQuery := 0, 0
	// Only dispatch bounded work, with one in-flight query per provider. A
	// failing credential/rate limit pauses that provider before another call.
	for {
		for active < concurrency && len(jobs) < budget && ctx.Err() == nil {
			chosen := job{provider: -1}
			for offset := range queries {
				qi := (nextQuery + offset) % len(queries)
				if keys[qi] == "" {
					continue
				}
				best := -1
				for p := range s.counts {
					if inflight[p] || s.paused[p] || s.attempted[searchPair{p, keys[qi]}] {
						continue
					}
					if best < 0 || s.counts[p] < s.counts[best] {
						best = p
					}
				}
				if best >= 0 {
					chosen = job{query: queries[qi], key: keys[qi], provider: best}
					nextQuery = (qi + 1) % len(queries)
					break
				}
			}
			if chosen.provider < 0 {
				break
			}
			j, index := chosen, len(jobs)
			jobs = append(jobs, j)
			results = append(results, outcome{})
			inflight[j.provider] = true
			s.attempted[searchPair{j.provider, j.key}] = true
			s.counts[j.provider]++
			active++
			go func() {
				var r outcome
				defer func() { completed <- completion{index, r} }()
				select {
				case s.engine.searchSlots <- struct{}{}:
					defer func() { <-s.engine.searchSlots }()
				case <-ctx.Done():
					r.err = ctx.Err()
					return
				}
				if ctx.Err() != nil {
					r.err = ctx.Err()
					return
				}
				callCtx, cancel := context.WithTimeout(ctx, s.engine.cfg.SearchTimeout)
				defer cancel()
				started := time.Now()
				r.started = true
				r.response, r.err = s.engine.cfg.Providers[j.provider].Search(callCtx, search.Request{Query: j.query, Limit: 15})
				r.elapsed = time.Since(started).Milliseconds()
				if r.err == nil {
					r.response = compactResponse(r.response)
				} else {
					r.response = search.Response{}
				}
			}()
		}
		if active == 0 {
			break
		}
		c := <-completed
		j := jobs[c.index]
		results[c.index] = c.result
		inflight[j.provider] = false
		active--
		if !c.result.started {
			delete(s.attempted, searchPair{j.provider, j.key})
			s.counts[j.provider]--
		}
		if pauseProvider(c.result.err) {
			s.paused[j.provider] = true
		} else if c.result.started && unavailableProvider(c.result.err) {
			// One transient failure may recover on the next distinct query.
			// Two consecutive network/server failures pause this provider for
			// this task, including recovery; other tasks start with a clean ledger.
			s.failures[j.provider]++
			if s.failures[j.provider] >= 2 {
				s.paused[j.provider] = true
			}
		} else if c.result.started {
			s.failures[j.provider] = 0
		}
	}
	var warnings []string
	for i, r := range results {
		name := s.engine.cfg.Providers[jobs[i].provider].Name()
		call := SearchCall{Provider: name, Query: reader.Clip(jobs[i].query, 2048), ElapsedMS: r.elapsed}
		if r.started {
			s.stats.SearchCalls++
		}
		if r.err != nil {
			call.ErrorCode = search.FailureCode(r.err)
			call.Error = reader.Clip(r.err.Error(), 256)
			warnings = append(warnings, fmt.Sprintf("%s 检索失败：%s", name, call.Error))
		} else {
			warnings = append(warnings, r.response.Warnings...)
			local := map[string]bool{}
			for rank, result := range r.response.Results[:min(15, len(r.response.Results))] {
				if len(result.URL) > 4096 {
					continue
				}
				key := search.DocumentURL(result.URL)
				if key == "" || local[key] {
					continue
				}
				local[key] = true
				call.Results++
				idx, ok := s.indices[key]
				if !ok {
					idx = len(s.hits)
					s.indices[key] = idx
					copy := result
					copy.Sources = nil
					s.hits = append(s.hits, hit{Result: copy, votes: map[string]float64{}, pageKey: key})
					call.NewDocuments++
				} else {
					old := &s.hits[idx]
					if len(result.Snippet) > len(old.Snippet) {
						old.Snippet = result.Snippet
					}
					if old.Content == "" && result.Content != "" {
						old.Content = result.Content
						old.ContentProvider = result.ContentProvider
						old.ContentTruncated = result.ContentTruncated
					}
					if old.PublishedAt == "" {
						old.PublishedAt = result.PublishedAt
					}
				}
				h := &s.hits[idx]
				if h.queryRanks == nil {
					h.queryRanks = map[string]int{}
				}
				if old := h.queryRanks[jobs[i].key]; old == 0 || rank+1 < old {
					h.queryRanks[jobs[i].key] = rank + 1
				}
				if !contains(h.queries, jobs[i].key) {
					h.queries = append(h.queries, jobs[i].key)
				}
				families := search.SourceFamilies(name, result.Sources)
				for _, family := range families {
					vote := 1 / (float64(61+rank) * float64(len(families)))
					if vote > h.votes[family] {
						h.votes[family] = vote
					}
				}
				// Keep the best provider rank; query repetitions do not add votes.
				found := false
				for si := range h.Sources {
					if h.Sources[si].Provider == name {
						h.Sources[si].Rank = min(h.Sources[si].Rank, rank+1)
						for _, src := range result.Sources {
							if src.Provider == name {
								for _, engine := range src.Engines {
									engine = reader.Clip(engine, 64)
									if !contains(h.Sources[si].Engines, engine) && len(h.Sources[si].Engines) < 8 {
										h.Sources[si].Engines = append(h.Sources[si].Engines, engine)
									}
								}
							}
						}
						found = true
						break
					}
				}
				if !found {
					var engines []string
					for _, source := range result.Sources {
						if source.Provider == name {
							for _, engine := range source.Engines {
								engine = reader.Clip(engine, 64)
								if !contains(engines, engine) && len(engines) < 8 {
									engines = append(engines, engine)
								}
							}
						}
					}
					h.Sources = append(h.Sources, search.Source{Provider: name, Rank: rank + 1, Engines: engines})
				}
			}
		}
		if r.started {
			s.stats.Calls = append(s.stats.Calls, call)
		}
	}
	for i := range s.hits {
		// Sort family keys for reproducible floating-point sums.
		keys := make([]string, 0, len(s.hits[i].votes))
		for key := range s.hits[i].votes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		s.hits[i].fusion = 0
		for _, key := range keys {
			s.hits[i].fusion += s.hits[i].votes[key]
		}
	}
	return warnings
}

func pauseProvider(err error) bool {
	var he *search.HTTPError
	return errors.Is(err, search.ErrBlocked) || (errors.As(err, &he) && (he.StatusCode == 401 || he.StatusCode == 403 || he.StatusCode == 429))
}

func unavailableProvider(err error) bool {
	var network net.Error
	var status *search.HTTPError
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) || (errors.As(err, &status) && status.StatusCode >= 500 && status.StatusCode <= 599)
}

func rankHits(plan Plan, hits []hit, attempted map[string]bool, limit int) []hit {
	if limit <= 0 {
		return nil
	}
	query := ""
	entities := relevanceEntities(plan)
	if len(plan.Queries) > 0 {
		query = plan.Queries[0]
	}
	query += " " + strings.Join(plan.focus, " ")
	overviews := overviewTargets(plan)
	l := newLexical(query, plan.Aspects, plan.Queries)
	scratch := make([]uint16, len(l.terms))
	type candidate struct {
		hit       hit
		vector    lexicalVector
		domain    string
		score     float64
		overview  uint8
		queryMask uint8
	}
	candidates := make([]candidate, 0, len(hits))
	vectors := make([]lexicalVector, 0, len(hits))
	previews := make([]string, 0, len(hits))
	peakFusion := 0.0
	for _, h := range hits {
		key := h.pageKey
		if key == "" {
			key = search.DocumentURL(h.URL)
		}
		if attempted[key] {
			continue
		}
		preview := h.Snippet
		if preview == "" {
			preview = reader.Clip(h.Content, 1400)
		}
		text := h.Title + " " + h.Title + " " + reader.Clip(preview, 1600)
		v := l.vector(text, scratch)
		previews = append(previews, text)
		candidates = append(candidates, candidate{hit: h, vector: v, domain: domain(h.URL)})
		vectors = append(vectors, v)
		peakFusion = math.Max(peakFusion, h.fusion)
	}
	scores := l.scores(vectors)
	peakLexical := 0.0
	for _, v := range scores {
		peakLexical = math.Max(peakLexical, v)
	}
	// Normalize each query separately. Chinese unigram/bigram overlap must not
	// drown an English documentation query simply because it has fewer terms.
	// This rewards relevance, never publisher authenticity. Work stays bounded
	// by the existing candidate and query limits, without retaining page vectors.
	queryScores := make([]float64, len(candidates))
	for qi, q := range plan.Queries[:min(4, len(plan.Queries))] {
		local := newLexical(q, nil, nil)
		scratch := make([]uint16, len(local.terms))
		vv := make([]lexicalVector, len(previews))
		for i, text := range previews {
			vv[i] = local.vector(text, scratch)
		}
		ss := local.scores(vv)
		peak := 0.0
		for _, score := range ss {
			peak = math.Max(peak, score)
		}
		if peak == 0 {
			continue
		}
		for i, score := range ss {
			normalized := score / peak
			queryScores[i] = math.Max(queryScores[i], normalized)
			if normalized >= .65 {
				candidates[i].queryMask |= 1 << qi
			}
		}
	}
	for i := range candidates {
		candidates[i].score = .15*scores[i]/math.Max(peakLexical, 1e-9) + .5*queryScores[i] + .35*candidates[i].hit.fusion/math.Max(peakFusion, 1e-9)
		if len(plan.Queries) == 0 {
			candidates[i].score += .5 * scores[i] / math.Max(peakLexical, 1e-9)
		}
		if len(entities) > 0 {
			h := candidates[i].hit
			if matchesEntities(h.Title+" "+h.URL+" "+h.Snippet+" "+reader.Clip(h.Content, 1600), entities) {
				candidates[i].score += 1
			}
		}
		var titledSides uint8
		if len(overviews) == 2 {
			for oi, overview := range overviews {
				if mentionsEntity(candidates[i].hit.Title, overview) {
					titledSides |= 1 << oi
				}
			}
			// A document devoted to one named side need not put its name in
			// the final URL segment (API manuals and /orm/ are common shapes).
			// Comparison articles naming both sides do not consume these slots.
			if titledSides == 1 || titledSides == 2 {
				candidates[i].overview |= titledSides
			}
		}
		for oi, overview := range overviews {
			fit := overviewFit(overview, candidates[i].hit.URL, candidates[i].hit.Title)
			if fit > 0 {
				candidates[i].overview |= 1 << oi
				candidates[i].score += fit
				lookup := queryKey(overviewQuery(overview))
				if contains(candidates[i].hit.queries, lookup) {
					candidates[i].score += .65
				}
			}
		}
	}
	used := make([]bool, len(candidates))
	domains := map[string]int{}
	covered := uint8(0)
	coveredOverviews := uint8(0)
	coveredQueries := uint8(0)
	queries := map[string]bool{}
	out := make([]hit, 0, min(limit, len(candidates)))
	for len(out) < limit {
		best, value := -1, math.Inf(-1)
		for i, c := range candidates {
			if used[i] {
				continue
			}
			novel := 0
			focus := 0.0
			for _, q := range c.hit.queries {
				for _, target := range plan.focus {
					if q == queryKey(target) {
						focus = .3
					}
				}
				if !queries[q] {
					novel++
				}
			}
			score := c.score + focus + .12*float64(bits.OnesCount8(c.vector.mask&^covered)) + .08*float64(novel) - .1*float64(domains[c.domain])
			score += .25 * float64(bits.OnesCount8(c.queryMask&^coveredQueries))
			if len(overviews) == 2 && c.overview&^coveredOverviews != 0 {
				score += .7 // reserve attention for each side, not two mirrors of one
			}
			if score > value {
				best, value = i, score
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		c := candidates[best]
		out = append(out, c.hit)
		covered |= c.vector.mask
		coveredOverviews |= c.overview
		coveredQueries |= c.queryMask
		domains[c.domain]++
		for _, q := range c.hit.queries {
			queries[q] = true
		}
	}
	return out
}

// A focused project landing page is a useful introduction candidate. This is
// page-shape relevance only: neither a matching URL nor "official" in a title
// authenticates the publisher. Body validation and citation checks still apply.
func overviewTargets(plan Plan) []string {
	if len(plan.Queries) == 0 || needsFresh(plan) || (plan.Intent != "compare" && !introductoryQuery(plan.Queries[0])) {
		return nil
	}
	entities := discoveryEntities(plan)
	if plan.Intent == "compare" {
		if len(entities) != 2 {
			return nil
		}
		return entities
	}
	if len(entities) == 1 {
		return entities
	}
	return nil
}

var entitySeparators = strings.NewReplacer("-", "", "_", "", " ", "")

func overviewFit(target, rawURL, title string) float64 {
	if target == "" || !mentionsEntity(title, target) {
		return 0
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return 0
	}
	switch strings.ToLower(u.Hostname()) {
	case "x.com", "www.x.com", "twitter.com", "www.twitter.com", "facebook.com", "www.facebook.com", "instagram.com", "www.instagram.com", "youtube.com", "www.youtube.com":
		return 0 // a social profile path is not a project overview
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 3 || len(parts) == 0 {
		return 0
	}
	key := func(s string) string {
		return strings.ToLower(entitySeparators.Replace(s))
	}
	if key(parts[len(parts)-1]) == key(target) {
		if len(parts) <= 2 {
			return .6
		}
		return .4
	}
	return 0
}

package research

import (
	"context"
	"strings"
	"time"
)

func inferIntent(query string) string {
	q := strings.ToLower(query)
	for _, entry := range []struct {
		intent  string
		markers []string
	}{
		{"verify", []string{"真的吗", "是否属实", "辟谣", "fact check"}},
		{"recent", []string{"最新", "最近", "今日", "今天", "latest", "recent"}},
		{"compare", []string{"对比", "比较", "区别", " vs ", " versus "}},
		{"howto", []string{"如何", "怎么", "教程", "how to"}},
	} {
		if entry.intent == "recent" {
			if requiresFreshQuery(query) {
				return "recent"
			}
			continue
		}
		if entry.intent == "compare" && relationshipQuery(q) {
			return "compare"
		}
		for _, marker := range entry.markers {
			if strings.Contains(q, marker) {
				return entry.intent
			}
		}
	}
	return "fact"
}

func requiresFreshQuery(query string) bool {
	q := strings.ToLower(query)
	for _, marker := range []string{"最新", "最近", "今日", "今天", "目前", "当前"} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	for _, word := range []string{"latest", "recent", "current", "today"} {
		for offset := 0; offset < len(q); {
			i := strings.Index(q[offset:], word)
			if i < 0 {
				break
			}
			i += offset
			end := i + len(word)
			letter := func(b byte) bool { return b >= 'a' && b <= 'z' }
			if (i == 0 || !letter(q[i-1])) && (end == len(q) || !letter(q[end])) {
				return true
			}
			offset = end
		}
	}
	return false
}

func needsFresh(plan Plan) bool { return plan.RequiresFresh || plan.Intent == "recent" }

func normalizePlan(base, proposed Plan) Plan {
	if len(base.Queries) == 0 {
		return base
	}
	switch proposed.Intent {
	case "fact", "compare", "howto", "recent", "verify", "navigate":
		base.Intent = proposed.Intent
		base.RequiresFresh = base.RequiresFresh || proposed.Intent == "recent"
	}
	// Explicit comparison/relationship wording remains a two-sided request even
	// if the planner calls it a fact. Freshness is tracked independently above.
	if len(base.Queries) > 0 && inferIntent(base.Queries[0]) == "compare" {
		base.Intent = "compare"
	}
	// A spelling alternative is fallback discovery, not an extra semantic
	// question. Reserve the original and planned queries before this alternative
	// so a two-query planner does not silently lose its second query at the cap.
	variant := searchSpellingQuery(base.Queries[0])
	variantKey := queryKey(variant)
	originalKey := queryKey(base.Queries[0])
	seen := map[string]bool{}
	queries := make([]string, 0, 4)
	for _, q := range base.Queries {
		key := queryKey(q)
		if key != originalKey && key == variantKey {
			continue
		}
		if !seen[key] && len(queries) < 4 {
			queries = append(queries, q)
			seen[key] = true
		}
	}
	base.Queries = queries
	for _, q := range proposed.Queries {
		q = strings.Join(strings.Fields(q), " ")
		if q == "" {
			continue
		}
		q = anchoredQuery(base.Queries[0], q)
		key := queryKey(q)
		if key == variantKey && key != originalKey {
			continue
		}
		if q != "" && len(q) <= 2048 && !seen[key] && len(base.Queries) < 4 {
			base.Queries = append(base.Queries, q)
			seen[key] = true
		}
	}
	if !seen[variantKey] && len(base.Queries) < 4 {
		base.Queries = append(base.Queries, variant)
	}
	var aspects []string
	seen = map[string]bool{}
	for _, a := range proposed.Aspects {
		a = strings.Join(strings.Fields(a), " ")
		key := queryKey(a)
		if a != "" && len(a) <= 400 && !seen[key] && len(aspects) < 4 {
			aspects = append(aspects, a)
			seen[key] = true
		}
	}
	if len(aspects) > 0 {
		base.Aspects = aspects
	}
	base = sourceDiscoveryPlan(base)
	base.Queries = base.Queries[:min(3, len(base.Queries))]
	return base
}

// A neutral overview query complements local-language results for explicitly
// named technical entities. It is a discovery hint, never proof of authority;
// no guessed domains or URLs are introduced. Keep the original and cap at three.
func sourceDiscoveryPlan(plan Plan) Plan {
	if len(plan.Queries) == 0 || needsFresh(plan) || (plan.Intent != "compare" && !introductoryQuery(plan.Queries[0])) {
		return plan
	}
	entities := discoveryEntities(plan)
	if len(entities) == 0 {
		return plan
	}
	queries := []string{plan.Queries[0]}
	for _, entity := range entities[:min(2, len(entities))] {
		queries = append(queries, overviewQuery(entity))
	}
	// Prefer a planned query over a spelling-only variant of the raw request.
	variant := searchSpellingQuery(plan.Queries[0])
	canonical := queryKey(variant)
	for _, q := range plan.Queries[1:] {
		if queryKey(q) != canonical && !contains(queries, q) && len(queries) < 3 {
			queries = append(queries, q)
		}
	}
	if len(queries) < 3 && canonical != queryKey(plan.Queries[0]) {
		queries = append(queries, variant)
	}
	plan.Queries = queries
	return plan
}

func overviewQuery(entity string) string {
	return `"` + entity + `" project overview official website`
}

// A planner may restore capitalization of an explicitly written lowercase
// name. Accept it only when every word occurs as that same entity in the query.
func discoveryEntities(plan Plan) []string {
	if len(plan.Queries) == 0 {
		return nil
	}
	if plan.Intent == "compare" {
		if names := comparisonNames(plan.Queries[0]); len(names) == 2 {
			return names
		}
	}
	entities := discoveryNames(plan.Queries[0])
	if len(entities) > 0 {
		return entities
	}
	for _, q := range plan.Queries[1:] {
		for _, entity := range discoveryNames(q) {
			if mentionsEntity(plan.Queries[0], entity) && !contains(entities, entity) && len(entities) < 2 {
				entities = append(entities, entity)
			}
		}
	}
	return entities
}

func modelContext(ctx context.Context, limit time.Duration, reserveVerification bool) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		reserve := min(200*time.Millisecond, max(0, remaining/10))
		if reserveVerification {
			reserve += min(6*time.Second, max(0, remaining/3))
		}
		limit = min(limit, max(0, remaining-reserve))
	}
	return context.WithTimeout(ctx, limit)
}

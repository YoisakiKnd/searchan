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
	switch proposed.Intent {
	case "fact", "compare", "howto", "recent", "verify", "navigate":
		base.Intent = proposed.Intent
		base.RequiresFresh = base.RequiresFresh || proposed.Intent == "recent"
	}
	seen := map[string]bool{queryKey(base.Queries[0]): true}
	for _, q := range proposed.Queries {
		q = strings.Join(strings.Fields(q), " ")
		key := queryKey(q)
		if q != "" && len(q) <= 2048 && !seen[key] && len(base.Queries) < 3 {
			base.Queries = append(base.Queries, q)
			seen[key] = true
		}
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
	return base
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

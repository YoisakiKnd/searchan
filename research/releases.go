package research

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"searchan/reader"
	"searchan/search"
)

// This is a discovery hint for an explicitly requested publication date, not
// a date parser or publisher allowlist. Planner expansions cannot trigger it.
func releaseDateQuery(query string) bool {
	q := reader.Clip(query, 1024)
	for _, marker := range []string{"发布日期", "发布时间", "发行日期", "发行时间", "什么时候发布", "何时发布", "哪天发布", "什么时候发行", "何时发行"} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	// Most technical questions are not about publication. Scan for the two
	// English stems before allocating a lower-case query on that rare path.
	possible := false
	for i := 0; i < len(q); i++ {
		c := q[i] | 0x20
		if c == 'r' && i+7 <= len(q) && strings.EqualFold(q[i:i+7], "release") || c == 'p' && i+6 <= len(q) && strings.EqualFold(q[i:i+6], "public") || c == 'p' && i+7 <= len(q) && strings.EqualFold(q[i:i+7], "publish") {
			possible = true
			break
		}
	}
	if !possible {
		return false
	}
	q = strings.ToLower(q)
	for _, marker := range []string{"release date", "release time", "released on", "publication date", "published on"} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	return (strings.Contains(q, "when was ") || strings.Contains(q, "when is ") || strings.Contains(q, "when did ") || strings.Contains(q, "when will ")) && (strings.Contains(q, " released") || strings.Contains(q, " release") || strings.Contains(q, " published"))
}

func releaseSubjectWord(word string) bool {
	if stopTerms[word] || len(word) < 2 || len(word) > 64 {
		return false
	}
	switch word {
	case "latest", "stable", "version", "versions", "current", "currently", "release", "released", "releases", "history", "date", "dates", "time", "first", "last", "newest", "official", "available", "publication", "published", "will", "did", "has", "have", "been", "it", "its", "today", "now", "new", "recent", "most", "year", "day", "month":
		return false
	}
	return true
}

func releaseSubjects(plan Plan) []string {
	if subjects := referenceEntities(plan); len(subjects) > 0 {
		return subjects
	}
	var subjects []string
	for _, word := range strings.FieldsFunc(strings.ToLower(reader.Clip(plan.Queries[0], 1024)), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if releaseSubjectWord(word) && !contains(subjects, word) {
			subjects = append(subjects, word)
			if len(subjects) == 4 {
				break
			}
		}
	}
	return subjects
}

func releasePageKind(title string) int {
	text := strings.ToLower(title)
	words := " " + strings.Join(strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ") + " "
	for _, phrase := range []string{" release history ", " version history ", " changelog "} {
		if strings.Contains(words, phrase) {
			return 3
		}
	}
	for _, phrase := range []string{"发布历史", "发行历史", "版本历史", "更新日志", "发布日志"} {
		if strings.Contains(text, phrase) {
			return 3
		}
	}
	if strings.Contains(words, " release notes ") || strings.Contains(text, "发行说明") || strings.Contains(text, "发布说明") {
		return 2
	}
	return 0
}

func releaseTitleKind(title string, subjects []string) int {
	kind := releasePageKind(title)
	if kind == 0 || len(subjects) == 0 {
		return 0
	}
	for _, subject := range subjects {
		if !mentionsEntity(title, subject) {
			return 0
		}
	}
	return kind
}

// Recognize explicit release labels only. A raw timestamp, document update or
// fetched_at is not a publication assertion. Dates remain local ranking hints;
// the original paragraph and all conditions are still supplied unchanged.
var explicitReleaseDate = regexp.MustCompile(`(?im)\(released[ \t]+([12][0-9]{3}-[01][0-9]-[0-3][0-9])\)|^(?:release date|released|发布日期|发行日期)[ \t]*[:：][ \t]*([12][0-9]{3}-[01][0-9]-[0-3][0-9])`)

func explicitReleaseDay(text string, fetched time.Time) int {
	cutoff := 0
	if !fetched.IsZero() {
		localDay := fetched.UTC().Add(8 * time.Hour)
		cutoff = localDay.Year()*10000 + int(localDay.Month())*100 + localDay.Day()
	}
	latest := 0
	for _, match := range explicitReleaseDate.FindAllStringSubmatch(text, -1) {
		value := match[1]
		if value == "" {
			value = match[2]
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil {
			continue
		}
		day := date.Year()*10000 + int(date.Month())*100 + date.Day()
		if day > latest && (cutoff == 0 || day <= cutoff) {
			latest = day
		}
	}
	return latest
}

// Reuse the same single reference-read opportunity. Only actual query leaders
// qualify, and all literal subjects must occur in the visible candidate title.
// A generic history label or a hostname alone cannot establish relevance.
func searchReleaseReference(plan Plan, hits []hit, docs []reader.Document, attempted map[string]bool) (hit, bool) {
	if len(plan.Queries) == 0 || plan.Intent == "compare" || !releaseDateQuery(plan.Queries[0]) {
		return hit{}, false
	}
	subjects := releaseSubjects(plan)
	if len(subjects) == 0 {
		return hit{}, false
	}
	for _, doc := range docs {
		if releaseTitleKind(reader.Clip(doc.Title, 200), subjects) > 0 {
			return hit{}, true
		}
	}
	var best hit
	bestKind := 0
	seen := map[string]bool{}
	for _, query := range plan.Queries[:min(3, len(plan.Queries))] {
		for _, h := range queryLeaders(hits, queryKey(query)) {
			key := search.DocumentURL(h.URL)
			if key == "" || len(key) > 2048 || seen[key] || attempted[key] || reader.ValidateURL(key) != nil {
				continue
			}
			seen[key] = true
			if kind := releaseTitleKind(reader.Clip(h.Title, 200), subjects); kind > bestKind {
				bestKind, best = kind, h
				best.URL, best.pageKey = key, key
			}
		}
	}
	return best, best.URL != ""
}

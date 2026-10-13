package research

import (
	"regexp"
	"strings"
	"unicode"

	"searchan/reader"
)

var spacedName = regexp.MustCompile(`[A-Z][A-Za-z]+(?:[ \t]+[A-Z][A-Za-z]+){1,3}`)
var technicalName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[ \t]+[A-Za-z][A-Za-z0-9]*){0,3}$`)

func asciiUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func asciiLower(c byte) bool { return c >= 'a' && c <= 'z' }

// Expose spelling variants without a product dictionary or an extra model call.
// Keep the original identifier as well: context.Done and C++ must stay distinct.
func camelWords(word string) []string {
	var parts []string
	start := 0
	for i := 1; i < len(word); i++ {
		if asciiUpper(word[i]) && (asciiLower(word[i-1]) || asciiUpper(word[i-1]) && i+1 < len(word) && asciiLower(word[i+1])) {
			parts = append(parts, word[start:i])
			start = i
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return append(parts, word[start:])
}

func spacedQuery(query string) string {
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start >= 0 {
			if parts := camelWords(query[start:end]); len(parts) > 1 {
				b.WriteString(strings.Join(parts, " "))
			} else {
				b.WriteString(query[start:end])
			}
			start = -1
		}
	}
	for i, r := range query {
		if r < 128 && unicode.IsLetter(r) {
			if start < 0 {
				start = i
			}
		} else {
			wasWord := start >= 0
			flush(i)
			if wasWord && unicode.Is(unicode.Han, r) {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
		}
	}
	flush(len(query))
	return strings.Join(strings.Fields(b.String()), " ")
}

// Only strong, explicit compound names constrain evidence. Expansions never
// get to redefine the user's entity. This is a relevance check, not fact checking.
func queryEntities(query string) []string {
	var entities []string
	for _, word := range strings.FieldsFunc(reader.Clip(query, 1024), func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) {
		parts := camelWords(word)
		if len(parts) < 2 || len(word) > 64 {
			continue
		}
		strong := true
		for _, p := range parts {
			if len(p) < 2 || stopTerms[strings.ToLower(p)] {
				strong = false
			}
		}
		if strong && !contains(entities, strings.Join(parts, " ")) {
			entities = append(entities, strings.Join(parts, " "))
		}
		if len(entities) == 4 {
			break
		}
	}
	for _, name := range spacedName.FindAllString(reader.Clip(query, 1024), 4) {
		parts := strings.Fields(spacedQuery(name))
		strong := true
		for _, p := range parts {
			if len(p) < 2 || stopTerms[strings.ToLower(p)] {
				strong = false
			}
		}
		name = strings.Join(parts, " ")
		if strong && len(name) <= 64 && !contains(entities, name) && len(entities) < 8 {
			entities = append(entities, name)
		}
	}
	// A compound ecosystem plus a compound child names an integration/module,
	// not either component alone. A lone context word before an API identifier
	// (e.g. a language name) does not prove that it is part of the identifier.
	// At most eight names enter this bounded containment pass.
	return maximalNames(entities, 2)
}

// Separators in project names may be spaces, hyphens, underscores or camel
// boundaries. Require complete words; SpringDatabase must not match SpringData.
func mentionsEntity(text, entity string) bool {
	words := strings.FieldsFunc(spacedQuery(text), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') })
	if parts := camelWords(entity); len(parts) > 1 {
		entity = strings.Join(parts, " ")
	}
	parts := strings.Fields(strings.ToLower(entity))
	if len(parts) == 0 {
		return false
	}
	joined := strings.Join(parts, "")
	for i, word := range words {
		if strings.EqualFold(word, joined) {
			return true
		}
		if i+len(parts) > len(words) {
			continue
		}
		match := true
		for j, p := range parts {
			if !strings.EqualFold(words[i+j], p) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Discovery uses complete names as written, rather than splitting an acronym
// suffix or spending both discovery queries on a name and its subphrase. The
// stricter queryEntities constraints used for evidence filtering are unchanged.
func discoveryNames(query string) []string {
	query = reader.Clip(query, 1024)
	names := spacedName.FindAllString(query, 4)
	pool := make([]string, 0, 8)
	for _, name := range names {
		if validDiscoveryName(name) {
			pool = append(pool, name)
		}
	}
	words := strings.FieldsFunc(query, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') })
	for _, entity := range queryEntities(query) {
		// Retain an explicit acronym-bearing identifier when it is a complete
		// match (e.g. MixedCaseDB), while still allowing plain camel aliases.
		canonical := queryKey(spacedQuery(entity))
		for _, word := range words {
			if queryKey(spacedQuery(word)) != canonical {
				continue
			}
			for i := 1; i < len(word); i++ {
				if asciiUpper(word[i-1]) && asciiUpper(word[i]) {
					entity = word
					break
				}
			}
		}
		pool = append(pool, entity)
	}
	return maximalNames(pool, 1)
}

func maximalNames(pool []string, minExtension int) []string {
	var out []string
	for i, name := range pool {
		covered := false
		for j, other := range pool {
			if i == j || !mentionsEntity(other, name) {
				continue
			}
			otherName, thisName := spacedQuery(other), spacedQuery(name)
			extension := len(strings.Fields(otherName)) - len(strings.Fields(thisName))
			if extension >= minExtension || j < i && queryKey(otherName) == queryKey(thisName) {
				covered = true
				break
			}
		}
		if !covered && len(out) < 4 {
			out = append(out, name)
		}
	}
	return out
}

func validDiscoveryName(name string) bool {
	if len(name) > 64 || !technicalName.MatchString(name) {
		return false
	}
	for _, part := range strings.Fields(spacedQuery(name)) {
		if len(part) < 2 || stopTerms[strings.ToLower(part)] {
			return false
		}
	}
	return true
}

func relationshipQuery(query string) bool {
	q := strings.ToLower(strings.TrimRight(strings.TrimSpace(query), "？?。.!！"))
	for _, suffix := range []string{"是什么关系", "有什么关系", "有何关系", "之间的关系", "的关系"} {
		if strings.HasSuffix(q, suffix) && (strings.Contains(q, "和") || strings.Contains(q, "与") || strings.Contains(q, "跟")) {
			return true
		}
	}
	return strings.Contains(q, "relationship between ") && strings.Contains(q, " and ") || strings.Contains(q, "relation between ") && strings.Contains(q, " and ")
}

// Single-word names may be used only when the user explicitly names both
// sides of a comparison. This does not guess a second entity from a planner's
// expansion, or reinterpret arbitrary prose as a project name.
func comparisonNames(query string) []string {
	q := strings.TrimRight(strings.TrimSpace(reader.Clip(query, 1024)), "？?。.!！")
	lower := strings.ToLower(q)
	for _, prefix := range []string{"what is the relationship between ", "what's the relationship between ", "relationship between ", "what is the difference between ", "difference between ", "compare ", "比较", "对比"} {
		if strings.HasPrefix(lower, prefix) {
			q = strings.TrimSpace(q[len(prefix):])
			break
		}
	}
	lower = strings.ToLower(q)
	for _, suffix := range []string{"是什么关系", "有什么关系", "有何关系", "之间的关系", "的关系", "有什么区别", "有何区别", "之间的区别", "的区别", "区别", "对比", "比较", " relationship", " differences", " difference", " comparison"} {
		if strings.HasSuffix(lower, suffix) {
			q = strings.TrimSpace(q[:len(q)-len(suffix)])
			break
		}
	}
	lower = strings.ToLower(q)
	for _, separator := range []string{"和", "与", "跟", " vs. ", " vs ", " versus ", " and "} {
		if strings.Count(lower, separator) != 1 {
			continue
		}
		i := strings.Index(lower, separator)
		left, right := strings.TrimSpace(q[:i]), strings.TrimSpace(q[i+len(separator):])
		if validDiscoveryName(left) && validDiscoveryName(right) && !strings.EqualFold(left, right) {
			return []string{left, right}
		}
	}
	return nil
}

func matchesEntities(text string, entities []string) bool {
	for _, entity := range entities {
		if mentionsEntity(text, entity) {
			return true
		}
	}
	return len(entities) == 0
}

func relevanceEntities(plan Plan) []string {
	if len(plan.Queries) == 0 {
		return nil
	}
	if plan.Intent == "compare" {
		if names := comparisonNames(plan.Queries[0]); len(names) == 2 {
			return names
		}
	}
	return queryEntities(plan.Queries[0])
}

func anchoredQuery(original, candidate string) string {
	entities := relevanceEntities(Plan{Intent: inferIntent(original), Queries: []string{original}})
	if !matchesEntities(candidate, entities) {
		return strings.Join(entities, " ") + " " + reader.Clip(candidate, 1700)
	}
	return candidate
}

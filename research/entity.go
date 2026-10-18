package research

import (
	"regexp"
	"strings"
	"unicode"

	"searchan/reader"
)

var spacedName = regexp.MustCompile(`[A-Z][A-Za-z]+(?:[ \t]+[A-Z][A-Za-z]+){1,3}`)

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

// searchVariant returns the query with run-together capitalized words split
// (SpringData简介 → Spring Data 简介), or "" when nothing qualifies. Acronym
// endings (PostgreSQL, MongoDB) and identifiers next to '.', '_', '-', '/' or
// digits (context.WithTimeout) are left alone: splitting them only wastes one
// of the few search calls on a near-duplicate or misspelled query.
func searchVariant(query string) string {
	var b strings.Builder
	changed := false
	identifier := func(c byte) bool { return c == '.' || c == '_' || c == '-' || c == '/' || c >= '0' && c <= '9' }
	for i := 0; i < len(query); {
		if !asciiUpper(query[i]) && !asciiLower(query[i]) {
			b.WriteByte(query[i])
			i++
			continue
		}
		j := i
		for j < len(query) && (asciiUpper(query[j]) || asciiLower(query[j])) {
			j++
		}
		word, parts := query[i:j], camelWords(query[i:j])
		split := len(parts) > 1 && (i == 0 || !identifier(query[i-1])) && (j == len(query) || !identifier(query[j]))
		for _, p := range parts {
			if len(p) < 2 || !asciiUpper(p[0]) || strings.ToLower(p[1:]) != p[1:] {
				split = false
			}
		}
		if split {
			word, changed = strings.Join(parts, " "), true
			if i > 0 && query[i-1] != ' ' {
				word = " " + word
			}
			if j < len(query) && query[j] != ' ' {
				word += " "
			}
		}
		b.WriteString(word)
		i = j
	}
	if !changed {
		return ""
	}
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
		if strong && len(name) <= 64 && !contains(entities, name) && len(entities) < 4 {
			entities = append(entities, name)
		}
	}
	return entities
}

// Separators in project names may be spaces, hyphens, underscores or camel
// boundaries. Require complete words; SpringDatabase must not match SpringData.
func mentionsEntity(text, entity string) bool {
	words := strings.FieldsFunc(spacedQuery(text), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') })
	parts := strings.Fields(strings.ToLower(entity))
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

func matchesEntities(text string, entities []string) bool {
	for _, entity := range entities {
		if mentionsEntity(text, entity) {
			return true
		}
	}
	return len(entities) == 0
}

func anchoredQuery(original, candidate string) string {
	entities := queryEntities(original)
	if !matchesEntities(candidate, entities) {
		return strings.Join(entities, " ") + " " + reader.Clip(candidate, 1700)
	}
	return candidate
}

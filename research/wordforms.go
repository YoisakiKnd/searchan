package research

import (
	"strings"

	"searchan/reader"
)

// Add a few surface forms of ordinary query words to the existing index. This
// is a ranking hint, not a stemmer or an identity/semantic equivalence rule.
// Terms, weights, scratch size and original text remain unchanged. At most
// three aliases per bounded term are added; exact query keys always win.
func (l *lexical) expandProseForms(query string, aspects, expansions []string) {
	const plain, exact = 1, 2
	var flags [maxQueryTerms]uint8
	mark := func(word string, flag uint8) {
		if i, ok := l.index[word]; ok {
			flags[i] |= flag
		}
	}
	collect := func(text string) {
		text = reader.Clip(text, 1024)
		quote := byte(0)
		for i := 0; i < len(text); {
			ch := text[i]
			if ch == '"' || ch == '`' || ch == '\'' {
				if quote == ch {
					quote = 0
				} else if quote == 0 && (ch != '\'' || i == 0 || !asciiFormLetter(text[i-1])) {
					quote = ch
				}
				i++
				continue
			}
			if !asciiFormLetter(ch) {
				i++
				continue
			}
			start := i
			for i < len(text) && (asciiFormLetter(text[i]) || text[i] >= '0' && text[i] <= '9' || strings.ContainsRune("._+#-", rune(text[i]))) {
				i++
			}
			raw := strings.Trim(text[start:i], "._-")
			if len(raw) < 4 {
				continue
			}
			eligible, vowel := quote == 0 && len(raw) <= 32, false
			for j := range len(raw) {
				c := raw[j]
				eligible = eligible && c >= 'a' && c <= 'z'
				vowel = vowel || strings.ContainsRune("aeiou", rune(c))
			}
			next := i
			for next < len(text) && (text[next] == ' ' || text[next] == '\t' || text[next] == '\r' || text[next] == '\n') {
				next++
			}
			if next < len(text) && text[next] == '(' || start > 0 && strings.ContainsRune("._+#/-<", rune(text[start-1])) || i < len(text) && (text[i] == '/' || text[i] == '>') {
				eligible = false
			}
			if eligible && vowel {
				mark(raw, plain)
			} else {
				scanTerms(raw, func(word string) { mark(word, exact) })
			}
		}
	}
	collect(query)
	for _, text := range aspects[:min(4, len(aspects))] {
		collect(text)
	}
	for _, text := range expansions[:min(5, len(expansions))] {
		collect(text)
	}
	for i, t := range l.terms {
		if flags[i] != plain || closedFormWord(t.text) {
			continue
		}
		w := t.text
		// Already plural-looking words are kept literal, rather than guessing
		// roots such as status/statu, analysis/analysi, or singular API names.
		if strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") {
			continue
		}
		insert := func(form string) {
			if _, exists := l.index[form]; !exists {
				l.index[form] = i
			}
		}
		switch {
		case strings.HasSuffix(w, "y") && !strings.ContainsRune("aeiou", rune(w[len(w)-2])):
			insert(w[:len(w)-1] + "ies")
			insert(w[:len(w)-1] + "ied")
			insert(w + "ing")
		case strings.HasSuffix(w, "e") && !strings.HasSuffix(w, "ee"):
			insert(w + "s")
			insert(w + "d")
			insert(w[:len(w)-1] + "ing")
		default:
			if strings.HasSuffix(w, "s") || strings.HasSuffix(w, "x") || strings.HasSuffix(w, "ch") || strings.HasSuffix(w, "sh") {
				insert(w + "es")
			} else {
				insert(w + "s")
			}
			insert(w + "ed")
			insert(w + "ing")
		}
	}
}

func asciiFormLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

func closedFormWord(word string) bool {
	switch word {
	case "only", "never", "without", "unless", "cannot", "must", "should", "would", "could", "might", "shall", "more", "some", "none", "when", "where", "what", "that", "these", "those", "then", "than", "both", "also", "even", "each", "such", "other", "which":
		return true
	}
	return false
}

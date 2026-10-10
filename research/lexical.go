package research

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
)

// Only query vocabulary is retained. Documents do not get full term maps.
// This is a small-corpus BM25 variant with Chinese unigram/bigram features.
const maxQueryTerms = 192

var stopTerms = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields("a an the is are was were be of to in on for and or with how what when where which can do does please 的 了 在 是 和 与 及 吗 呢 啊 怎么 如何 什么 请问 可以 一个 一下 这个 那个") {
		m[s] = true
	}
	return m
}()

func scanTerms(s string, visit func(string)) {
	scanTokens(s, visit, true)
}
func scanTokens(s string, visit func(string), filterStops bool) {
	previous := -1
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		word := strings.ToLower(strings.Trim(s[start:end], "._-"))
		if !filterStops || !stopTerms[word] {
			if word != "" {
				visit(word)
			}
		}
		// Keep identifiers/versions intact, and also expose identifier parts.
		if strings.ContainsAny(word, "._-") {
			first := 0
			for i := 0; i <= len(word); i++ {
				if i == len(word) || word[i] == '.' || word[i] == '_' || word[i] == '-' {
					part := word[first:i]
					if part != "" && (!filterStops || !stopTerms[part]) {
						visit(part)
					}
					first = i + 1
				}
			}
		}
		start = -1
	}
	for i, r := range s {
		if unicode.Is(unicode.Han, r) {
			flush(i)
			size := utf8.RuneLen(r)
			if r == utf8.RuneError {
				_, size = utf8.DecodeRuneInString(s[i:])
			}
			one := s[i : i+size]
			if !filterStops || !stopTerms[one] {
				visit(one)
			}
			if previous >= 0 {
				two := s[previous : i+size]
				if !filterStops || !stopTerms[two] {
					visit(two)
				}
			}
			previous = i
			continue
		}
		previous = -1
		if unicode.IsLetter(r) || unicode.IsDigit(r) || (start >= 0 && (r == '+' || r == '#' || r == '.' || r == '_' || r == '-')) {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(s))
}

type lexicalTerm struct {
	text    string
	weight  float64
	aspects uint8
}
type lexical struct {
	index         map[string]int
	terms         []lexicalTerm
	aspectWeights [4]float64
}
type frequency struct {
	term  uint16
	count uint16
}
type lexicalVector struct {
	matches []frequency
	length  int
	mask    uint8
}

func newLexical(query string, aspects, expansions []string) *lexical {
	l := &lexical{index: map[string]int{}}
	add := func(text string, weight float64, mask uint8) {
		anchors := map[string]bool{}
		for _, word := range strings.FieldsFunc(reader.Clip(text, 1024), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '+' || r == '#' || r == '.' || r == '-')
		}) {
			word = strings.Trim(word, "._-")
			technical := strings.ContainsAny(word, "+#_.")
			upper, lower := false, false
			for i := range len(word) {
				if word[i] >= 'A' && word[i] <= 'Z' {
					upper = true
					if i > 0 && word[i-1] >= 'a' && word[i-1] <= 'z' {
						technical = true
					}
				}
				if word[i] >= 'a' && word[i] <= 'z' {
					lower = true
				}
			}
			if technical || upper && !lower && len(word) > 1 {
				anchors[strings.ToLower(word)] = true
			}
		}
		scanTerms(reader.Clip(text, 1024), func(term string) {
			idx, ok := l.index[term]
			if !ok {
				if len(l.terms) >= maxQueryTerms {
					return
				}
				idx = len(l.terms)
				l.index[term] = idx
				w := 1 + .35*math.Log1p(float64(utf8.RuneCountInString(term)))
				if anchors[term] {
					w *= 1.8
				}
				if utf8.RuneCountInString(term) == 1 {
					if r, _ := utf8.DecodeRuneInString(term); unicode.Is(unicode.Han, r) {
						w = .25
					}
				}
				l.terms = append(l.terms, lexicalTerm{text: term, weight: w * weight})
			}
			l.terms[idx].aspects |= mask
		})
	}
	add(query, 1, 0)
	for i, a := range aspects[:min(4, len(aspects))] {
		add(a, .8, 1<<i)
	}
	for _, q := range expansions[:min(5, len(expansions))] {
		add(q, .45, 0)
	}
	for _, t := range l.terms {
		for i := range 4 {
			if t.aspects&(1<<i) != 0 {
				l.aspectWeights[i] += t.weight
			}
		}
	}
	return l
}

func (l *lexical) vector(text string, scratch []uint16) lexicalVector {
	clear(scratch)
	v := lexicalVector{}
	scanTerms(text, func(term string) {
		v.length++
		if i, ok := l.index[term]; ok && scratch[i] < 65535 {
			scratch[i]++
		}
	})
	for i, n := range scratch {
		if n > 0 {
			v.matches = append(v.matches, frequency{uint16(i), n})
		}
	}
	var coverage [4]float64
	for _, f := range v.matches {
		t := l.terms[f.term]
		for i := range 4 {
			if t.aspects&(1<<i) != 0 {
				coverage[i] += t.weight
			}
		}
	}
	for i, w := range l.aspectWeights {
		if w > 0 && coverage[i]/w >= .45 {
			v.mask |= 1 << i
		}
	}
	return v
}

func (l *lexical) scores(vectors []lexicalVector) []float64 {
	frequency := make([]int, len(l.terms))
	average := 0.0
	for _, v := range vectors {
		average += float64(v.length)
		for _, f := range v.matches {
			frequency[f.term]++
		}
	}
	average /= float64(max(1, len(vectors)))
	average = math.Max(1, average)
	idf := make([]float64, len(l.terms))
	for i, n := range frequency {
		idf[i] = math.Log1p((float64(len(vectors)-n)+.5)/(float64(n)+.5)) * l.terms[i].weight
	}
	scores := make([]float64, len(vectors))
	for i, v := range vectors {
		norm := 1.2 * (.25 + .75*float64(v.length)/average)
		for _, f := range v.matches {
			tf := float64(f.count)
			scores[i] += idf[f.term] * tf * 2.2 / (tf + norm)
		}
	}
	return scores
}

func relevance(query, text string) float64 {
	l := newLexical(query, nil, nil)
	v := l.vector(text, make([]uint16, len(l.terms)))
	return l.scores([]lexicalVector{v})[0]
}

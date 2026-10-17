package research

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"searchan/reader"
)

func TestProseFormsMatchCommonEnglishVariantsWithoutRewritingTerms(t *testing.T) {
	for _, tc := range []struct {
		word  string
		forms []string
	}{
		{"reuse", []string{"reuses", "reused", "reusing"}},
		{"cache", []string{"caches", "cached", "caching"}},
		{"query", []string{"queries", "queried", "querying"}},
		{"retry", []string{"retries", "retried", "retrying"}},
		{"watch", []string{"watches", "watched", "watching"}},
	} {
		l := newLexical(tc.word, nil, nil)
		before := append([]lexicalTerm(nil), l.terms...)
		l.expandProseForms(tc.word, nil, nil)
		if !reflect.DeepEqual(before, l.terms) {
			t.Fatal("forms changed query weights or canonical terms")
		}
		for _, form := range tc.forms {
			v := l.vector(form, make([]uint16, len(l.terms)))
			if len(v.matches) != 1 || l.terms[v.matches[0].term].text != tc.word {
				t.Fatalf("variant not matched: %s %+v", form, v)
			}
		}
	}
}

func TestProseFormsProtectIdentifiersQuotesAndExactKeysAcrossQueries(t *testing.T) {
	for _, q := range []string{"reuse()", "reuse ()", "reuse\n()", "http.reuse", "reuse_cache", "Reuse", "REUSE", "`reuse`", `"reuse"`, "'reuse'", "net/reuse", "--reuse", ".reuse", "_reuse", "#reuse", "<reuse>"} {
		l := newLexical(q, nil, []string{"reuse"})
		l.expandProseForms(q, nil, []string{"reuse"})
		if _, ok := l.index["reused"]; ok {
			t.Fatal("literal or symbol received aliases", q)
		}
	}
	l := newLexical("reuse Reused v1.2.3", nil, nil)
	exact := l.index["reused"]
	l.expandProseForms("reuse Reused v1.2.3", nil, nil)
	if l.index["reused"] != exact || l.terms[exact].text != "reused" {
		t.Fatal("alias overwrote an exact query key")
	}
	if _, ok := l.index["v1.2.3s"]; ok {
		t.Fatal("version received a word form")
	}
	numbered := newLexical("reuse2", nil, []string{"reuse"})
	key := numbered.index["reuse2"]
	numbered.expandProseForms("reuse2", nil, []string{"reuse"})
	if numbered.index["reuse2"] != key {
		t.Fatal("numbered identifier changed")
	}
	if _, ok := numbered.index["reuse2s"]; ok {
		t.Fatal("numbered identifier received aliases")
	}
	for _, q := range []string{"only", "never", "without", "unless", "cannot"} {
		l := newLexical(q, nil, nil)
		n := len(l.index)
		l.expandProseForms(q, nil, nil)
		if len(l.index) != n {
			t.Fatal("logical word expanded", q)
		}
	}
}

func TestProseFormsAreBoundedByExistingQueryVocabulary(t *testing.T) {
	q := strings.Repeat("reuse cache query watch transmit respond ", 300)
	l := newLexical(q, []string{q, q, q, q}, []string{q, q, q, q, q})
	n := len(l.terms)
	l.expandProseForms(q, []string{q, q, q, q}, []string{q, q, q, q, q})
	if len(l.terms) != n || len(l.index) > 4*n {
		t.Fatalf("unbounded aliases: %d terms %d keys", n, len(l.index))
	}
	for _, i := range l.index {
		if i < 0 || i >= len(l.terms) {
			t.Fatal("alias exceeds vector bounds")
		}
	}
	// Without expansions, quoted prose and an already plural-looking token
	// remain literal; this is deliberately not a universal stemming algorithm.
	for _, q := range []string{`"shared connection reuse"`, "connections"} {
		l := newLexical(q, nil, nil)
		n := len(l.index)
		l.expandProseForms(q, nil, nil)
		if len(l.index) != n {
			t.Fatal("literal/root was guessed", q)
		}
	}
}

func TestEvidenceCanFindInflectedContractWithoutInventingText(t *testing.T) {
	contract := "Connections may be reused when the existing pool is available. Cached sessions avoid creating another transport."
	doc := reader.MakeDocument("https://manual.example/pool", "Connection pool", contract, "fixture")
	ev := SelectEvidenceForPlan(Plan{Queries: []string{"reuse cache sessions"}}, []reader.Document{doc}, nil, 1)
	if len(ev) != 1 || ev[0].Text != contract || ev[0].DocumentHash != doc.Hash {
		t.Fatal("word matching changed evidence text or provenance", ev)
	}
	// Helpers used for identity checks and document discovery stay exact.
	l := newLexical("reuse", nil, nil)
	if v := l.vector("reused", make([]uint16, len(l.terms))); len(v.matches) != 0 {
		t.Fatal("prose aliases escaped evidence ranking")
	}
}

func TestProseFormIndexRespectsFullVocabularyCap(t *testing.T) {
	group := func(offset int) string {
		var b strings.Builder
		for i := offset; i < offset+40; i++ {
			fmt.Fprintf(&b, "match%c%c ", 'a'+rune(i/26), 'a'+rune(i%26))
		}
		return b.String()
	}
	aspects := []string{group(40), group(80), group(120), group(160)}
	l := newLexical(group(0), aspects, nil)
	if len(l.terms) != maxQueryTerms {
		t.Fatal("fixture did not reach vocabulary cap", len(l.terms))
	}
	l.expandProseForms(group(0), aspects, nil)
	if len(l.terms) != maxQueryTerms || len(l.index) > 4*maxQueryTerms {
		t.Fatal("alias index exceeded cap", len(l.index))
	}
	for _, i := range l.index {
		if i < 0 || i >= maxQueryTerms {
			t.Fatal("alias exceeds scratch capacity", i)
		}
	}
}

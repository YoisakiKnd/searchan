// Package evaluation replays bounded offline cases or saved research outputs.
// Its metrics measure annotated source ranking and literal phrase coverage,
// never model truthfulness or general web-search quality.
package evaluation

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"searchan/internal/httpx"
	"searchan/reader"
	"searchan/research"
	"searchan/search"
)

type Document struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}
type Judgment struct {
	URL     string   `json:"url"`
	Aliases []string `json:"aliases,omitempty"`
	Grade   int      `json:"grade"`
}
type Case struct {
	ID              string     `json:"id"`
	Query           string     `json:"query"`
	Aspects         []string   `json:"aspects,omitempty"`
	Limit           int        `json:"limit,omitempty"`
	Documents       []Document `json:"documents"`
	Judgments       []Judgment `json:"judgments"`
	RequiredPhrases []string   `json:"required_phrases,omitempty"`
}
type Dataset struct {
	Version     int    `json:"version"`
	Description string `json:"description"`
	Cases       []Case `json:"cases"`
}
type Scores struct {
	SourceNDCG           float64 `json:"source_ndcg"`
	SourceRecall         float64 `json:"source_recall"`
	RequiredPhraseRecall float64 `json:"required_phrase_recall"`
}
type CaseReport struct {
	ID             string   `json:"id"`
	Limit          int      `json:"limit"`
	Sources        []string `json:"sources"`
	Scores         Scores   `json:"scores"`
	MissingPhrases []string `json:"missing_phrases,omitempty"`
	RetrievalNDCG  *float64 `json:"retrieval_ndcg_at_20,omitempty"`
}
type Report struct {
	Mode  string       `json:"mode"`
	Note  string       `json:"note"`
	Cases []CaseReport `json:"cases"`
	Mean  Scores       `json:"mean"`
}

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func Load(path string) (Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Dataset{}, err
	}
	defer f.Close()
	var d Dataset
	if err := httpx.Decode(f, 8<<20, &d); err != nil {
		return d, err
	}
	if d.Version != 1 || len(d.Cases) == 0 || len(d.Cases) > 256 {
		return d, errors.New("invalid evaluation dataset")
	}
	seen := map[string]bool{}
	for _, c := range d.Cases {
		if !validID.MatchString(c.ID) || seen[c.ID] || c.Query == "" || len(c.Query) > 8192 || len(c.Aspects) > 4 || len(c.Documents) > 64 || c.Limit < 0 || c.Limit > 24 || len(c.RequiredPhrases) > 32 || len(c.Judgments) > 128 {
			return d, errors.New("invalid evaluation case")
		}
		seen[c.ID] = true
		aliases := map[string]bool{}
		for _, j := range c.Judgments {
			if j.Grade < 0 || j.Grade > 3 || len(j.Aliases) > 16 {
				return d, errors.New("invalid relevance grade")
			}
			for _, u := range append([]string{j.URL}, j.Aliases...) {
				key := search.DocumentURL(u)
				if key == "" || aliases[key] {
					return d, errors.New("invalid or overlapping source judgment")
				}
				aliases[key] = true
			}
		}
		for _, doc := range c.Documents {
			if search.DocumentURL(doc.URL) == "" || len(doc.Text) > reader.MaxText {
				return d, errors.New("invalid evaluation document")
			}
		}
		for _, phrase := range c.RequiredPhrases {
			if strings.TrimSpace(phrase) == "" || len(phrase) > 2048 {
				return d, errors.New("invalid required phrase")
			}
		}
	}
	return d, nil
}
func normalized(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func Run(d Dataset, replayDir string, limit int) (Report, error) {
	if limit < 1 || limit > 24 {
		return Report{}, errors.New("evaluation limit must be 1-24")
	}
	r := Report{Mode: "synthetic-evidence-replay", Note: "Development fixtures; source relevance and literal phrase coverage do not establish factual correctness or live retrieval quality."}
	if replayDir != "" {
		r.Mode = "saved-output-replay"
	}
	for _, c := range d.Cases {
		if !validID.MatchString(c.ID) {
			return r, errors.New("invalid replay case ID")
		}
		k := limit
		if c.Limit > 0 {
			k = c.Limit
		}
		var ev []research.Evidence
		var retrieval []research.RetrievedHit
		if replayDir != "" {
			f, err := os.Open(filepath.Join(replayDir, c.ID+".json"))
			if err != nil {
				return r, err
			}
			var out research.Output
			err = httpx.Decode(f, 1<<20, &out)
			f.Close()
			if err != nil {
				return r, err
			}
			if normalized(out.Query) != normalized(c.Query) {
				return r, fmt.Errorf("query mismatch in replay %s", c.ID)
			}
			ev = out.Evidence
			retrieval = out.Retrieval
		} else {
			var docs []reader.Document
			for _, doc := range c.Documents {
				docs = append(docs, reader.MakeDocument(doc.URL, doc.Title, doc.Text, "evaluation-fixture"))
			}
			ev = research.SelectEvidenceForPlan(research.Plan{Queries: []string{c.Query}, Aspects: c.Aspects}, docs, nil, k)
		}
		var urls []string
		var text strings.Builder
		for _, e := range ev[:min(k, len(ev))] {
			urls = append(urls, e.URL)
			text.WriteString(e.Text)
			text.WriteByte('\n')
		}
		ndcg, recall, sources := sourceScores(urls, c.Judgments, k)
		cr := CaseReport{ID: c.ID, Limit: k, Sources: sources, Scores: Scores{SourceNDCG: ndcg, SourceRecall: recall}}
		body := normalized(text.String())
		matched := 0
		for _, phrase := range c.RequiredPhrases {
			if strings.Contains(body, normalized(phrase)) {
				matched++
			} else {
				cr.MissingPhrases = append(cr.MissingPhrases, phrase)
			}
		}
		if len(c.RequiredPhrases) > 0 {
			cr.Scores.RequiredPhraseRecall = float64(matched) / float64(len(c.RequiredPhrases))
		} else {
			cr.Scores.RequiredPhraseRecall = 1
		}
		if len(retrieval) > 0 {
			var ranked []string
			for _, h := range retrieval {
				ranked = append(ranked, h.URL)
			}
			v, _, _ := sourceScores(ranked, c.Judgments, 20)
			cr.RetrievalNDCG = &v
		}
		r.Cases = append(r.Cases, cr)
		r.Mean.SourceNDCG += ndcg
		r.Mean.SourceRecall += recall
		r.Mean.RequiredPhraseRecall += cr.Scores.RequiredPhraseRecall
	}
	if len(r.Cases) > 0 {
		n := float64(len(r.Cases))
		r.Mean.SourceNDCG /= n
		r.Mean.SourceRecall /= n
		r.Mean.RequiredPhraseRecall /= n
	}
	return r, nil
}

func sourceScores(urls []string, judgments []Judgment, k int) (float64, float64, []string) {
	aliases := map[string]int{}
	var ideal []int
	relevant := 0
	for i, j := range judgments {
		for _, raw := range append([]string{j.URL}, j.Aliases...) {
			aliases[search.DocumentURL(raw)] = i
		}
		ideal = append(ideal, j.Grade)
		if j.Grade > 0 {
			relevant++
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ideal)))
	seen := map[string]bool{}
	hit := 0
	dcg := 0.0
	var sources []string
	for _, raw := range urls {
		url := search.DocumentURL(raw)
		grade := 0
		key := url
		if i, ok := aliases[url]; ok {
			key = fmt.Sprint("judgment:", i)
			grade = judgments[i].Grade
		}
		if url == "" || seen[key] {
			continue
		}
		seen[key] = true
		if len(sources) >= k {
			break
		}
		sources = append(sources, raw)
		if grade > 0 {
			hit++
		}
		dcg += (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(len(sources)+1))
	}
	idcg := 0.0
	for i, g := range ideal[:min(k, len(ideal))] {
		idcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	ndcg, recall := 0.0, 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	if relevant > 0 {
		recall = float64(hit) / float64(relevant)
	}
	return ndcg, recall, sources
}

package evaluation

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"searchan/internal/fsutil"
	"searchan/research"
)

func TestSourceMetricsAndAliasDedup(t *testing.T) {
	judgments := []Judgment{{URL: "https://a.example/", Aliases: []string{"https://mirror.example/"}, Grade: 3}, {URL: "https://b.example/", Grade: 2}}
	n, r, s := sourceScores([]string{"https://a.example/#one", "https://mirror.example/", "https://b.example/"}, judgments, 2)
	if n != 1 || r != 1 || len(s) != 2 {
		t.Fatalf("alias counted as independent relevance: n=%g r=%g sources=%v", n, r, s)
	}
	n, r, _ = sourceScores([]string{"https://wrong.example/", "https://a.example/"}, judgments, 2)
	want := (7 / math.Log2(3)) / (7 + 3/math.Log2(3))
	if math.Abs(n-want) > 1e-12 || r != .5 {
		t.Fatalf("incorrect metric: %g %g", n, r)
	}
}

func TestDevelopmentFixtures(t *testing.T) {
	d, err := Load("cases.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(d, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Cases {
		if len(c.MissingPhrases) > 0 || c.Scores.SourceNDCG < .9 {
			t.Errorf("fixture regression %s: %+v", c.ID, c)
		}
	}
}

func TestReplayQueryAndDatasetValidation(t *testing.T) {
	d := Dataset{Version: 1, Cases: []Case{{ID: "test", Query: "question", Judgments: []Judgment{{URL: "https://a.example/", Grade: 3}}, RequiredPhrases: []string{"fact"}}}}
	dir := t.TempDir()
	if err := fsutil.WriteJSON(filepath.Join(dir, "test.json"), research.Output{Query: "question", Evidence: []research.Evidence{{URL: "https://a.example/", Text: "fact"}}, Retrieval: []research.RetrievedHit{{URL: "https://a.example/"}}}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(d, dir, 5)
	if err != nil || r.Cases[0].RetrievalNDCG == nil || *r.Cases[0].RetrievalNDCG != 1 {
		t.Fatalf("saved output replay failed: %+v %v", r, err)
	}
	d.Cases[0].Query = "different question"
	if _, err := Run(d, dir, 5); err == nil {
		t.Fatal("wrong output scored")
	}
	file := filepath.Join(dir, "invalid.json")
	_ = os.WriteFile(file, []byte(`{"version":1,"cases":[{"id":"../escape","query":"question"}]}`), 0600)
	if _, err := Load(file); err == nil {
		t.Fatal("path traversal case ID accepted")
	}
}

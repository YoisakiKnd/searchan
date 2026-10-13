package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBatchStopsOnlyForConsecutiveSearchOutages(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		middle                string
		wantCases, wantModels int
		continueOnUnavailable bool
		totalCases            int
	}{
		{"continuous-outage", "", 2, 0, false, 5},
		{"success-resets", "success", 4, 1, false, 5},
		{"empty-result-resets", "empty", 4, 0, false, 5},
		{"explicit-continue", "", 5, 0, true, 5},
		{"outage-at-end", "", 2, 0, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var searches, models atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/search" {
					models.Add(1)
					body := `{"answers_query":true,"title":"Atlas","lead":{"text":"Atlas supports data access for applications.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`
					fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, body)
					return
				}
				searches.Add(1)
				var request struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if strings.Contains(request.Query, "case 1") && tc.middle != "" {
					if tc.middle == "success" {
						io.WriteString(w, `{"results":[{"title":"Atlas","url":"https://example.org/atlas","text":"Atlas supports data access for applications and provides a consistent repository interface."}]}`)
					} else {
						io.WriteString(w, `{"results":[]}`)
					}
					return
				}
				http.Error(w, "TOPSECRET-TEST upstream body", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			env := map[string]string{"SEARCHAN_PROVIDERS": "exa", "EXA_ENDPOINT": srv.URL + "/search", "EXA_API_KEY": "TOPSECRET-TEST", "MODEL_ENDPOINT": srv.URL + "/model", "MODEL_NAME": "fixture", "MODEL_API_KEY": "TOPSECRET-TEST", "MODEL_RETRIES": "0", "SEARCHAN_MODEL_PLAN": "false", "SEARCHAN_MODEL_VERIFY": "false", "SEARCHAN_SEARCH_CALLS": "1"}
			var cases []Case
			for i := range tc.totalCases {
				cases = append(cases, Case{ID: fmt.Sprintf("case-%d", i), Query: fmt.Sprintf("Atlas availability case %d", i), Split: "pilot", Checks: []string{"Answers the question"}})
			}
			out := filepath.Join(t.TempDir(), "run")
			r, err := Run(context.Background(), Options{Suite: Suite{Version: 1, Cases: cases}, Cases: cases, Out: out, ContinueOnUnavailable: tc.continueOnUnavailable}, func(k string) string { return env[k] }, io.Discard)
			if err == nil || len(r.Cases) != tc.wantCases || int(searches.Load()) != tc.wantCases || int(models.Load()) != tc.wantModels {
				t.Fatalf("outage did not stop or reset correctly: cases=%d search=%d model=%d error=%v", len(r.Cases), searches.Load(), models.Load(), err)
			}
			var saved struct {
				Selected []string `json:"selected_cases"`
				Unrun    []string `json:"unrun_cases"`
				Reason   string   `json:"stopped_reason"`
			}
			b, err := os.ReadFile(filepath.Join(out, "report.json"))
			wantReason := ""
			if tc.wantCases < tc.totalCases {
				wantReason = "consecutive_search_unavailable"
			}
			if err != nil || json.Unmarshal(b, &saved) != nil || len(saved.Selected) != tc.totalCases || len(saved.Unrun) != tc.totalCases-tc.wantCases || saved.Reason != wantReason || strings.Contains(string(b), "TOPSECRET") {
				t.Fatal("batch status missing or unsafe", string(b), err)
			}
			for _, id := range saved.Unrun {
				if _, err := os.Stat(filepath.Join(out, id)); !os.IsNotExist(err) {
					t.Fatal("unrun case has fabricated output", id, err)
				}
			}
			summary, err := SummarizeReview(out)
			if err != nil || summary.Total != tc.wantCases || summary.Pending != tc.wantCases {
				t.Fatal("unrun cases entered factual review", summary, err)
			}
		})
	}
}

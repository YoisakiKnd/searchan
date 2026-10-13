package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"searchan/internal/fsutil"
	"searchan/internal/httpx"
	"searchan/reader"
	"searchan/research"
	"searchan/search"
)

func TestSuiteSelectionAndPathSafety(t *testing.T) {
	s, err := Load("cases.json")
	if err != nil || len(s.Cases) != 30 {
		t.Fatalf("suite: %d %v", len(s.Cases), err)
	}
	selected, err := Select(s, "springdata-intro", "all", 30)
	if err != nil || len(selected) != 1 {
		t.Fatalf("selection spilled into unrelated cases: %+v %v", selected, err)
	}
	for _, ids := range []string{"../secret", "missing", "springdata-intro,missing"} {
		if _, err := Select(s, ids, "all", 30); err == nil {
			t.Fatal("unknown selection accepted", ids)
		}
	}
	s.Cases[0].ID = "../secret"
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := fsutil.WriteJSON(p, s); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("unsafe case ID accepted")
	}
}

func TestEnvironmentIsDataAndDoesNotExposeSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private.env")
	contents := "export MODEL_NAME='literal $(touch /tmp/do-not-execute)'\nMODEL_API_KEY=TOPSECRET-TEST\nMODEL_ENDPOINT=\"https://example.org/v1\"\nSEARCHAN_PROVIDERS=ddg # comment\nTELEGRAM_BOT_TOKEN=TOPSECRET-TEST\n"
	if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	env, err := Environment(p, func(k string) string {
		if k == "MODEL_NAME" {
			return "ambient"
		}
		return "inherited"
	})
	if err != nil {
		t.Fatal(err)
	}
	if env("MODEL_NAME") != "literal $(touch /tmp/do-not-execute)" || env("SEARCHAN_PROVIDERS") != "ddg" || env("TELEGRAM_BOT_TOKEN") != "" || env("QQ_BACKEND") != "" || env("UNSET") != "inherited" {
		t.Fatal("unsafe env loading or precedence")
	}
	for _, content := range []string{"BAD KEY=TOPSECRET-TEST", "MODEL_API_KEY='TOPSECRET-TEST", "MODEL_API_KEY=TOPSECRET-TEST\nMODEL_API_KEY=TOPSECRET-TEST"} {
		os.WriteFile(p, []byte(content), 0600)
		_, err := Environment(p, func(string) string { return "" })
		if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
			t.Fatal("invalid env accepted or secret in error", err)
		}
	}
}

type countedProvider struct{ calls atomic.Int32 }

func (p *countedProvider) Name() string { return "fixture" }
func (p *countedProvider) Search(_ context.Context, q search.Request) (search.Response, error) {
	p.calls.Add(1)
	return search.Response{Provider: "fixture", Results: []search.Result{{Title: q.Query, URL: "https://example.org/atlas", Content: "Atlas provides data access repository abstractions for application developers."}}}, nil
}

type countedReader struct{ calls atomic.Int32 }

func (r *countedReader) Read(_ context.Context, u string) (reader.Document, error) {
	r.calls.Add(1)
	return reader.MakeDocument(u, "Atlas", "Atlas provides data access repository abstractions for application developers.", "http"), nil
}

func TestConcurrentSnapshotReplayNeverCallsLiveAdapters(t *testing.T) {
	p, r := &countedProvider{}, &countedReader{}
	tape := Record()
	cfg := research.Config{Providers: []search.Provider{p}, Reader: &reader.Cache{Next: r}, FallbackReader: r}
	tape.Wrap(&cfg)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := search.Request{Query: fmt.Sprintf("Atlas %d", i), Limit: 10}
			if _, err := cfg.Providers[0].Search(context.Background(), q); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	url := "https://example.org/atlas"
	doc, _ := cfg.Reader.Read(context.Background(), url)
	cfg.FallbackReader.Read(context.Background(), url)
	replay, err := Replay(tape.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	replayCfg := research.Config{Providers: []search.Provider{p}, Reader: r, FallbackReader: r}
	replay.Wrap(&replayCfg)
	resp, err := replayCfg.Providers[0].Search(context.Background(), search.Request{Query: "Atlas 5", Limit: 10})
	if err != nil || len(resp.Results) != 1 || resp.Results[0].Title != "Atlas 5" {
		t.Fatalf("replay lost response: %+v %v", resp, err)
	}
	got, err := replayCfg.Reader.Read(context.Background(), url)
	if err != nil || !reflect.DeepEqual(got, doc) {
		t.Fatal("replay lost document", err)
	}
	replayCfg.FallbackReader.Read(context.Background(), url)
	if _, err := replayCfg.Providers[0].Search(context.Background(), search.Request{Query: "unseen"}); !errors.Is(err, ErrSnapshotMiss) {
		t.Fatal("missing snapshot went live", err)
	}
	if p.calls.Load() != 12 || r.calls.Load() != 2 || replay.Misses() != 1 {
		t.Fatal("replay performed live I/O")
	}
	tape.save("direct", "huge", strings.Repeat("x", (2<<20)+1), nil)
	if tape.Snapshot().Complete {
		t.Fatal("snapshot silently truncated")
	}
	if _, err := Replay(tape.Snapshot()); err == nil {
		t.Fatal("incomplete snapshot replay allowed")
	}
}

type fakeModel struct{ calls int }

func (m *fakeModel) Complete(_ context.Context, _ string, _ any, output any) error {
	m.calls++
	if p, ok := output.(*research.Plan); ok {
		*p = research.Plan{Intent: "fact", Queries: []string{"changed"}}
	}
	return nil
}

func TestReplayPreservesPlanAndFailureSemantics(t *testing.T) {
	tape := Record()
	plan := research.Plan{Intent: "fact", Queries: []string{"Atlas introduction"}, Aspects: []string{"definition"}}
	tape.SetPlan(plan)
	tape.save("direct", "https://example.org/bad", reader.Document{}, reader.ContentError("interstitial"))
	tape.save("search", `{"Provider":"fixture"}`, search.Response{}, &search.HTTPError{Provider: "fixture", StatusCode: 429})
	replay, err := Replay(tape.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeModel{}
	cfg := research.Config{Model: m, Providers: []search.Provider{&countedProvider{}}, Reader: &countedReader{}}
	replay.Wrap(&cfg)
	var proposed research.Plan
	if err := cfg.Model.Complete(context.Background(), "planner", nil, &proposed); err != nil || !reflect.DeepEqual(proposed, plan) || m.calls != 0 || replay.LiveModelCalls() != 0 {
		t.Fatal("replay replanned", proposed, err)
	}
	var answer research.Answer
	if err := cfg.Model.Complete(context.Background(), "answer", nil, &answer); err != nil || m.calls != 1 || replay.LiveModelCalls() != 1 {
		t.Fatal("answer not regenerated")
	}
	var doc reader.Document
	var content reader.ContentError
	if err := replay.load("direct", "https://example.org/bad", &doc); !errors.As(err, &content) || string(content) != "interstitial" {
		t.Fatal("reader failure type lost", err)
	}
	var resp search.Response
	var httpErr *search.HTTPError
	if err := replay.load("search", `{"Provider":"fixture"}`, &resp); !errors.As(err, &httpErr) || httpErr.StatusCode != 429 || httpErr.Provider != "fixture" {
		t.Fatal("provider pause semantics lost", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cfg.Model.Complete(ctx, "answer", nil, &answer); !errors.Is(err, context.Canceled) || m.calls != 1 {
		t.Fatal("cancelled replay continued", err)
	}
}

func TestModelDiagnosticsOnlyContainFixedErrorCategories(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("request: %w", syscall.ECONNRESET), "connection_reset"},
		{&httpx.StatusError{Code: 429, RetryAfter: "private-value"}, "http_429"},
		{context.DeadlineExceeded, "deadline"},
		{errors.New("model output was truncated; check output token and thinking budgets"), "output_incomplete"},
		{errors.New("invalid or oversized JSON response"), "invalid_json"},
		{errors.New("https://endpoint.example/private?key=TOPSECRET-TEST response-body TOPSECRET-TEST"), "model_error"},
	} {
		if got := modelFailureCode(tt.err); got != tt.want || strings.Contains(got, "private") || strings.Contains(got, "TOPSECRET") {
			t.Fatalf("unsafe diagnostic: %s", got)
		}
	}
}

func TestRunReplayAndManualReview(t *testing.T) {
	var searches, models atomic.Int32
	var breakModel, invalidCitation atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			searches.Add(1)
			io.WriteString(w, `{"results":[{"title":"Atlas","url":"https://example.org/atlas","text":"Atlas provides repository abstractions for data access in applications."}]}`)
			return
		}
		models.Add(1)
		if breakModel.Load() {
			io.WriteString(w, `{}`)
			return
		}
		body := `{"answers_query":true,"title":"Atlas","lead":{"text":"Atlas provides repository abstractions for data access.","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}`
		if invalidCitation.Load() {
			body = strings.Replace(body, `"e1"`, `"TOPSECRET-TEST private draft citation"`, 1)
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, body)
	}))
	defer srv.Close()
	env := map[string]string{"SEARCHAN_PROVIDERS": "exa", "EXA_ENDPOINT": srv.URL + "/search", "EXA_API_KEY": "TOPSECRET-TEST", "MODEL_ENDPOINT": srv.URL + "/model", "MODEL_NAME": "fixture", "MODEL_API_KEY": "TOPSECRET-TEST", "MODEL_RETRIES": "0", "SEARCHAN_MODEL_PLAN": "false", "SEARCHAN_MODEL_VERIFY": "false"}
	env["TYPST_BIN"] = os.Getenv("SEARCHAN_TEST_TYPST")
	getenv := func(k string) string { return env[k] }
	c := Case{ID: "atlas", Query: "Atlas introduction", Category: "fixture", Split: "pilot", Checks: []string{"Defines Atlas"}}
	suite := Suite{Version: 1, Cases: []Case{c}}
	root := t.TempDir()
	opt := Options{Suite: suite, Cases: []Case{c}, Out: filepath.Join(root, "live")}
	opt.Render = env["TYPST_BIN"] != ""
	r, err := Run(context.Background(), opt, getenv, io.Discard)
	if err != nil || r.Completed != 1 || r.Cases[0].Stats.ModelCalls != 1 || searches.Load() != 1 || models.Load() != 1 {
		t.Fatalf("live fixture run failed: %+v %v", r, err)
	}
	if opt.Render {
		if len(r.Cases[0].Images) != 1 {
			t.Fatal("missing actual Typst image")
		}
		info, err := os.Stat(r.Cases[0].Images[0].Path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("image not private", err)
		}
	}
	for _, file := range []string{"report.json", "review.json", "suite.json", "atlas/output.json", "atlas/snapshot.json", "atlas/review.md"} {
		b, err := os.ReadFile(filepath.Join(opt.Out, file))
		if err != nil || strings.Contains(string(b), "TOPSECRET-TEST") {
			t.Fatal("artifact missing or credential leaked", file, err)
		}
		info, _ := os.Stat(filepath.Join(opt.Out, file))
		if info.Mode().Perm() != 0600 {
			t.Fatal("artifact not private", file)
		}
	}
	summary, err := SummarizeReview(opt.Out)
	if err != nil || summary.Reviewed != 0 || summary.Pending != 1 {
		t.Fatal("pending review silently scored", summary, err)
	}
	var review ReviewFile
	readJSON(filepath.Join(opt.Out, "review.json"), 1<<20, &review)
	yes, no := true, false
	x := &review.Cases[0]
	x.Reviewer, x.CoreAnswered, x.EntityCorrect, x.UnnecessaryRefusal = "fixture-human", &yes, &yes, &no
	x.FactualVerdict = "pass"
	x.Checks[0].Verdict, x.Claims[0].Verdict = "pass", "supported"
	fsutil.WriteJSON(filepath.Join(opt.Out, "review.json"), review)
	summary, err = SummarizeReview(opt.Out)
	if err != nil || summary.Pending != 1 || summary.Reviewed != 0 {
		t.Fatal("unreviewed title silently scored", summary, err)
	}
	if review.Version != 2 || len(x.UncitedText) != 1 || x.UncitedText[0].Location != "title" {
		t.Fatal("title missing from review", review)
	}
	x.UncitedText[0].Verdict = "pass"
	fsutil.WriteJSON(filepath.Join(opt.Out, "review.json"), review)
	summary, err = SummarizeReview(opt.Out)
	if err != nil || summary.Reviewed != 1 || summary.CoreAnswered != 1 || summary.SupportedStatements != 1 || summary.FullTextReviewed != 1 || summary.UncitedTextPassed != 1 || summary.LegacyBodyOnly != 0 {
		t.Fatal("manual review not counted", summary, err)
	}
	x.Claims[0].EvidenceIDs = []string{"e999"}
	fsutil.WriteJSON(filepath.Join(opt.Out, "review.json"), review)
	if _, err := SummarizeReview(opt.Out); err == nil {
		t.Fatal("modified citation review accepted")
	}
	opt.Replay, opt.Out = opt.Out, filepath.Join(root, "replay")
	r, err = Run(context.Background(), opt, getenv, io.Discard)
	if err != nil || r.Completed != 1 || searches.Load() != 1 || models.Load() != 2 || r.Mode != "snapshot-model-live" {
		t.Fatal("snapshot replay went live or failed", r, err)
	}
	fixedOpt := Options{Suite: suite, Cases: []Case{c}, Out: filepath.Join(root, "fixed"), FixedEvidence: opt.Replay, Render: opt.Render}
	r, err = Run(context.Background(), fixedOpt, getenv, io.Discard)
	if err != nil || r.Completed != 1 || searches.Load() != 1 || models.Load() != 3 || r.Mode != "evidence-model-live" || r.Cases[0].Stats.SearchCalls != 0 || r.Cases[0].Stats.PagesRead != 0 || r.Cases[0].Stats.ModelCalls != 1 {
		t.Fatal("fixed evidence performed retrieval or lost writing calls", r, err)
	}
	var frozen research.SynthesisInput
	if err := readJSON(filepath.Join(fixedOpt.Out, "atlas/synthesis_input.json"), 2<<20, &frozen); err != nil || hashJSON(frozen) != r.Cases[0].EvidenceInputHash || r.Cases[0].SourceAnswerHash == "" {
		t.Fatal("fixed evidence input provenance missing", err)
	}
	var fixedOutput, original research.Output
	readJSON(filepath.Join(fixedOpt.Out, "atlas/output.json"), 2<<20, &fixedOutput)
	readJSON(filepath.Join(opt.Replay, "atlas/output.json"), 2<<20, &original)
	if !reflect.DeepEqual(fixedOutput.Evidence, original.Evidence) || !reflect.DeepEqual(fixedOutput.Plan, original.Plan) || !fixedOutput.GeneratedAt.Equal(original.GeneratedAt) {
		t.Fatal("fixed evidence changed passages, IDs, plan or reference date")
	}
	original.Evidence[0].Text += " altered after recording"
	fsutil.WriteJSON(filepath.Join(opt.Replay, "atlas/output.json"), original)
	fixedOpt.Out = filepath.Join(root, "changed-fixed-source")
	if _, err := Run(context.Background(), fixedOpt, getenv, io.Discard); err == nil || models.Load() != 3 || searches.Load() != 1 {
		t.Fatal("changed evidence source reached model")
	}
	if _, err := os.Stat(fixedOpt.Out); !os.IsNotExist(err) {
		t.Fatal("changed source created a run directory")
	}
	fixedOpt.Replay = opt.Replay
	if _, err := Run(context.Background(), fixedOpt, getenv, io.Discard); err == nil || models.Load() != 3 {
		t.Fatal("ambiguous replay modes accepted")
	}
	var snap Snapshot
	readJSON(filepath.Join(opt.Replay, "atlas/snapshot.json"), 20<<20, &snap)
	for i := range snap.Exchanges {
		snap.Exchanges[i].Key = "different-query"
	}
	fsutil.WriteJSON(filepath.Join(opt.Replay, "atlas/snapshot.json"), snap)
	opt.Out = filepath.Join(root, "miss")
	r, err = Run(context.Background(), opt, getenv, io.Discard)
	if err == nil || r.Incomplete != 1 || r.Cases[0].SnapshotMisses == 0 || searches.Load() != 1 {
		t.Fatal("snapshot miss masked", r, err)
	}
	breakModel.Store(true)
	opt.Replay, opt.Out = "", filepath.Join(root, "failed")
	r, err = Run(context.Background(), opt, getenv, io.Discard)
	if err == nil || r.Failures != 1 || r.Cases[0].Error != "generation" {
		t.Fatal("generation failure lost", r, err)
	}
	var saved research.Output
	if err := readJSON(filepath.Join(opt.Out, "atlas/output.json"), 2<<20, &saved); err != nil || len(saved.Evidence) == 0 {
		t.Fatal("failed output not retained", err)
	}
	if _, err := Run(context.Background(), opt, getenv, io.Discard); err == nil {
		t.Fatal("existing run overwritten")
	}
	breakModel.Store(false)
	invalidCitation.Store(true)
	opt.Out = filepath.Join(root, "invalid-citation")
	var progress strings.Builder
	r, err = Run(context.Background(), opt, getenv, &progress)
	if err == nil || r.Failures != 1 || r.Cases[0].Error != "answer_validation" || r.Cases[0].ValidationCode != "unknown_citation" {
		t.Fatal("validation diagnostic lost", r, err)
	}
	for _, path := range []string{"report.json", "atlas/output.json", "atlas/review.md"} {
		data, readErr := os.ReadFile(filepath.Join(opt.Out, path))
		if readErr != nil || strings.Contains(string(data), "TOPSECRET-TEST") {
			t.Fatal("validation diagnostic leaked failed draft", path, readErr)
		}
	}
	if !strings.Contains(progress.String(), "validation=unknown_citation") || strings.Contains(progress.String(), "TOPSECRET-TEST") {
		t.Fatal("unsafe diagnostic progress", progress.String())
	}
	// Fixture quality is not an empirical score for real model accuracy.
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "TOPSECRET") {
		t.Fatal("report secret")
	}
}

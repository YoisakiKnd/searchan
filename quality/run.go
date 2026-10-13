package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
	"unicode/utf8"

	"searchan/app"
	"searchan/internal/fsutil"
	"searchan/model"
	"searchan/render"
	"searchan/research"
)

type Options struct {
	Suite                 Suite
	Cases                 []Case
	Out                   string
	Replay                string
	FixedEvidence         string
	Render                bool
	Label                 string
	ContinueOnUnavailable bool
}

type Result struct {
	ID                string                   `json:"id"`
	Query             string                   `json:"query"`
	State             string                   `json:"state"`
	ValidationCode    string                   `json:"validation_code,omitempty"`
	Error             string                   `json:"error,omitempty"`
	AnswerHash        string                   `json:"answer_hash"`
	EvidenceInputHash string                   `json:"evidence_input_hash,omitempty"`
	SourceAnswerHash  string                   `json:"source_answer_hash,omitempty"`
	AnswerMode        string                   `json:"answer_mode,omitempty"`
	AnswersQuery      *bool                    `json:"answers_query,omitempty"`
	FormatValid       bool                     `json:"format_valid"`
	SnapshotComplete  bool                     `json:"snapshot_complete"`
	SnapshotMisses    int                      `json:"snapshot_misses"`
	LiveModelCalls    int                      `json:"live_model_calls"`
	ModelErrors       []string                 `json:"model_errors,omitempty"`
	ModelOutputIssues []model.OutputDiagnostic `json:"model_output_issues,omitempty"`
	ModelRequests     []model.CallMetrics      `json:"model_requests,omitempty"`
	AnswerCharacters  int                      `json:"answer_characters"`
	Statements        int                      `json:"statements"`
	ElapsedMS         int64                    `json:"elapsed_ms"`
	Stats             research.Stats           `json:"stats"`
	Images            []render.Artifact        `json:"images,omitempty"`
	RenderError       string                   `json:"render_error,omitempty"`
}

type Report struct {
	Version        int               `json:"version"`
	Mode           string            `json:"mode"`
	Label          string            `json:"label,omitempty"`
	Started        time.Time         `json:"started"`
	SuiteHash      string            `json:"suite_hash"`
	ExecutableHash string            `json:"executable_hash"`
	GoVersion      string            `json:"go_version"`
	Settings       map[string]string `json:"settings"`
	Budget         Budget            `json:"budget"`
	Cases          []Result          `json:"cases"`
	Completed      int               `json:"completed"`
	Failures       int               `json:"failures"`
	Incomplete     int               `json:"incomplete"`
	P50MS          int64             `json:"p50_ms"`
	P95MS          int64             `json:"p95_ms"`
	SelectedCases  []string          `json:"selected_cases,omitempty"`
	UnrunCases     []string          `json:"unrun_cases,omitempty"`
	StoppedReason  string            `json:"stopped_reason,omitempty"`
	BatchPolicy    string            `json:"batch_policy,omitempty"`
}

type Budget struct {
	Providers         []string `json:"providers"`
	SearchCalls       int      `json:"search_calls"`
	Pages             int      `json:"pages"`
	Evidence          int      `json:"evidence"`
	SearchConcurrency int      `json:"search_concurrency"`
	ReadConcurrency   int      `json:"read_concurrency"`
	TaskTimeout       string   `json:"task_timeout"`
	ResearchTimeout   string   `json:"research_timeout"`
	SearchTimeout     string   `json:"search_timeout"`
	ReadTimeout       string   `json:"read_timeout"`
	ModelTimeout      string   `json:"model_timeout"`
	PlanningTimeout   string   `json:"planning_timeout"`
	Plan              bool     `json:"plan"`
	Verify            bool     `json:"verify"`
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func executableHash() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Run(ctx context.Context, opt Options, env func(string) string, progress io.Writer) (Report, error) {
	report := Report{Version: 1, Mode: "live", Label: opt.Label, Started: time.Now().UTC(), SuiteHash: hashJSON(opt.Suite), GoVersion: runtime.Version(), Settings: map[string]string{}}
	report.BatchPolicy = "stop_after_2_consecutive_search_unavailable"
	if opt.ContinueOnUnavailable {
		report.BatchPolicy = "continue_after_search_unavailable"
	}
	if opt.Replay != "" {
		report.Mode = "snapshot-model-live"
		report.Settings["REPLAY_PLANNING"] = "recorded-final-plan"
	}
	if opt.FixedEvidence != "" {
		report.Mode = "evidence-model-live"
		report.Settings["EVIDENCE_REFERENCE_TIME"] = "recorded-generated-at"
	}
	if opt.Replay != "" && opt.FixedEvidence != "" {
		return report, errors.New("replay and fixed evidence are mutually exclusive")
	}
	if len(opt.Cases) == 0 || len(opt.Cases) > 100 || opt.Out == "" {
		return report, errors.New("invalid quality run options")
	}
	known := map[string]Case{}
	for _, c := range opt.Suite.Cases {
		known[c.ID] = c
	}
	seen := map[string]bool{}
	for _, c := range opt.Cases {
		if !caseID.MatchString(c.ID) || seen[c.ID] || hashJSON(known[c.ID]) != hashJSON(c) {
			return report, errors.New("case selection does not match suite")
		}
		seen[c.ID] = true
		report.SelectedCases = append(report.SelectedCases, c.ID)
	}
	// Validate and hash every fixed input before any model request. A changed,
	// missing or incomplete source run cannot silently become a new baseline.
	fixed := map[string]research.SynthesisInput{}
	sourceHashes := map[string]string{}
	if opt.FixedEvidence != "" {
		var source Report
		if err := readJSON(filepath.Join(opt.FixedEvidence, "report.json"), 4<<20, &source); err != nil {
			return report, err
		}
		if source.Version != 1 || source.SuiteHash != report.SuiteHash || len(source.Cases) > 100 {
			return report, errors.New("fixed evidence suite does not match")
		}
		for _, c := range opt.Cases {
			var old research.Output
			if err := readJSON(filepath.Join(opt.FixedEvidence, c.ID, "output.json"), 2<<20, &old); err != nil {
				return report, err
			}
			hash := hashJSON(old)
			matches := 0
			for _, entry := range source.Cases {
				if entry.ID != c.ID {
					continue
				}
				matches++
				if entry.Query != c.Query || entry.State != "completed" || !entry.SnapshotComplete || entry.SnapshotMisses != 0 || entry.AnswerHash != hash {
					return report, errors.New("fixed evidence source is incomplete, changed or mismatched")
				}
			}
			if old.Query != c.Query || matches != 1 {
				return report, errors.New("fixed evidence source is incomplete, changed or mismatched")
			}
			fixed[c.ID] = research.SynthesisInput{Query: old.Query, Plan: old.Plan, Evidence: old.Evidence, GeneratedAt: old.GeneratedAt}
			sourceHashes[c.ID] = hash
		}
	}
	// Never persist the entire environment. Endpoint hashes identify endpoint
	// changes without writing credentials embedded in a trusted endpoint URL.
	for _, k := range []string{"MODEL_NAME", "MODEL_API", "MODEL_AUTH", "MODEL_MAX_TOKENS", "MODEL_JSON_MODE", "MODEL_TOKEN_PARAMETER", "MODEL_THINKING", "MODEL_THINKING_BUDGET", "MODEL_REASONING_EFFORT", "MODEL_RETRIES", "MODEL_ANTHROPIC_VERSION", "SEARXNG_ENGINES", "JINA_READER_ENABLED"} {
		report.Settings[k] = env(k)
	}
	for _, k := range []string{"MODEL_ENDPOINT", "DDG_ENDPOINT", "BRAVE_ENDPOINT", "TAVILY_ENDPOINT", "EXA_ENDPOINT", "SEARXNG_ENDPOINT", "JINA_ENDPOINT", "JINA_READER_ENDPOINT"} {
		report.Settings[k+"_SHA256"] = hashJSON(env(k))
	}
	var err error
	report.ExecutableHash, err = executableHash()
	if err != nil {
		return report, err
	}
	// Preflight before creating a run directory or issuing any requests.
	probe, err := app.New(env)
	if err != nil {
		return report, err
	}
	cfg := probe.Config
	missingModel := probe.Model == nil
	probe.Close()
	if missingModel {
		return report, errors.New("quality requires MODEL_ENDPOINT and MODEL_NAME; provide -env-file")
	}
	for _, in := range fixed {
		if err := in.Validate(cfg.MaxEvidence); err != nil {
			return report, err
		}
	}
	report.Budget = Budget{cfg.Providers, cfg.MaxSearchCalls, cfg.MaxPages, cfg.MaxEvidence, cfg.SearchConcurrency, cfg.ReadConcurrency, cfg.Timeout.String(), cfg.ResearchTimeout.String(), cfg.SearchTimeout.String(), cfg.ReadTimeout.String(), cfg.ModelTimeout.String(), cfg.PlanningTimeout.String(), cfg.PlanWithModel, cfg.VerifyWithModel}
	if err := os.MkdirAll(filepath.Dir(opt.Out), 0700); err != nil {
		return report, err
	}
	if err := os.Mkdir(opt.Out, 0700); err != nil {
		return report, fmt.Errorf("create fresh run directory: %w", err)
	}
	if err := fsutil.WriteJSON(filepath.Join(opt.Out, "suite.json"), opt.Suite); err != nil {
		return report, err
	}
	reviews := ReviewFile{Version: 2}
	persist := func() error {
		updateSummary(&report)
		if err := fsutil.WriteJSON(filepath.Join(opt.Out, "report.json"), report); err != nil {
			return err
		}
		return fsutil.WriteJSON(filepath.Join(opt.Out, "review.json"), reviews)
	}
	if err := persist(); err != nil {
		return report, err
	}
	consecutiveUnavailable := 0
	for caseIndex, c := range opt.Cases {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		tape := Record()
		tape.snapshot.Query = c.Query
		if opt.Replay != "" {
			var s Snapshot
			if err := readJSON(filepath.Join(opt.Replay, c.ID, "snapshot.json"), 20<<20, &s); err != nil {
				return report, err
			}
			if s.Query != c.Query {
				return report, errors.New("snapshot query does not match case")
			}
			if s.Plan == nil {
				// Early local snapshots saved the plan in output.json only.
				var old research.Output
				if err := readJSON(filepath.Join(opt.Replay, c.ID, "output.json"), 2<<20, &old); err != nil {
					return report, err
				}
				if old.Query != c.Query {
					return report, errors.New("recorded plan query mismatch")
				}
				s.Plan = &old.Plan
			}
			tape, err = Replay(s)
			if err != nil {
				return report, err
			}
		}
		dir := filepath.Join(opt.Out, c.ID)
		caseEnv := func(k string) string {
			if k == "SEARCHAN_DATA_DIR" {
				return filepath.Join(dir, ".cache")
			}
			return env(k)
		}
		rt, err := app.NewWithResearch(caseEnv, tape.Wrap)
		if err != nil {
			return report, err
		}
		fmt.Fprintf(progress, "[%d/%d] %s\n", len(report.Cases)+1, len(opt.Cases), c.ID)
		start := time.Now()
		taskCtx, stopTask := context.WithTimeout(ctx, rt.Config.Timeout)
		researchCtx, stopResearch := context.WithTimeout(taskCtx, rt.Config.ResearchTimeout)
		var output research.Output
		var runErr error
		if opt.FixedEvidence != "" {
			output, runErr = rt.Engine.Synthesize(researchCtx, fixed[c.ID])
		} else {
			output, runErr = rt.Engine.Run(researchCtx, c.Query)
		}
		stopResearch()
		tape.SetPlan(output.Plan)
		result := Result{ID: c.ID, Query: c.Query, State: "completed", Error: ErrorCode(runErr), ValidationCode: research.AnswerValidationCode(runErr), AnswerHash: hashJSON(output), AnswerMode: output.AnswerMode, AnswersQuery: output.Answer.AnswersQuery, FormatValid: research.ValidateAnswer(output.Answer, output.Evidence) == nil, Stats: output.Stats, SnapshotComplete: tape.Snapshot().Complete, SnapshotMisses: tape.Misses()}
		if opt.FixedEvidence != "" {
			result.EvidenceInputHash, result.SourceAnswerHash = hashJSON(fixed[c.ID]), sourceHashes[c.ID]
			if err := fsutil.WriteJSON(filepath.Join(dir, "synthesis_input.json"), fixed[c.ID]); err != nil {
				stopTask()
				rt.Close()
				return report, err
			}
		}
		result.LiveModelCalls = tape.LiveModelCalls()
		result.ModelErrors = tape.ModelErrors()
		result.ModelOutputIssues = tape.ModelOutputIssues()
		result.ModelRequests = tape.ModelRequests()
		for _, claim := range claims(output) {
			result.Statements++
			result.AnswerCharacters += utf8.RuneCountInString(claim.Text)
		}
		if runErr != nil {
			result.State = "failed"
		}
		if result.SnapshotMisses > 0 || !result.SnapshotComplete {
			result.State = "incomplete"
		}
		if err := fsutil.WriteJSON(filepath.Join(dir, "output.json"), output); err != nil {
			stopTask()
			rt.Close()
			return report, err
		}
		if err := fsutil.WriteJSONLimit(filepath.Join(dir, "snapshot.json"), tape.Snapshot(), 20<<20); err != nil {
			stopTask()
			rt.Close()
			return report, err
		}
		if opt.Render && runErr == nil && result.State == "completed" {
			result.Images, err = rt.Renderer.Render(taskCtx, c.ID, output, dir)
			if err == nil {
				for _, image := range result.Images {
					if err = os.Chmod(image.Path, 0600); err != nil {
						break
					}
				}
			}
			if err != nil {
				result.RenderError = ErrorCode(err)
			}
		}
		stopTask()
		rt.Close()
		result.ElapsedMS = time.Since(start).Milliseconds()
		if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(reviewMarkdown(c, output, result)), 0600); err != nil {
			return report, err
		}
		report.Cases = append(report.Cases, result)
		reviews.Cases = append(reviews.Cases, newReview(c, output, result.AnswerHash))
		if errors.Is(runErr, research.ErrSearchUnavailable) {
			consecutiveUnavailable++
		} else {
			// A successful search with no evidence is still an available service.
			// Do not let unrelated content/model failures trip this batch policy.
			consecutiveUnavailable = 0
		}
		stopBatch := !opt.ContinueOnUnavailable && consecutiveUnavailable >= 2 && caseIndex+1 < len(opt.Cases)
		if stopBatch {
			report.StoppedReason = "consecutive_search_unavailable"
			report.UnrunCases = append([]string(nil), report.SelectedCases[caseIndex+1:]...)
		}
		if err := persist(); err != nil {
			return report, err
		}
		fmt.Fprintf(progress, "  %s error=%s validation=%s model=%d search=%d read=%d images=%d\n", result.State, result.Error, result.ValidationCode, result.Stats.ModelCalls, result.Stats.SearchCalls, result.Stats.PagesRead, len(result.Images))
		if stopBatch {
			return report, errors.New("quality run stopped after two consecutive search provider outages; remaining cases were not run; inspect report.json")
		}
	}
	if report.Failures+report.Incomplete > 0 {
		return report, errors.New("quality run contains failed or incomplete cases; inspect report.json")
	}
	if opt.Render {
		for _, c := range report.Cases {
			if c.RenderError != "" {
				return report, errors.New("quality rendering failed; inspect report.json")
			}
		}
	}
	return report, nil
}

func updateSummary(r *Report) {
	r.Completed, r.Failures, r.Incomplete = 0, 0, 0
	var elapsed []int64
	for _, c := range r.Cases {
		switch c.State {
		case "completed":
			r.Completed++
		case "failed":
			r.Failures++
		default:
			r.Incomplete++
		}
		elapsed = append(elapsed, c.ElapsedMS)
	}
	sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
	if len(elapsed) > 0 {
		r.P50MS = elapsed[(len(elapsed)*50+99)/100-1]
		r.P95MS = elapsed[(len(elapsed)*95+99)/100-1]
	}
}

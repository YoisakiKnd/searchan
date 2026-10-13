package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"searchan/reader"
)

var ErrInvalidEvidenceInput = errors.New("invalid fixed evidence input")

// SynthesisInput freezes the inputs to answer generation and verification.
// GeneratedAt is the original evaluation's reference time, not this rerun's
// wall clock. Prior answers, warnings and verifier feedback are not accepted.
type SynthesisInput struct {
	Query       string     `json:"query"`
	Plan        Plan       `json:"plan"`
	Evidence    []Evidence `json:"evidence"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// Synthesize evaluates writing with exactly the supplied evidence, IDs, plan
// and reference date. It never plans, searches, selects passages or recovers
// missing evidence. The production bot uses Run; local quality experiments
// use this entry point to distinguish writing changes from retrieval changes.
func (e *Engine) Synthesize(ctx context.Context, in SynthesisInput) (Output, error) {
	out := Output{Query: in.Query, GeneratedAt: in.GeneratedAt, Stats: Stats{StageMS: map[string]int64{}}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if e.cfg.Model == nil {
		return out, ErrModelRequired
	}
	if len(in.Evidence) == 0 {
		return out, ErrNoEvidence
	}
	if err := in.Validate(e.cfg.MaxEvidence); err != nil {
		return out, err
	}
	out.Plan = in.Plan
	out.Plan.Queries = append([]string(nil), in.Plan.Queries...)
	out.Plan.Aspects = append([]string(nil), in.Plan.Aspects...)
	out.Evidence = append([]Evidence(nil), in.Evidence...)
	for i := range out.Evidence {
		out.Evidence[i].MatchedAspects = append([]int(nil), in.Evidence[i].MatchedAspects...)
	}
	started := time.Now()
	err := e.summarize(ctx, &out, nil)
	out.Stats.ElapsedMS = time.Since(started).Milliseconds()
	out.Stats.StageMS["answering"] = out.Stats.ElapsedMS
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err == nil {
		out.Sources = Sources(out.Answer, out.Evidence)
	}
	return out, err
}

// Validate checks local experiment bounds without rewriting any input.
func (in SynthesisInput) Validate(limit int) error {
	if limit < 1 || limit > 24 || strings.TrimSpace(in.Query) == "" || len(in.Query) > 8192 || !utf8.ValidString(in.Query) || in.GeneratedAt.IsZero() || len(in.Evidence) == 0 || len(in.Evidence) > limit || len(in.Plan.Queries) == 0 || len(in.Plan.Queries) > 4 || len(in.Plan.Aspects) > 4 {
		return ErrInvalidEvidenceInput
	}
	switch in.Plan.Intent {
	case "fact", "compare", "howto", "recent", "verify", "navigate":
	default:
		return ErrInvalidEvidenceInput
	}
	for _, q := range in.Plan.Queries {
		if len(q) > 8192 || !utf8.ValidString(q) {
			return ErrInvalidEvidenceInput
		}
	}
	for _, a := range in.Plan.Aspects {
		if len(a) > 8192 || !utf8.ValidString(a) {
			return ErrInvalidEvidenceInput
		}
	}
	seen := map[string]bool{}
	for _, ev := range in.Evidence {
		if ev.ID == "" || len(ev.ID) > 64 || !utf8.ValidString(ev.ID) || seen[ev.ID] || len(ev.URL) > 2048 || reader.ValidateURL(ev.URL) != nil || (ev.Kind != "body" && ev.Kind != "snippet") || strings.TrimSpace(ev.Text) == "" || len(ev.Text) > 4096 || !utf8.ValidString(ev.Text) || len(ev.Title) > 1024 || !utf8.ValidString(ev.Title) || len(ev.MatchedAspects) > 4 || len(ev.ReadMethod) > 64 || len(ev.PublishedAt) > 256 || len(ev.DocumentHash) > 128 {
			return ErrInvalidEvidenceInput
		}
		seen[ev.ID] = true
	}
	b, err := json.Marshal(in)
	if err != nil || len(b) > 120<<10 {
		return ErrInvalidEvidenceInput
	}
	return nil
}

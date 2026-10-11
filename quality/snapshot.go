// Package quality provides opt-in local, real-provider answer evaluation.
// It is never used by the bot's request path.
package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"searchan/internal/httpx"

	"searchan/model"
	"searchan/reader"
	"searchan/research"
	"searchan/search"
)

var ErrSnapshotMiss = errors.New("request not present in snapshot")

type Exchange struct {
	Kind  string          `json:"kind"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Snapshot records the parsed adapter output, not HTTP headers or credentials.
// Loss of any exchange marks the snapshot incomplete; replay cannot go live.
type Snapshot struct {
	Version   int            `json:"version"`
	Query     string         `json:"query"`
	Plan      *research.Plan `json:"plan,omitempty"`
	Complete  bool           `json:"complete"`
	Exchanges []Exchange     `json:"exchanges"`
}

type Tape struct {
	mu                sync.Mutex
	snapshot          Snapshot
	replay            bool
	bytes             int
	misses            int
	liveModelCalls    int
	modelErrors       []string
	modelOutputIssues []model.OutputDiagnostic
}

func Record() *Tape { return &Tape{snapshot: Snapshot{Version: 1, Complete: true}} }

func Replay(s Snapshot) (*Tape, error) {
	if s.Version != 1 || !s.Complete || len(s.Exchanges) > 96 {
		return nil, errors.New("snapshot invalid or incomplete")
	}
	t := &Tape{snapshot: s, replay: true}
	for _, x := range s.Exchanges {
		if (x.Kind != "search" && x.Kind != "direct" && x.Kind != "fallback") || len(x.Key) > 16384 || len(x.Value) > 2<<20 || !json.Valid(x.Value) {
			return nil, errors.New("invalid snapshot exchange")
		}
		t.bytes += len(x.Value) + len(x.Key)
	}
	if t.bytes > 16<<20 {
		return nil, errors.New("snapshot exceeds 16 MiB")
	}
	return t, nil
}

func (t *Tape) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.snapshot
	s.Exchanges = append([]Exchange(nil), s.Exchanges...)
	return s
}
func (t *Tape) Misses() int         { t.mu.Lock(); defer t.mu.Unlock(); return t.misses }
func (t *Tape) LiveModelCalls() int { t.mu.Lock(); defer t.mu.Unlock(); return t.liveModelCalls }
func (t *Tape) ModelErrors() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.modelErrors...)
}
func (t *Tape) ModelOutputIssues() []model.OutputDiagnostic {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]model.OutputDiagnostic(nil), t.modelOutputIssues...)
}

// SetPlan fixes the retrieval plan for controlled snapshot experiments. This
// deliberately isolates later stages rather than re-evaluating the planner.
func (t *Tape) SetPlan(p research.Plan) { t.mu.Lock(); defer t.mu.Unlock(); t.snapshot.Plan = &p }

func (t *Tape) save(kind, key string, value any, err error) {
	b, encodeErr := json.Marshal(value)
	t.mu.Lock()
	defer t.mu.Unlock()
	if encodeErr != nil || len(b) > 2<<20 || len(key) > 16384 || t.bytes+len(b)+len(key) > 16<<20 || len(t.snapshot.Exchanges) >= 96 {
		t.snapshot.Complete = false
		return
	}
	t.bytes += len(b) + len(key)
	t.snapshot.Exchanges = append(t.snapshot.Exchanges, Exchange{kind, key, b, ErrorCode(err)})
}

func (t *Tape) load(kind, key string, value any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, x := range t.snapshot.Exchanges {
		if x.Kind == kind && x.Key == key {
			if err := json.Unmarshal(x.Value, value); err != nil {
				return err
			}
			switch x.Error {
			case "":
				return nil
			case "deadline":
				return context.DeadlineExceeded
			case "cancelled":
				return context.Canceled
			case "blocked":
				return search.ErrBlocked
			default:
				if strings.HasPrefix(x.Error, "content_") {
					return reader.ContentError(strings.TrimPrefix(x.Error, "content_"))
				}
				if code, err := strconv.Atoi(strings.TrimPrefix(x.Error, "http_")); err == nil && code >= 100 && code <= 599 {
					var request struct{ Provider string }
					json.Unmarshal([]byte(key), &request)
					return &search.HTTPError{Provider: request.Provider, StatusCode: code}
				}
				return fmt.Errorf("recorded adapter failure: %s", x.Error)
			}
		}
	}
	t.misses++
	return ErrSnapshotMiss
}

// ErrorCode deliberately excludes remote response bodies and endpoint strings.
func ErrorCode(err error) string {
	var content reader.ContentError
	if errors.As(err, &content) {
		switch string(content) {
		case "empty_text", "interstitial", "invalid_utf8", "insufficient_text":
			return "content_" + string(content)
		}
	}
	var httpError *search.HTTPError
	if errors.As(err, &httpError) {
		return "http_" + strconv.Itoa(httpError.StatusCode)
	}
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, search.ErrBlocked):
		return "blocked"
	case errors.Is(err, research.ErrModelRequired):
		return "model_required"
	case errors.Is(err, research.ErrNoEvidence):
		return "no_evidence"
	case errors.Is(err, research.ErrAnswerGeneration):
		return "generation"
	case errors.Is(err, research.ErrAnswerValidation):
		return "answer_validation"
	default:
		return "io_error"
	}
}

// Wrap replaces adapter I/O only. The real engine and its budgets remain in use.
// Cache bypass avoids warm-cache differences between independent live runs.
func (t *Tape) Wrap(c *research.Config) {
	if c.Model != nil {
		c.Model = &tapedModel{c.Model, t}
	}
	ps := make([]search.Provider, len(c.Providers))
	for i, p := range c.Providers {
		ps[i] = &tapedProvider{p, t}
	}
	c.Providers = ps
	r := c.Reader
	if cache, ok := r.(*reader.Cache); ok {
		r = cache.Next
	}
	c.Reader = &tapedReader{r, t, "direct"}
	if c.FallbackReader != nil {
		c.FallbackReader = &tapedReader{c.FallbackReader, t, "fallback"}
	}
}

type tapedModel struct {
	next model.Completer
	tape *Tape
}

func (m *tapedModel) Complete(ctx context.Context, prompt string, input, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proposed, ok := output.(*research.Plan); ok && m.tape.replay && m.tape.snapshot.Plan != nil {
		*proposed = *m.tape.snapshot.Plan
		return nil
	}
	m.tape.mu.Lock()
	m.tape.liveModelCalls++
	m.tape.mu.Unlock()
	err := m.next.Complete(ctx, prompt, input, output)
	if err != nil {
		m.tape.mu.Lock()
		if len(m.tape.modelErrors) < 6 {
			m.tape.modelErrors = append(m.tape.modelErrors, modelFailureCode(err))
			var output *model.OutputError
			if errors.As(err, &output) {
				m.tape.modelOutputIssues = append(m.tape.modelOutputIssues, output.Diagnostic())
			}
		}
		m.tape.mu.Unlock()
	}
	return err
}

// Only fixed error categories leave the model adapter; no remote body, URL,
// arbitrary error message, credentials or generated draft is retained.
func modelFailureCode(err error) string {
	var output *model.OutputError
	if errors.As(err, &output) {
		code := "invalid_json_" + output.Kind()
		if output.Field() != "" {
			code += "_" + strings.ReplaceAll(output.Field(), ".", "_")
		}
		return code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var status *httpx.StatusError
	if errors.As(err, &status) {
		return "http_" + strconv.Itoa(status.Code)
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection_reset"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "connection_eof"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network_timeout"
		}
		return "network_error"
	}
	switch err.Error() {
	case "model output was truncated; check output token and thinking budgets", "model response incomplete or failed; check output token and thinking budgets":
		return "output_incomplete"
	case "model returned no answer content; check thinking and output token budgets", "model returned no answer content", "model returned no unique answer":
		return "answer_missing"
	case "invalid or oversized JSON response", "JSON response exceeds size limit", "unexpected trailing JSON data":
		return "invalid_json"
	case "model returned an API error in a successful HTTP response":
		return "api_error"
	default:
		return "model_error"
	}
}

type tapedProvider struct {
	search.Provider
	tape *Tape
}

func (p *tapedProvider) Search(ctx context.Context, r search.Request) (search.Response, error) {
	if err := ctx.Err(); err != nil {
		return search.Response{}, err
	}
	key, _ := json.Marshal(struct {
		Provider string
		Request  search.Request
	}{p.Name(), r})
	var out search.Response
	if p.tape.replay {
		err := p.tape.load("search", string(key), &out)
		return out, err
	}
	out, err := p.Provider.Search(ctx, r)
	p.tape.save("search", string(key), out, err)
	return out, err
}

type tapedReader struct {
	next reader.Reader
	tape *Tape
	kind string
}

func (r *tapedReader) Read(ctx context.Context, url string) (reader.Document, error) {
	if err := ctx.Err(); err != nil {
		return reader.Document{}, err
	}
	var out reader.Document
	if r.tape.replay {
		err := r.tape.load(r.kind, url, &out)
		return out, err
	}
	out, err := r.next.Read(ctx, url)
	r.tape.save(r.kind, url, out, err)
	return out, err
}

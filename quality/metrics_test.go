package quality

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"searchan/model"
	"searchan/research"
)

type metricsTransport func(*http.Request) (*http.Response, error)

func (f metricsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTapeUsageDoesNotInferUnknownUsageAndSkipsReplayedPlan(t *testing.T) {
	plan := research.Plan{Queries: []string{"Atlas"}, Intent: "fact"}
	tape, err := Replay(Snapshot{Version: 1, Complete: true, Plan: &plan})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	cfg := research.Config{Model: &model.Client{Endpoint: "https://model.example/", Model: "fixture", Client: &http.Client{Transport: metricsTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{}"}}]}`))}, nil
	})}}}
	tape.Wrap(&cfg)
	var proposed research.Plan
	if err := cfg.Model.Complete(context.Background(), "plan", nil, &proposed); err != nil || calls != 0 || len(tape.ModelRequests()) != 0 || tape.LiveModelCalls() != 0 {
		t.Fatal("replayed plan acquired fictional request usage", err)
	}
	for range 8 {
		var answer research.Answer
		if err := cfg.Model.Complete(context.Background(), "JSON", nil, &answer); err != nil {
			t.Fatal(err)
		}
	}
	got := tape.ModelRequests()
	if len(got) != 6 || tape.LiveModelCalls() != 8 || calls != 8 {
		t.Fatalf("unbounded observations: %d", len(got))
	}
	for _, m := range got {
		if m.Usage != nil || m.RequestAttempts != 1 || !m.ResponseDecoded || !m.OutputDecoded {
			t.Fatalf("missing usage guessed: %+v", m)
		}
	}
	got[0].RequestAttempts = 999
	if tape.ModelRequests()[0].RequestAttempts != 1 {
		t.Fatal("collector slice exposed")
	}
}

func TestTapeCustomCompleterWithoutObservationRemainsCompatible(t *testing.T) {
	tape := Record()
	m := &fakeModel{}
	cfg := research.Config{Model: m}
	tape.Wrap(&cfg)
	var out research.Answer
	if err := cfg.Model.Complete(context.Background(), "JSON", nil, &out); err != nil || tape.LiveModelCalls() != 1 || len(tape.ModelRequests()) != 0 {
		t.Fatalf("custom model requires observation: %v", err)
	}
}

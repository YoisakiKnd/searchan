package research

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"searchan/model"
)

func wireEvidenceFixture() []Evidence {
	return []Evidence{{
		ID: "original-id", URL: "https://manual.example/spec?v=2#contract", Title: "条件与范围",
		Text: "当 err == nil 时，需要关闭 Body。\n\n仅通知不保证停止；路径 C:\\tmp，字段 \"value\"。",
		Kind: "body", PublishedAt: "2026-08-01", FetchedAt: time.Date(2026, 8, 2, 1, 2, 3, 0, time.UTC),
		ReadMethod: "direct", Truncated: true, DocumentHash: strings.Repeat("a", 64),
		Paragraph: 7, ParagraphEnd: 9, RelevanceScore: 17.243591, MatchedAspects: []int{0, 2},
	}}
}

// Assert the serialized boundary, including fields a provider would receive.
// The original evidence remains the authority for storage and citation checks.
func assertWireEvidence(t *testing.T, input any, original []Evidence) {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var expected []map[string]any
	if err := json.Unmarshal(b, &expected); err != nil {
		t.Fatal(err)
	}
	for _, item := range expected {
		for _, key := range []string{"document_hash", "paragraph", "paragraph_end", "relevance_score", "matched_aspects"} {
			delete(item, key)
		}
	}
	if !reflect.DeepEqual(wire.Evidence, expected) {
		t.Fatalf("model evidence must remove only local audit/ranking fields: got=%+v expected=%+v", wire.Evidence, expected)
	}
}

func TestSynthesisModelEvidencePreservesContentAndCitationIdentity(t *testing.T) {
	for _, path := range []string{"summary", "verification_failed", "brief_repair", "syntax_retry", "snippets_only"} {
		t.Run(path, func(t *testing.T) {
			in := fixedSynthesisInput()
			in.Evidence = wireEvidenceFixture()
			if path == "snippets_only" {
				in.Evidence[0].Kind = "snippet"
			}
			before, _ := json.Marshal(in)
			calls, briefs := 0, 0
			var malformed error
			if path == "syntax_retry" {
				malformed = modelOutputFailure(t, `{"title":`)
			}
			ps, r := fixtures()
			e, err := New(Config{Providers: ps, Reader: r, VerifyWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, input, output any) error {
				calls++
				assertWireEvidence(t, input, in.Evidence)
				if prompt == verifyPrompt {
					if path == "verification_failed" || path == "brief_repair" {
						return assign(output, `{"answers_query":true,"unsupported":["scope"],"missing":[]}`)
					}
					return assign(output, `{"answers_query":true,"unsupported":[],"missing":[]}`)
				}
				if calls == 1 && malformed != nil {
					return malformed
				}
				if prompt == briefPrompt || prompt == briefRepairPrompt {
					briefs++
					if path == "brief_repair" && briefs == 1 {
						return assign(output, `{"title":"Brief","lead":{"text":"`+strings.Repeat("字", 310)+`","evidence_ids":["original-id"]},"sections":[]}`)
					}
				}
				return assign(output, `{"answers_query":true,"title":"条件","lead":{"text":"仅通知不保证停止。","evidence_ids":["original-id"]},"sections":[],"uncertainties":[]}`)
			})})
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Synthesize(context.Background(), in)
			wantCalls := map[string]int{"summary": 2, "verification_failed": 3, "brief_repair": 4, "syntax_retry": 3, "snippets_only": 1}[path]
			after, _ := json.Marshal(in)
			if err != nil || calls != wantCalls || out.Stats.ModelCalls != wantCalls || string(before) != string(after) || !reflect.DeepEqual(out.Evidence, in.Evidence) || len(out.Sources) != 1 || out.Sources[0].EvidenceIDs[0] != "original-id" {
				t.Fatalf("projection changed evidence, citations or budgets: calls=%d output=%+v error=%v", calls, out, err)
			}
		})
	}
}

func TestCoverageModelEvidenceExcludesLocalMetadata(t *testing.T) {
	ps, r := fixtures()
	checked := false
	e, _ := New(Config{Providers: ps, Reader: r, PlanWithModel: true, VerifyWithModel: true, Model: modelFunc(func(_ context.Context, prompt string, input, output any) error {
		if prompt == coveragePrompt {
			checked = true
			b, _ := json.Marshal(input)
			var got struct {
				Evidence []Evidence `json:"evidence"`
			}
			if err := json.Unmarshal(b, &got); err != nil || len(got.Evidence) == 0 {
				t.Fatal("coverage lost evidence", err)
			}
			for _, ev := range got.Evidence {
				if ev.DocumentHash != "" || ev.Paragraph != 0 || ev.ParagraphEnd != 0 || ev.RelevanceScore != 0 || len(ev.MatchedAspects) != 0 || ev.ID == "" || ev.URL == "" || ev.Text == "" || ev.Kind != "body" || ev.FetchedAt.IsZero() || ev.ReadMethod == "" {
					t.Fatalf("coverage lost source state or retained local fields: %+v", ev)
				}
			}
		}
		return plannedModel(prompt, output)
	})})
	if _, err := e.Run(context.Background(), "Go context cancellation"); err != nil || !checked {
		t.Fatal("coverage boundary not exercised", checked, err)
	}
}

type evidenceWireTransport func(*http.Request) (*http.Response, error)

func (f evidenceWireTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelEvidenceAcrossProtocols(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses", "anthropic"} {
		t.Run(api, func(t *testing.T) {
			in := fixedSynthesisInput()
			in.Evidence = wireEvidenceFixture()
			calls := 0
			c := &model.Client{Endpoint: "https://gateway.example/custom", API: api, Model: "configured-model", MaxTokens: 2400, JSONMode: true}
			c.Client = &http.Client{Transport: evidenceWireTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				defer r.Body.Close()
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["model"] != "configured-model" {
					t.Fatal("configured model changed")
				}
				var data string
				if api == "responses" {
					data = payload["input"].(string)
				} else {
					messages := payload["messages"].([]any)
					data = messages[len(messages)-1].(map[string]any)["content"].(string)
				}
				assertWireEvidence(t, json.RawMessage(data), in.Evidence)
				content := `{"answers_query":true,"title":"条件","lead":{"text":"仅通知不保证停止。","evidence_ids":["original-id"]},"sections":[],"uncertainties":[]}`
				if calls == 2 {
					content = `{"answers_query":true,"unsupported":[],"missing":[]}`
				}
				var wire any
				switch api {
				case "chat_completions":
					wire = map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}}
				case "responses":
					wire = map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": content}}}}}
				case "anthropic":
					wire = map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": content}}}
				}
				b, err := json.Marshal(wire)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: make(http.Header)}, nil
			})}
			ps, r := fixtures()
			e, _ := New(Config{Providers: ps, Reader: r, Model: c, VerifyWithModel: true})
			out, err := e.Synthesize(context.Background(), in)
			if err != nil || calls != 2 || out.Stats.ModelCalls != 2 || !reflect.DeepEqual(out.Evidence, in.Evidence) || out.AnswerMode != "summary" {
				t.Fatalf("wire projection changed protocol, metadata or call count: calls=%d output=%+v error=%v", calls, out, err)
			}
		})
	}
}

func BenchmarkModelEvidenceEncoding(b *testing.B) {
	evidence := make([]Evidence, 12)
	for i := range evidence {
		evidence[i] = wireEvidenceFixture()[0]
		evidence[i].ID = fmt.Sprintf("e%d", i+1)
		evidence[i].Text = strings.Repeat("Source evidence retains its conditions and scope. ", 30)
	}
	for _, compact := range []bool{false, true} {
		b.Run(fmt.Sprintf("compact=%t", compact), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var input any = evidence
				if compact {
					input = evidenceForModel(evidence)
				}
				if _, err := json.Marshal(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

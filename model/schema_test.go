package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type structuredRow struct {
	Cells []string `json:"cells"`
}
type structuredResult struct {
	Title string          `json:"title"`
	Rows  []structuredRow `json:"rows"`
	Note  *string         `json:"note,omitempty"`
}

func TestNativeStructuredOutputAcrossProtocols(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses", "anthropic"} {
		t.Run(api, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Fatal("invalid request")
				}
				encoded, _ := json.Marshal(p)
				if !strings.Contains(string(encoded), "不为填字段补充事实") {
					t.Fatal("schema-required keys conflict with optional-content examples")
				}
				var format map[string]any
				switch api {
				case "chat_completions":
					f := p["response_format"].(map[string]any)
					if f["type"] != "json_schema" {
						t.Fatal("JSON object mode was not replaced")
					}
					format = f["json_schema"].(map[string]any)
				case "responses":
					format = p["text"].(map[string]any)["format"].(map[string]any)
				case "anthropic":
					config := p["output_config"].(map[string]any)
					if config["effort"] != "high" {
						t.Fatal("format overwrote Anthropic effort")
					}
					format = config["format"].(map[string]any)
				}
				if api != "anthropic" && (format["strict"] != true || format["name"] != "searchan_output") {
					t.Fatal("missing native strict schema settings", format)
				}
				schema := format["schema"].(map[string]any)
				properties := schema["properties"].(map[string]any)
				if schema["additionalProperties"] != false || len(schema["required"].([]any)) != 3 || len(properties) != 3 {
					t.Fatal("fields are not strictly bounded")
				}
				row := properties["rows"].(map[string]any)["items"].(map[string]any)
				if row["additionalProperties"] != false || len(row["required"].([]any)) != 1 || properties["note"].(map[string]any)["anyOf"] == nil {
					t.Fatal("nested object or nullable field lost")
				}
				writeStructuredResponse(w, api, `{"title":"ok","rows":[{"cells":["中文"]}],"note":null}`)
			}))
			defer s.Close()
			c := Client{Endpoint: s.URL, API: api, Model: "fixture", JSONMode: true, StructuredOutput: true, ReasoningEffort: "high", Client: s.Client()}
			var out structuredResult
			if err := c.Complete(context.Background(), "JSON", nil, &out); err != nil || calls != 1 || out.Title != "ok" || out.Rows[0].Cells[0] != "中文" || out.Note != nil {
				t.Fatalf("structured result failed: %+v %v calls=%d", out, err, calls)
			}
		})
	}
}

func writeStructuredResponse(w http.ResponseWriter, api, text string) {
	w.Header().Set("Content-Type", "application/json")
	switch api {
	case "chat_completions":
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": text}}}})
	case "responses":
		json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}})
	case "anthropic":
		json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": text}}})
	}
}

func TestStructuredOutputRejectsIgnoredSchemasWithoutRepeatingRequests(t *testing.T) {
	for _, text := range []string{
		`{"title":"ok","rows":[]}`, // missing optional Go field still required by wire schema
		`{"title":"ok","rows":null,"note":null}`,
		`{"title":"ok","rows":[{}],"note":null}`,
		`{"title":"ok","rows":[{"cells":[],"SECRET_KEY":"secret-value"}],"note":null}`,
		`{"title":"ok","rows":[],"note":null,"extra":true}`,
		`null`,
	} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeStructuredResponse(w, "chat_completions", text)
		}))
		c := Client{Endpoint: s.URL, Model: "fixture", StructuredOutput: true, MaxRetries: 1, Client: s.Client()}
		var out structuredResult
		err := c.Complete(context.Background(), "JSON", nil, &out)
		s.Close()
		var invalid *OutputError
		if !errors.As(err, &invalid) || invalid.Kind() != "schema" || calls != 1 || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "SECRET_KEY") {
			t.Fatal("ignored schema was accepted, retried or leaked", err, calls)
		}
	}
}

func TestStructuredShapeDiagnosticsDoNotExposeUnknownKeys(t *testing.T) {
	s, err := schemaForOutput(&struct {
		Missing []string `json:"missing"`
		Lead    struct {
			Text string `json:"text"`
		} `json:"lead"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ raw, cause, field string }{
		{`{"lead":{"text":"ok"}}`, "missing_field", "missing"},
		{`{"missing":null,"lead":{"text":"ok"}}`, "null_not_allowed", "missing"},
		{`{"missing":[],"lead":{},"PRIVATE_TOKEN":"private-value"}`, "missing_field", "lead.text"},
		{`{"missing":[],"lead":{"text":"ok","PRIVATE_TOKEN":"private-value"}}`, "unknown_field", "lead"},
	} {
		cause, field := s.shape.issue(json.RawMessage(tc.raw), "")
		if cause != tc.cause || field != tc.field || strings.Contains(cause+field, "PRIVATE") {
			t.Fatal("unsafe or inaccurate diagnostic", cause, field)
		}
	}
}

func TestSchemaRejectsUnsupportedTargetsBeforeHTTP(t *testing.T) {
	type recursive struct{ Next *recursive }
	type embedded struct{ structuredResult }
	duplicate := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: `json:"same"`},
		{Name: "B", Type: reflect.TypeFor[int](), Tag: `json:"same"`},
	})
	for _, out := range []any{
		nil, (*structuredResult)(nil), new(any), &map[string]string{}, &recursive{}, &embedded{},
		&struct{ Data []byte }{}, &struct{ Data map[string]string }{}, &struct{ At time.Time }{},
		&struct {
			A int `json:"n,string"`
		}{}, reflect.New(duplicate).Interface(),
	} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
		c := Client{Endpoint: s.URL, Model: "fixture", StructuredOutput: true, Client: s.Client()}
		err := c.Complete(context.Background(), "JSON", nil, out)
		s.Close()
		if err == nil || calls != 0 {
			t.Fatal("unsupported output reached the endpoint", err, calls)
		}
	}
}

func TestStructuredModeIsOptInAndDoesNotDowngrade(t *testing.T) {
	for _, strict := range []bool{false, true} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			format := p["response_format"].(map[string]any)
			if strict {
				if format["type"] != "json_schema" {
					t.Fatal("missing schema")
				}
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"unsupported schema private-details"}`))
				return
			}
			if format["type"] != "json_object" {
				t.Fatal("default mode changed")
			}
			writeStructuredResponse(w, "chat_completions", `{"title":"ok"}`)
		}))
		c := Client{Endpoint: s.URL, Model: "fixture", JSONMode: true, StructuredOutput: strict, MaxRetries: 1, Client: s.Client()}
		var out structuredResult
		err := c.Complete(context.Background(), "JSON", nil, &out)
		s.Close()
		if calls != 1 || strict && err == nil || !strict && err != nil || err != nil && strings.Contains(err.Error(), "private-details") {
			t.Fatal("unsupported mode retried, downgraded or leaked", strict, calls, err)
		}
	}
}

func TestSchemaBoundsAndConcurrentReuse(t *testing.T) {
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := schemaForOutput(&structuredResult{}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	for i := range 24 {
		typ := reflect.StructOf([]reflect.StructField{{Name: fmt.Sprintf("Field%d", i), Type: reflect.TypeFor[string]()}})
		if _, err := schemaForOutput(reflect.New(typ).Interface()); err != nil {
			t.Fatal(err)
		}
	}
	outputSchemas.Lock()
	retained := len(outputSchemas.entries)
	outputSchemas.Unlock()
	if retained > 16 {
		t.Fatal("schema retention grew without bound")
	}
	fields := make([]reflect.StructField, 130)
	for i := range fields {
		fields[i] = reflect.StructField{Name: fmt.Sprintf("Field%d", i), Type: reflect.TypeFor[string]()}
	}
	if _, err := schemaForOutput(reflect.New(reflect.StructOf(fields)).Interface()); err == nil {
		t.Fatal("oversized schema admitted")
	}
	deep := reflect.TypeFor[string]()
	for range 10 {
		deep = reflect.StructOf([]reflect.StructField{{Name: "Nested", Type: deep}})
	}
	if _, err := schemaForOutput(reflect.New(deep).Interface()); err == nil {
		t.Fatal("overdeep schema admitted")
	}
	fields = fields[:80]
	for i := range fields {
		fields[i].Tag = reflect.StructTag(fmt.Sprintf(`json:"%s%d"`, strings.Repeat("x", 120), i))
	}
	if _, err := schemaForOutput(reflect.New(reflect.StructOf(fields)).Interface()); err == nil || !strings.Contains(err.Error(), "16 KiB") {
		t.Fatal("oversized serialized schema admitted", err)
	}
}

func TestStructuredOutputStillRejectsTruncationAndRefusal(t *testing.T) {
	for _, finish := range []string{"length", "content_filter", "tool_calls"} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": `{"title":"ok","rows":[],"note":null}`}}}})
		}))
		c := Client{Endpoint: s.URL, Model: "fixture", StructuredOutput: true, MaxRetries: 1, Client: s.Client()}
		var out structuredResult
		err := c.Complete(context.Background(), "JSON", nil, &out)
		s.Close()
		if err == nil || calls != 1 || out.Title != "" {
			t.Fatal("schema bypassed termination checks", finish, err, calls, out)
		}
	}
}

func BenchmarkCachedOutputSchema(b *testing.B) {
	var out structuredResult
	if _, err := schemaForOutput(&out); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := schemaForOutput(&out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOutputShapeValidation(b *testing.B) {
	var out structuredResult
	s, err := schemaForOutput(&out)
	if err != nil {
		b.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"title": strings.Repeat("evidence ", 400), "rows": []structuredRow{{Cells: []string{"quoted facts", "bounded source"}}}, "note": nil})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if !s.shape.matches(raw) {
			b.Fatal("valid shape rejected")
		}
	}
}

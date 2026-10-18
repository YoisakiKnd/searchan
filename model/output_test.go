package model

import (
	"errors"
	"strings"
	"testing"
)

func TestOutputErrorsDescribeSchemaWithoutRetainingContent(t *testing.T) {
	for _, tt := range []struct {
		body, kind, field string
	}{
		{`{"lead":"private-draft"}`, "type", "lead"},
		{`{"lead":{"text":42}}`, "type", "lead.text"},
		{`{"lead":`, "syntax", ""},
		{"说明：{\"lead\":{\"text\":\"private-draft\",", "syntax", ""},
		{strings.Repeat(" ", 64<<10) + `{}`, "oversized", ""},
	} {
		var out struct {
			Lead struct {
				Text string `json:"text"`
			} `json:"lead"`
		}
		err := decodeOutput(tt.body, &out)
		var detail *OutputError
		if !errors.As(err, &detail) || detail.Kind() != tt.kind || detail.Field() != tt.field || strings.Contains(err.Error(), "private-") {
			t.Fatalf("unsafe or unhelpful diagnostic: %v", err)
		}
		if d := detail.Diagnostic(); d.Bytes != len(tt.body) || d.Form == "" || strings.Contains(d.Field, "private-") {
			t.Fatalf("unsafe diagnostic metadata: %+v", d)
		}
	}
	var secret struct {
		Value bool `json:"private-field-name"`
	}
	err := decodeOutput(`{"private-field-name":"private-value"}`, &secret)
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("untrusted field/value exposed", err)
	}
	var out struct{ Value string }
	if err := decodeOutput(" \n {\"value\":\"好\"} \n ", &out); err != nil || out.Value != "好" {
		t.Fatal("valid output rejected", err)
	}
}

func TestOutputRecoversWrappedObject(t *testing.T) {
	for _, body := range []string{
		"```json\n{\"value\":\"好\"}\n```",
		"```JSON\n{\"value\":\"好\"}\n```\n",
		"```\n{\"value\":\"好\"}\n```",
		"以下是结果：\n{\"value\":\"好\"}\n希望有帮助。",
		"{\"value\":\"好\"} {\"value\":\"重复\"}",
		"{\"value\":\"好\"}\n```",
	} {
		var out struct{ Value string }
		if err := decodeOutput(body, &out); err != nil || out.Value != "好" {
			t.Fatalf("wrapped object rejected: %q %v %q", body, err, out.Value)
		}
	}
	var out struct{ Value string }
	if err := decodeOutput("{\"value\":\"第一行\n第二行\t{x}\"}", &out); err != nil || out.Value != "第一行\n第二行\t{x}" {
		t.Fatalf("raw control characters rejected: %v %q", err, out.Value)
	}
	if err := decodeOutput("```json\n[1, 2]\n```", &out); err == nil {
		t.Fatal("non-object output accepted")
	}
}

func TestOutputRepairsMissingClosers(t *testing.T) {
	type answer struct {
		Sections []struct {
			Claims []struct {
				Text string   `json:"text"`
				IDs  []string `json:"evidence_ids"`
			} `json:"claims"`
		} `json:"sections"`
		Uncertainties []string `json:"uncertainties"`
	}
	for _, body := range []string{
		// Observed from deepseek-flash: the sections array is never closed.
		`{"sections":[{"claims":[{"text":"a ] } , \"b\": c","evidence_ids":["e1"]}]},"uncertainties":["u"]}`,
		`{"sections":[{"claims":[{"text":"a ] } , \"b\": c","evidence_ids":["e1"]]}],"uncertainties":["u"]}`,
	} {
		var out answer
		if err := decodeOutput(body, &out); err != nil || len(out.Sections) != 1 || out.Sections[0].Claims[0].Text != `a ] } , "b": c` || len(out.Uncertainties) != 1 {
			t.Fatalf("structure not repaired: %q %v %+v", body, err, out)
		}
	}
	var out answer
	for _, body := range []string{`{"sections":[{"claims":[{"text":"unterminated`, `{"sections":[],"uncertainties":["u"],`, `{"sections":[],"uncertainties":`, `{"sections":[`} {
		if err := decodeOutput(body, &out); err == nil {
			t.Fatal("cut-off output accepted", body)
		}
	}
	// Omitted final closers after a complete value (finish_reason=stop).
	for _, body := range []string{`{"sections":[],"uncertainties":["u"]`, `{"sections":[],"uncertainties":["u"`} {
		if err := decodeOutput(body, &out); err != nil || len(out.Uncertainties) != 1 {
			t.Fatal("omitted final closer not repaired", body, err)
		}
	}
}

func TestOutputTypeDiagnosticIgnoresArrayIndices(t *testing.T) {
	var out struct {
		Sections []struct {
			Claims []struct {
				Text string `json:"text"`
			} `json:"claims"`
		} `json:"sections"`
	}
	err := decodeOutput(`{"sections":[{"claims":[{"text":"a"},{"text":1}]}]}`, &out)
	var detail *OutputError
	if !errors.As(err, &detail) || detail.Field() != "sections.claims.text" {
		t.Fatalf("nested field not reported: %v", err)
	}
}

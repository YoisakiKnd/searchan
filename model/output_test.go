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
		{`{} {"private-upstream":true}`, "syntax", ""},
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
	var detail *OutputError
	if err := decodeOutput(`{"value":`, &out); !errors.As(err, &detail) || detail.Diagnostic().Cause != "unexpected_end" {
		t.Fatal("missing safe incomplete JSON cause", err)
	}
}

package render

import (
	"context"
	"os"
	"strings"
	"testing"

	"searchan/demo"
)

func TestViewCitationsAndLiteralContent(t *testing.T) {
	out := demo.Output()
	out.Answer.Sections[0].Claims[0].Text = `#read("/etc/passwd") <script>`
	v := makeView("test", out)
	if v.Lead.Citation != "[1, 2]" || v.Sections[0].Claims[0].Text != out.Answer.Sections[0].Claims[0].Text {
		t.Fatal("citation mapping or literal data lost")
	}
	if !strings.Contains(template, `text(c.text)`) {
		t.Fatal("untrusted content should be rendered as literal text")
	}
}

func TestTypstIntegration(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST to run actual PNG rendering")
	}
	artifacts, err := New(binary, 1).Render(context.Background(), "test", demo.Output(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Width < 1000 || artifacts[0].Bytes < 1000 {
		t.Fatalf("invalid PNG artifacts: %+v", artifacts)
	}
}

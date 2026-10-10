package render

import (
	"context"
	"os"
	"strings"
	"testing"

	"searchan/demo"
	"searchan/research"
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

func TestTypstContentHeightAndBoundedLongReport(t *testing.T) {
	binary := os.Getenv("SEARCHAN_TEST_TYPST")
	if binary == "" {
		t.Skip("set SEARCHAN_TEST_TYPST for real layout regression")
	}
	out := demo.Output()
	out.Answer.Sections = nil
	out.Answer.Table = nil
	out.Answer.Uncertainties = nil
	short, err := New(binary, 1).Render(context.Background(), "compact", out, t.TempDir())
	if err != nil || len(short) != 1 || short[0].Height >= 1000 {
		t.Fatalf("short answer has fixed-page whitespace: %+v %v", short, err)
	}
	// A report just longer than the former fixed page stays one continuous card.
	out.Answer.Sections = []research.Section{{Heading: "正文"}}
	for i := 0; i < 4; i++ {
		out.Answer.Sections[0].Claims = append(out.Answer.Sections[0].Claims, research.Claim{Text: strings.Repeat("中文段落用于检查连续排版与引用。", 20), EvidenceIDs: []string{"e1"}})
	}
	medium, err := New(binary, 1).Render(context.Background(), "continuous", out, t.TempDir())
	if err != nil || len(medium) != 1 || medium[0].Height <= 1676 || medium[0].Height > 6000 {
		t.Fatalf("ordinary report needlessly split: %+v %v", medium, err)
	}
	// Exercise the measured pagination before PNG export.
	out.Answer.Sections = nil
	for i := 0; i < 3; i++ {
		s := research.Section{Heading: "长段落"}
		for j := 0; j < 2; j++ {
			s.Claims = append(s.Claims, research.Claim{Text: strings.Repeat("一\n", 100), EvidenceIDs: []string{"e1"}})
		}
		out.Answer.Sections = append(out.Answer.Sections, s)
	}
	r := New(binary, 1)
	r.MaxPages = 24
	long, err := r.Render(context.Background(), "paged", out, t.TempDir())
	if err != nil || len(long) < 2 || len(long) > 24 {
		t.Fatalf("large report did not paginate: pages=%d %v", len(long), err)
	}
	for _, a := range long {
		if a.Height > 6000 {
			t.Fatal("oversized fallback page")
		}
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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"searchan/quality"
)

func TestQualityCLIListsWithoutCredentialsAndRejectsUnknownCases(t *testing.T) {
	var out bytes.Buffer
	env := func(string) string { return "" }
	path := filepath.Join("..", "..", "quality", "cases.json")
	err := run(context.Background(), []string{"quality", "list", "-cases", path, "-split", "all", "-case", "springdata-intro", "-limit", "30"}, &out, env)
	var cases []quality.Case
	if err != nil || json.Unmarshal(out.Bytes(), &cases) != nil || len(cases) != 1 || cases[0].ID != "springdata-intro" {
		t.Fatalf("quality dispatch/list failed: %s %v", out.String(), err)
	}
	if err := run(context.Background(), []string{"quality", "list", "-cases", path, "-case", "missing"}, &out, env); err == nil {
		t.Fatal("unknown case accepted")
	}
	if err := run(context.Background(), []string{"quality", "run", "-cases", path, "-out", filepath.Join(t.TempDir(), "absent")}, &out, env); err == nil {
		t.Fatal("live quality run without model accepted")
	}
	if err := run(context.Background(), []string{"quality", "review"}, &out, env); err == nil {
		t.Fatal("review without run accepted")
	}
}

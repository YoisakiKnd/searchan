package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"title":"文档","url":"https://example.org","content":"说明"}]}`)
	}))
	defer srv.Close()
	env := func(k string) string {
		switch k {
		case "SEARXNG_ENDPOINT":
			return srv.URL
		}
		return ""
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-providers", "searxng", "中文查询"}, &out, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "文档") || !strings.Contains(out.String(), "searxng") {
		t.Fatalf("bad JSON: %s", out.String())
	}
	for _, args := range [][]string{{}, {"-providers", "unknown", "q"}, {"-providers", "brave", "q"}, {"-timeout", "0s", "q"}, {"-limit", "0", "-providers", "searxng", "q"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, env); err == nil {
			t.Errorf("accepted invalid args: %v", args)
		}
	}
}

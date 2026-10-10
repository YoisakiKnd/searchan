package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const ddgFixture = `<html><body>
<div class="result result--ad"><a class="result__a" href="https://ad.example">Ad</a></div>
<div class="result results_links"><h2><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fdoc%3Fx%3D1%26y%3D2&amp;rut=abc">Go <b>文档</b> &amp; API</a></h2><a class="result__snippet">Read <b>this</b> page.</a></div>
<div class="result"><a class="result__a" href="javascript:alert(1)">Invalid</a></div>
<div class="result"><a class="result__a" href="https://other.example/doc">Other</a><span class="result__snippet">Second</span></div>
</body></html>`

func TestProviderContracts(t *testing.T) {
	query := "Go 中文 / C++? x=1&y=2#片段"
	tests := []struct {
		name, method, authHeader, authValue, body string
		check                                     func(*testing.T, *http.Request)
	}{
		{"tavily", "POST", "Authorization", "Bearer secret", `{"results":[{"title":"Document","url":"https://example.org/doc","content":"snippet","published_date":"2026-01-02"}]}`, func(t *testing.T, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["query"] != query || body["max_results"] != float64(2) || body["include_answer"] != false {
				t.Errorf("wrong payload: %#v", body)
			}
		}},
		{"exa", "POST", "x-api-key", "secret", `{"results":[{"title":"Document","url":"https://example.org/doc","highlights":["one","two"],"text":"page text","publishedDate":"2026-01-02"}]}`, func(t *testing.T, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != query || body["numResults"] != float64(2) || body["type"] != "auto" {
				t.Errorf("wrong payload: %#v", body)
			}
		}},
		{"brave", "GET", "X-Subscription-Token", "secret", `{"web":{"results":[{"title":"Document","url":"https://example.org/doc","description":"snippet","extra_snippets":["more"]}]}}`, func(t *testing.T, r *http.Request) {
			q := r.URL.Query()
			if q.Get("q") != query || q.Get("offset") != "0" || q.Get("count") != "2" {
				t.Errorf("wrong query: %v", q)
			}
		}},
		{"searxng", "GET", "Authorization", "Bearer secret", `{"results":[{"title":"Document","url":"https://example.org/doc","content":"snippet","engines":["bing","duckduckgo"]}],"unresponsive_engines":[["other","timeout"]]}`, func(t *testing.T, r *http.Request) {
			q := r.URL.Query()
			if q.Get("q") != query || q.Get("format") != "json" || q.Get("engines") != "bing,duckduckgo" {
				t.Errorf("wrong query: %v", q)
			}
		}},
		{"jina", "GET", "Authorization", "Bearer secret", `{"code":200,"data":[{"title":"Document","url":"https://example.org/doc","description":"snippet","content":"page text"}]}`, func(t *testing.T, r *http.Request) {
			if r.URL.Path != "/"+query || r.URL.RawQuery != "" || r.Header.Get("Accept") != "application/json" {
				t.Errorf("query not preserved: %s", r.URL)
			}
		}},
		{"ddg", "POST", "", "", ddgFixture, func(t *testing.T, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("q") != query {
				t.Errorf("query not preserved: %v", r.Form)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method {
					t.Errorf("method = %s", r.Method)
				}
				if tt.authHeader != "" && r.Header.Get(tt.authHeader) != tt.authValue {
					t.Error("missing authentication")
				}
				tt.check(t, r)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			p, err := NewProvider(tt.name, Config{APIKey: "secret", Endpoint: srv.URL, Engines: []string{"bing", "duckduckgo"}})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := p.Search(context.Background(), Request{Query: query, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Results) == 0 || resp.Provider != tt.name {
				t.Fatalf("bad response: %+v", resp)
			}
			first := resp.Results[0]
			if first.Sources[0].Provider != tt.name || first.Sources[0].Rank != 1 || first.Snippet == "" {
				t.Fatalf("bad metadata: %+v", first)
			}
			if tt.name == "jina" && (first.Content != "page text" || first.ContentProvider != "jina") {
				t.Fatal("lost content provenance")
			}
			if tt.name == "ddg" && (len(resp.Results) != 2 || first.URL != "https://example.org/doc?x=1&y=2" || first.Title != "Go 文档 & API" || first.Snippet != "Read this page.") {
				t.Fatalf("bad DDG parsing: %+v", resp)
			}
			if tt.name == "searxng" && (len(resp.Warnings) == 0 || len(first.Sources[0].Engines) != 2) {
				t.Fatal("lost upstream metadata")
			}
		})
	}
}

func TestHTTPFailuresAndCancellation(t *testing.T) {
	for _, name := range []string{"tavily", "exa", "brave", "searxng", "jina", "ddg"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(429)
				fmt.Fprint(w, "private provider error and secret")
			}))
			defer srv.Close()
			p, err := NewProvider(name, Config{APIKey: "secret", Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Search(context.Background(), Request{Query: "test"})
			var he *HTTPError
			if !errors.As(err, &he) || he.StatusCode != 429 || he.RetryAfter != "3" {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
				t.Fatal("leaked error body or retried request")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = p.Search(ctx, Request{Query: "test"})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

func TestMalformedAndEmptyResponses(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		wantErr    bool
	}{
		{"tavily", `{"results":[]}`, false}, {"tavily", `{}`, true},
		{"exa", `{"results":[]}`, false}, {"exa", `{"results":null}`, true},
		{"brave", `{"query":{"original":"q"}}`, false}, {"brave", `{}`, true},
		{"searxng", `{"results":[]}`, false}, {"searxng", `<html>login</html>`, true},
		{"jina", `{"code":200,"data":[]}`, false}, {"jina", `{"code":401,"data":[]}`, true},
		{"ddg", `<div class="no-results">No results</div>`, false},
		{"ddg", `<form id="challenge-form"></form>`, true},
		{"ddg", `<html>Unexpected page</html>`, true},
	} {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) }))
			defer srv.Close()
			p, err := NewProvider(tt.name, Config{APIKey: "secret", Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Search(context.Background(), Request{Query: "q"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(tt.body, "challenge-form") && !errors.Is(err, ErrBlocked) {
				t.Fatalf("challenge not classified: %v", err)
			}
		})
	}
}

func TestNoCredentialRedirect(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer origin.Close()
	p, err := NewBrave(Config{APIKey: "secret", Endpoint: origin.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Search(context.Background(), Request{Query: "q"})
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 307 || hits.Load() != 0 {
		t.Fatalf("unsafe redirect: %v, hits=%d", err, hits.Load())
	}
}

func TestRequestValidation(t *testing.T) {
	for _, name := range []string{"tavily", "exa", "jina", "brave"} {
		if _, err := NewProvider(name, Config{}); err == nil {
			t.Errorf("%s accepted missing key", name)
		}
	}
	if _, err := NewSearXNG(Config{}); err == nil {
		t.Error("accepted missing endpoint")
	}
	if _, err := NewDDG(Config{Endpoint: "file:///etc/passwd"}); err == nil {
		t.Error("accepted bad endpoint")
	}
	p, _ := NewDDG(Config{})
	for _, req := range []Request{{Query: " "}, {Query: "q", Limit: -1}, {Query: "q", Limit: 101}, {Query: "q", Page: -1}} {
		if _, err := p.Search(context.Background(), req); err == nil {
			t.Errorf("accepted invalid request: %+v", req)
		}
	}
	for _, name := range []string{"tavily", "exa", "jina", "ddg", "brave"} {
		p, _ := NewProvider(name, Config{APIKey: "secret"})
		page := 2
		if name == "brave" {
			page = 11
		}
		_, err := p.Search(context.Background(), Request{Query: "q", Page: page})
		if !errors.Is(err, ErrPageUnsupported) {
			t.Errorf("%s: wrong pagination error %v", name, err)
		}
	}
}

func TestResponseSizeAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", maxBody+1)) }))
	defer srv.Close()
	p, _ := NewDDG(Config{Endpoint: srv.URL})
	if _, err := p.Search(context.Background(), Request{Query: "q"}); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("unbounded response: %v", err)
	}
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	p, _ = NewDDG(Config{Endpoint: slow.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Search(ctx, Request{Query: "q"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost timeout: %v", err)
	}
}

func TestProviderLimitsAndPaging(t *testing.T) {
	for _, tt := range []struct {
		name        string
		page, limit int
		body        string
		check       func(*testing.T, *http.Request)
	}{
		{"tavily", 1, 40, `{"results":[]}`, func(t *testing.T, r *http.Request) {
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["max_results"] != float64(20) {
				t.Errorf("uncapped request: %v", v)
			}
		}},
		{"brave", 3, 40, `{"web":{"results":[]}}`, func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("count") != "20" || r.URL.Query().Get("offset") != "2" {
				t.Errorf("wrong paging: %s", r.URL)
			}
		}},
		{"searxng", 3, 10, `{"results":[]}`, func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("pageno") != "3" {
				t.Errorf("wrong paging: %s", r.URL)
			}
		}},
		{"jina", 1, 10, `{"code":200,"data":[]}`, func(t *testing.T, r *http.Request) {}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tt.check(t, r); fmt.Fprint(w, tt.body) }))
			defer srv.Close()
			p, err := NewProvider(tt.name, Config{APIKey: "secret", Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := p.Search(context.Background(), Request{Query: "q", Limit: tt.limit, Page: tt.page})
			if err != nil || len(resp.Warnings) == 0 {
				t.Fatalf("missing limits warning: %+v %v", resp, err)
			}
		})
	}
}

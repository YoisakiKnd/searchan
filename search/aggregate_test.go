package search

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	name   string
	search func(context.Context, Request) (Response, error)
}

func (p fakeProvider) Name() string { return p.name }
func (p fakeProvider) Search(ctx context.Context, r Request) (Response, error) {
	return p.search(ctx, r)
}

func TestFusionPartialFailureAndProvenance(t *testing.T) {
	makeProvider := func(name string, urls []string) Provider {
		return fakeProvider{name, func(ctx context.Context, req Request) (Response, error) {
			resp := Response{Provider: name}
			for i, u := range urls {
				r := Result{URL: u, Title: name}
				if name == "jina" {
					r.Content = "page"
				}
				addResult(&resp, r, i+1, 100, nil)
			}
			return resp, nil
		}}
	}
	providers := []Provider{
		makeProvider("ddg", []string{"https://example.org/a?utm_source=ddg", "https://example.org/b", "https://example.org/a"}),
		makeProvider("jina", []string{"https://example.org/b", "https://example.org/a?utm_source=jina"}),
		fakeProvider{"broken", func(context.Context, Request) (Response, error) { return Response{}, errors.New("timeout") }},
	}
	r, err := SearchAll(context.Background(), providers, Request{Query: "q"})
	if err != nil || len(r.Results) != 2 || r.Errors["broken"] != "timeout" {
		t.Fatalf("unexpected report: %+v, %v", r, err)
	}
	// Reciprocal ranks tie; provider/input order is the deterministic tiebreaker.
	if r.Results[0].Title != "ddg" || len(r.Results[0].Sources) != 2 || r.Results[0].Sources[1].Provider != "jina" {
		t.Fatalf("lost provenance or duplicate votes: %+v", r)
	}
	if r.Results[0].Content != "page" || r.Results[0].ContentProvider != "jina" {
		t.Fatal("lost content provenance")
	}
}

func TestFusionConcurrencyAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	makeP := func(name string) Provider {
		return fakeProvider{name, func(ctx context.Context, _ Request) (Response, error) {
			started <- struct{}{}
			<-ctx.Done()
			return Response{}, ctx.Err()
		}}
	}
	done := make(chan error, 1)
	go func() { _, err := SearchAll(ctx, []Provider{makeP("a"), makeP("b")}, Request{Query: "q"}); done <- err }()
	<-started
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("all-failure not reported")
	}
}

func TestFusionValidationAndCanonicalURLs(t *testing.T) {
	p := fakeProvider{"a", func(context.Context, Request) (Response, error) {
		t.Fatal("should not call provider")
		return Response{}, nil
	}}
	for _, providers := range [][]Provider{nil, {p, p}, {nil}} {
		if _, err := SearchAll(context.Background(), providers, Request{Query: "q"}); err == nil {
			t.Fatal("accepted invalid providers")
		}
	}
	if canonicalURL("https://EXAMPLE.org/a?utm_source=x&id=1") != "https://example.org/a?id=1" {
		t.Fatal("tracking not removed")
	}
	for _, pair := range [][2]string{{"https://x/a?id=1", "https://x/a?id=2"}, {"https://x/a", "https://x/a/"}, {"https://x/a#one", "https://x/a#two"}} {
		if canonicalURL(pair[0]) == canonicalURL(pair[1]) {
			t.Fatalf("merged distinct URLs: %v", pair)
		}
	}
	if DocumentURL("https://EXAMPLE.org:443/a?utm_source=x#one") != "https://example.org/a" || DocumentURL("https://example.org/a#one") != DocumentURL("https://example.org/a#two") {
		t.Fatal("document fetch identity not normalized")
	}
}

func TestFusionDiscountsKnownSharedUpstreams(t *testing.T) {
	makeP := func(name, u string, rank int, engines []string) Provider {
		return fakeProvider{name, func(context.Context, Request) (Response, error) {
			rs := make([]Result, rank)
			rs[rank-1] = Result{URL: u, Title: name, Sources: []Source{{Provider: name, Rank: rank, Engines: engines}}}
			return Response{Results: rs}, nil
		}}
	}
	r, err := SearchAll(context.Background(), []Provider{makeP("ddg", "https://a.example/", 2, nil), makeP("searxng", "https://a.example/", 2, []string{"duckduckgo"}), makeP("brave", "https://b.example/", 1, nil)}, Request{Query: "q"})
	if err != nil || len(r.Results) != 2 || r.Results[0].URL != "https://b.example/" {
		t.Fatalf("shared upstream inflated ranking: %+v %v", r, err)
	}
}

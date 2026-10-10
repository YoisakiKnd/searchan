package search

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Brave struct{ *base }

func NewBrave(cfg Config) (*Brave, error) {
	b, err := newBase("brave", "https://api.search.brave.com/res/v1/web/search", cfg, true)
	if err != nil {
		return nil, err
	}
	return &Brave{b}, nil
}

func (p *Brave) Search(ctx context.Context, req Request) (Response, error) {
	req, resp, err := prepare(req, p.Name(), 20, true)
	if err != nil {
		return resp, err
	}
	if req.Page > 10 {
		return resp, fmt.Errorf("brave: %w (maximum page is 10)", ErrPageUnsupported)
	}
	var wire struct {
		Query map[string]any `json:"query"`
		Web   *struct {
			Results []struct {
				Title, URL, Description string
				Extra                   []string `json:"extra_snippets"`
			} `json:"results"`
		} `json:"web"`
	}
	endpoint := withQuery(p.endpoint, url.Values{"q": {req.Query}, "count": {strconv.Itoa(req.Limit)}, "offset": {strconv.Itoa(req.Page - 1)}, "extra_snippets": {"true"}, "text_decorations": {"false"}})
	err = p.json(ctx, "GET", endpoint, nil, map[string]string{"X-Subscription-Token": p.key}, &wire)
	if err != nil {
		return resp, err
	}
	if wire.Web == nil {
		if wire.Query == nil {
			return resp, missingResults(p.Name())
		}
		return resp, nil
	}
	for i, r := range wire.Web.Results {
		snippets := append([]string{r.Description}, r.Extra...)
		addResult(&resp, Result{Title: r.Title, URL: r.URL, Snippet: strings.TrimSpace(strings.Join(snippets, "\n"))}, i+1, req.Limit, nil)
	}
	return resp, nil
}

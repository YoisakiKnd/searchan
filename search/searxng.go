package search

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type SearXNG struct{ *base }

func NewSearXNG(cfg Config) (*SearXNG, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("searxng: endpoint is required (e.g. http://localhost:8080/search)")
	}
	b, err := newBase("searxng", "", cfg, false)
	if err != nil {
		return nil, err
	}
	return &SearXNG{b}, nil
}

func (p *SearXNG) Search(ctx context.Context, req Request) (Response, error) {
	req, resp, err := prepare(req, p.Name(), 100, true)
	if err != nil {
		return resp, err
	}
	q := url.Values{"q": {req.Query}, "format": {"json"}, "pageno": {strconv.Itoa(req.Page)}}
	if len(p.engines) > 0 {
		q.Set("engines", strings.Join(p.engines, ","))
	}
	headers := map[string]string{}
	if p.key != "" {
		headers["Authorization"] = "Bearer " + p.key
	}
	var wire struct {
		Results *[]struct {
			Title, URL, Content string
			Engines             []string
			Published           string `json:"publishedDate"`
		} `json:"results"`
		Unresponsive [][]string `json:"unresponsive_engines"`
	}
	err = p.json(ctx, "GET", withQuery(p.endpoint, q), nil, headers, &wire)
	if err != nil {
		return resp, err
	}
	if wire.Results == nil {
		return resp, missingResults(p.Name())
	}
	for i, r := range *wire.Results {
		addResult(&resp, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, PublishedAt: r.Published}, i+1, req.Limit, r.Engines)
	}
	if len(wire.Unresponsive) > 0 {
		resp.Warnings = append(resp.Warnings, "some SearXNG upstream engines were unresponsive; results may be incomplete")
	}
	if req.Page > 1 {
		resp.Warnings = append(resp.Warnings, "pagination depends on enabled SearXNG engines; some may repeat results")
	}
	return resp, nil
}

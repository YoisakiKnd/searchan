package search

import (
	"context"
	"fmt"
	"strings"
)

func missingResults(name string) error { return fmt.Errorf("%s: response is missing results", name) }

type Exa struct{ *base }

func NewExa(cfg Config) (*Exa, error) {
	b, err := newBase("exa", "https://api.exa.ai/search", cfg, true)
	if err != nil {
		return nil, err
	}
	return &Exa{b}, nil
}

func (p *Exa) Search(ctx context.Context, req Request) (Response, error) {
	req, resp, err := prepare(req, p.Name(), 100, false)
	if err != nil {
		return resp, err
	}
	var wire struct {
		Results *[]struct {
			Title, URL, Text string
			Highlights       []string
			Published        string `json:"publishedDate"`
		} `json:"results"`
	}
	err = p.json(ctx, "POST", p.endpoint, map[string]any{"query": req.Query, "numResults": req.Limit, "type": "auto", "contents": map[string]any{"highlights": true}}, map[string]string{"x-api-key": p.key}, &wire)
	if err != nil {
		return resp, err
	}
	if wire.Results == nil {
		return resp, missingResults(p.Name())
	}
	for i, r := range *wire.Results {
		addResult(&resp, Result{Title: r.Title, URL: r.URL, Snippet: strings.Join(r.Highlights, "\n"), Content: r.Text, PublishedAt: r.Published}, i+1, req.Limit, nil)
	}
	return resp, nil
}

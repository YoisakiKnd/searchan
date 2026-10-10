package search

import "context"

type Tavily struct{ *base }

func NewTavily(cfg Config) (*Tavily, error) {
	b, err := newBase("tavily", "https://api.tavily.com/search", cfg, true)
	if err != nil {
		return nil, err
	}
	return &Tavily{b}, nil
}

func (p *Tavily) Search(ctx context.Context, req Request) (Response, error) {
	req, resp, err := prepare(req, p.Name(), 20, false)
	if err != nil {
		return resp, err
	}
	var wire struct {
		Results *[]struct {
			Title, URL, Content string
			Published           string `json:"published_date"`
		} `json:"results"`
	}
	err = p.json(ctx, "POST", p.endpoint, map[string]any{
		"query": req.Query, "max_results": req.Limit, "search_depth": "basic", "include_answer": false, "include_raw_content": false,
	}, map[string]string{"Authorization": "Bearer " + p.key}, &wire)
	if err != nil {
		return resp, err
	}
	if wire.Results == nil {
		return resp, missingResults(p.Name())
	}
	for i, r := range *wire.Results {
		addResult(&resp, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, PublishedAt: r.Published}, i+1, req.Limit, nil)
	}
	return resp, nil
}

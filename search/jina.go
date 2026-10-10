package search

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Jina uses Search (s.jina.ai), not the URL Reader (r.jina.ai).
type Jina struct{ *base }

func NewJina(cfg Config) (*Jina, error) {
	b, err := newBase("jina", "https://s.jina.ai/", cfg, true)
	if err != nil {
		return nil, err
	}
	return &Jina{b}, nil
}

func (p *Jina) Search(ctx context.Context, req Request) (Response, error) {
	// The documented default search returns five pages. Do not claim arbitrary
	// limits/pagination that the endpoint does not promise.
	req, resp, err := prepare(req, p.Name(), 5, false)
	if err != nil {
		return resp, err
	}
	u, _ := url.Parse(p.endpoint)
	prefix := strings.TrimRight(u.EscapedPath(), "/")
	u.Path = strings.TrimRight(u.Path, "/") + "/" + req.Query
	u.RawPath = prefix + "/" + url.PathEscape(req.Query)
	var wire struct {
		Code int
		Data *[]struct{ Title, URL, Description, Content string }
	}
	err = p.json(ctx, "GET", u.String(), nil, map[string]string{"Authorization": "Bearer " + p.key}, &wire)
	if err != nil {
		return resp, err
	}
	if wire.Code != 0 && wire.Code != 200 {
		return resp, fmt.Errorf("jina: application error %d", wire.Code)
	}
	if wire.Data == nil {
		return resp, missingResults(p.Name())
	}
	for i, r := range *wire.Data {
		addResult(&resp, Result{Title: r.Title, URL: r.URL, Snippet: r.Description, Content: r.Content}, i+1, req.Limit, nil)
	}
	return resp, nil
}

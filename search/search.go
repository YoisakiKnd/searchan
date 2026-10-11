// Package search provides independently usable web search adapters.
package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"searchan/internal/httpx"
)

type Request struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	// Page is one-based. Only Brave and SearXNG accept pages after the first.
	Page int `json:"page,omitempty"`
}

type Source struct {
	Provider string   `json:"provider"`
	Rank     int      `json:"rank"`
	Engines  []string `json:"engines,omitempty"`
}

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	// Content is optional provider-supplied page text, not a verified answer.
	Content          string   `json:"content,omitempty"`
	ContentProvider  string   `json:"content_provider,omitempty"`
	ContentTruncated bool     `json:"content_truncated,omitempty"`
	PublishedAt      string   `json:"published_at,omitempty"`
	Sources          []Source `json:"sources"`
}

type Response struct {
	Provider string   `json:"provider"`
	Results  []Result `json:"results"`
	Warnings []string `json:"warnings,omitempty"`
}

type Provider interface {
	Name() string
	Search(context.Context, Request) (Response, error)
}

// Config is immutable after construction. Endpoint overrides are trusted
// operator configuration, never values taken from a search user's request.
type Config struct {
	APIKey   string
	Endpoint string
	Client   *http.Client
	// Engines applies only to SearXNG; empty uses its instance configuration.
	Engines []string
}

var ErrBlocked = errors.New("search engine returned an anti-bot challenge")
var ErrPageUnsupported = errors.New("provider does not support this page")

type HTTPError struct {
	Provider   string
	StatusCode int
	RetryAfter string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d", e.Provider, e.StatusCode)
}

type base struct {
	name, endpoint, key string
	client              *http.Client
	engines             []string
}

func (b *base) Name() string { return b.name }

func newBase(name, endpoint string, cfg Config, keyRequired bool) (*base, error) {
	if cfg.Endpoint != "" {
		endpoint = cfg.Endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("%s: invalid endpoint", name)
	}
	if keyRequired && strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%s: API key is required", name)
	}
	client := *httpx.NewClient(20 * time.Second)
	if cfg.Client != nil {
		client = *cfg.Client
	}
	if client.Timeout <= 0 {
		client.Timeout = 20 * time.Second
	}
	// Never forward API keys to redirect targets. Operators can configure the
	// final endpoint explicitly if their service redirects.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &base{name: name, endpoint: endpoint, key: cfg.APIKey, client: &client, engines: append([]string(nil), cfg.Engines...)}, nil
}

func normalize(req Request) (Request, error) {
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return req, errors.New("query must not be empty")
	}
	if len(req.Query) > 8192 {
		return req, errors.New("query exceeds 8192 bytes")
	}
	if req.Limit == 0 {
		req.Limit = 10
	}
	if req.Limit < 1 || req.Limit > 100 {
		return req, errors.New("limit must be between 1 and 100")
	}
	if req.Page == 0 {
		req.Page = 1
	}
	if req.Page < 1 {
		return req, errors.New("page must be positive")
	}
	return req, nil
}

func prepare(req Request, name string, max int, paging bool) (Request, Response, error) {
	resp := Response{Provider: name, Results: []Result{}}
	req, err := normalize(req)
	if err != nil {
		return req, resp, err
	}
	if !paging && req.Page != 1 {
		return req, resp, fmt.Errorf("%s: %w", name, ErrPageUnsupported)
	}
	if req.Limit > max {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("%s adapter limits each request to %d results; requested %d", name, max, req.Limit))
		req.Limit = max
	}
	return req, resp, nil
}

func addResult(resp *Response, r Result, rank, limit int, engines []string) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	if len(resp.Results) >= limit {
		return
	}
	r.Sources = []Source{{Provider: resp.Provider, Rank: rank, Engines: engines}}
	if r.Content != "" {
		r.ContentProvider = resp.Provider
	}
	resp.Results = append(resp.Results, r)
}

func NewProvider(name string, cfg Config) (Provider, error) {
	switch name {
	case "tavily":
		return NewTavily(cfg)
	case "exa":
		return NewExa(cfg)
	case "searxng":
		return NewSearXNG(cfg)
	case "jina":
		return NewJina(cfg)
	case "ddg":
		return NewDDG(cfg)
	case "brave":
		return NewBrave(cfg)
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}

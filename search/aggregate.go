package search

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

type Report struct {
	Results  []Result            `json:"results"`
	Errors   map[string]string   `json:"errors,omitempty"`
	Warnings map[string][]string `json:"warnings,omitempty"`
}

// SearchAll queries only the explicitly supplied providers. Partial results
// survive individual failures. Limit is per provider; the merged pool is not
// truncated. Fusion ranks relevance, not factual confidence.
func SearchAll(ctx context.Context, providers []Provider, req Request) (Report, error) {
	report := Report{Results: []Result{}, Errors: map[string]string{}, Warnings: map[string][]string{}}
	if len(providers) == 0 {
		return report, errors.New("at least one provider is required")
	}
	var err error
	req, err = normalize(req)
	if err != nil {
		return report, err
	}
	names := map[string]bool{}
	for _, p := range providers {
		if p == nil {
			return report, errors.New("nil provider")
		}
		if names[p.Name()] {
			return report, fmt.Errorf("duplicate provider %q", p.Name())
		}
		names[p.Name()] = true
	}
	type outcome struct {
		response Response
		err      error
	}
	out := make([]outcome, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p Provider) { defer wg.Done(); out[i].response, out[i].err = p.Search(ctx, req) }(i, p)
	}
	wg.Wait()
	type ranked struct {
		result Result
		score  float64
		order  int
		votes  map[string]float64
	}
	merged := map[string]*ranked{}
	succeeded := 0
	for i, o := range out {
		name := providers[i].Name()
		if o.err != nil {
			report.Errors[name] = o.err.Error()
			continue
		}
		succeeded++
		if len(o.response.Warnings) > 0 {
			report.Warnings[name] = o.response.Warnings
		}
		seen := map[string]bool{}
		for rank, r := range o.response.Results {
			key := canonicalURL(r.URL)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			entry, ok := merged[key]
			if !ok {
				copy := r
				copy.Sources = append([]Source(nil), r.Sources...)
				entry = &ranked{result: copy, order: len(merged), votes: map[string]float64{}}
				merged[key] = entry
			} else {
				entry.result.Sources = append(entry.result.Sources, r.Sources...)
				if entry.result.Content == "" && r.Content != "" {
					entry.result.Content = r.Content
					entry.result.ContentProvider = r.ContentProvider
				}
			}
			families := SourceFamilies(name, r.Sources)
			for _, family := range families {
				vote := 1.0 / (float64(61+rank) * float64(len(families)))
				if vote > entry.votes[family] {
					entry.score += vote - entry.votes[family]
					entry.votes[family] = vote
				}
			}
		}
	}
	items := make([]*ranked, 0, len(merged))
	for _, r := range merged {
		items = append(items, r)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].order < items[j].order
		}
		return items[i].score > items[j].score
	})
	for _, r := range items {
		report.Results = append(report.Results, r.result)
	}
	if succeeded == 0 {
		return report, errors.New("all search providers failed")
	}
	return report, nil
}

// Normalize only known tracking parameters. Preserve meaningful query params,
// path case, trailing slashes, and scheme to avoid merging distinct resources.
func canonicalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// CanonicalURL is shared with request-level research deduplication.
func CanonicalURL(raw string) string { return canonicalURL(raw) }

// DocumentURL deduplicates page fetches while CanonicalURL retains anchor
// distinctions for search results and source links.
func DocumentURL(raw string) string {
	canonical := canonicalURL(raw)
	if canonical == "" {
		return ""
	}
	u, _ := url.Parse(canonical)
	u.Fragment = ""
	u.RawFragment = ""
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	return u.String()
}

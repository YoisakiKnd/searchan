package search

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// DDG parses DuckDuckGo HTML directly; it does not call SearXNG or Instant Answers.
// HTML is not a stable public API. Challenges and layout changes return errors.
type DDG struct{ *base }

func NewDDG(cfg Config) (*DDG, error) {
	b, err := newBase("ddg", "https://html.duckduckgo.com/html/", cfg, false)
	if err != nil {
		return nil, err
	}
	return &DDG{b}, nil
}

func (p *DDG) Search(ctx context.Context, req Request) (Response, error) {
	req, resp, err := prepare(req, p.Name(), 30, false)
	if err != nil {
		return resp, err
	}
	if utf8.RuneCountInString(req.Query) > 499 {
		return resp, fmt.Errorf("ddg: query exceeds 499 characters")
	}
	data, err := p.do(ctx, "POST", p.endpoint, []byte(url.Values{"q": {req.Query}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "text/html"})
	if err != nil {
		return resp, err
	}
	results, err := parseDDG(data)
	if err != nil {
		return resp, err
	}
	for i, r := range results {
		addResult(&resp, r, i+1, req.Limit, nil)
	}
	if len(resp.Results) < req.Limit {
		resp.Warnings = append(resp.Warnings, "DDG adapter reads the first HTML page only; requested result count is not guaranteed")
	}
	return resp, nil
}

// parseDDG tokenizes with a bounded stack instead of retaining an HTML DOM.
func parseDDG(data []byte) ([]Result, error) {
	z := html.NewTokenizer(bytes.NewReader(data))
	z.SetMaxBuf(256 << 10)
	type frame struct {
		tag                    string
		result, title, snippet bool
	}
	stack := make([]frame, 0, 32)
	var results []Result
	var title, snippet strings.Builder
	raw := ""
	active, ad := false, false
	titleDepth, snippetDepth := 0, 0
	blocked, noResults := false, false
	appendText := func(b *strings.Builder, text string, max int) {
		text = strings.TrimSpace(text)
		if text == "" || b.Len() >= max {
			return
		}
		n := min(len(text), max-b.Len())
		for n > 0 && n < len(text) && !utf8.RuneStart(text[n]) {
			n--
		}
		b.WriteString(text[:n])
		if b.Len() < max {
			b.WriteByte(' ')
		}
	}
	flush := func() {
		if active && !ad && raw != "" && len(results) < 30 {
			u, err := url.Parse(raw)
			if err == nil {
				if u.Host == "duckduckgo.com" || u.Host == "www.duckduckgo.com" || (u.Host == "" && strings.HasPrefix(u.Path, "/l/")) {
					if target := u.Query().Get("uddg"); target != "" {
						raw = target
					}
				}
				if strings.HasPrefix(raw, "//") {
					raw = "https:" + raw
				}
				results = append(results, Result{Title: strings.Join(strings.Fields(title.String()), " "), URL: raw, Snippet: strings.Join(strings.Fields(snippet.String()), " ")})
			}
		}
		active, ad = false, false
		raw = ""
		title.Reset()
		snippet.Reset()
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return nil, fmt.Errorf("ddg: invalid or oversized HTML token")
			}
			flush()
			if blocked {
				return nil, fmt.Errorf("ddg: %w", ErrBlocked)
			}
			if len(results) == 0 && !noResults {
				return nil, fmt.Errorf("ddg: unrecognized HTML layout or challenge")
			}
			return results, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			attrs := func(key string) string {
				for _, a := range t.Attr {
					if a.Key == key {
						return a.Val
					}
				}
				return ""
			}
			classes := " " + strings.Join(strings.Fields(attrs("class")), " ") + " "
			has := func(class string) bool { return strings.Contains(classes, " "+class+" ") }
			id := attrs("id")
			if id == "challenge-form" || id == "anomaly-form" || has("anomaly-modal") {
				blocked = true
			}
			if has("no-results") || has("no-results__message") {
				noResults = true
			}
			f := frame{tag: t.Data}
			if has("result") {
				flush()
				active = true
				ad = has("result--ad")
				f.result = true
			}
			if active && !ad {
				if t.Data == "a" && has("result__a") && raw == "" {
					raw = attrs("href")
					f.title = true
					titleDepth++
				}
				if has("result__snippet") {
					f.snippet = true
					snippetDepth++
				}
			}
			void := tt == html.SelfClosingTagToken
			switch t.Data {
			case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
				void = true
			}
			if void {
				if f.title {
					titleDepth--
				}
				if f.snippet {
					snippetDepth--
				}
			} else {
				if len(stack) >= 512 {
					return nil, fmt.Errorf("ddg: HTML nesting limit exceeded")
				}
				stack = append(stack, f)
			}
		case html.EndTagToken:
			tag, _ := z.TagName()
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == string(tag) {
					for _, f := range stack[i:] {
						if f.title {
							titleDepth--
						}
						if f.snippet {
							snippetDepth--
						}
						if f.result {
							flush()
						}
					}
					stack = stack[:i]
					break
				}
			}
		case html.TextToken:
			if active && !ad {
				if titleDepth > 0 {
					appendText(&title, string(z.Text()), 512)
				}
				if snippetDepth > 0 {
					appendText(&snippet, string(z.Text()), 4096)
				}
			}
		}
	}
}

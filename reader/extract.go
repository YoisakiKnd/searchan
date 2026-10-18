package reader

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// textSink collapses HTML whitespace without inventing spaces at inline tag
// boundaries. Builders and pending separators stay bounded independently.
type textSink struct {
	b         strings.Builder
	limit     int
	pending   string
	truncated bool
}

func (s *textSink) append(raw []byte) {
	if len(raw) > s.limit-s.b.Len() {
		s.truncated = true
	}
	n := min(len(raw), s.limit-s.b.Len())
	for n > 0 && n < len(raw) && !utf8.RuneStart(raw[n]) {
		n--
	}
	if n > 0 {
		s.b.Write(raw[:n])
	}
}
func (s *textSink) separate(sep string) {
	if len(sep) > len(s.pending) || sep == "\t" && s.pending == " " {
		s.pending = sep
	}
}
func (s *textSink) flush() {
	if s.b.Len() > 0 {
		s.append([]byte(s.pending))
	}
	s.pending = ""
}
func (s *textSink) text(raw []byte, pre bool) {
	if s.b.Len() >= s.limit {
		s.truncated = s.truncated || len(raw) > 0
		return
	}
	if pre {
		s.flush()
		s.append(raw)
		return
	}
	for i := 0; i < len(raw); {
		start := i
		for i < len(raw) && !htmlSpace(raw[i]) {
			i++
		}
		if i > start {
			s.flush()
			s.append(raw[start:i])
		}
		if i < len(raw) {
			s.separate(" ")
		}
		for i < len(raw) && htmlSpace(raw[i]) {
			i++
		}
	}
}
func htmlSpace(b byte) bool { return b <= ' ' && (b == ' ' || b >= '\t' && b <= '\r' && b != '\v') }

// Extract streams tokens without a DOM. Inline identifiers, preformatted code
// and table cell boundaries survive; hidden/navigation trees are excluded.
func Extract(r io.Reader) (string, string, error) {
	title, text, _, err := extract(r)
	return title, text, err
}

func extract(r io.Reader) (string, string, bool, error) {
	z := html.NewTokenizer(r)
	z.SetMaxBuf(256 << 10)
	all, main, title := textSink{limit: MaxText}, textSink{limit: MaxText}, textSink{limit: 512}
	type frame struct {
		tag                                   string
		skip, main, body, article, title, pre bool
	}
	stack := make([]frame, 0, 32)
	skip, primary, inBody, inArticle, inTitle, pre := 0, 0, 0, 0, 0, 0
	selected := 0
	pop := func(i int) {
		for _, f := range stack[i:] {
			if f.skip {
				skip--
			}
			if f.main {
				primary--
			}
			if f.body {
				inBody--
			}
			if f.article {
				inArticle--
			}
			if f.title {
				inTitle--
			}
			if f.pre {
				pre--
			}
		}
		stack = stack[:i]
	}
	separate := func(sep string) {
		if skip == 0 {
			all.separate(sep)
			if primary > 0 {
				main.separate(sep)
			}
		}
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return "", "", false, fmt.Errorf("HTML tokenization: %w", z.Err())
			}
			text := strings.TrimSpace(all.b.String())
			truncated := all.truncated
			if strings.TrimSpace(main.b.String()) != "" {
				text = strings.TrimSpace(main.b.String())
				truncated = main.truncated
			}
			return strings.TrimSpace(title.b.String()), text, truncated, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			tag := atom.Lookup(name).String()
			if tag == "" {
				tag = string(name)
			}
			f := frame{tag: tag, skip: skipTag(tag) || tag == "header" && primary == 0, main: tag == "main" || tag == "article", article: tag == "article", title: tag == "title", pre: tag == "pre"}
			for more {
				key, val, next := z.TagAttr()
				more = next
				switch string(key) {
				case "id", "class", "itemprop":
					body, excluded := contentAttributes(tag, string(key), string(val))
					f.body = f.body || body
					f.main = f.main || body
					f.skip = f.skip || excluded
				case "hidden":
					f.skip = true
				case "aria-hidden":
					if strings.EqualFold(string(val), "true") {
						f.skip = true
					}
				case "role":
					switch strings.ToLower(string(val)) {
					case "main":
						f.main = true
					case "navigation", "complementary":
						f.skip = true
					}
				}
			}
			// Like browsers, ignore repeated document-level tags and let <body>
			// close an unterminated <head>; otherwise a stray second <head>
			// hides the whole page body.
			if tag == "html" || tag == "head" || tag == "body" {
				open := -1
				for i, old := range stack {
					if old.tag == tag || tag == "body" && old.tag == "head" {
						open = i
						break
					}
				}
				if open >= 0 && stack[open].tag == tag {
					continue
				}
				if open >= 0 {
					pop(open)
				}
			}
			// Common optional end tags must not grow an unbounded stack or leak
			// pre/main state. Stop at the matching list/table scope for nested markup.
			for i := len(stack) - 1; i >= 0; i-- {
				old := stack[i].tag
				if optionalClose(old, tag) {
					pop(i)
					i = len(stack)
					continue
				}
				if scopeBoundary(old, tag) {
					break
				}
			}
			if !f.skip {
				if blockTag(tag) {
					separate("\n\n")
				} else if tag == "td" || tag == "th" {
					separate("\t")
				}
			}
			if tt != html.SelfClosingTagToken && !voidTag(tag) {
				if len(stack) >= 512 {
					return "", "", false, errors.New("HTML nesting limit exceeded")
				}
				stack = append(stack, f)
				if f.skip {
					skip++
				}
				if f.main {
					primary++
				}
				if f.body {
					inBody++
				}
				if f.article {
					inArticle++
				}
				if f.title {
					inTitle++
				}
				if f.pre {
					pre++
				}
				if tag == "script" || tag == "style" {
					// Large inline assets are not evidence. Permit a bounded raw
					// token here so a site's CSS cannot discard a useful page.
					z.SetMaxBuf(MaxBody + 1)
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if blockTag(tag) {
				separate("\n\n")
			}
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == tag {
					pop(i)
					break
				}
			}
			if tag == "script" || tag == "style" {
				z.SetMaxBuf(256 << 10)
			}
		case html.TextToken:
			if skip > 0 && inTitle == 0 {
				continue // Do not unescape or copy discarded assets/widgets.
			}
			raw := z.Text()
			if inTitle > 0 {
				title.text(raw, false)
			}
			if skip == 0 && inTitle == 0 {
				if primary > 0 {
					level := 1 // main/role=main
					if inArticle > 0 {
						level = 2
					}
					if inBody > 0 {
						level = 3
					}
					if level > selected && strings.TrimSpace(string(raw)) != "" {
						// Prefer explicit prose over page chrome or card articles.
						// Only one primary builder survives a promotion.
						main = textSink{limit: MaxText}
						selected = level
					}
					if level >= selected {
						main.text(raw, pre > 0)
					}
				} else if main.b.Len() == 0 {
					// Once explicit primary text exists, the fallback builder can
					// never be selected. Avoid scanning and storing that text twice.
					all.text(raw, pre > 0)
				}
			}
		}
	}
}

func optionalClose(old, next string) bool {
	if old == "p" && (blockTag(next) || next == "ul" || next == "ol" || next == "table" || next == "dl") {
		return true
	}
	return old == "li" && next == "li" || (old == "td" || old == "th") && (next == "td" || next == "th" || next == "tr") || old == "tr" && next == "tr" || (old == "dt" || old == "dd") && (next == "dt" || next == "dd")
}
func scopeBoundary(old, next string) bool {
	return (next == "li" && (old == "ul" || old == "ol")) || ((next == "tr" || next == "td" || next == "th") && old == "table") || ((next == "dt" || next == "dd") && old == "dl") || old == "body" || old == "html"
}
func skipTag(s string) bool {
	switch s {
	case "script", "style", "nav", "footer", "aside", "form", "noscript", "svg", "head", "template":
		return true
	}
	return false
}
func blockTag(s string) bool {
	switch s {
	case "p", "div", "article", "main", "section", "header", "h1", "h2", "h3", "h4", "h5", "h6", "li", "pre", "tr", "br", "ul", "ol", "dl", "dt", "dd", "blockquote", "table":
		return true
	}
	return false
}
func voidTag(s string) bool {
	switch s {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

package reader

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// textSink collapses HTML whitespace without inventing spaces at inline tag
// boundaries. Builders and pending separators stay bounded independently.
type textSink struct {
	b                       strings.Builder
	limit                   int
	pending                 string
	truncated               bool
	headings                []Heading
	headingID, headingIndex int
	codeBlocks              []CodeBlock
	codeID, codeIndex       int
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

// Capture offsets in the same bounded builder that emits the final text, so
// discarded navigation or promotion to articleBody cannot misalign sections.
func (s *textSink) content(raw []byte, code, id, level int) {
	if code > 0 && code != s.codeID {
		s.codeID, s.codeIndex = code, -1
		s.flush()
		if s.b.Len() < s.limit && len(s.codeBlocks) < 128 {
			s.codeIndex = len(s.codeBlocks)
			s.codeBlocks = append(s.codeBlocks, CodeBlock{Start: s.b.Len(), End: s.b.Len()})
		}
	}
	if id != 0 && id != s.headingID {
		s.headingID, s.headingIndex = id, -1
		s.flush()
		if s.b.Len() < s.limit && len(s.headings) < 128 {
			s.headingIndex = len(s.headings)
			s.headings = append(s.headings, Heading{Start: s.b.Len(), End: s.b.Len(), Level: level})
		}
	}
	s.text(raw, code > 0)
	if id != 0 && s.headingIndex >= 0 {
		s.headings[s.headingIndex].End = s.b.Len()
	}
	if code > 0 && s.codeIndex >= 0 {
		s.codeBlocks[s.codeIndex].End = s.b.Len()
	}
}

func (s *textSink) result() (string, []Heading, []CodeBlock) {
	raw := s.b.String()
	left := strings.TrimLeftFunc(raw, unicode.IsSpace)
	offset := len(raw) - len(left)
	text := strings.TrimRightFunc(left, unicode.IsSpace)
	out := s.headings[:0]
	for _, h := range s.headings {
		h.Start -= offset
		h.End = min(h.End-offset, len(text))
		if h.Start >= 0 && h.End > h.Start && h.Start < len(text) {
			out = append(out, h)
		}
	}
	code := s.codeBlocks[:0]
	for _, c := range s.codeBlocks {
		c.Start = max(0, c.Start-offset)
		c.End = min(c.End-offset, len(text))
		if c.Start >= 0 && c.End > c.Start && c.Start < len(text) {
			code = append(code, c)
		}
	}
	return text, out, code
}

func htmlSpace(b byte) bool { return b <= ' ' && (b == ' ' || b >= '\t' && b <= '\r' && b != '\v') }

// Extract streams tokens without a DOM. Inline identifiers, preformatted code
// and table cell boundaries survive; hidden/navigation trees are excluded.
func Extract(r io.Reader) (string, string, error) {
	title, text, _, err := extract(r)
	return title, text, err
}

func extract(r io.Reader) (string, string, bool, error) {
	title, text, truncated, _, err := extractPage(r, "")
	return title, text, truncated, err
}

func extractPage(r io.Reader, pageURL string) (string, string, bool, []Link, error) {
	title, text, truncated, links, _, err := extractOutlinedPage(r, pageURL)
	return title, text, truncated, links, err
}

func extractOutlinedPage(r io.Reader, pageURL string) (string, string, bool, []Link, []Heading, error) {
	title, text, truncated, links, headings, _, err := extractDocumentPage(r, pageURL)
	return title, text, truncated, links, headings, err
}

func extractDocumentPage(r io.Reader, pageURL string) (string, string, bool, []Link, []Heading, []CodeBlock, error) {
	base, _ := url.Parse(pageURL)
	selfURL, _, _ := strings.Cut(pageURL, "#")
	var links []Link
	var sameSite []Link
	seenLinks := map[string]bool{}
	z := html.NewTokenizer(r)
	z.SetMaxBuf(256 << 10)
	all, main, title := textSink{limit: MaxText}, textSink{limit: MaxText}, textSink{limit: 512}
	type frame struct {
		tag                              string
		skip, main, body, article, title bool
		link, heading, code              int
	}
	stack := make([]frame, 0, 32)
	skip, primary, inBody, inArticle, inTitle := 0, 0, 0, 0, 0
	activeCode, codeSequence := 0, 0
	selected := 0
	activeLink := 0
	activeHeading, headingSequence := 0, 0
	titleSeen := false
	pop := func(i int) {
		linkClosed := false
		headingClosed := false
		codeClosed := false
		for _, f := range stack[i:] {
			linkClosed = linkClosed || f.link != 0
			headingClosed = headingClosed || f.heading > 0
			codeClosed = codeClosed || f.code > 0
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
		}
		stack = stack[:i]
		if codeClosed {
			activeCode = 0
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].code > 0 {
					activeCode = stack[j].code
					break
				}
			}
		}
		if headingClosed {
			activeHeading = 0
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].heading > 0 {
					activeHeading = j + 1
					break
				}
			}
		}
		if linkClosed {
			activeLink = 0
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].link != 0 {
					activeLink = stack[j].link
					break
				}
			}
		}
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
				return "", "", false, nil, nil, nil, fmt.Errorf("HTML tokenization: %w", z.Err())
			}
			text, headings, codeBlocks := all.result()
			truncated := all.truncated
			if strings.TrimSpace(main.b.String()) != "" {
				text, headings, codeBlocks = main.result()
				truncated = main.truncated
			}
			// External sources retain all 32 slots. At most eight same-site
			// body links fill unused slots, never displacing external references.
			if len(links) == 0 {
				links = sameSite
			} else {
				links = append(links, sameSite[:min(len(sameSite), 32-len(links))]...)
			}
			return strings.TrimSpace(title.b.String()), text, truncated, links, headings, codeBlocks, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			tag := atom.Lookup(name).String()
			if tag == "" {
				tag = string(name)
			}
			documentTitle := tag == "title" && !titleSeen
			if documentTitle {
				// The tokenizer also emits SVG/widget <title> tags. Only the
				// first document-level title is metadata; skipped subtrees must
				// not escape their exclusion through the title text exception.
				for _, parent := range stack {
					if parent.tag != "html" && parent.tag != "head" {
						documentTitle = false
						break
					}
				}
			}
			f := frame{tag: tag, skip: skipTag(tag) || tag == "title" || tag == "header" && primary == 0, main: tag == "main" || tag == "article", article: tag == "article", title: documentTitle}
			if documentTitle {
				titleSeen = true
			}
			href := ""
			for more {
				key, val, next := z.TagAttr()
				more = next
				switch string(key) {
				case "href":
					if tag == "a" && pageURL != "" && len(val) <= 2048 {
						href = string(val)
					}
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
					case "navigation", "complementary", "contentinfo":
						f.skip = true
					}
				}
			}
			if base != nil && base.Host != "" && skip == 0 && !f.skip && href != "" && !strings.HasPrefix(href, "#") && len(links) < 32 {
				if target, err := base.Parse(href); err == nil {
					target.Fragment = ""
					raw := target.String()
					if len(raw) <= 2048 && !seenLinks[raw] && ValidateURL(raw) == nil {
						local := strings.EqualFold(target.Hostname(), base.Hostname())
						if local && primary > 0 && len(sameSite) < 8 && raw != selfURL {
							// Followable references can be on the same documentation
							// site. Collect only links inside a visible primary body;
							// they have a separate small reserve until extraction ends.
							seenLinks[raw] = true
							sameSite = append(sameSite, Link{URL: raw})
							f.link = -len(sameSite)
						} else if !local {
							seenLinks[raw] = true
							links = append(links, Link{URL: raw})
							f.link = len(links)
						}
					}
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
					return "", "", false, nil, nil, nil, errors.New("HTML nesting limit exceeded")
				}
				if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
					headingSequence++
					f.heading = headingSequence
				}
				if tag == "pre" {
					codeSequence++
					f.code = codeSequence
				}
				stack = append(stack, f)
				if f.heading > 0 {
					activeHeading = len(stack)
				}
				if f.code > 0 {
					activeCode = f.code
				}
				if f.link != 0 {
					activeLink = f.link
				}
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
				if activeLink != 0 {
					var link *Link
					if activeLink > 0 {
						link = &links[activeLink-1]
					} else {
						link = &sameSite[-activeLink-1]
					}
					if len(link.Text) < 160 {
						link.Text = Clip(strings.TrimSpace(link.Text+" "+strings.TrimSpace(Clip(string(raw), 160))), 160)
					}
				}
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
						id, level := 0, 0
						if activeHeading > 0 {
							f := stack[activeHeading-1]
							id = f.heading
							level = int(f.tag[1] - '0')
						}
						main.content(raw, activeCode, id, level)
					}
				} else if main.b.Len() == 0 {
					// Once explicit primary text exists, the fallback builder can
					// never be selected. Avoid scanning and storing that text twice.
					id, level := 0, 0
					if activeHeading > 0 {
						f := stack[activeHeading-1]
						id = f.heading
						level = int(f.tag[1] - '0')
					}
					all.content(raw, activeCode, id, level)
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

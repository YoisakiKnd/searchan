package research

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"searchan/reader"
)

// Section boundaries come only from visible HTML headings in the original
// text. Old snapshots and readers without outlines retain the previous split.
// Validate offsets before slicing; metadata cannot replace or invent evidence.
func documentPassages(doc reader.Document) []passage {
	text := reader.Clip(doc.Text, reader.MaxText)
	if len(doc.Headings) == 0 || len(doc.Headings) > 128 {
		return splitPassages(text)
	}
	previous, previousEnd := -1, 0
	for _, h := range doc.Headings {
		if h.Start <= previous || h.Start < previousEnd || h.Start < 0 || h.End <= h.Start || h.End > len(text) || h.Level < 1 || h.Level > 6 || !utf8.RuneStart(text[h.Start]) || h.End < len(text) && !utf8.RuneStart(text[h.End]) {
			return splitPassages(text)
		}
		previous, previousEnd = h.Start, h.End
	}
	code := validCodeBlocks(text, doc.CodeBlocks)
	codeCursor := 0
	var out []passage
	start, paragraph := 0, 1
	heading := ""
	emit := func(end int) {
		section := text[start:end]
		body := section
		// An explicitly labeled generated overview is a separate derivative
		// widget, not the article's own supporting text. Omit only short blocks
		// terminated by a validated heading with substantive body after it.
		// Preserve the fetched document and original paragraph coordinates.
		if end < len(text) && len(strings.TrimSpace(text[end:])) >= 200 {
			if cut := generatedOverviewStart(section); cut >= 0 {
				body = section[:cut]
			}
		}
		appendParts := func(segment string, offset int, prose bool) {
			parts := splitPassages(segment)
			base := paragraph - 1 + strings.Count(section[:offset], "\n\n")
			for i := range parts {
				parts[i].first += base
				parts[i].last += base
				minimum := len(heading) + 40
				if prose {
					minimum = 40
				}
				if i == 0 && len(heading) <= 240 && len(parts[i].text) >= minimum {
					parts[i].heading = heading
				}
			}
			out = append(out, parts...)
		}
		for codeCursor < len(code) && code[codeCursor].End <= start {
			codeCursor++
		}
		cut := 0
		if codeCursor < len(code) {
			c := code[codeCursor]
			// A declaration comes directly after its visible section heading.
			// The first prose afterwards carries the same heading context; later
			// examples do not displace an already present explanatory opening.
			if heading != "" && c.Start >= start && c.End < start+len(body) && c.Start-start <= 512 && strings.TrimSpace(body[:c.Start-start]) == heading {
				// Adjacent pre blocks (including malformed nested markup) still
				// form the leading declaration, not explanatory prose.
				for i := codeCursor + 1; i < len(code); i++ {
					next := code[i]
					if next.End > start+len(body) || strings.TrimSpace(text[c.End:next.Start]) != "" {
						break
					}
					c.End = next.End
				}
				cut = c.End - start
				cut += len(body[cut:]) - len(strings.TrimLeftFunc(body[cut:], unicode.IsSpace))
			}
		}
		if cut > 0 && cut < len(body) {
			appendParts(body[:cut], 0, false)
			appendParts(body[cut:], cut, true)
		} else {
			appendParts(body, 0, false)
		}
		paragraph += strings.Count(section, "\n\n")
		start = end
	}
	for _, h := range doc.Headings {
		if h.Start > start {
			emit(h.Start)
		}
		heading = strings.TrimSpace(text[h.Start:h.End])
	}
	emit(len(text))
	return out
}

func validCodeBlocks(text string, blocks []reader.CodeBlock) []reader.CodeBlock {
	if len(blocks) > 128 {
		return nil
	}
	end := 0
	for _, c := range blocks {
		if c.Start < end || c.Start < 0 || c.End <= c.Start || c.End > len(text) || !utf8.RuneStart(text[c.Start]) || c.End < len(text) && !utf8.RuneStart(text[c.End]) {
			return nil
		}
		end = c.End
	}
	return blocks
}

func generatedOverviewStart(section string) int {
	if len(section) > 4096 {
		return -1
	}
	start := 0
	for start < len(section) && start <= 1024 {
		end := strings.Index(section[start:], "\n\n")
		if end < 0 {
			break
		}
		end += start
		if end-start <= 96 {
			label := strings.TrimSpace(section[start:end])
			if strings.EqualFold(label, "AI Summary") || strings.EqualFold(label, "AI-generated summary") || strings.EqualFold(label, "AI generated summary") || strings.EqualFold(label, "AI生成摘要") || strings.EqualFold(label, "AI 生成摘要") || label == "人工智能生成摘要" {
				if strings.TrimSpace(section[end+2:]) != "" {
					return start
				}
			}
		}
		start = end + 2
	}
	return -1
}

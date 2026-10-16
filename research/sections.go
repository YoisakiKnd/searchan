package research

import (
	"strings"
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
		parts := splitPassages(body)
		for i := range parts {
			parts[i].first += paragraph - 1
			parts[i].last += paragraph - 1
			if i == 0 && len(heading) <= 240 && len(parts[i].text) >= len(heading)+40 {
				parts[i].heading = heading
			}
		}
		out = append(out, parts...)
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

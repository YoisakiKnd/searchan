package research

import (
	"strings"

	"searchan/reader"
	"searchan/search"
)

// Copy truncated fields so a small retained prefix cannot pin a multi-MiB
// supplier string. Compact each response before waiting for slower peers.
func retainedField(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.Clone(reader.Clip(s, limit))
}

func compactResponse(in search.Response) search.Response {
	out := search.Response{Provider: retainedField(in.Provider, 64)}
	out.Results = make([]search.Result, min(15, len(in.Results)))
	for i, r := range in.Results[:len(out.Results)] {
		if len(r.URL) > 4096 {
			continue
		} // Keep its original rank position.
		r.Title = retainedField(r.Title, 512)
		r.Snippet = retainedField(r.Snippet, 4096)
		r.ContentTruncated = r.ContentTruncated || len(r.Content) > reader.MaxText
		r.Content = retainedField(r.Content, reader.MaxText)
		r.PublishedAt = retainedField(r.PublishedAt, 128)
		r.ContentProvider = retainedField(r.ContentProvider, 64)
		sources := make([]search.Source, 0, min(8, len(r.Sources)))
		for _, source := range r.Sources[:min(8, len(r.Sources))] {
			copy := search.Source{Provider: retainedField(source.Provider, 64), Rank: source.Rank}
			for _, engine := range source.Engines[:min(8, len(source.Engines))] {
				copy.Engines = append(copy.Engines, retainedField(engine, 64))
			}
			sources = append(sources, copy)
		}
		r.Sources = sources
		out.Results[i] = r
	}
	for _, warning := range in.Warnings[:min(8, len(in.Warnings))] {
		out.Warnings = append(out.Warnings, retainedField(warning, 256))
	}
	return out
}

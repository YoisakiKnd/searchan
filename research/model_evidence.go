package research

import "time"

// modelEvidence retains source content and limitations without local ranking
// or audit bookkeeping. Output.Evidence keeps the complete provenance record.
// Use the same view for coverage, writing, verification and fresh regeneration.
type modelEvidence struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Text        string    `json:"text"`
	Kind        string    `json:"kind"`
	PublishedAt string    `json:"published_at,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
	ReadMethod  string    `json:"read_method,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
}

func evidenceForModel(evidence []Evidence) []modelEvidence {
	if evidence == nil {
		return nil
	}
	out := make([]modelEvidence, len(evidence))
	for i, ev := range evidence {
		out[i] = modelEvidence{ID: ev.ID, URL: ev.URL, Title: ev.Title, Text: ev.Text, Kind: ev.Kind, PublishedAt: ev.PublishedAt, FetchedAt: ev.FetchedAt, ReadMethod: ev.ReadMethod, Truncated: ev.Truncated}
	}
	return out
}

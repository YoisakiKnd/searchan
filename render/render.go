// Package render produces bounded PNG pages with an embedded trusted template.
package render

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"searchan/research"
)

//go:embed card.typ
var template string

type Artifact struct {
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int64  `json:"bytes"`
}
type Renderer interface {
	Render(context.Context, string, research.Output, string) ([]Artifact, error)
}
type Typst struct {
	Binary    string
	FontPaths []string
	MaxPages  int
	Timeout   time.Duration
	slots     chan struct{}
}

func New(binary string, concurrency int) *Typst {
	if binary == "" {
		binary = "typst"
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return &Typst{Binary: binary, MaxPages: 3, Timeout: 15 * time.Second, slots: make(chan struct{}, concurrency)}
}

type viewClaim struct {
	Text     string `json:"text"`
	Citation string `json:"citation"`
}
type viewSection struct {
	Heading string      `json:"heading"`
	Claims  []viewClaim `json:"claims"`
}
type viewRow struct {
	Cells    []string `json:"cells"`
	Citation string   `json:"citation"`
}
type viewTable struct {
	Columns []string  `json:"columns"`
	Rows    []viewRow `json:"rows"`
}
type viewSource struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Domain string `json:"domain"`
}
type view struct {
	Title         string        `json:"title"`
	Timestamp     string        `json:"timestamp"`
	TaskID        string        `json:"task_id"`
	Lead          viewClaim     `json:"lead"`
	Sections      []viewSection `json:"sections"`
	Table         *viewTable    `json:"table"`
	Uncertainties []string      `json:"uncertainties"`
	Sources       []viewSource  `json:"sources"`
}

func makeView(task string, o research.Output) view {
	v := view{Title: o.Answer.Title, Timestamp: o.GeneratedAt.In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02 15:04 MST"), TaskID: task, Sections: []viewSection{}, Sources: []viewSource{}, Uncertainties: append([]string{}, o.Answer.Uncertainties...)}
	ids := map[string]int{}
	for _, s := range o.Sources {
		u, _ := url.Parse(s.URL)
		domain := ""
		if u != nil {
			domain = u.Hostname()
		}
		v.Sources = append(v.Sources, viewSource{s.ID, s.Title, domain})
		for _, id := range s.EvidenceIDs {
			ids[id] = s.ID
		}
	}
	convert := func(c research.Claim) viewClaim {
		seen := map[int]bool{}
		var nums []int
		for _, id := range c.EvidenceIDs {
			n := ids[id]
			if n > 0 && !seen[n] {
				seen[n] = true
				nums = append(nums, n)
			}
		}
		sort.Ints(nums)
		var refs []string
		for _, n := range nums {
			refs = append(refs, strconv.Itoa(n))
		}
		citation := ""
		if len(refs) > 0 {
			citation = "[" + strings.Join(refs, ", ") + "]"
		}
		return viewClaim{c.Text, citation}
	}
	v.Lead = convert(o.Answer.Lead)
	for _, s := range o.Answer.Sections {
		section := viewSection{Heading: s.Heading, Claims: []viewClaim{}}
		for _, c := range s.Claims {
			section.Claims = append(section.Claims, convert(c))
		}
		v.Sections = append(v.Sections, section)
	}
	if o.Answer.Table != nil {
		v.Table = &viewTable{Columns: o.Answer.Table.Columns, Rows: []viewRow{}}
		for _, r := range o.Answer.Table.Rows {
			v.Table.Rows = append(v.Table.Rows, viewRow{r.Cells, convert(research.Claim{EvidenceIDs: r.EvidenceIDs}).Citation})
		}
	}
	return v
}

// cappedLog keeps compiler diagnostics from consuming unbounded memory.
type cappedLog struct{ b bytes.Buffer }

func (w *cappedLog) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - w.b.Len(); remaining > 0 {
		w.b.Write(p[:min(remaining, n)])
	}
	return n, nil
}

func (r *Typst) Render(ctx context.Context, task string, o research.Output, dest string) ([]Artifact, error) {
	if err := research.ValidateAnswer(o.Answer, o.Evidence); err != nil {
		return nil, fmt.Errorf("cannot render invalid answer: %w", err)
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	abs, err := filepath.Abs(dest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(abs, ".render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	data, err := json.Marshal(makeView(task, o))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(work, "answer.json"), data, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(work, "card.typ"), []byte(template), 0600); err != nil {
		return nil, err
	}
	args := []string{"compile", "--root", work, "--ppi", "152"}
	for _, font := range r.FontPaths {
		args = append(args, "--font-path", font)
	}
	args = append(args, filepath.Join(work, "card.typ"), filepath.Join(work, "page-{0p}.png"))
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = work
	var log cappedLog
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("Typst compile failed: %w: %s", err, log.b.String())
	}
	paths, err := filepath.Glob(filepath.Join(work, "page-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	maxPages := r.MaxPages
	if maxPages <= 0 {
		maxPages = 3
	}
	if len(paths) == 0 || len(paths) > maxPages {
		return nil, fmt.Errorf("rendered %d pages; limit is %d", len(paths), maxPages)
	}
	var artifacts []Artifact
	// Check image headers before publishing any output. PNG pixel data is never
	// decoded in Go, keeping the service's memory separate from compiler memory.
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		cfg, _, decodeErr := image.DecodeConfig(io.LimitReader(f, 4096))
		info, statErr := f.Stat()
		f.Close()
		if decodeErr != nil || statErr != nil {
			return nil, errors.New("invalid rendered PNG")
		}
		if cfg.Width > 4096 || cfg.Height > 8192 || cfg.Width < 400 || info.Size() > 8<<20 {
			return nil, errors.New("rendered PNG exceeds platform budget")
		}
		artifacts = append(artifacts, Artifact{Path: filepath.Join(abs, filepath.Base(path)), Width: cfg.Width, Height: cfg.Height, Bytes: info.Size()})
	}
	for i, path := range paths {
		if err := os.Rename(path, artifacts[i].Path); err != nil {
			return nil, err
		}
	}
	return artifacts, nil
}

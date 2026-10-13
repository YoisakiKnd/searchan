package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"searchan/quality"
)

func qualityMode(ctx context.Context, args []string, out io.Writer, ambient func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: searchan quality list|run|review [flags]")
	}
	fs := flag.NewFlagSet("quality "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	cases := fs.String("cases", "quality/cases.json", "fixed real-query rubric suite")
	ids := fs.String("case", "", "comma-separated case IDs")
	split := fs.String("split", "pilot", "pilot, development, reserve, all")
	limit := fs.Int("limit", 3, "maximum cases to run serially (1-100)")
	envFile := fs.String("env-file", "", "private local env data file; never executed as shell")
	dest := fs.String("out", "", "fresh run directory; review uses an existing directory")
	replay := fs.String("replay", "", "prior run directory; replay search/read snapshots with the real model")
	fixed := fs.String("fixed-evidence", "", "prior run directory; freeze final evidence, IDs, plan and reference date; model only")
	render := fs.Bool("render", false, "render actual Typst PNGs locally")
	typst := fs.String("typst", "", "override Typst executable for this machine")
	label := fs.String("label", "", "human-readable run label")
	continueUnavailable := fs.Bool("continue-on-unavailable", false, "continue batch after consecutive search outages; may waste paid model calls")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected quality arguments")
	}
	if args[0] == "review" {
		if *dest == "" {
			return errors.New("quality review requires -out <run-dir>")
		}
		summary, err := quality.SummarizeReview(*dest)
		if err != nil {
			return err
		}
		return writeJSON(out, summary)
	}
	if args[0] != "run" && args[0] != "list" {
		return errors.New("unknown quality command")
	}
	suite, err := quality.Load(*cases)
	if err != nil {
		return err
	}
	selected, err := quality.Select(suite, *ids, *split, *limit)
	if err != nil {
		return err
	}
	if args[0] == "list" {
		return writeJSON(out, selected)
	}
	env, err := quality.Environment(*envFile, ambient)
	if err != nil {
		return err
	}
	if *typst != "" {
		base := env
		env = func(k string) string {
			if k == "TYPST_BIN" {
				return *typst
			}
			return base(k)
		}
	}
	if *dest == "" {
		*dest = filepath.Join("artifacts", "quality", time.Now().Format("20060102-150405.000000000"))
	}
	abs, err := filepath.Abs(*dest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Quality run: %s\n", abs)
	// Progress stays visible; report.json is the machine-readable result. Neither
	// the private configuration nor any raw model request is printed.
	report, err := quality.Run(ctx, quality.Options{Suite: suite, Cases: selected, Out: abs, Replay: *replay, FixedEvidence: *fixed, Render: *render, Label: *label, ContinueOnUnavailable: *continueUnavailable}, env, out)
	if len(report.Cases) > 0 {
		fmt.Fprintf(out, "completed=%d failed=%d incomplete=%d p50_ms=%d p95_ms=%d; factual review pending\n", report.Completed, report.Failures, report.Incomplete, report.P50MS, report.P95MS)
		if len(report.UnrunCases) > 0 {
			fmt.Fprintf(out, "stopped=%s unrun=%d; unrun cases have no outputs or factual review\n", report.StoppedReason, len(report.UnrunCases))
		}
	}
	return err
}

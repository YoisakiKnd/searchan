package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"time"

	"searchan/app"
	"searchan/demo"
	"searchan/evaluation"
	"searchan/internal/fsutil"
	"searchan/render"
)

func modes(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	switch args[0] {
	case "eval":
		fs := flag.NewFlagSet("eval", flag.ContinueOnError)
		fs.SetOutput(out)
		cases := fs.String("cases", "evaluation/cases.json", "offline evaluation dataset")
		replays := fs.String("replays", "", "directory of saved <case-id>.json research outputs")
		limit := fs.Int("limit", 5, "maximum selected evidence passages")
		dest := fs.String("out", "", "optional JSON report path")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		data, err := evaluation.Load(*cases)
		if err != nil {
			return err
		}
		report, err := evaluation.Run(data, *replays, *limit)
		if err != nil {
			return err
		}
		if *dest != "" {
			if err := fsutil.WriteJSON(*dest, report); err != nil {
				return err
			}
		}
		return writeJSON(out, report)
	case "demo":
		fs := flag.NewFlagSet("demo", flag.ContinueOnError)
		fs.SetOutput(out)
		dest := fs.String("out", "artifacts/demo", "image output directory")
		binary := fs.String("typst", getenv("TYPST_BIN"), "Typst executable")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		output := demo.Output()
		renderer := render.New(*binary, 1)
		artifacts, err := renderer.Render(ctx, "demo", output, *dest)
		if err != nil {
			return err
		}
		if err := fsutil.WriteJSON(filepath.Join(*dest, "output.json"), output); err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"mode": "offline-demo", "images": artifacts})
	case "ask":
		fs := flag.NewFlagSet("ask", flag.ContinueOnError)
		fs.SetOutput(out)
		dest := fs.String("out", "", "image output directory")
		textOnly := fs.Bool("text", false, "skip rendering and return structured research JSON")
		providers := fs.String("providers", "", "override SEARCHAN_PROVIDERS")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() == 0 {
			return errors.New("usage: searchan ask [-providers ddg,brave] [-out artifacts/result] <question>")
		}
		env := getenv
		if *providers != "" {
			env = func(k string) string {
				if k == "SEARCHAN_PROVIDERS" {
					return *providers
				}
				return getenv(k)
			}
		}
		runtime, err := app.New(env)
		if err != nil {
			return err
		}
		defer runtime.Close()
		id := time.Now().UTC().Format("20060102T150405.000000000")
		if *dest == "" {
			*dest = filepath.Join("artifacts", id)
		}
		taskCtx, cancel := context.WithTimeout(ctx, runtime.Config.Timeout)
		defer cancel()
		researchCtx, stop := context.WithTimeout(taskCtx, runtime.Config.ResearchTimeout)
		output, err := runtime.Engine.Run(researchCtx, joinQuery(fs.Args()))
		stop()
		if err != nil {
			_ = writeJSON(out, output)
			return err
		}
		if *textOnly {
			return writeJSON(out, output)
		}
		if err := fsutil.WriteJSON(filepath.Join(*dest, "output.json"), output); err != nil {
			return err
		}
		artifacts, err := runtime.Renderer.Render(taskCtx, id, output, *dest)
		if err != nil {
			_ = writeJSON(out, map[string]any{"output": output, "render_error": err.Error()})
			return err
		}
		return writeJSON(out, map[string]any{"images": artifacts, "output_file": filepath.Join(*dest, "output.json"), "stats": output.Stats})
	case "serve":
		if len(args) > 1 {
			return errors.New("serve configuration uses environment variables; see .env.example")
		}
		runtime, err := app.New(getenv)
		if err != nil {
			return err
		}
		defer runtime.Close()
		server, err := app.NewServer(runtime)
		if err != nil {
			return err
		}
		defer server.Close()
		return server.Run(ctx)
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		fs.SetOutput(out)
		strict := fs.Bool("strict", false, "require synthesis, verification, access policy and actual rendering")
		online := fs.Bool("online", false, "read Telegram identity and webhook status; never sends messages")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected doctor argument")
		}
		runtime, err := app.New(getenv)
		if err != nil {
			return err
		}
		defer runtime.Close()
		report := runtime.Preflight(ctx, *strict, *online)
		if err := writeJSON(out, report); err != nil {
			return err
		}
		if !report.Ready {
			return errors.New("preflight failed; resolve failing checks before serving")
		}
		return nil
	case "healthcheck", "telegram", "version":
		return operations(ctx, args, out, getenv)
	}
	return errors.New("unknown subcommand")
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"searchan/search"
)

func main() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(96 << 20)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	if len(args) > 0 {
		switch args[0] {
		case "ask", "demo", "serve", "doctor", "eval", "healthcheck", "telegram", "version", "model":
			return modes(ctx, args, out, getenv)
		}
	}
	fs := flag.NewFlagSet("searchan", flag.ContinueOnError)
	fs.SetOutput(out)
	providerNames := fs.String("providers", "ddg", "comma-separated: ddg,brave,tavily,exa,searxng,jina")
	limit := fs.Int("limit", 10, "maximum results per provider (1-100, subject to provider caps)")
	page := fs.Int("page", 1, "one-based page; pages >1 require Brave or SearXNG")
	timeout := fs.Duration("timeout", 25*time.Second, "overall search timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return errors.New("usage: searchan [-providers ddg,brave] [-limit 10] <query>")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if *limit < 1 || *limit > 100 || *page < 1 {
		return errors.New("limit must be 1-100 and page must be positive")
	}
	var providers []search.Provider
	for _, name := range strings.Split(*providerNames, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		prefix := strings.ToUpper(name)
		cfg := search.Config{APIKey: getenv(prefix + "_API_KEY"), Endpoint: getenv(prefix + "_ENDPOINT")}
		if name == "searxng" {
			for _, engine := range strings.Split(getenv("SEARXNG_ENGINES"), ",") {
				if engine = strings.TrimSpace(engine); engine != "" {
					cfg.Engines = append(cfg.Engines, engine)
				}
			}
		}
		p, err := search.NewProvider(name, cfg)
		if err != nil {
			return err
		}
		providers = append(providers, p)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := search.SearchAll(ctx, providers, search.Request{Query: query, Limit: *limit, Page: *page})
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if encodeErr := enc.Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}

func joinQuery(args []string) string { return strings.TrimSpace(strings.Join(args, " ")) }

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"runtime"
	"time"

	"searchan/internal/httpx"
	"searchan/platform"
)

// Set through -ldflags in release builds; development binaries remain explicit.
var version = "dev"
var commit = "unknown"

func operations(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	if args[0] == "version" {
		if len(args) != 1 {
			return errors.New("version accepts no arguments")
		}
		return writeJSON(out, map[string]string{"version": version, "commit": commit, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH})
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	if args[0] == "healthcheck" {
		address := getenv("SEARCHAN_LISTEN")
		if address == "" {
			address = "127.0.0.1:8090"
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return errors.New("invalid SEARCHAN_LISTEN")
		}
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		} else if host == "::" {
			host = "::1"
		}
		url := fs.String("url", "http://"+net.JoinHostPort(host, port)+"/readyz", "readiness URL")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected healthcheck argument")
		}
		client := httpx.NewClient(3 * time.Second)
		defer client.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, "GET", *url, nil)
		if err != nil {
			return errors.New("invalid readiness URL")
		}
		res, err := client.Do(req)
		if err != nil {
			return errors.New("readiness request failed")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("service is not ready")
		}
		var state struct {
			Status string `json:"status"`
		}
		if err := httpx.Decode(res.Body, 4096, &state); err != nil || state.Status != "ready" {
			return errors.New("invalid readiness response")
		}
		return writeJSON(out, state)
	}
	if len(args) < 2 {
		return errors.New("usage: searchan telegram info | webhook -url https://bot.example.org/webhooks/telegram")
	}
	url := fs.String("url", "", "public HTTPS Telegram webhook URL")
	drop := fs.Bool("drop-pending", false, "explicitly discard pending Telegram updates when registering")
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected Telegram argument")
	}
	client := httpx.NewClient(10 * time.Second)
	defer client.CloseIdleConnections()
	bot := &platform.Telegram{Token: getenv("TELEGRAM_BOT_TOKEN"), WebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"), Endpoint: getenv("TELEGRAM_API_ENDPOINT"), Client: client}
	if bot.Token == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	switch args[1] {
	case "info":
		identity, err := bot.GetMe(ctx)
		if err != nil {
			return err
		}
		webhook, err := bot.WebhookInfo(ctx)
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"bot": identity, "webhook": webhook})
	case "webhook":
		if err := bot.SetWebhook(ctx, *url, *drop); err != nil {
			return err
		}
		return writeJSON(out, map[string]bool{"registered": true, "dropped_pending": *drop})
	default:
		return errors.New("unknown Telegram operation")
	}
}

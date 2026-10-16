// Command litefeed-mcp collects RSS feeds, stores them and serves them
// through a web UI, a REST API and an MCP endpoint.
package main

import (
	"flag"
	"log/slog"
	"os"

	"litefeed-mcp/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load configuration", "path", *configPath, "err", err)
		os.Exit(1)
	}

	slog.Info("configuration loaded",
		"addr", cfg.Server.Addr(),
		"storage", cfg.Storage.Type,
		"feeds", len(cfg.Feeds),
		"interval", cfg.Scrape.Interval,
	)

	// TODO: start storage, scraper, retention worker and HTTP server.
}

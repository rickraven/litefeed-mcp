// Command litefeed-mcp collects RSS feeds, stores them and serves them
// through a web UI, a REST API and an MCP endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embedded time zone database: the API accepts IANA zones ("tz") and
	// the standalone binary must not depend on the host's zoneinfo.
	_ "time/tzdata"

	"litefeed-mcp/internal/api"
	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/mcpserver"
	"litefeed-mcp/internal/scraper"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/markdown"
	"litefeed-mcp/internal/storage/sqlite"
	"litefeed-mcp/internal/web"
)

// version is reported to MCP clients; set at build time with
// -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	debug := flag.Bool("debug", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("litefeed-mcp", version)
		return
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := run(*configPath); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

// run wires the components together and blocks until SIGINT/SIGTERM.
func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	slog.Info("configuration loaded",
		"path", configPath,
		"addr", cfg.Server.Addr(),
		"storage", cfg.Storage.Type,
		"feeds", len(cfg.Feeds),
		"interval", cfg.Scrape.Interval,
	)

	store, err := openStore(cfg.Storage)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The scheduler stops on context cancellation; waiting for it ensures
	// no write is in progress when the store is closed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		scraper.NewScheduler(cfg, store).Run(ctx)
	}()

	mux := http.NewServeMux()
	api.New(store, cfg.Feeds).Register(mux)
	web.New(store).Register(mux)
	mux.Handle("/mcp", mcpserver.Handler(store, cfg.Feeds, version))

	srv := &http.Server{
		Addr:              cfg.Server.Addr(),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	srvErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-srvErr:
		// The listener failed (e.g. port in use): stop the scheduler too.
		stop()
	}
	slog.Info("shutting down")

	// In-flight requests get a few seconds to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http server shutdown", "err", err)
	}
	<-done
	if runErr != nil {
		return fmt.Errorf("http server: %w", runErr)
	}
	return nil
}

// openStore creates the storage backend selected in the configuration.
func openStore(cfg config.Storage) (storage.Store, error) {
	switch cfg.Type {
	case config.StorageSQLite:
		return sqlite.Open(cfg.SQLite.Path)
	case config.StorageMarkdown:
		return markdown.Open(cfg.Markdown.Dir)
	default:
		return nil, fmt.Errorf("unknown storage type %q", cfg.Type)
	}
}

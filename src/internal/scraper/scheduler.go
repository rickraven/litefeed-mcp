package scraper

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/storage"
)

// maxParallelFeeds limits how many feeds are downloaded at the same time,
// keeping resource usage predictable with long feed lists.
const maxParallelFeeds = 4

// Scheduler periodically scrapes all configured feeds and removes
// entries older than the retention period.
type Scheduler struct {
	fetcher  *Fetcher
	store    storage.Store
	feeds    []config.Feed
	interval time.Duration
	// maxAge is the retention period; zero disables cleanup.
	maxAge time.Duration
	// now returns the current time; replaced in tests.
	now func() time.Time
}

// NewScheduler creates a scheduler from the application configuration.
func NewScheduler(cfg *config.Config, store storage.Store) *Scheduler {
	return &Scheduler{
		fetcher:  NewFetcher(cfg.Scrape.Timeout, cfg.Scrape.UserAgent),
		store:    store,
		feeds:    cfg.Feeds,
		interval: cfg.Scrape.Interval,
		maxAge:   cfg.Retention.MaxAge(),
		now:      time.Now,
	}
}

// Run performs a cycle immediately and then every interval until ctx is
// cancelled. A cycle that takes longer than the interval delays the next
// one instead of overlapping with it.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce scrapes every feed and then applies the retention policy.
// Errors are logged: a failing feed must not affect the others.
func (s *Scheduler) RunOnce(ctx context.Context) {
	start := s.now()
	cutoff := s.cutoff()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		saved int
	)
	sem := make(chan struct{}, maxParallelFeeds)
	for _, feed := range s.feeds {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Go(func() {
			defer func() { <-sem }()
			res, err := ScrapeFeed(ctx, s.fetcher, s.store, feed, cutoff)
			if err != nil {
				slog.Error("feed scrape failed", "feed", feed.Name, "err", err)
				return
			}
			slog.Debug("feed scraped", "feed", feed.Name, "items", res.Items,
				"saved", res.Saved, "skipped", res.Skipped, "old", res.Old)
			mu.Lock()
			saved += res.Saved
			mu.Unlock()
		})
	}
	wg.Wait()
	slog.Info("scrape cycle finished", "feeds", len(s.feeds), "saved", saved,
		"duration", s.now().Sub(start).Round(time.Millisecond))

	s.cleanup(ctx)
}

// cutoff returns the retention boundary (now - maxAge) or the zero time
// when retention is disabled.
func (s *Scheduler) cutoff() time.Time {
	if s.maxAge <= 0 {
		return time.Time{}
	}
	return s.now().Add(-s.maxAge)
}

// cleanup removes entries published before the retention cutoff.
func (s *Scheduler) cleanup(ctx context.Context) {
	cutoff := s.cutoff()
	if cutoff.IsZero() || ctx.Err() != nil {
		return
	}
	n, err := s.store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		slog.Error("retention cleanup failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("old entries removed", "count", n)
	}
}

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/storage"
)

// Result summarizes a single feed scrape.
type Result struct {
	// Items is the number of items in the downloaded feed.
	Items int
	// Saved is the number of new entries written to the storage.
	Saved int
	// Skipped is the number of items that could not be converted.
	Skipped int
	// Old is the number of items ignored because they were published
	// before the retention cutoff.
	Old int
}

// ScrapeFeed downloads one feed and saves its new items. Items that fail
// to convert are logged and skipped; a storage error aborts the scrape.
//
// Items published before cutoff are ignored: otherwise they would be saved
// and removed by the retention cleanup again on every cycle. A zero cutoff
// disables the check.
func ScrapeFeed(ctx context.Context, f *Fetcher, store storage.Store, feed config.Feed, cutoff time.Time) (Result, error) {
	var res Result
	parsed, err := f.Fetch(ctx, feed.URL)
	if err != nil {
		return res, err
	}
	res.Items = len(parsed.Items)

	now := time.Now()
	for _, item := range parsed.Items {
		e, err := itemToEntry(feed.Name, item, now)
		if err != nil {
			slog.Warn("skipping feed item", "feed", feed.Name, "link", item.Link, "err", err)
			res.Skipped++
			continue
		}
		if e.ID == "" {
			slog.Warn("skipping feed item without guid, link and title", "feed", feed.Name)
			res.Skipped++
			continue
		}
		if !cutoff.IsZero() && e.Published.Before(cutoff) {
			res.Old++
			continue
		}
		saved, err := store.Save(ctx, e)
		if err != nil {
			return res, fmt.Errorf("save entry: %w", err)
		}
		if saved {
			res.Saved++
		}
	}
	return res, nil
}

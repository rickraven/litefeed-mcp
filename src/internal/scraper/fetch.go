// Package scraper downloads feeds, converts their items into entries and
// saves them to the storage.
package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mmcdole/gofeed"
)

// maxFeedSize limits the size of a downloaded feed to protect against
// misbehaving servers.
const maxFeedSize = 20 << 20 // 20 MiB

// Fetcher downloads and parses feeds over HTTP.
type Fetcher struct {
	client    *http.Client
	userAgent string
}

// NewFetcher returns a Fetcher with the given request timeout and
// User-Agent header.
func NewFetcher(timeout time.Duration, userAgent string) *Fetcher {
	return &Fetcher{
		client:    &http.Client{Timeout: timeout},
		userAgent: userAgent,
	}
}

// Fetch downloads the feed at url and parses it. RSS, Atom and JSON Feed
// formats are supported (detected by gofeed).
func (f *Fetcher) Fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, */*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("download feed: unexpected status %s", resp.Status)
	}

	feed, err := gofeed.NewParser().Parse(io.LimitReader(resp.Body, maxFeedSize))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return feed, nil
}

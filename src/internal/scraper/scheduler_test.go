package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/markdown"
)

// TestSchedulerRunOnce verifies that working feeds are saved despite a
// failing one and that old entries are cleaned up.
func TestSchedulerRunOnce(t *testing.T) {
	ctx := context.Background()
	var ua string
	srv := newServer(t, &ua)
	store, _ := markdown.Open(t.TempDir())

	old := &entry.Entry{Feed: "old", Title: "old", Link: "https://x/old"}
	old.Normalize(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	store.Save(ctx, old)

	cfg := &config.Config{
		Scrape:    config.Scrape{Interval: time.Hour, Timeout: 5 * time.Second, UserAgent: "x"},
		Retention: config.Retention{MaxAgeDays: 30},
		Feeds: []config.Feed{
			{Name: "good", URL: srv.URL + "/rss"},
			{Name: "broken", URL: srv.URL + "/missing"},
		},
	}
	NewScheduler(cfg, store).RunOnce(ctx)

	if ok, _ := store.Has(ctx, old.ID); ok {
		t.Error("old entry must be removed by retention")
	}
	list, _ := store.List(ctx, storage.Query{Feeds: []string{"good"}})
	if len(list) != 2 {
		t.Errorf("expected 2 entries from the good feed, got %d", len(list))
	}
}

// TestSchedulerRun verifies periodic execution and stopping on cancel.
func TestSchedulerRun(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(testRSS))
	}))
	defer srv.Close()
	store, _ := markdown.Open(t.TempDir())

	s := NewScheduler(&config.Config{
		Scrape: config.Scrape{Interval: 20 * time.Millisecond, Timeout: time.Second, UserAgent: "x"},
		Feeds:  []config.Feed{{Name: "f", URL: srv.URL}},
	}, store)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
	if n := hits.Load(); n < 3 {
		t.Errorf("expected at least 3 scrapes, got %d", n)
	}
}

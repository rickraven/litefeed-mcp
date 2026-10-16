package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/markdown"
)

// testRSS covers: content:encoded with a relative image, description-only
// item, HTML entities in the title, image enclosure and a duplicate item.
const testRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
  <title>Test</title>
  <link>https://example.com/</link>
  <item>
    <title>Новая версия Kubernetes &amp; Go</title>
    <link>https://example.com/posts/1</link>
    <guid>post-1</guid>
    <dc:creator>Иван</dc:creator>
    <pubDate>Fri, 16 Oct 2026 09:30:00 +0300</pubDate>
    <category>k8s</category>
    <description>Short &lt;b&gt;summary&lt;/b&gt;</description>
    <content:encoded><![CDATA[<p>Full <strong>text</strong> <a href="/docs">docs</a></p><p><img src="/img/a.png" alt="diagram"></p>]]></content:encoded>
    <enclosure url="https://cdn.example.com/cover.jpg" type="image/jpeg" length="1"/>
  </item>
  <item>
    <title>Plain</title>
    <link>https://example.com/posts/2</link>
    <description>line one
line two</description>
  </item>
  <item>
    <title>Duplicate</title>
    <link>https://example.com/posts/1-copy</link>
    <guid>post-1</guid>
  </item>
</channel>
</rss>`

// newServer serves testRSS and records the received User-Agent.
func newServer(t *testing.T, ua *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ua = r.Header.Get("User-Agent")
		if r.URL.Path != "/rss" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(testRSS))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestScrapeFeed runs the full fetch -> convert -> save pipeline.
func TestScrapeFeed(t *testing.T) {
	ctx := context.Background()
	var ua string
	srv := newServer(t, &ua)
	store, err := markdown.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fetcher := NewFetcher(5*time.Second, "litefeed-test/1.0")
	feed := config.Feed{Name: "test", URL: srv.URL + "/rss"}

	res, err := ScrapeFeed(ctx, fetcher, store, feed, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if ua != "litefeed-test/1.0" {
		t.Errorf("User-Agent = %q", ua)
	}
	// Three items, the third duplicates the first by GUID.
	if res.Items != 3 || res.Saved != 2 || res.Skipped != 0 {
		t.Errorf("result = %+v", res)
	}

	// A repeated scrape must not save anything new.
	if res, _ := ScrapeFeed(ctx, fetcher, store, feed, time.Time{}); res.Saved != 0 {
		t.Errorf("second scrape saved %d entries", res.Saved)
	}

	list, err := store.List(ctx, storage.Query{})
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d entries, err %v", len(list), err)
	}
	byLink := make(map[string]entry.Entry)
	for _, e := range list {
		byLink[e.Link] = e
	}
	first, plain := byLink["https://example.com/posts/1"], byLink["https://example.com/posts/2"]
	if first.Title != "Новая версия Kubernetes & Go" || first.Author != "Иван" ||
		!first.Published.Equal(time.Date(2026, 10, 16, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("unexpected entry: %+v", first)
	}
	for _, want := range []string{
		"![](https://cdn.example.com/cover.jpg)",
		"**text**",
		"[docs](https://example.com/docs)",
		"![diagram](https://example.com/img/a.png)",
	} {
		if !strings.Contains(first.Content, want) {
			t.Errorf("content misses %q:\n%s", want, first.Content)
		}
	}
	if first.Summary != "Short **summary**" {
		t.Errorf("summary = %q", first.Summary)
	}
	if plain.Content != "line one\nline two" || plain.Summary != "" {
		t.Errorf("plain entry: content %q, summary %q", plain.Content, plain.Summary)
	}
}

// TestScrapeFeedCutoff verifies that items older than the retention cutoff
// are not saved. Only the first item has an explicit date (2026-10-16);
// the undated ones get the current time and are kept.
func TestScrapeFeedCutoff(t *testing.T) {
	var ua string
	srv := newServer(t, &ua)
	store, _ := markdown.Open(t.TempDir())
	feed := config.Feed{Name: "test", URL: srv.URL + "/rss"}
	cutoff := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	res, err := ScrapeFeed(context.Background(), NewFetcher(time.Second, "x"), store, feed, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if res.Old != 1 || res.Saved != 2 {
		t.Errorf("result = %+v", res)
	}
}

// TestFetchErrors verifies that HTTP and parse errors are reported.
func TestFetchErrors(t *testing.T) {
	var ua string
	srv := newServer(t, &ua)
	f := NewFetcher(5*time.Second, "x")
	for _, path := range []string{"/missing", "/"} {
		if _, err := f.Fetch(context.Background(), srv.URL+path); err == nil {
			t.Errorf("expected error for %s", path)
		}
	}
}

// TestPlainText verifies title cleanup.
func TestPlainText(t *testing.T) {
	cases := map[string]string{
		"  Hello   world ":             "Hello world",
		"A &amp; B":                    "A & B",
		"<b>Bold</b> title":            "Bold title",
		"Price *50%* [sale] \\o/ <br>": `Price *50%* [sale] \o/`,
	}
	for in, want := range cases {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}

package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/markdown"
)

// articlePage is a typical article page: navigation and footer around the
// main content, which is long enough for Readability to accept it.
var articlePage = `<!doctype html><html><head><title>Article</title></head><body>
<nav><a href="/">Home</a> <a href="/about">About</a></nav>
<article>
  <h1>Full article</h1>
  <p>` + strings.Repeat("This is the full article text with many words. ", 30) + `</p>
  <p><img src="/img/diagram.png" alt="diagram"> See the <a href="/docs">docs</a>.</p>
  <p>` + strings.Repeat("Another long paragraph to make the content substantial. ", 20) + `</p>
</article>
<footer>Copyright footer text</footer>
</body></html>`

// completeFeedText is a feed description longer than the article page
// text, as in feeds that publish full articles.
var completeFeedText = strings.Repeat("The feed already has the complete text. ", 100)

// newFullTextServer serves a feed whose items point to: a good article,
// a missing page and a page without readable content. articleHits counts
// requests to the good article.
func newFullTextServer(t *testing.T, articleHits *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/rss", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Good</title><link>%[1]s/good</link><description>Short teaser</description></item>
<item><title>Missing</title><link>%[1]s/missing</link><description>Teaser of missing</description></item>
<item><title>Empty</title><link>%[1]s/empty</link><description>Teaser of empty page</description></item>
<item><title>Complete</title><link>%[1]s/complete</link><description>%[2]s</description></item>
</channel></rss>`, srv.URL, completeFeedText)
	})
	mux.HandleFunc("/complete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(articlePage))
	})
	mux.HandleFunc("/good", func(w http.ResponseWriter, r *http.Request) {
		articleHits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(articlePage))
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><nav>menu</nav></body></html>`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestScrapeFeedFullText(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := newFullTextServer(t, &hits)
	store, _ := markdown.Open(t.TempDir())
	fetcher := NewFetcher(5*time.Second, "x")
	feed := config.Feed{Name: "f", URL: srv.URL + "/rss", FullText: true}

	res, err := ScrapeFeed(ctx, fetcher, store, feed, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved != 4 || res.FullTextFailed != 2 {
		t.Errorf("result = %+v", res)
	}

	list, _ := store.List(ctx, storage.Query{})
	byTitle := make(map[string]entry.Entry)
	for _, e := range list {
		byTitle[e.Title] = e
	}

	good := byTitle["Good"]
	if good.FullText != entry.FullTextOK || good.Summary != "Short teaser" {
		t.Errorf("good: status %q, summary %q", good.FullText, good.Summary)
	}
	for _, want := range []string{
		"This is the full article text",
		"![diagram](" + srv.URL + "/img/diagram.png)",
		"[docs](" + srv.URL + "/docs)",
	} {
		if !strings.Contains(good.Content, want) {
			t.Errorf("good content misses %q:\n%s", want, good.Content)
		}
	}
	for _, junk := range []string{"Home", "Copyright footer"} {
		if strings.Contains(good.Content, junk) {
			t.Errorf("good content contains page chrome %q", junk)
		}
	}

	for _, title := range []string{"Missing", "Empty"} {
		e := byTitle[title]
		if e.FullText != entry.FullTextFailed || !strings.HasPrefix(e.Content, "Teaser of") || e.Summary != "" {
			t.Errorf("%s: status %q, content %q, summary %q", title, e.FullText, e.Content, e.Summary)
		}
	}

	if c := byTitle["Complete"]; c.FullText != entry.FullTextNone ||
		c.Content != strings.TrimSpace(completeFeedText) || c.Summary != "" {
		t.Errorf("complete: status %q, summary %q", c.FullText, c.Summary)
	}

	// Known entries must not trigger another page download.
	if _, err := ScrapeFeed(ctx, fetcher, store, feed, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("article downloaded %d times, want 1", n)
	}
}

// TestFetchArticleRejectsNonHTML verifies the content type check.
func TestFetchArticleRejectsNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF"))
	}))
	defer srv.Close()
	if _, err := NewFetcher(time.Second, "x").FetchArticle(context.Background(), srv.URL); err == nil {
		t.Error("expected error for a PDF response")
	}
}

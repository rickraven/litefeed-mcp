package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"

	"litefeed-mcp/internal/entry"
)

// maxPageSize limits the size of a downloaded article page.
const maxPageSize = 10 << 20 // 10 MiB

// minArticleLen is the minimal plain text length (in characters) of an
// extracted article. Shorter results mean the page has no article (menus
// only, consent or error pages). The value matches the default
// Readability.js charThreshold.
const minArticleLen = 500

// FetchArticle downloads the page at link and extracts its main content
// with the Readability algorithm (a port of Mozilla's Readability.js).
// The result is converted to markdown; relative links are resolved
// against the page URL.
func (f *Fetcher) FetchArticle(ctx context.Context, link string) (string, error) {
	pageURL, err := url.Parse(link)
	if err != nil || (pageURL.Scheme != "http" && pageURL.Scheme != "https") {
		return "", fmt.Errorf("invalid article link %q", link)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download article: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download article: unexpected status %s", resp.Status)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "" &&
		mt != "text/html" && mt != "application/xhtml+xml" {
		return "", fmt.Errorf("download article: unsupported content type %q", mt)
	}

	// Redirects may lead to another URL; relative links must be resolved
	// against the final one.
	if resp.Request != nil && resp.Request.URL != nil {
		pageURL = resp.Request.URL
	}

	parser := readability.NewParser()
	article, err := parser.Parse(io.LimitReader(resp.Body, maxPageSize), pageURL)
	if err != nil {
		return "", fmt.Errorf("extract article: %w", err)
	}
	if article.Node == nil {
		return "", errors.New("extract article: no readable content found")
	}

	var html strings.Builder
	if err := article.RenderHTML(&html); err != nil {
		return "", fmt.Errorf("render article: %w", err)
	}
	md, err := toMarkdown(html.String(), pageURL.String())
	if err != nil {
		return "", err
	}
	// The limit len(md) never truncates: the plain text is not longer
	// (in characters) than the markdown is in bytes.
	if n := utf8.RuneCountInString(entry.Excerpt(md, len(md))); n < minArticleLen {
		return "", fmt.Errorf("extract article: no readable content (%d characters)", n)
	}
	return md, nil
}

// enrichWithFullText replaces the entry body with the full article text.
// The feed content is kept as Summary (unless the feed already provided a
// separate description). On failure the entry keeps the feed content and
// is marked with FullTextFailed; the error is returned for logging.
//
// When the page yields no more text than the feed (feeds that already
// publish full articles), this is not a failure: the feed content is kept
// with FullTextNone.
func enrichWithFullText(ctx context.Context, f *Fetcher, e *entry.Entry) error {
	md, err := f.FetchArticle(ctx, e.Link)
	if err != nil {
		e.FullText = entry.FullTextFailed
		return err
	}
	if len(md) <= len(e.Content) {
		slog.Debug("feed already contains the full text", "feed", e.Feed, "link", e.Link)
		e.FullText = entry.FullTextNone
		return nil
	}
	if e.Summary == "" {
		e.Summary = e.Content
	}
	e.Content = md
	e.FullText = entry.FullTextOK
	return nil
}

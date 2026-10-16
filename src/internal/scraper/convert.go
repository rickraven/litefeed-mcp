package scraper

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"

	"litefeed-mcp/internal/entry"
)

// itemToEntry converts a parsed feed item into a normalized entry.
//
// The body is taken from the full content (content:encoded in RSS,
// content in Atom) when present, otherwise from the description. When
// both exist, the description is kept as Summary. HTML is converted to
// markdown; relative links and images are resolved against the item link.
func itemToEntry(feedName string, item *gofeed.Item, now time.Time) (*entry.Entry, error) {
	e := &entry.Entry{
		GUID:       item.GUID,
		Feed:       feedName,
		Title:      plainText(item.Title),
		Link:       item.Link,
		Author:     author(item),
		Categories: item.Categories,
		FullText:   entry.FullTextNone,
	}
	if item.PublishedParsed != nil {
		e.Published = *item.PublishedParsed
	} else if item.UpdatedParsed != nil {
		e.Published = *item.UpdatedParsed
	}
	if item.UpdatedParsed != nil {
		e.Updated = *item.UpdatedParsed
	}

	content := strings.TrimSpace(item.Content)
	description := strings.TrimSpace(item.Description)
	var err error
	switch {
	case content != "":
		if e.Content, err = toMarkdown(content, item.Link); err != nil {
			return nil, err
		}
		if description != "" && description != content {
			if e.Summary, err = toMarkdown(description, item.Link); err != nil {
				return nil, err
			}
		}
	case description != "":
		if e.Content, err = toMarkdown(description, item.Link); err != nil {
			return nil, err
		}
	}
	e.Content = prependImages(e.Content, imageURLs(item))

	e.Normalize(now)
	return e, nil
}

// toMarkdown converts an HTML fragment to markdown. Strings without any
// markup are returned as is, because the converter would collapse their
// line breaks. baseURL (usually the article link) is used to make
// relative links and image sources absolute; it may be empty.
func toMarkdown(s, baseURL string) (string, error) {
	if !strings.ContainsAny(s, "<&") {
		return s, nil
	}
	var opts []converter.ConvertOptionFunc
	if domain := siteRoot(baseURL); domain != "" {
		opts = append(opts, converter.WithDomain(domain))
	}
	md, err := htmltomarkdown.ConvertString(s, opts...)
	if err != nil {
		return "", fmt.Errorf("convert html to markdown: %w", err)
	}
	return strings.TrimSpace(md), nil
}

// plainText turns a title that may contain markup or entities into plain
// text: tags are dropped, entities are decoded and whitespace is
// collapsed. The markdown converter is not used here because it escapes
// markdown special characters, which is not wanted in titles.
func plainText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return strings.Join(strings.Fields(s), " ")
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			// io.EOF or malformed input: return what was collected.
			return strings.Join(strings.Fields(b.String()), " ")
		case html.TextToken:
			b.Write(z.Text()) // Text() returns unescaped text
		}
	}
}

// siteRoot returns "scheme://host" of an absolute URL or "" otherwise.
func siteRoot(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// author returns the first author name reported for the item.
func author(item *gofeed.Item) string {
	if item.Author != nil && item.Author.Name != "" {
		return item.Author.Name
	}
	for _, a := range item.Authors {
		if a != nil && a.Name != "" {
			return a.Name
		}
	}
	return ""
}

// imageURLs collects images attached to the item outside its body: the
// item image and image enclosures (often used for cover pictures).
func imageURLs(item *gofeed.Item) []string {
	var urls []string
	if item.Image != nil && item.Image.URL != "" {
		urls = append(urls, item.Image.URL)
	}
	for _, enc := range item.Enclosures {
		if enc != nil && enc.URL != "" && strings.HasPrefix(enc.Type, "image/") {
			urls = append(urls, enc.URL)
		}
	}
	return urls
}

// prependImages adds markdown images for urls that are not already
// referenced in the body, so cover pictures are not lost.
func prependImages(body string, urls []string) string {
	var b strings.Builder
	seen := make(map[string]bool)
	for _, u := range urls {
		if seen[u] || strings.Contains(body, u) {
			continue
		}
		seen[u] = true
		fmt.Fprintf(&b, "![](%s)\n\n", u)
	}
	if b.Len() == 0 {
		return body
	}
	return strings.TrimSpace(b.String() + body)
}

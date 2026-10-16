// Package mcpserver exposes the stored entries to LLM clients through the
// Model Context Protocol (Streamable HTTP transport).
//
// Tools:
//
//	list_feeds      configured feeds
//	search_entries  brief entries filtered by feeds, dates and text
//	get_entry       a single entry with the full markdown body
//
// All tools are read-only, so the server runs in stateless mode: every
// HTTP request is handled independently, no session is kept.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
)

// Search limits. LLM context is expensive, so the default is small.
const (
	defaultLimit = 20
	maxLimit     = 100
)

// dateLayout is the short date format accepted by since/until.
const dateLayout = "2006-01-02"

// serverInstructions are sent to the client on initialization and help
// the model use the tools efficiently.
const serverInstructions = "litefeed stores articles collected from RSS/Atom feeds. " +
	"Use list_feeds to learn feed names, search_entries to find articles " +
	"(it returns short summaries only) and get_entry to read the full text " +
	"of a specific article. Prefer narrowing searches by feed, date and query."

// Handler returns the HTTP handler serving the MCP endpoint.
func Handler(store storage.Store, feeds []config.Feed, version string) http.Handler {
	server := NewServer(store, feeds, version)
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
}

// NewServer creates the MCP server with all tools registered.
func NewServer(store storage.Store, feeds []config.Feed, version string) *mcp.Server {
	t := &tools{store: store, feeds: feeds}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "litefeed-mcp",
		Title:   "litefeed",
		Version: version,
	}, &mcp.ServerOptions{Instructions: serverInstructions})

	// Tools only read local data.
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_feeds",
		Title:       "List feeds",
		Description: "Returns the configured feeds. Feed names are used to filter search_entries.",
		Annotations: readOnly,
	}, t.listFeeds)

	mcp.AddTool(server, &mcp.Tool{
		Name:  "search_entries",
		Title: "Search entries",
		Description: "Searches stored articles, newest first. Returns brief entries " +
			"(title, link, date, short summary) without the full text; use get_entry " +
			"to read an article. All filters are optional and combined with AND.",
		Annotations: readOnly,
	}, t.searchEntries)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_entry",
		Title:       "Get entry",
		Description: "Returns one article with its full text in markdown, by the id from search_entries.",
		Annotations: readOnly,
	}, t.getEntry)

	return server
}

// tools implements the tool handlers.
type tools struct {
	store storage.Store
	feeds []config.Feed
}

// ------------------------------------------------------------- list_feeds

// feedInfo describes a configured feed.
type feedInfo struct {
	Name  string `json:"name" jsonschema:"feed identifier used in filters"`
	Title string `json:"title" jsonschema:"human readable feed title"`
	URL   string `json:"url" jsonschema:"feed URL"`
}

type listFeedsOutput struct {
	Feeds []feedInfo `json:"feeds"`
}

func (t *tools) listFeeds(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, listFeedsOutput, error) {
	out := listFeedsOutput{Feeds: make([]feedInfo, 0, len(t.feeds))}
	for _, f := range t.feeds {
		out.Feeds = append(out.Feeds, feedInfo{Name: f.Name, Title: f.Title, URL: f.URL})
	}
	return nil, out, nil
}

// --------------------------------------------------------- search_entries

type searchInput struct {
	Feeds []string `json:"feeds,omitempty" jsonschema:"feed names to search in (see list_feeds); all feeds when empty"`
	Query string   `json:"query,omitempty" jsonschema:"words that must all occur in the article (case-insensitive substring match)"`
	Since string   `json:"since,omitempty" jsonschema:"only articles published at or after this date: YYYY-MM-DD (UTC) or RFC3339"`
	Until string   `json:"until,omitempty" jsonschema:"only articles published up to this date inclusive: YYYY-MM-DD (UTC) or RFC3339 (exclusive)"`
	Limit int      `json:"limit,omitempty" jsonschema:"maximum number of results, 1-100, default 20"`
}

// briefEntry is the search result item. Times are strings because the
// output schema is generated from this type.
type briefEntry struct {
	ID         string   `json:"id" jsonschema:"entry id for get_entry"`
	Feed       string   `json:"feed"`
	Title      string   `json:"title"`
	Link       string   `json:"link,omitempty"`
	Author     string   `json:"author,omitempty"`
	Published  string   `json:"published" jsonschema:"publication time, RFC3339"`
	Categories []string `json:"categories,omitempty"`
	Summary    string   `json:"summary,omitempty" jsonschema:"short plain text excerpt"`
}

type searchOutput struct {
	Entries []briefEntry `json:"entries"`
	More    bool         `json:"more" jsonschema:"true when more articles match than returned; narrow the filters or raise the limit"`
}

func (t *tools) searchEntries(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
	var out searchOutput
	q, limit, err := t.buildQuery(in)
	if err != nil {
		return nil, out, err
	}
	q.Limit = limit + 1 // one extra to detect "more"
	list, err := t.store.List(ctx, q)
	if err != nil {
		return nil, out, fmt.Errorf("search failed: %w", err)
	}
	if len(list) > limit {
		out.More = true
		list = list[:limit]
	}
	out.Entries = make([]briefEntry, 0, len(list))
	for i := range list {
		b := list[i].Brief()
		out.Entries = append(out.Entries, briefEntry{
			ID:         b.ID,
			Feed:       b.Feed,
			Title:      b.Title,
			Link:       b.Link,
			Author:     b.Author,
			Published:  b.Published.Format(time.RFC3339),
			Categories: b.Categories,
			Summary:    b.Summary,
		})
	}
	return nil, out, nil
}

// buildQuery validates the search input and converts it to a storage query.
func (t *tools) buildQuery(in searchInput) (storage.Query, int, error) {
	q := storage.Query{Text: in.Query}
	for _, name := range in.Feeds {
		if !slices.ContainsFunc(t.feeds, func(f config.Feed) bool { return f.Name == name }) {
			return q, 0, fmt.Errorf("unknown feed %q; call list_feeds to get valid names", name)
		}
		q.Feeds = append(q.Feeds, name)
	}

	limit := defaultLimit
	if in.Limit != 0 {
		if in.Limit < 1 || in.Limit > maxLimit {
			return q, 0, fmt.Errorf("limit must be between 1 and %d", maxLimit)
		}
		limit = in.Limit
	}

	var err error
	if q.Since, err = parseBound(in.Since, false); err != nil {
		return q, 0, fmt.Errorf("since: %w", err)
	}
	if q.Until, err = parseBound(in.Until, true); err != nil {
		return q, 0, fmt.Errorf("until: %w", err)
	}
	return q, limit, nil
}

// parseBound parses YYYY-MM-DD (UTC) or RFC3339. For an upper bound a
// plain date includes the whole day, so the next midnight is returned.
func parseBound(s string, upper bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.Parse(dateLayout, s); err == nil {
		if upper {
			d = d.AddDate(0, 0, 1)
		}
		return d, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.New("expected YYYY-MM-DD or RFC3339, e.g. 2026-10-16 or 2026-10-16T09:00:00Z")
	}
	return t, nil
}

// -------------------------------------------------------------- get_entry

type getEntryInput struct {
	ID string `json:"id" jsonschema:"entry id returned by search_entries"`
}

// fullEntry is the get_entry result.
type fullEntry struct {
	ID         string   `json:"id"`
	Feed       string   `json:"feed"`
	Title      string   `json:"title"`
	Link       string   `json:"link,omitempty"`
	Author     string   `json:"author,omitempty"`
	Published  string   `json:"published" jsonschema:"publication time, RFC3339"`
	Categories []string `json:"categories,omitempty"`
	Summary    string   `json:"summary,omitempty" jsonschema:"feed description when it differs from the content"`
	Content    string   `json:"content" jsonschema:"article text in markdown"`
}

func (t *tools) getEntry(ctx context.Context, _ *mcp.CallToolRequest, in getEntryInput) (*mcp.CallToolResult, fullEntry, error) {
	e, err := t.store.Get(ctx, strings.TrimSpace(in.ID))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fullEntry{}, fmt.Errorf("entry %q not found", in.ID)
	}
	if err != nil {
		return nil, fullEntry{}, fmt.Errorf("failed to load entry: %w", err)
	}
	out := fullEntry{
		ID:         e.ID,
		Feed:       e.Feed,
		Title:      e.Title,
		Link:       e.Link,
		Author:     e.Author,
		Published:  e.Published.Format(time.RFC3339),
		Categories: e.Categories,
		Summary:    e.Summary,
		Content:    e.Content,
	}
	// The text content is a readable markdown document rather than the
	// default JSON dump: models handle long articles better this way.
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: renderMarkdown(e)}}}
	return res, out, nil
}

// renderMarkdown formats an entry as a markdown document with a short
// metadata header.
func renderMarkdown(e *entry.Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", e.Title)
	fmt.Fprintf(&b, "- Feed: %s\n- Published: %s\n", e.Feed, e.Published.Format(time.RFC3339))
	if e.Author != "" {
		fmt.Fprintf(&b, "- Author: %s\n", e.Author)
	}
	if e.Link != "" {
		fmt.Fprintf(&b, "- Link: %s\n", e.Link)
	}
	if len(e.Categories) > 0 {
		fmt.Fprintf(&b, "- Categories: %s\n", strings.Join(e.Categories, ", "))
	}
	b.WriteString("\n")
	b.WriteString(e.Content)
	b.WriteString("\n")
	return b.String()
}

package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/sqlite"
)

var testFeeds = []config.Feed{
	{Name: "habr", Title: "Habr", URL: "https://habr.com/rss"},
	{Name: "go", Title: "Go Blog", URL: "https://go.dev/blog/feed.atom"},
}

// newStore creates a store with three entries on Oct 14-16, 2026.
func newStore(t *testing.T) storage.Store {
	t.Helper()
	store, err := sqlite.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for _, it := range []struct {
		feed, title, content string
		day                  int
	}{
		{"go", "Go 1.27 released", "Generic methods are here.", 14},
		{"habr", "Новая версия Kubernetes", "Релиз включает **улучшения** планировщика.", 15},
		{"habr", "Обзор Go 1.27", "Что нового в Go.", 16},
	} {
		e := &entry.Entry{Feed: it.feed, Title: it.title, Link: "https://x/" + it.title, Content: it.content}
		e.Normalize(time.Date(2026, 10, it.day, 12, 0, 0, 0, time.UTC))
		if _, err := store.Save(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// connect returns a client session talking to the server over an
// in-memory transport.
func connect(t *testing.T, store storage.Store) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := NewServer(store, testFeeds, "test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool and decodes its structured output into out.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
	}
	return res
}

// titles joins result titles.
func titles(out searchOutput) string {
	var s []string
	for _, e := range out.Entries {
		s = append(s, e.Title)
	}
	return strings.Join(s, " | ")
}

func TestListTools(t *testing.T) {
	cs := connect(t, newStore(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be read-only", tool.Name)
		}
	}
	if got := strings.Join(names, ","); got != "get_entry,list_feeds,search_entries" {
		t.Errorf("tools = %s", got)
	}
}

func TestListFeeds(t *testing.T) {
	cs := connect(t, newStore(t))
	var out listFeedsOutput
	call(t, cs, "list_feeds", nil, &out)
	if len(out.Feeds) != 2 || out.Feeds[1].Title != "Go Blog" {
		t.Errorf("feeds = %+v", out.Feeds)
	}
}

func TestSearchEntries(t *testing.T) {
	cs := connect(t, newStore(t))
	cases := []struct {
		args map[string]any
		want string
		more bool
	}{
		{map[string]any{}, "Обзор Go 1.27 | Новая версия Kubernetes | Go 1.27 released", false},
		{map[string]any{"limit": 1}, "Обзор Go 1.27", true},
		{map[string]any{"feeds": []string{"go"}}, "Go 1.27 released", false},
		{map[string]any{"query": "go 1.27"}, "Обзор Go 1.27 | Go 1.27 released", false},
		{map[string]any{"query": "ПЛАНИРОВЩИКА"}, "Новая версия Kubernetes", false},
		{map[string]any{"since": "2026-10-15", "until": "2026-10-15"}, "Новая версия Kubernetes", false},
		{map[string]any{"until": "2026-10-15T00:00:00Z"}, "Go 1.27 released", false},
	}
	for _, c := range cases {
		var out searchOutput
		res := call(t, cs, "search_entries", c.args, &out)
		if res.IsError {
			t.Errorf("%v: tool error %v", c.args, res.Content)
			continue
		}
		if titles(out) != c.want || out.More != c.more {
			t.Errorf("%v: got %q more=%v, want %q more=%v", c.args, titles(out), out.More, c.want, c.more)
		}
	}

	// Summaries are plain text excerpts of the content.
	var out searchOutput
	call(t, cs, "search_entries", map[string]any{"query": "kubernetes"}, &out)
	if len(out.Entries) != 1 || out.Entries[0].Summary != "Релиз включает улучшения планировщика." {
		t.Errorf("summary = %+v", out.Entries)
	}
}

func TestSearchErrors(t *testing.T) {
	cs := connect(t, newStore(t))
	for _, args := range []map[string]any{
		{"feeds": []string{"unknown"}},
		{"limit": 500},
		{"since": "yesterday"},
		{"limit": "ten"}, // rejected by the input schema validation
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_entries", Arguments: args})
		if err == nil && !res.IsError {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestGetEntry(t *testing.T) {
	cs := connect(t, newStore(t))
	var found searchOutput
	call(t, cs, "search_entries", map[string]any{"query": "kubernetes"}, &found)

	var out fullEntry
	res := call(t, cs, "get_entry", map[string]any{"id": found.Entries[0].ID}, &out)
	if out.Content != "Релиз включает **улучшения** планировщика." || out.Feed != "habr" {
		t.Errorf("entry = %+v", out)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "# Новая версия Kubernetes\n") || !strings.Contains(text, "- Feed: habr") {
		t.Errorf("markdown = %q", text)
	}

	res = call(t, cs, "get_entry", map[string]any{"id": "missing"}, nil)
	if !res.IsError {
		t.Error("missing entry must be a tool error")
	}
}

// TestStreamableHTTP checks the real HTTP transport used in production.
func TestStreamableHTTP(t *testing.T) {
	srv := httptest.NewServer(Handler(newStore(t), testFeeds, "test"))
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if init := cs.InitializeResult(); init == nil || init.ServerInfo.Name != "litefeed-mcp" {
		t.Errorf("initialize result = %+v", init)
	}
	var out searchOutput
	call(t, cs, "search_entries", map[string]any{"limit": 2}, &out)
	if len(out.Entries) != 2 || !out.More {
		t.Errorf("search over HTTP = %+v", out)
	}
}

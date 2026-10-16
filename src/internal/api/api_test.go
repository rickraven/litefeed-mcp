package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage/sqlite"
)

// newTestServer creates an API over a sqlite store with entries on
// Oct 14 (feed a), Oct 16 (feeds a and b) and a late Oct 16 UTC entry
// that falls on Oct 17 in Moscow (UTC+3).
func newTestServer(t *testing.T) (*httptest.Server, *entry.Entry) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	var first *entry.Entry
	for i, it := range []struct {
		feed string
		at   time.Time
	}{
		{"a", time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)},
		{"a", time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)},
		{"b", time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC)},
		{"b", time.Date(2026, 10, 16, 22, 0, 0, 0, time.UTC)},
	} {
		e := &entry.Entry{
			Feed:    it.feed,
			Title:   "entry " + string(rune('0'+i)),
			Link:    "https://x/" + string(rune('0'+i)),
			Summary: "short",
			Content: "full **body**",
		}
		e.Normalize(it.at)
		if _, err := store.Save(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = e
		}
	}

	mux := http.NewServeMux()
	New(store, []config.Feed{{Name: "a", Title: "A", URL: "https://a/"}, {Name: "b", Title: "B", URL: "https://b/"}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, first
}

// get performs a GET request and decodes the JSON body into out.
func get(t *testing.T, srv *httptest.Server, path string, out any) int {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("%s: content type %q", path, ct)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

// rawList decodes list responses without assuming the entry type.
type rawList struct {
	Entries []map[string]any `json:"entries"`
	More    bool             `json:"more"`
	Day     string           `json:"day"`
	PrevDay string           `json:"prev_day"`
}

// titles joins entry titles of a list response.
func (l rawList) titles() string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e["title"].(string))
	}
	return strings.Join(out, ",")
}

func TestFeeds(t *testing.T) {
	srv, _ := newTestServer(t)
	var feeds []feedJSON
	if code := get(t, srv, "/api/feeds", &feeds); code != 200 || len(feeds) != 2 || feeds[1].Title != "B" {
		t.Errorf("code %d, feeds %+v", code, feeds)
	}
}

func TestEntriesFilters(t *testing.T) {
	srv, _ := newTestServer(t)
	cases := []struct {
		path, want string
		more       bool
	}{
		{"/api/entries", "entry 3,entry 2,entry 1,entry 0", false},
		{"/api/entries?limit=2", "entry 3,entry 2", true},
		{"/api/entries?feed=a", "entry 1,entry 0", false},
		{"/api/entries?feed=a,b&limit=4", "entry 3,entry 2,entry 1,entry 0", false},
		{"/api/entries?since=2026-10-16T00:00:00Z&until=2026-10-16T12:00:00Z", "entry 2,entry 1", false},
		{"/api/entries?q=ENTRY+2", "entry 2", false},
	}
	for _, c := range cases {
		var l rawList
		if code := get(t, srv, c.path, &l); code != 200 {
			t.Errorf("%s: status %d", c.path, code)
		}
		if l.titles() != c.want || l.More != c.more {
			t.Errorf("%s: got %q more=%v, want %q more=%v", c.path, l.titles(), l.More, c.want, c.more)
		}
		if _, ok := l.Entries[0]["content"]; !ok {
			t.Errorf("%s: full entries must include content", c.path)
		}
	}
}

func TestDayMode(t *testing.T) {
	srv, _ := newTestServer(t)
	cases := []struct {
		path, want, prev string
	}{
		{"/api/entries/brief?day=2026-10-16", "entry 3,entry 2,entry 1", "2026-10-14"},
		// 22:00 UTC is already Oct 17 in Moscow.
		{"/api/entries/brief?day=2026-10-16&tz=Europe/Moscow", "entry 2,entry 1", "2026-10-14"},
		{"/api/entries/brief?day=2026-10-15&feed=b", "", ""},
		{"/api/entries/brief?day=2026-10-14", "entry 0", ""},
	}
	for _, c := range cases {
		var l rawList
		get(t, srv, c.path, &l)
		if l.titles() != c.want || l.PrevDay != c.prev || l.Day == "" {
			t.Errorf("%s: got %q prev=%q day=%q, want %q prev=%q", c.path, l.titles(), l.PrevDay, l.Day, c.want, c.prev)
		}
	}
}

func TestBriefHasNoContent(t *testing.T) {
	srv, _ := newTestServer(t)
	var l rawList
	get(t, srv, "/api/entries/brief?limit=1", &l)
	if len(l.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(l.Entries))
	}
	if _, ok := l.Entries[0]["content"]; ok {
		t.Error("brief entries must not include content")
	}
	if l.Entries[0]["summary"] != "short" {
		t.Errorf("summary = %v", l.Entries[0]["summary"])
	}
}

func TestGetEntry(t *testing.T) {
	srv, first := newTestServer(t)
	var e entry.Entry
	if code := get(t, srv, "/api/entries/"+first.ID, &e); code != 200 || e.Content != "full **body**" {
		t.Errorf("code %d, entry %+v", code, e)
	}
	if code := get(t, srv, "/api/entries/missing", nil); code != 404 {
		t.Errorf("missing entry: status %d", code)
	}
}

func TestBadRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{
		"/api/entries?feed=unknown",
		"/api/entries?limit=0",
		"/api/entries?limit=5000",
		"/api/entries?since=yesterday",
		"/api/entries?day=16.10.2026",
		"/api/entries?day=2026-10-16&tz=Mars/Base",
		"/api/entries?day=2026-10-16&since=2026-10-16T00:00:00Z",
	} {
		var body map[string]string
		if code := get(t, srv, path, &body); code != 400 || body["error"] == "" {
			t.Errorf("%s: status %d, body %v", path, code, body)
		}
	}
}

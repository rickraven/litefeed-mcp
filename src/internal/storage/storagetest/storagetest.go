// Package storagetest contains a conformance test suite that every
// storage.Store implementation must pass, guaranteeing identical behavior
// of all backends.
package storagetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
)

// Factory creates a new empty store. reopen must return a store over the
// same underlying data, closing the previous one if needed; it is used to
// check persistence.
type Factory func(t *testing.T) (s storage.Store, reopen func() storage.Store)

// day returns the given October 2026 day and hour in UTC.
func day(d, hour int) time.Time {
	return time.Date(2026, 10, d, hour, 0, 0, 0, time.UTC)
}

// newEntry builds a normalized entry with every field populated.
func newEntry(feed, title, link string, published time.Time) *entry.Entry {
	e := &entry.Entry{
		GUID:       link + "#guid",
		Feed:       feed,
		Title:      title,
		Link:       link,
		Author:     "Author",
		Published:  published.Add(123456789 * time.Nanosecond),
		Updated:    published.Add(time.Hour),
		Categories: []string{"go", "rss"},
		Summary:    "summary",
		Content:    "Text with ![img](https://example.com/a.png)\n\n---\n\nafter rule",
	}
	e.Normalize(published)
	return e
}

// Run executes the whole suite against the store produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Run("SaveGetPersist", func(t *testing.T) { testSaveGetPersist(t, newStore) })
	t.Run("List", func(t *testing.T) { testList(t, newStore) })
	t.Run("DeleteOlderThan", func(t *testing.T) { testDelete(t, newStore) })
}

// mustSave saves an entry and fails the test on error.
func mustSave(t *testing.T, s storage.Store, e *entry.Entry) bool {
	t.Helper()
	ok, err := s.Save(context.Background(), e)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return ok
}

// assertEqual compares every persisted field of two entries.
func assertEqual(t *testing.T, got, want *entry.Entry) {
	t.Helper()
	if got.ID != want.ID || got.GUID != want.GUID || got.Feed != want.Feed ||
		got.Title != want.Title || got.Link != want.Link || got.Author != want.Author ||
		!got.Published.Equal(want.Published) || !got.Updated.Equal(want.Updated) ||
		!got.Fetched.Equal(want.Fetched) || strings.Join(got.Categories, ",") != strings.Join(want.Categories, ",") ||
		got.Summary != want.Summary || got.FullText != want.FullText || got.Content != want.Content {
		t.Errorf("entry mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func testSaveGetPersist(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s, reopen := newStore(t)
	e := newEntry("habr", "Новая версия Kubernetes", "https://x/1", day(16, 9))
	// An entry without the optional fields must round trip as well.
	bare := &entry.Entry{Feed: "habr", Title: "bare", Link: "https://x/2"}
	bare.Normalize(day(16, 8))

	if !mustSave(t, s, e) || !mustSave(t, s, bare) {
		t.Fatal("first save must store the entry")
	}
	if mustSave(t, s, e) {
		t.Fatal("second save must be deduplicated")
	}
	if ok, _ := s.Has(ctx, e.ID); !ok {
		t.Error("Has must report saved entry")
	}
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	s = reopen()
	for _, want := range []*entry.Entry{e, bare} {
		got, err := s.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("get after reopen: %v", err)
		}
		assertEqual(t, got, want)
	}
	if mustSave(t, s, e) {
		t.Error("deduplication must survive reopen")
	}
}

func testList(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s, _ := newStore(t)
	mustSave(t, s, newEntry("a", "one", "https://x/1", day(14, 12)))
	mustSave(t, s, newEntry("a", "two", "https://x/2", day(15, 12)))
	mustSave(t, s, newEntry("b", "three", "https://x/3", day(16, 12)))
	mustSave(t, s, newEntry("c", "four", "https://x/4", day(16, 13)))

	cases := []struct {
		q    storage.Query
		want string
	}{
		{storage.Query{}, "four,three,two,one"},
		{storage.Query{Limit: 2}, "four,three"},
		{storage.Query{Feeds: []string{"a", "b"}}, "three,two,one"},
		{storage.Query{Since: day(15, 0), Until: day(16, 0)}, "two"},
		{storage.Query{Feeds: []string{"none"}}, ""},
	}
	for _, c := range cases {
		list, err := s.List(ctx, c.q)
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, e := range list {
			titles = append(titles, e.Title)
		}
		if got := strings.Join(titles, ","); got != c.want {
			t.Errorf("List(%+v) = %q, want %q", c.q, got, c.want)
		}
	}
}

func testDelete(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s, reopen := newStore(t)
	old := newEntry("a", "old", "https://x/1", day(1, 0))
	fresh := newEntry("a", "fresh", "https://x/2", day(16, 0))
	mustSave(t, s, old)
	mustSave(t, s, fresh)

	n, err := s.DeleteOlderThan(ctx, day(10, 0))
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, err %v", n, err)
	}
	s = reopen()
	if ok, _ := s.Has(ctx, old.ID); ok {
		t.Error("old entry must be gone")
	}
	if ok, _ := s.Has(ctx, fresh.ID); !ok {
		t.Error("fresh entry must remain")
	}
}

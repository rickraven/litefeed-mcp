package markdown

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/storagetest"
)

// TestConformance runs the shared storage test suite.
func TestConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) (storage.Store, func() storage.Store) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s, func() storage.Store {
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	})
}

// day returns the given October 2026 day and hour in UTC.
func day(d, hour int) time.Time {
	return time.Date(2026, 10, d, hour, 0, 0, 0, time.UTC)
}

// newEntry builds a normalized entry for tests.
func newEntry(feed, title, link string, published time.Time) *entry.Entry {
	e := &entry.Entry{Feed: feed, Title: title, Link: link, Published: published}
	e.Normalize(published)
	return e
}

// mustSave saves an entry and fails the test on error.
func mustSave(t *testing.T, s *Store, e *entry.Entry) {
	t.Helper()
	if _, err := s.Save(context.Background(), e); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// TestFileLayout verifies the <feed>/<date>_<slug>.md layout.
func TestFileLayout(t *testing.T) {
	s, _ := Open(t.TempDir())
	mustSave(t, s, newEntry("habr", "Новая версия Kubernetes", "https://x/1", day(16, 9)))
	if _, err := os.Stat(filepath.Join(s.dir, "habr", "2026-10-16_novaia-versiia-kubernetes.md")); err != nil {
		t.Errorf("expected file: %v", err)
	}
}

// TestFileNameCollision verifies that two different entries with the same
// date and title get different files.
func TestFileNameCollision(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := newEntry("f", "Same", "https://x/a", day(16, 1))
	b := newEntry("f", "Same", "https://x/b", day(16, 2))
	mustSave(t, s, a)
	mustSave(t, s, b)

	files, _ := os.ReadDir(filepath.Join(s.dir, "f"))
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if s.index[b.ID].path != filepath.Join("f", b.FileNameUnique()) {
		t.Errorf("unexpected path %q", s.index[b.ID].path)
	}
}

// TestLoadSkipsGarbage verifies that foreign and broken files do not
// break startup.
func TestLoadSkipsGarbage(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	mustSave(t, s, newEntry("a", "one", "https://x/1", day(14, 12)))
	os.WriteFile(filepath.Join(dir, "a", "broken.md"), []byte("no front matter"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "noid.md"), []byte("---\ntitle: x\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "root.md"), []byte("x"), 0o644)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.index) != 1 {
		t.Errorf("expected 1 indexed entry, got %d", len(s.index))
	}
}

// TestSaveRejectsBadFeed verifies the path traversal guard.
func TestSaveRejectsBadFeed(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, feed := range []string{"", "..", "a/b", ".hidden"} {
		e := newEntry(feed, "t", "https://x/"+feed, day(1, 0))
		if _, err := s.Save(context.Background(), e); err == nil {
			t.Errorf("feed %q must be rejected", feed)
		}
	}
}

// TestDecodeVariants verifies tolerance to hand-edited files.
func TestDecodeVariants(t *testing.T) {
	cases := map[string]string{
		"crlf and bom": "\ufeff---\r\nid: x\r\ntitle: T\r\n---\r\n\r\nbody\r\n",
		"no body":      "---\nid: x\ntitle: T\n---",
	}
	for name, data := range cases {
		e, err := decode([]byte(data))
		if err != nil || e.ID != "x" || e.Title != "T" {
			t.Errorf("%s: got %+v, %v", name, e, err)
		}
	}
	if _, err := decode([]byte("---\nid: x\n")); err == nil {
		t.Error("unterminated front matter must fail")
	}
}

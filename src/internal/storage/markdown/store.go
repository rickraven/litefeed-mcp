// Package markdown implements storage.Store on top of markdown files with
// YAML front matter. Layout: <dir>/<feed>/<date>_<slug>.md.
//
// All metadata is kept in an in-memory index built once at startup by
// reading every file's front matter. The index answers Has/List/Delete
// queries; file bodies are read from disk only when entries are returned.
// The store assumes it is the only writer of the directory while running.
package markdown

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
)

// Compile-time check that Store implements storage.Store.
var _ storage.Store = (*Store)(nil)

// indexItem holds the metadata needed to filter and locate an entry.
type indexItem struct {
	id        string
	feed      string
	published time.Time
	// path is relative to the store root, e.g. "habr/2026-10-16_x.md".
	path string
}

// Store is a markdown files backend. It is safe for concurrent use.
type Store struct {
	dir string

	mu    sync.RWMutex
	index map[string]*indexItem
	// paths contains every indexed relative path, used to detect file
	// name collisions between different entries.
	paths map[string]bool
}

// Open creates the root directory if needed and builds the index from the
// existing files. Files that cannot be parsed are skipped with a warning,
// so a single broken file does not prevent the application from starting.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	s := &Store{
		dir:   dir,
		index: make(map[string]*indexItem),
		paths: make(map[string]bool),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	slog.Info("markdown storage opened", "dir", dir, "entries", len(s.index))
	return s, nil
}

// load walks <dir>/<feed>/*.md and fills the index. Only one level of
// subdirectories is scanned; other files are ignored.
func (s *Store) load() error {
	feeds, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read storage dir: %w", err)
	}
	for _, fd := range feeds {
		if !fd.IsDir() || strings.HasPrefix(fd.Name(), ".") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.dir, fd.Name()))
		if err != nil {
			return fmt.Errorf("read feed dir: %w", err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			rel := filepath.Join(fd.Name(), f.Name())
			e, err := s.readFile(rel)
			if err != nil {
				slog.Warn("skipping unreadable entry file", "path", rel, "err", err)
				continue
			}
			if e.ID == "" {
				slog.Warn("skipping entry file without id", "path", rel)
				continue
			}
			if prev, ok := s.index[e.ID]; ok {
				slog.Warn("skipping duplicate entry file", "path", rel, "original", prev.path)
				continue
			}
			// The directory is authoritative for the feed name: it is what
			// List filters by and where Save would put the entry.
			s.add(&indexItem{id: e.ID, feed: fd.Name(), published: e.Published, path: rel})
		}
	}
	return nil
}

// add registers an item in the index. The caller must hold the write lock
// (or be the only goroutine using the store, as in load).
func (s *Store) add(it *indexItem) {
	s.index[it.id] = it
	s.paths[it.path] = true
}

// readFile reads and decodes the file at the given relative path.
func (s *Store) readFile(rel string) (*entry.Entry, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, rel))
	if err != nil {
		return nil, err
	}
	return decode(data)
}

// Has reports whether an entry with the given ID is stored.
func (s *Store) Has(_ context.Context, id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.index[id]
	return ok, nil
}

// Save writes a normalized entry to <dir>/<feed>/<file name>. Existing
// entries are not overwritten. When the preferred file name is taken by a
// different entry, a name with an ID suffix is used instead.
func (s *Store) Save(_ context.Context, e *entry.Entry) (bool, error) {
	if e.ID == "" {
		return false, errors.New("entry has no id")
	}
	if !validFeedName(e.Feed) {
		return false, fmt.Errorf("invalid feed name %q", e.Feed)
	}
	data, err := encode(e)
	if err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.index[e.ID]; ok {
		return false, nil
	}
	rel := filepath.Join(e.Feed, e.FileName())
	if s.paths[rel] || fileExists(filepath.Join(s.dir, rel)) {
		rel = filepath.Join(e.Feed, e.FileNameUnique())
	}
	if err := os.MkdirAll(filepath.Join(s.dir, e.Feed), 0o755); err != nil {
		return false, fmt.Errorf("create feed dir: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(s.dir, rel), data); err != nil {
		return false, err
	}
	s.add(&indexItem{id: e.ID, feed: e.Feed, published: e.Published, path: rel})
	return true, nil
}

// Get returns the entry with the given ID.
func (s *Store) Get(_ context.Context, id string) (*entry.Entry, error) {
	s.mu.RLock()
	it, ok := s.index[id]
	s.mu.RUnlock()
	if !ok {
		return nil, storage.ErrNotFound
	}
	e, err := s.readFile(it.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	return e, err
}

// List returns entries matching the query, newest first. Filtering and
// sorting happen on the index; only the selected files are read.
func (s *Store) List(ctx context.Context, q storage.Query) ([]entry.Entry, error) {
	s.mu.RLock()
	items := make([]*indexItem, 0, len(s.index))
	for _, it := range s.index {
		if matches(it, q) {
			items = append(items, it)
		}
	}
	s.mu.RUnlock()

	// Newest first; ID is a tie breaker for a stable order.
	slices.SortFunc(items, func(a, b *indexItem) int {
		if c := b.published.Compare(a.published); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}

	out := make([]entry.Entry, 0, len(items))
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := s.readFile(it.path)
		if err != nil {
			// A file removed or broken by hand should not fail the whole
			// listing; it is reported and skipped.
			slog.Warn("failed to read entry file", "path", it.path, "err", err)
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

// DeleteOlderThan removes entries published before t.
func (s *Store) DeleteOlderThan(ctx context.Context, t time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for id, it := range s.index {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if !it.published.Before(t) {
			continue
		}
		err := os.Remove(filepath.Join(s.dir, it.path))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, fmt.Errorf("remove %s: %w", it.path, err)
		}
		delete(s.index, id)
		delete(s.paths, it.path)
		n++
	}
	return n, nil
}

// Close is a no-op: files are written synchronously.
func (s *Store) Close() error { return nil }

// matches reports whether the index item satisfies the query filters.
func matches(it *indexItem, q storage.Query) bool {
	if len(q.Feeds) > 0 && !slices.Contains(q.Feeds, it.feed) {
		return false
	}
	if !q.Since.IsZero() && it.published.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && !it.published.Before(q.Until) {
		return false
	}
	return true
}

// validFeedName guards against path traversal: the feed name is used as
// a directory name and must be a single, non-special path element.
func validFeedName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".")
}

// fileExists reports whether a file exists at path.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFileAtomic writes data to a temporary file in the target directory
// and renames it, so readers never observe a partially written file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

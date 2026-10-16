// Package sqlite implements storage.Store on top of a local SQLite
// database using the pure-Go modernc.org/sqlite driver (no CGO needed,
// which keeps the binary static).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
)

// Compile-time check that Store implements storage.Store.
var _ storage.Store = (*Store)(nil)

// schemaVersion is stored in PRAGMA user_version and allows future
// migrations to detect the layout of an existing database.
const schemaVersion = 1

// schema creates the tables for schemaVersion. Times are stored as Unix
// nanoseconds (0 for "unset") to keep exact round trips and cheap range
// queries. Categories are stored as a JSON array.
const schema = `
CREATE TABLE IF NOT EXISTS entries (
	id         TEXT PRIMARY KEY,
	guid       TEXT NOT NULL DEFAULT '',
	feed       TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT '',
	link       TEXT NOT NULL DEFAULT '',
	author     TEXT NOT NULL DEFAULT '',
	published  INTEGER NOT NULL,
	updated    INTEGER NOT NULL DEFAULT 0,
	fetched    INTEGER NOT NULL,
	categories TEXT NOT NULL DEFAULT '[]',
	summary    TEXT NOT NULL DEFAULT '',
	full_text  TEXT NOT NULL DEFAULT 'none',
	content    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS entries_published ON entries (published);
CREATE INDEX IF NOT EXISTS entries_feed_published ON entries (feed, published);
`

// columns lists the selected columns in the order expected by scanEntry.
const columns = `id, guid, feed, title, link, author, published, updated,
	fetched, categories, summary, full_text, content`

// Store is a SQLite backend. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the
// schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database dir: %w", err)
		}
	}

	// Pragmas are passed in the DSN so that they apply to every pooled
	// connection: WAL lets readers work while the scraper writes, and
	// busy_timeout makes SQLite wait for locks instead of failing.
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite allows a single writer anyway; one connection removes any
	// chance of SQLITE_BUSY between our own goroutines. The load of this
	// application is far below what a single connection can handle.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate creates the schema in a new database and refuses to work with
// databases created by a newer version of the application.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported %d", version, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

// Has reports whether an entry with the given ID is stored.
func (s *Store) Has(ctx context.Context, id string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM entries WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Save inserts an entry; an existing entry with the same ID is left
// untouched and false is returned.
func (s *Store) Save(ctx context.Context, e *entry.Entry) (bool, error) {
	if e.ID == "" {
		return false, errors.New("entry has no id")
	}
	cats, err := json.Marshal(nonNil(e.Categories))
	if err != nil {
		return false, fmt.Errorf("marshal categories: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO entries (`+columns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.GUID, e.Feed, e.Title, e.Link, e.Author,
		toUnix(e.Published), toUnix(e.Updated), toUnix(e.Fetched),
		string(cats), e.Summary, string(e.FullText), e.Content,
	)
	if err != nil {
		return false, fmt.Errorf("insert entry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert entry: %w", err)
	}
	return n == 1, nil
}

// Get returns the entry with the given ID.
func (s *Store) Get(ctx context.Context, id string) (*entry.Entry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM entries WHERE id = ?`, id)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	return e, err
}

// List returns entries matching the query, newest first.
func (s *Store) List(ctx context.Context, q storage.Query) ([]entry.Entry, error) {
	var (
		where []string
		args  []any
	)
	if len(q.Feeds) > 0 {
		where = append(where, "feed IN (?"+strings.Repeat(", ?", len(q.Feeds)-1)+")")
		for _, f := range q.Feeds {
			args = append(args, f)
		}
	}
	if !q.Since.IsZero() {
		where = append(where, "published >= ?")
		args = append(args, toUnix(q.Since))
	}
	if !q.Until.IsZero() {
		where = append(where, "published < ?")
		args = append(args, toUnix(q.Until))
	}

	query := `SELECT ` + columns + ` FROM entries`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	// ID is a tie breaker for a stable order, same as the markdown backend.
	query += ` ORDER BY published DESC, id ASC`
	// With a text filter rows are matched in Go (see entry.Entry.Matches),
	// so the limit is applied while scanning instead of in SQL.
	terms := entry.SearchTerms(q.Text)
	if q.Limit > 0 && len(terms) == 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	var out []entry.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		if !e.Matches(terms) {
			continue
		}
		out = append(out, *e)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, rows.Err()
}

// DeleteOlderThan removes entries published before t.
func (s *Store) DeleteOlderThan(ctx context.Context, t time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE published < ?`, toUnix(t))
	if err != nil {
		return 0, fmt.Errorf("delete entries: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanEntry reads a row selected with the columns constant.
func scanEntry(r scanner) (*entry.Entry, error) {
	var (
		e                           entry.Entry
		published, updated, fetched int64
		cats, fullText              string
	)
	err := r.Scan(&e.ID, &e.GUID, &e.Feed, &e.Title, &e.Link, &e.Author,
		&published, &updated, &fetched, &cats, &e.Summary, &fullText, &e.Content)
	if err != nil {
		return nil, err
	}
	e.Published = fromUnix(published)
	e.Updated = fromUnix(updated)
	e.Fetched = fromUnix(fetched)
	e.FullText = entry.FullTextStatus(fullText)
	if err := json.Unmarshal([]byte(cats), &e.Categories); err != nil {
		return nil, fmt.Errorf("decode categories of %s: %w", e.ID, err)
	}
	if len(e.Categories) == 0 {
		e.Categories = nil
	}
	return &e, nil
}

// toUnix converts a time to Unix nanoseconds; the zero time maps to 0.
func toUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// fromUnix is the inverse of toUnix; 0 maps to the zero time.
func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// nonNil returns an empty slice instead of nil so JSON encodes "[]".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

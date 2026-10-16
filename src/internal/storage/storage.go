// Package storage defines the interface implemented by the storage
// backends (sqlite and markdown files).
package storage

import (
	"context"
	"errors"
	"time"

	"litefeed-mcp/internal/entry"
)

// ErrNotFound is returned when the requested entry does not exist.
var ErrNotFound = errors.New("entry not found")

// Query selects entries for listing. Zero values mean "no restriction".
type Query struct {
	// Feeds limits results to the given feed names.
	Feeds []string
	// Since includes entries published at or after this time.
	Since time.Time
	// Until includes entries published strictly before this time.
	Until time.Time
	// Limit caps the number of returned entries.
	Limit int
}

// Store is implemented by every storage backend. Implementations must be
// safe for concurrent use. Listing results are ordered by publication
// time, newest first.
type Store interface {
	// Has reports whether an entry with the given ID is already stored.
	Has(ctx context.Context, id string) (bool, error)
	// Save stores a normalized entry. It returns false without error when
	// an entry with the same ID already exists (deduplication).
	Save(ctx context.Context, e *entry.Entry) (bool, error)
	// Get returns the entry with the given ID or ErrNotFound.
	Get(ctx context.Context, id string) (*entry.Entry, error)
	// List returns entries matching the query.
	List(ctx context.Context, q Query) ([]entry.Entry, error)
	// DeleteOlderThan removes entries published before t and returns the
	// number of removed entries.
	DeleteOlderThan(ctx context.Context, t time.Time) (int, error)
	// Close releases resources held by the backend.
	Close() error
}

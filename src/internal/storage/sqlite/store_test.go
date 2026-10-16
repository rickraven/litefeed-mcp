package sqlite

import (
	"path/filepath"
	"testing"

	"litefeed-mcp/internal/storage"
	"litefeed-mcp/internal/storage/storagetest"
)

// TestConformance runs the shared storage test suite.
func TestConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) (storage.Store, func() storage.Store) {
		path := filepath.Join(t.TempDir(), "sub dir", "test.db")
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s, func() storage.Store {
			s.Close()
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	})
}

// TestPragmas verifies that DSN pragmas are applied to the connection.
func TestPragmas(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var mode string
	var timeout int
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" || timeout != 5000 {
		t.Errorf("journal_mode = %q, busy_timeout = %d", mode, timeout)
	}
}

// TestNewerSchemaRejected verifies the schema version guard.
func TestNewerSchemaRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`PRAGMA user_version = 99`)
	s.Close()
	if _, err := Open(path); err == nil {
		t.Error("expected error for newer schema")
	}
}

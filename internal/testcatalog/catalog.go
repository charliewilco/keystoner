// Package testcatalog builds tiny, isolated snapshots for behavioral tests.
package testcatalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Write creates a valid published snapshot, optionally adding application tables.
func Write(t testing.TB, schema string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog with spaces #1")
	path := filepath.Join(root, "releases", "test-1", "catalog.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=1;
 CREATE TABLE release_metadata(release TEXT, schema_version INTEGER, published_at TEXT);
 INSERT INTO release_metadata VALUES('test-1',1,'2026-11-12T12:00:00Z');` + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	Refresh(t, root)
	return root
}

// Refresh recalculates the checksum so tests can distinguish metadata validation
// from byte-integrity validation after deliberately modifying a snapshot.
func Refresh(t testing.TB, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "releases/test-1/catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"release": "test-1", "schema_version": 1,
		"published_at": "2026-11-12T12:00:00Z", "url": "/v1/catalog/releases/test-1/catalog.sqlite",
		"byte_size": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
}

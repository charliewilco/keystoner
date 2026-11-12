package core

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charliewilco/keystoner/internal/testcatalog"
)

func TestOpenVerifiedReadOnlySnapshot(t *testing.T) {
	root := testcatalog.Write(t, "")
	c, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.DB.Close()
	if c.Manifest.Release != "test-1" || !filepath.IsAbs(c.Snapshot) {
		t.Fatalf("unexpected catalog: %+v", c)
	}
	if _, err := c.DB.Exec("DELETE FROM release_metadata"); err == nil {
		t.Fatal("published catalog was writable")
	}
	var release string
	if err := c.DB.QueryRow("SELECT release FROM release_metadata").Scan(&release); err != nil || release != "test-1" {
		t.Fatalf("read after rejected write: %q, %v", release, err)
	}
}

func TestOpenRejectsInvalidManifest(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
		want        string
	}{
		{"schema", "schema_version", 2, "invalid manifest"},
		{"traversal", "release", "../outside", "invalid manifest"},
		{"empty release", "release", "", "invalid manifest"},
		{"long release", "release", strings.Repeat("a", 81), "invalid manifest"},
		{"wrong URL", "url", "/v1/catalog/releases/other/catalog.sqlite", "invalid manifest"},
		{"size", "byte_size", 1, "size/hash mismatch"},
		{"checksum", "sha256", strings.Repeat("0", 64), "size/hash mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testcatalog.Write(t, "")
			path := filepath.Join(root, "manifest.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			m[tc.field] = tc.value
			raw, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
			if c, err := Open(root); err == nil {
				c.DB.Close()
				t.Fatal("accepted invalid manifest")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOpenRejectsSnapshotMetadataEvenWithValidChecksum(t *testing.T) {
	for _, statement := range []string{
		"PRAGMA user_version=2",
		"UPDATE release_metadata SET schema_version=2",
		"UPDATE release_metadata SET release='other'",
		"UPDATE release_metadata SET published_at='2026-11-11T12:00:00Z'",
		"DELETE FROM release_metadata",
		"DROP TABLE release_metadata",
	} {
		t.Run(statement, func(t *testing.T) {
			root := testcatalog.Write(t, "")
			db, err := sql.Open("sqlite", filepath.Join(root, "releases/test-1/catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(statement); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			testcatalog.Refresh(t, root)
			if c, err := Open(root); err == nil {
				c.DB.Close()
				t.Fatal("accepted incompatible snapshot")
			}
		})
	}
}

func TestOpenRejectsMissingMalformedAndCorruptFiles(t *testing.T) {
	for _, kind := range []string{"missing manifest", "malformed manifest", "missing snapshot", "changed bytes", "not SQLite"} {
		t.Run(kind, func(t *testing.T) {
			root := testcatalog.Write(t, "")
			manifest := filepath.Join(root, "manifest.json")
			snapshot := filepath.Join(root, "releases/test-1/catalog.sqlite")
			var err error
			switch kind {
			case "missing manifest":
				err = os.Remove(manifest)
			case "malformed manifest":
				err = os.WriteFile(manifest, []byte("{"), 0644)
			case "missing snapshot":
				err = os.Remove(snapshot)
			default:
				err = os.WriteFile(snapshot, []byte("this is not a SQLite database"), 0644)
				if err == nil && kind == "not SQLite" {
					testcatalog.Refresh(t, root)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if c, err := Open(root); err == nil {
				c.DB.Close()
				t.Fatal("accepted invalid files")
			}
		})
	}
}

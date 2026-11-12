package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charliewilco/keystoner/core"
)

const published = "2026-11-12T12:00:00Z"
const seedSQL = `PRAGMA foreign_keys=ON;
CREATE TABLE benefits(benefit_id INTEGER PRIMARY KEY, eligibility TEXT, caveats TEXT, evidence_status TEXT, checked_on TEXT);
CREATE TABLE benefit_details(benefit_id INTEGER REFERENCES benefits(benefit_id), price_notes TEXT, funding_basis TEXT, availability TEXT);
CREATE TABLE sources(source_id TEXT PRIMARY KEY, url TEXT, checked_on TEXT);
CREATE TABLE benefit_sources(benefit_id INTEGER REFERENCES benefits(benefit_id), source_id TEXT REFERENCES sources(source_id));
INSERT INTO benefits VALUES(7,'Residents','Verify availability','Preliminary','2026-11-12');
INSERT INTO benefit_details VALUES(7,'Free if eligible','City funding','Call provider');
INSERT INTO sources VALUES('city','https://example.org/help','2026-11-12');
INSERT INTO benefit_sources VALUES(7,'city');
CREATE VIEW expanded_catalog AS SELECT * FROM benefits;`

func writeSeed(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.sql")
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPublishProducesVerifiedReproducibleSnapshot(t *testing.T) {
	seed := writeSeed(t, seedSQL)
	var previous core.Manifest
	for i := 0; i < 2; i++ {
		root := filepath.Join(t.TempDir(), "catalog with spaces")
		m, err := Publish(context.Background(), seed, root, "release-1", published)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && m != previous {
			t.Fatalf("non-reproducible manifests: %+v %+v", previous, m)
		}
		previous = m
		c, err := core.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err = c.DB.QueryRow("SELECT id FROM resources").Scan(&id); err != nil || id != "ks-000007" {
			t.Fatalf("stable ID: %q %v", id, err)
		}
		if err = c.DB.Close(); err != nil {
			t.Fatal(err)
		}
		snapshot := read(t, c.Snapshot)
		sum := sha256.Sum256(snapshot)
		if int64(len(snapshot)) != m.ByteSize || hex.EncodeToString(sum[:]) != m.SHA256 {
			t.Fatal("manifest does not describe snapshot")
		}
		latest := read(t, filepath.Join(root, "manifest.json"))
		if !bytes.Equal(latest, read(t, filepath.Join(root, "releases/release-1/manifest.json"))) {
			t.Fatal("release and active manifests differ")
		}
		var decoded core.Manifest
		if err = json.Unmarshal(latest, &decoded); err != nil || decoded != m {
			t.Fatalf("manifest JSON: %+v %v", decoded, err)
		}
	}
}

func TestValidateRejectsIncompleteEvidence(t *testing.T) {
	cases := []struct{ name, mutation, want string }{
		{"empty", "DELETE FROM benefit_sources; DELETE FROM benefit_details; DELETE FROM benefits;", "empty catalog"},
		{"missing details", "DELETE FROM benefit_details;", "missing cost/funding details"},
		{"unsourced", "DELETE FROM benefit_sources;", "unsourced benefit"},
		{"foreign key", "PRAGMA foreign_keys=OFF; INSERT INTO benefit_sources VALUES(999,'city');", "foreign key"},
		{"URL", "UPDATE sources SET url='http://example.org/';", "invalid source"},
		{"date format", "UPDATE sources SET checked_on='yesterday';", "invalid source"},
		{"impossible date", "UPDATE sources SET checked_on='2026-02-30';", "invalid source"},
	}
	for table, fields := range map[string][]string{"benefits": {"eligibility", "caveats", "evidence_status", "checked_on"}, "benefit_details": {"price_notes", "funding_basis", "availability"}} {
		for _, field := range fields {
			for _, value := range []string{"'   '", "NULL"} {
				cases = append(cases, struct{ name, mutation, want string }{field + value, "UPDATE " + table + " SET " + field + "=" + value + ";", "missing " + field})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if _, err = db.Exec(seedSQL); err != nil {
				t.Fatal(err)
			}
			if err = validate(context.Background(), db); err != nil {
				t.Fatalf("valid fixture: %v", err)
			}
			if _, err = db.Exec(tc.mutation); err != nil {
				t.Fatal(err)
			}
			if err = validate(context.Background(), db); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPublishFailurePreservesActiveAndHistoricalReleases(t *testing.T) {
	for _, tc := range []struct{ name, seed string }{
		{"invalid evidence", seedSQL + "DELETE FROM benefit_sources;"},
		{"malformed SQL", "not SQL"},
		{"missing view", strings.Replace(seedSQL, "CREATE VIEW expanded_catalog AS SELECT * FROM benefits;", "", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			if _, err := Publish(ctx, writeSeed(t, seedSQL), root, "good", published); err != nil {
				t.Fatal(err)
			}
			before := read(t, filepath.Join(root, "manifest.json"))
			snapshot := read(t, filepath.Join(root, "releases/good/catalog.sqlite"))
			if _, err := Publish(ctx, writeSeed(t, tc.seed), root, "broken", published); err == nil {
				t.Fatal("invalid release published")
			}
			if !bytes.Equal(before, read(t, filepath.Join(root, "manifest.json"))) || !bytes.Equal(snapshot, read(t, filepath.Join(root, "releases/good/catalog.sqlite"))) {
				t.Fatal("failed publication changed active release")
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if !reflect.DeepEqual(names, []string{"manifest.json", "releases"}) {
				t.Fatalf("staging leftovers: %v", names)
			}
			if _, err = os.Stat(filepath.Join(root, "releases/broken")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial release exists: %v", err)
			}
		})
	}
}

func TestPublishRetainsHistoryAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	seed := writeSeed(t, seedSQL)
	ctx := context.Background()
	if _, err := Publish(ctx, seed, root, "old", published); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "releases/old/catalog.sqlite")
	oldBytes := read(t, oldPath)
	if _, err := Publish(ctx, seed, root, "new", published); err != nil {
		t.Fatal(err)
	}
	latest := read(t, filepath.Join(root, "manifest.json"))
	if _, err := Publish(ctx, seed, root, "old", published); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite: %v", err)
	}
	if !bytes.Equal(oldBytes, read(t, oldPath)) || !bytes.Equal(latest, read(t, filepath.Join(root, "manifest.json"))) {
		t.Fatal("history or active manifest changed")
	}
	c, err := core.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.DB.Close()
	if c.Manifest.Release != "new" {
		t.Fatalf("active release=%s", c.Manifest.Release)
	}
}

func TestPublishRejectsArgumentsBeforeCreatingOutput(t *testing.T) {
	for _, tc := range []struct{ release, date string }{
		{"../escape", published}, {"", published}, {strings.Repeat("a", 81), published}, {"/absolute", published},
		{"valid", "not-a-date"}, {"valid", "2026-11-12"}, {"valid", "2026-11-12T12:00:00-05:00"},
	} {
		t.Run(tc.release+tc.date, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "catalog")
			if _, err := Publish(context.Background(), "unused.sql", root, tc.release, tc.date); err == nil {
				t.Fatal("invalid arguments accepted")
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("output created: %v", err)
			}
		})
	}
}

func TestPublishCancellationLeavesNoManifest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	if _, err := Publish(ctx, writeSeed(t, seedSQL), root, "cancelled", published); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest: %v", err)
	}
}

func TestPublishRollsBackWhenLatestManifestCannotBeReplaced(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.json")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), writeSeed(t, seedSQL), root, "failed", published); err == nil {
		t.Fatal("replaced directory with manifest")
	}
	if _, err := os.Stat(filepath.Join(root, "releases/failed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial release: %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("original manifest path modified: %v", err)
	}
}

func TestPublishCheckedInSeed(t *testing.T) {
	root := t.TempDir()
	if _, err := Publish(context.Background(), "../../seed/catalog.sql", root, "seed-test", published); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.DB.Close()
	var count int
	if err = c.DB.QueryRow("SELECT count(*) FROM resources").Scan(&count); err != nil || count == 0 {
		t.Fatalf("empty published seed: %d %v", count, err)
	}
}

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSeedSources(t *testing.T) {
	sources, err := ReadSeedSources("../seed/catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 111 {
		t.Fatalf("got %d unique source URLs", len(sources))
	}
	if sources["https://www.phila.gov/services/mental-physical-health/find-free-food-or-meals/"] != "food" {
		t.Fatal("source IDs not preserved")
	}
	if _, err = ReadSeedSources("missing.sql"); err == nil {
		t.Fatal("missing seed accepted")
	}
}

func TestReadSeedSourcesDuplicateURLsAndInvalidSchema(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		wantError bool
	}{
		{"duplicates", `CREATE TABLE sources(source_id TEXT,url TEXT); INSERT INTO sources VALUES('z','https://example.org/'),('a','https://example.org/');`, false},
		{"empty", `CREATE TABLE sources(source_id TEXT,url TEXT);`, false},
		{"invalid SQL", `not SQL`, true},
		{"missing table", `CREATE TABLE other(id TEXT);`, true},
		{"null URL", `CREATE TABLE sources(source_id TEXT,url TEXT);INSERT INTO sources VALUES('a',NULL);`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "seed.sql")
			if err := os.WriteFile(path, []byte(tc.sql), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := ReadSeedSources(path)
			if (err != nil) != tc.wantError {
				t.Fatalf("sources=%v error=%v", got, err)
			}
			if tc.name == "duplicates" && (len(got) != 1 || got["https://example.org/"] != "z") {
				t.Fatalf("lexically last source must win: %v", got)
			}
			if tc.name == "empty" && (got == nil || len(got) != 0) {
				t.Fatalf("empty result: %v", got)
			}
		})
	}
}

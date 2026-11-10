// Package core provides catalog storage and validation without HTTP or CLI dependencies.
package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
)

type Manifest struct {
	Release       string `json:"release"`
	SchemaVersion int    `json:"schema_version"`
	PublishedAt   string `json:"published_at"`
	URL           string `json:"url"`
	ByteSize      int64  `json:"byte_size"`
	SHA256        string `json:"sha256"`
}
type Catalog struct {
	DB       *sql.DB
	Manifest Manifest
	Snapshot string
}

var releaseID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

// Open verifies and opens a published immutable catalog. The caller closes DB.
func Open(root string) (*Catalog, error) {
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	s := &Catalog{}
	if err = json.Unmarshal(raw, &s.Manifest); err != nil {
		return nil, err
	}
	m := s.Manifest
	if m.SchemaVersion != 1 || !releaseID.MatchString(m.Release) || m.URL != "/v1/catalog/releases/"+m.Release+"/catalog.sqlite" {
		return nil, fmt.Errorf("unsupported or invalid manifest")
	}
	s.Snapshot, err = filepath.Abs(filepath.Join(root, "releases", m.Release, "catalog.sqlite"))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.Snapshot)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != m.ByteSize || hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, fmt.Errorf("snapshot size/hash mismatch")
	}
	uri := url.URL{Scheme: "file", Path: s.Snapshot, RawQuery: "mode=ro&immutable=1"}
	s.DB, err = sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Catalog, error) { s.DB.Close(); return nil, e }
	var version int
	var integrity, release, published string
	var schema int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if err = s.DB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fail(err)
	}
	if err = s.DB.QueryRow("SELECT release,schema_version,published_at FROM release_metadata").Scan(&release, &schema, &published); err != nil {
		return fail(err)
	}
	if version != 1 || schema != 1 || integrity != "ok" || release != m.Release || published != m.PublishedAt {
		return fail(fmt.Errorf("invalid snapshot metadata/integrity"))
	}
	return s, nil
}

// Package publisher builds immutable catalog releases from trusted maintainer SQL.
package publisher

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charliewilco/keystoner/core"
)

var releaseID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

// validate checks evidence completeness as well as SQLite integrity. Seed SQL is
// trusted input; publication does not infer eligibility, cost, or funding.
func validate(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity check failed: %s", integrity)
	}
	for _, check := range []struct{ query, message string }{
		{"PRAGMA foreign_key_check", "foreign key check failed"},
		{"SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM benefits)", "empty catalog"},
		{"SELECT benefit_id FROM benefits EXCEPT SELECT benefit_id FROM benefit_details", "missing cost/funding details"},
		{"SELECT benefit_id FROM benefits EXCEPT SELECT benefit_id FROM benefit_sources", "unsourced benefit"},
	} {
		rows, err := db.QueryContext(ctx, check.query)
		if err != nil {
			return err
		}
		invalid := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if invalid {
			return fmt.Errorf("%s", check.message)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT url,checked_on FROM sources")
	if err != nil {
		return err
	}
	// Close this cursor before subsequent queries on the single connection.
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var url, date string
			if err := rows.Scan(&url, &date); err != nil {
				return err
			}
			if _, err := time.Parse("2006-01-02", date); err != nil || !strings.HasPrefix(url, "https://") {
				return fmt.Errorf("invalid source URL/review date")
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}
	for _, table := range []struct {
		name   string
		fields []string
	}{
		{"benefits", []string{"eligibility", "caveats", "evidence_status", "checked_on"}},
		{"benefit_details", []string{"price_notes", "funding_basis", "availability"}},
	} {
		for _, field := range table.fields {
			var missing bool
			query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE %s IS NULL OR trim(%s) = '')", table.name, field, field)
			if err := db.QueryRowContext(ctx, query).Scan(&missing); err != nil {
				return err
			}
			if missing {
				return fmt.Errorf("missing %s", field)
			}
		}
	}
	return nil
}

// Publish stages and verifies a standalone SQLite snapshot, installs its immutable
// release directory, then atomically replaces the active manifest. Run one
// publisher at a time. Only trusted, maintainer-owned SQL may be used as seed.
func Publish(ctx context.Context, seed, output, release, published string) (core.Manifest, error) {
	var m core.Manifest
	if !releaseID.MatchString(release) {
		return m, fmt.Errorf("invalid release ID")
	}
	at, err := time.Parse(time.RFC3339, published)
	if err != nil {
		return m, fmt.Errorf("published-at must be an RFC3339 UTC timestamp: %w", err)
	}
	if _, offset := at.Zone(); offset != 0 {
		return m, fmt.Errorf("published-at must be a UTC timestamp")
	}
	data, err := os.ReadFile(seed)
	if err != nil {
		return m, err
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		return m, err
	}
	destination := filepath.Join(output, "releases", release)
	if _, err = os.Lstat(destination); err == nil {
		return m, fmt.Errorf("release already exists; never replace immutable releases")
	} else if !os.IsNotExist(err) {
		return m, err
	}
	stage, err := os.MkdirTemp(output, ".publish-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(stage)
	db, err := sql.Open("sqlite", filepath.Join(stage, "build.sqlite"))
	if err != nil {
		return m, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, string(data)); err != nil {
		return m, err
	}
	if err = validate(ctx, db); err != nil {
		return m, err
	}
	if _, err = db.ExecContext(ctx, `PRAGMA user_version=1;
 CREATE TABLE release_metadata(release TEXT NOT NULL,schema_version INTEGER NOT NULL,published_at TEXT NOT NULL);
 CREATE VIEW resources AS SELECT 'ks-' || printf('%06d',benefit_id) AS id, * FROM expanded_catalog;`); err != nil {
		return m, err
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO release_metadata VALUES(?,1,?)", release, published); err != nil {
		return m, err
	}
	// SQLite permits unresolved references when creating a view. Execute it so
	// an unusable API catalog cannot become the active release.
	var resourceCount int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM resources").Scan(&resourceCount); err != nil {
		return m, fmt.Errorf("invalid resources view: %w", err)
	}
	stagedRelease := filepath.Join(stage, "releases", release)
	if err = os.MkdirAll(stagedRelease, 0755); err != nil {
		return m, err
	}
	snapshot := filepath.Join(stagedRelease, "catalog.sqlite")
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return m, err
	}
	if err = db.Close(); err != nil {
		return m, err
	}
	raw, err := os.ReadFile(snapshot)
	if err != nil {
		return m, err
	}
	sum := sha256.Sum256(raw)
	m = core.Manifest{Release: release, SchemaVersion: 1, PublishedAt: published, URL: "/v1/catalog/releases/" + release + "/catalog.sqlite", ByteSize: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	manifest = append(manifest, '\n')
	if err = os.WriteFile(filepath.Join(stagedRelease, "manifest.json"), manifest, 0644); err != nil {
		return m, err
	}
	// Use the same immutable loader as the API before exposing a release.
	if err = os.WriteFile(filepath.Join(stage, "manifest.json"), manifest, 0644); err != nil {
		return m, err
	}
	catalog, err := core.Open(stage)
	if err != nil {
		return m, err
	}
	err = validate(ctx, catalog.DB)
	closeErr := catalog.DB.Close()
	if err != nil {
		return m, err
	}
	if closeErr != nil {
		return m, closeErr
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return m, err
	}
	// Reserve the name exclusively: even an empty existing release must survive.
	if err = os.Mkdir(destination, 0755); err != nil {
		return m, err
	}
	installed := false
	defer func() {
		if !installed {
			os.RemoveAll(destination)
		}
	}()
	for _, name := range []string{"catalog.sqlite", "manifest.json"} {
		if err = os.Rename(filepath.Join(stagedRelease, name), filepath.Join(destination, name)); err != nil {
			return m, err
		}
	}
	if err = os.Rename(filepath.Join(stage, "manifest.json"), filepath.Join(output, "manifest.json")); err != nil {
		return m, err
	}
	installed = true
	return m, nil
}

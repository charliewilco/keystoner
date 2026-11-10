// Package api serves a verified immutable Keystoner catalog.
package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charliewilco/keystoner/docs"
	_ "modernc.org/sqlite"
)

type Manifest struct {
	Release       string `json:"release"`
	SchemaVersion int    `json:"schema_version"`
	PublishedAt   string `json:"published_at"`
	URL           string `json:"url"`
	ByteSize      int64  `json:"byte_size"`
	SHA256        string `json:"sha256"`
}
type Server struct {
	DB       *sql.DB
	Manifest Manifest
	snapshot string
	root     string
	mux      *http.ServeMux
}

var releaseID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

func Open(root string) (*Server, error) {
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	s := &Server{root: root}
	if err = json.Unmarshal(raw, &s.Manifest); err != nil {
		return nil, err
	}
	m := s.Manifest
	if m.SchemaVersion != 1 || !releaseID.MatchString(m.Release) || m.URL != "/v1/catalog/releases/"+m.Release+"/catalog.sqlite" {
		return nil, fmt.Errorf("unsupported or invalid manifest")
	}
	s.snapshot, err = filepath.Abs(filepath.Join(root, "releases", m.Release, "catalog.sqlite"))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.snapshot)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != m.ByteSize || hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, fmt.Errorf("snapshot size/hash mismatch")
	}
	uri := url.URL{Scheme: "file", Path: s.snapshot, RawQuery: "mode=ro&immutable=1"}
	s.DB, err = sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Server, error) { s.DB.Close(); return nil, e }
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
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		s.json(w, 200, map[string]any{"status": "ok", "release": m.Release})
	})
	s.mux.HandleFunc("GET /v1/resources", s.resources)
	s.mux.HandleFunc("GET /v1/resources/{id}", s.detail)
	s.mux.HandleFunc("GET /v1/categories", s.categories)
	s.mux.HandleFunc("GET /v1/catalog/manifest", func(w http.ResponseWriter, r *http.Request) { s.json(w, 200, m) })
	s.mux.HandleFunc("GET /v1/catalog/releases/{release}/catalog.sqlite", s.download)
	s.mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(docs.OpenAPI)
	})
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		s.json(w, 200, map[string]string{"name": "Keystoner", "openapi": "/openapi.yaml", "resources": "/v1/resources", "notice": "Preliminary online research; verify eligibility, cost and availability with providers."})
	})
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { s.problem(w, 404, "not_found") })
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Catalog-Release", s.Manifest.Release)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.mux.ServeHTTP(w, r)
}
func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) problem(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	s.json(w, status, map[string]string{"error": msg})
}
func query(ctx context.Context, db *sql.DB, q string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		v := map[string]any{}
		for i, k := range cols {
			v[k] = vals[i]
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func bound(q url.Values, key string, def, min, max int) (int, error) {
	v := q.Get(key)
	if v == "" {
		return def, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil || n < min || n > max {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}
func (s *Server) resources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	allowed := map[string]bool{"q": true, "category": true, "geography": true, "cost_tier": true, "funding_class": true, "limit": true, "offset": true}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 || len(v[0]) > 256 {
			s.problem(w, 400, "invalid_query")
			return
		}
	}
	limit, e := bound(q, "limit", 20, 1, 100)
	if e != nil {
		s.problem(w, 400, e.Error())
		return
	}
	offset, e := bound(q, "offset", 0, 0, 100000)
	if e != nil {
		s.problem(w, 400, e.Error())
		return
	}
	where := []string{"1=1"}
	args := []any{}
	for _, k := range []string{"category", "geography", "cost_tier", "funding_class"} {
		if v := q.Get(k); v != "" {
			where = append(where, k+" = ?")
			args = append(args, v)
		}
	}
	if v := q.Get("q"); v != "" {
		where = append(where, "instr(lower(name || ' ' || provider || ' ' || benefit || ' ' || category),lower(?)) > 0")
		args = append(args, v)
	}
	clause := strings.Join(where, " AND ")
	var count int
	if e = s.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM resources WHERE "+clause, args...).Scan(&count); e != nil {
		s.problem(w, 500, "catalog_error")
		return
	}
	items, e := query(r.Context(), s.DB, "SELECT * FROM resources WHERE "+clause+" ORDER BY benefit_id LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if e != nil {
		s.problem(w, 500, "catalog_error")
		return
	}
	s.json(w, 200, map[string]any{"release": s.Manifest.Release, "schema_version": 1, "total": count, "limit": limit, "offset": offset, "resources": items})
}
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	items, e := query(r.Context(), s.DB, "SELECT * FROM resources WHERE id = ?", r.PathValue("id"))
	if e != nil {
		s.problem(w, 500, "catalog_error")
		return
	}
	if len(items) == 0 {
		s.problem(w, 404, "resource_not_found")
		return
	}
	sources, e := query(r.Context(), s.DB, "SELECT s.* FROM sources s JOIN benefit_sources bs USING(source_id) WHERE bs.benefit_id = ? ORDER BY source_id", items[0]["benefit_id"])
	if e != nil {
		s.problem(w, 500, "catalog_error")
		return
	}
	items[0]["sources"] = sources
	s.json(w, 200, map[string]any{"release": s.Manifest.Release, "schema_version": 1, "resource": items[0]})
}
func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	items, e := query(r.Context(), s.DB, "SELECT category AS id,count(*) AS resource_count FROM resources GROUP BY category ORDER BY category")
	if e != nil {
		s.problem(w, 500, "catalog_error")
		return
	}
	s.json(w, 200, map[string]any{"release": s.Manifest.Release, "categories": items})
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	release := r.PathValue("release")
	if !releaseID.MatchString(release) {
		s.problem(w, 404, "release_not_available")
		return
	}
	m := s.Manifest
	path := s.snapshot
	if release != m.Release {
		raw, err := os.ReadFile(filepath.Join(s.root, "releases", release, "manifest.json"))
		if err != nil {
			s.problem(w, 404, "release_not_available")
			return
		}
		if json.Unmarshal(raw, &m) != nil || m.Release != release {
			s.problem(w, 500, "catalog_error")
			return
		}
		path = filepath.Join(s.root, "releases", release, "catalog.sqlite")
		data, err := os.ReadFile(path)
		sum := sha256.Sum256(data)
		if err != nil || int64(len(data)) != m.ByteSize || hex.EncodeToString(sum[:]) != m.SHA256 {
			s.problem(w, 500, "catalog_error")
			return
		}
	}
	w.Header().Set("X-Catalog-Release", m.Release)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+m.SHA256+`"`)
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	http.ServeFile(w, r, path)
}

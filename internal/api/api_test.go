package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) *Server {
	t.Helper()
	s, e := Open("../../catalog")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func get(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func TestSearchFiltersPagination(t *testing.T) {
	s := setup(t)
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/v1/resources", 200}, {"/v1/resources?limit=0", 400}, {"/v1/resources?offset=-1", 400}, {"/v1/resources?limit=101", 400}, {"/v1/resources?wat=x", 400}, {"/v1/resources?limit=1&limit=2", 400}, {"/v1/resources/ks-000001", 200}, {"/v1/resources/1", 404}, {"/missing", 404}, {"/v1/categories", 200},
	} {
		if w := get(s, tc.path); w.Code != tc.code {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	var first, second struct {
		Total     int
		Resources []map[string]any
	}
	json.Unmarshal(get(s, "/v1/resources?limit=1").Body.Bytes(), &first)
	json.Unmarshal(get(s, "/v1/resources?limit=1&offset=1").Body.Bytes(), &second)
	if first.Total != 108 || len(first.Resources) != 1 || first.Resources[0]["id"] == second.Resources[0]["id"] {
		t.Fatal("pagination")
	}
	for _, path := range []string{"/v1/resources?q=dental", "/v1/resources?cost_tier=free&funding_class=taxpayer_funded", "/v1/resources?category=vaccines"} {
		var v struct {
			Total     int
			Resources []map[string]any
		}
		w := get(s, path)
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || v.Total == 0 {
			t.Fatalf("filter %s: %s", path, w.Body.String())
		}
		if path == "/v1/resources?cost_tier=free&funding_class=taxpayer_funded" {
			for _, r := range v.Resources {
				if r["cost_tier"] != "free" || r["funding_class"] != "taxpayer_funded" || r["price_notes"] == "" {
					t.Fatal("fidelity")
				}
			}
		}
	}
	var v struct{ Total int }
	json.Unmarshal(get(s, "/v1/resources?q=%27%20OR%201=1--").Body.Bytes(), &v)
	if v.Total != 0 {
		t.Fatal("SQL injection")
	}
	var d struct{ Resource map[string]any }
	json.Unmarshal(get(s, "/v1/resources/ks-000001").Body.Bytes(), &d)
	if len(d.Resource["sources"].([]any)) == 0 || d.Resource["availability"] == nil || d.Resource["eligibility"] == nil {
		t.Fatal("detail data missing")
	}
}
func TestManifestDownload(t *testing.T) {
	s := setup(t)
	var m Manifest
	json.Unmarshal(get(s, "/v1/catalog/manifest").Body.Bytes(), &m)
	w := get(s, m.URL)
	sum := sha256.Sum256(w.Body.Bytes())
	if w.Code != 200 || int64(w.Body.Len()) != m.ByteSize || fmt.Sprintf("%x", sum) != m.SHA256 {
		t.Fatal("download parity")
	}
	req := httptest.NewRequest("GET", m.URL, nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	s.ServeHTTP(cached, req)
	if cached.Code != 304 {
		t.Fatal("etag")
	}
	if get(s, "/v1/catalog/releases/unknown/catalog.sqlite").Code != 404 {
		t.Fatal("unknown release")
	}
}
func TestStartupRejectsIncompatibleOrCorruptCatalog(t *testing.T) {
	for _, kind := range []string{"schema", "hash"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			raw, _ := os.ReadFile("../../catalog/manifest.json")
			var m Manifest
			json.Unmarshal(raw, &m)
			dir := filepath.Join(root, "releases", m.Release)
			os.MkdirAll(dir, 0755)
			data, _ := os.ReadFile(filepath.Join("../../catalog/releases", m.Release, "catalog.sqlite"))
			if kind == "schema" {
				m.SchemaVersion = 999
			} else {
				data[0] = 'X'
			}
			os.WriteFile(filepath.Join(dir, "catalog.sqlite"), data, 0644)
			raw, _ = json.Marshal(m)
			os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0644)
			if s, e := Open(root); e == nil {
				s.DB.Close()
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}

func TestHistoricalReleaseRetained(t *testing.T) {
	root := t.TempDir()
	raw, err := os.ReadFile("../../catalog/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var current Manifest
	json.Unmarshal(raw, &current)
	for _, release := range []string{current.Release, "older-release"} {
		dir := filepath.Join(root, "releases", release)
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join("../../catalog/releases", current.Release, "catalog.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "catalog.sqlite"), data, 0644); err != nil {
			t.Fatal(err)
		}
		m := current
		m.Release = release
		m.URL = "/v1/catalog/releases/" + release + "/catalog.sqlite"
		manifest, _ := json.Marshal(m)
		os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0644)
	}
	os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0644)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if w := get(s, "/v1/catalog/releases/older-release/catalog.sqlite"); w.Code != 200 || w.Header().Get("X-Catalog-Release") != "older-release" {
		t.Fatalf("historical download: %d", w.Code)
	}
}

func TestCostFundingAndGeographyFidelity(t *testing.T) {
	s := setup(t)
	rows, err := s.DB.Query("SELECT benefit_id,cost_tier,funding_class,price_notes,availability FROM benefit_details ORDER BY benefit_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var cost, funding, price, availability string
		if err = rows.Scan(&id, &cost, &funding, &price, &availability); err != nil {
			t.Fatal(err)
		}
		var v struct{ Resource map[string]any }
		w := get(s, fmt.Sprintf("/v1/resources/ks-%06d", id))
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Resource["cost_tier"] != cost || v.Resource["funding_class"] != funding || v.Resource["price_notes"] != price || v.Resource["availability"] != availability {
			t.Fatalf("lost source semantics for %d", id)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	var v struct{ Resources []map[string]any }
	json.Unmarshal(get(s, "/v1/resources?geography=Philadelphia&limit=100").Body.Bytes(), &v)
	if len(v.Resources) == 0 {
		t.Fatal("no geography matches")
	}
	for _, r := range v.Resources {
		if r["geography"] != "Philadelphia" {
			t.Fatal("wrong geography")
		}
	}
}

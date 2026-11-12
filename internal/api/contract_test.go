package api

import (
	"context"
	"encoding/json"
	"github.com/charliewilco/keystoner/tools/publisher"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/charliewilco/keystoner/internal/testcatalog"
)

func fixtureServer(t *testing.T) *Server {
	t.Helper()
	// Insert out of order: pagination must follow benefit_id, not insertion order.
	root := testcatalog.Write(t, `
 CREATE TABLE resources(id TEXT, benefit_id INTEGER, name TEXT, provider TEXT, benefit TEXT, category TEXT, geography TEXT, cost_tier TEXT, funding_class TEXT);
 INSERT INTO resources VALUES
 ('ks-000003',3,'Food pantry','Neighborhood Center','Groceries','food','Philadelphia','free','charitable_or_provider'),
 ('ks-000001',1,'Dental clinic','City Health','Checkups','health','Philadelphia','free','taxpayer_funded'),
 ('ks-000002',2,'Dental discount','Regional Center','Checkups','health','Pennsylvania','sliding_scale','charitable_or_provider');
 CREATE TABLE sources(source_id TEXT, url TEXT);
 INSERT INTO sources VALUES('z','https://example.org/z'),('a','https://example.org/a'),('unrelated','https://example.org/unrelated');
 CREATE TABLE benefit_sources(benefit_id INTEGER, source_id TEXT);
 INSERT INTO benefit_sources VALUES(1,'z'),(1,'a'),(2,'unrelated');`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}

func TestResourceQueryContract(t *testing.T) {
	s := fixtureServer(t)
	for _, tc := range []struct {
		name, query string
		total       int
		ids         []string
	}{
		{"stable order", "", 3, []string{"ks-000001", "ks-000002", "ks-000003"}},
		{"page", "?limit=1&offset=1", 3, []string{"ks-000002"}},
		{"past end", "?offset=3", 3, []string{}},
		{"case insensitive name", "?q=DENTAL", 2, []string{"ks-000001", "ks-000002"}},
		{"provider", "?q=neighborhood", 1, []string{"ks-000003"}},
		{"benefit", "?q=groceries", 1, []string{"ks-000003"}},
		{"category search", "?q=health", 2, []string{"ks-000001", "ks-000002"}},
		{"AND filters", "?category=health&geography=Philadelphia&cost_tier=free&funding_class=taxpayer_funded", 1, []string{"ks-000001"}},
		{"no matches", "?category=food&funding_class=taxpayer_funded", 0, []string{}},
		{"SQL is literal", "?q=%27%20OR%201=1--", 0, []string{}},
		{"wildcards are literal", "?q=%25", 0, []string{}},
		{"exact filter", "?category=HEALTH", 0, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := get(s, "/v1/resources"+tc.query)
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			var got struct {
				Release   string
				Total     int
				Resources []struct{ ID string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, r := range got.Resources {
				ids = append(ids, r.ID)
			}
			if got.Total != tc.total || !reflect.DeepEqual(ids, tc.ids) || got.Resources == nil || got.Release != "test-1" {
				t.Fatalf("got total=%d ids=%v release=%s; want total=%d ids=%v", got.Total, ids, got.Release, tc.total, tc.ids)
			}
		})
	}
}

func TestInvalidQueriesAreUncacheable(t *testing.T) {
	s := fixtureServer(t)
	for _, q := range []string{"limit=0", "limit=101", "limit=no", "offset=-1", "offset=100001", "offset=1.5", "limit=999999999999999999999999", "q=a&q=b", "unknown=x", "q=" + strings.Repeat("x", 257)} {
		t.Run(q, func(t *testing.T) {
			w := get(s, "/v1/resources?"+q)
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || body["error"] == "" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body.String())
			}
		})
	}
	for _, q := range []string{"limit=1", "limit=100", "offset=0", "offset=100000", "q=" + strings.Repeat("x", 256)} {
		if w := get(s, "/v1/resources?"+q); w.Code != 200 {
			t.Errorf("valid boundary %s: %d", q, w.Code)
		}
	}
}

func TestDetailIncludesOnlyItsSourcesInStableOrder(t *testing.T) {
	s := fixtureServer(t)
	w := get(s, "/v1/resources/ks-000001")
	var got struct {
		Resource struct {
			ID      string
			Sources []struct {
				SourceID string `json:"source_id"`
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, source := range got.Resource.Sources {
		ids = append(ids, source.SourceID)
	}
	if w.Code != 200 || got.Resource.ID != "ks-000001" || !reflect.DeepEqual(ids, []string{"a", "z"}) {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	if w := get(s, "/v1/resources/%27%20OR%201=1--"); w.Code != 404 {
		t.Fatalf("nonexistent ID: %d", w.Code)
	}
}

func TestCategoriesAggregateCounts(t *testing.T) {
	w := get(fixtureServer(t), "/v1/categories")
	var got struct {
		Categories []struct {
			ID    string
			Count int `json:"resource_count"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(got.Categories) != 2 || got.Categories[0].ID != "food" || got.Categories[0].Count != 1 || got.Categories[1].ID != "health" || got.Categories[1].Count != 2 {
		t.Fatalf("categories: %s", w.Body.String())
	}
}

func TestDatabaseErrorsDoNotLeakOrCache(t *testing.T) {
	s := fixtureServer(t)
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/resources", "/v1/resources/ks-000001", "/v1/categories"} {
		w := get(s, path)
		if w.Code != 500 || strings.TrimSpace(w.Body.String()) != `{"error":"catalog_error"}` || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestSnapshotRangeAndHead(t *testing.T) {
	s := fixtureServer(t)
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, s.Manifest.URL, nil)
		if method == "GET" {
			req.Header.Set("Range", "bytes=0-15")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if method == "GET" && (w.Code != 206 || w.Body.String() != "SQLite format 3\x00" || !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes 0-15/")) {
			t.Fatalf("range: %d %q %v", w.Code, w.Body.String(), w.Header())
		}
		if method == "HEAD" && (w.Code != 200 || w.Body.Len() != 0) {
			t.Fatalf("HEAD: %d %d", w.Code, w.Body.Len())
		}
		if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || w.Header().Get("ETag") != `"`+s.Manifest.SHA256+`"` {
			t.Fatalf("snapshot cache headers: %v", w.Header())
		}
	}
}

// Publishing the maintained seed must preserve every public field and citation,
// not merely produce a database that passes its own validation.
func TestGoPublishedCatalogMatchesExistingAPI(t *testing.T) {
	root := t.TempDir()
	if _, err := publisher.Publish(context.Background(), "../../seed/catalog.sql", root, "migration-check", "2026-11-12T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.DB.Close()
	existing := setup(t)
	paths := []string{"/v1/resources?limit=100", "/v1/resources?limit=100&offset=100", "/v1/categories"}
	rows, err := existing.DB.Query("SELECT id FROM resources ORDER BY benefit_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		paths = append(paths, "/v1/resources/"+id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			var responses [2]map[string]any
			for i, s := range []*Server{existing, fresh} {
				w := get(s, path)
				if w.Code != 200 {
					t.Fatalf("%d: %s", w.Code, w.Body.String())
				}
				if err := json.Unmarshal(w.Body.Bytes(), &responses[i]); err != nil {
					t.Fatal(err)
				}
				delete(responses[i], "release")
			}
			if !reflect.DeepEqual(responses[0], responses[1]) {
				t.Fatalf("published catalog changed API data at %s", path)
			}
		})
	}
}

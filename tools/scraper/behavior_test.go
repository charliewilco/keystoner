package scraper

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFetchRedirectSuccessCachesRobots(t *testing.T) {
	var paths []string
	f := fixtureFetcher(t, func(r *http.Request) (int, string, string) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/robots.txt":
			return 404, "", ""
		case "/start":
			return 302, "", "/free#apply"
		default:
			return 200, "<title>Free help</title>", ""
		}
	})
	final, body, err := f.Fetch(context.Background(), "https://www.phila.gov/start")
	if err != nil || final != "https://www.phila.gov/free" || body != "<title>Free help</title>" {
		t.Fatalf("%q %q %v", final, body, err)
	}
	if _, _, err = f.Fetch(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/robots.txt", "/start", "/free", "/free"}) {
		t.Fatalf("requests: %v", paths)
	}
}

func TestFetchRejectsRedirectLoopsAndMissingLocations(t *testing.T) {
	for _, tc := range []struct {
		name, location, want string
		requests             int
	}{
		{"loop", "/free", "redirect limit", 7},
		{"missing location", "", "invalid redirect", 2},
		{"downgrade", "http://www.phila.gov/free", "only HTTPS", 2},
		{"query", "/free?token=secret", "without credentials or queries", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := fixtureFetcher(t, func(r *http.Request) (int, string, string) {
				calls++
				if r.URL.Path == "/robots.txt" {
					return 404, "", ""
				}
				return 302, "", tc.location
			})
			if _, _, err := f.Fetch(context.Background(), "https://www.phila.gov/free"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error: %v", err)
			}
			if calls != tc.requests {
				t.Fatalf("requests=%d want %d", calls, tc.requests)
			}
		})
	}
}

func TestFetchContentTypesEncodingAndLimits(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body, want, err string }{
		{"HTML", "text/html; charset=utf-8", "<p>help</p>", "<p>help</p>", ""},
		{"XHTML", "application/xhtml+xml", "<p>help</p>", "<p>help</p>", ""},
		{"legacy encoding", "text/html; charset=windows-1252", "caf\xe9", "café", ""},
		{"non HTML", "application/pdf", "pdf", "", "not an HTML"},
		{"missing type", "", "text", "", "not an HTML"},
		{"exact byte limit", "text/html; charset=utf-8", strings.Repeat("a", maxBytes), strings.Repeat("a", maxBytes), ""},
		{"decoded limit", "text/html; charset=windows-1252", strings.Repeat("\xe9", maxBytes/2+1), "", "decoded response exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewHTTPFetcher([]string{"example.org"})
			f.delay = 0
			f.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				status, body, contentType := 200, tc.body, tc.contentType
				if r.URL.Path == "/robots.txt" {
					status, body = 404, ""
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			_, body, err := f.Fetch(context.Background(), "https://example.org/free")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error=%v want %s", err, tc.err)
				}
				return
			}
			if err != nil || body != tc.want {
				t.Fatalf("decoded result len=%d want=%d error=%v", len(body), len(tc.want), err)
			}
		})
	}
}

type fetchFunc func(context.Context, string) (string, string, error)

func (f fetchFunc) Fetch(ctx context.Context, u string) (string, string, error) { return f(ctx, u) }

func TestCrawlDeduplicatesAndResolvesAgainstFinalURL(t *testing.T) {
	var visited []string
	f := fetchFunc(func(_ context.Context, u string) (string, string, error) {
		visited = append(visited, u)
		return "https://example.org/services/", `<a href="free#one">Free</a><a href="free#two">Free</a><a href="https://outside.org/free">Free</a><a href="/free?q=1">Free</a><a href="/about">About us</a>`, nil
	})
	r, err := Crawl(context.Background(), Config{[]string{"example.org"}, []string{"https://example.org/", "https://example.org/#duplicate"}}, nil, filepath.Join(t.TempDir(), "state.sqlite"), 10, 1, f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(visited, []string{"https://example.org/", "https://example.org/services/free"}) || r.Attempted != 2 || len(r.Candidates) != 1 || r.Candidates[0].URL != "https://example.org/services/free" || !r.ReviewRequired {
		t.Fatalf("visited=%v report=%+v", visited, r)
	}
}

func TestCrawlFailuresCountTowardBudget(t *testing.T) {
	calls := 0
	f := fetchFunc(func(_ context.Context, u string) (string, string, error) {
		calls++
		return "", "", errors.New("offline")
	})
	r, err := Crawl(context.Background(), Config{[]string{"example.org"}, []string{"https://example.org/a", "https://example.org/b", "https://example.org/c"}}, nil, filepath.Join(t.TempDir(), "state.sqlite"), 2, 0, f)
	if err != nil || calls != 2 || r.Attempted != 2 || len(r.Errors) != 2 || r.RemainingQueue != 1 || len(r.Pages) != 0 {
		t.Fatalf("calls=%d report=%+v err=%v", calls, r, err)
	}
}

func TestCrawlChecksUnseenSourcesBeforePreviouslyChecked(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.sqlite")
	config := Config{AllowedHosts: []string{"example.org"}}
	var visited []string
	f := fetchFunc(func(_ context.Context, u string) (string, string, error) {
		visited = append(visited, u)
		return u, "<p>help</p>", nil
	})
	if _, err := Crawl(context.Background(), config, map[string]string{"https://example.org/a": "a"}, state, 1, 0, f); err != nil {
		t.Fatal(err)
	}
	visited = nil
	if _, err := Crawl(context.Background(), config, map[string]string{"https://example.org/a": "a", "https://example.org/z": "z"}, state, 1, 0, f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(visited, []string{"https://example.org/z"}) {
		t.Fatalf("visited %v; unseen source starved", visited)
	}
}

func TestCrawlIgnoresMarkupOnlyChanges(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.sqlite")
	config := Config{[]string{"example.org"}, []string{"https://example.org/"}}
	f := &fakeFetcher{body: "<p>Free   help</p><script>v1</script>"}
	if _, err := Crawl(context.Background(), config, nil, state, 1, 0, f); err != nil {
		t.Fatal(err)
	}
	f.body = "<div> Free help </div><script>v2</script><style>new</style>"
	r, err := Crawl(context.Background(), config, nil, state, 1, 0, f)
	if err != nil || len(r.Pages) != 1 || r.Pages[0].Status != "unchanged" {
		t.Fatalf("%+v %v", r, err)
	}
}

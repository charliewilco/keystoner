package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeFetcher struct {
	body string
	fail bool
}

func (f *fakeFetcher) Fetch(_ context.Context, u string) (string, string, error) {
	if f.fail {
		return "", "", errors.New("timeout")
	}
	return u, f.body, nil
}
func TestCrawlReviewAndChanges(t *testing.T) {
	f := &fakeFetcher{body: `<title>Benefits</title><script>noise</script><a href="/new-assistance#apply">Free assistance</a><a href="https://other.example/free">Free</a>`}
	config := Config{[]string{"www.phila.gov"}, []string{"https://www.phila.gov/services/"}}
	state := filepath.Join(t.TempDir(), "state.sqlite")
	run := func() Report {
		t.Helper()
		r, e := Crawl(context.Background(), config, "../../seed/catalog.sql", state, 1, 0, f)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := run()
	if r.Pages[0].Status != "first_seen" || len(r.Candidates) != 1 || r.Attempted != 1 {
		t.Fatalf("unexpected report: %+v", r)
	}
	c := r.Candidates[0]
	if c.CostTier != "varies_or_unconfirmed" || c.FundingClass != "unverified" || c.URL != "https://www.phila.gov/new-assistance" {
		t.Fatalf("candidate: %+v", c)
	}
	if run().Pages[0].Status != "unchanged" {
		t.Fatal("unchanged")
	}
	f.body += "<p>Enrollment closed</p>"
	if run().Pages[0].Status != "changed" {
		t.Fatal("change not detected")
	}
	f.fail = true
	if len(run().Errors) != 1 {
		t.Fatal("error missing")
	}
	f.fail = false
	if run().Pages[0].Status != "unchanged" {
		t.Fatal("failure overwrote baseline")
	}
}
func TestCanonicalAndParser(t *testing.T) {
	for _, u := range []string{"http://www.phila.gov/", "https://user:pass@www.phila.gov/", "https://www.phila.gov/?q=x", "https://www.phila.gov:9000/"} {
		if _, e := canonical(u); e == nil {
			t.Errorf("accepted %s", u)
		}
	}
	u, e := canonical("https://WWW.PHILA.GOV/free#apply")
	if e != nil || u != "https://www.phila.gov/free" {
		t.Fatal(u, e)
	}
	text, title, links, e := parsePage(`<title>A &amp; B</title><script>hidden</script><style>noise</style><a href="/free"><b>Free</b> health</a>`)
	if e != nil || strings.Contains(text, "hidden") || strings.Contains(text, "noise") || title != "A & B" || links[0].label != "Free health" {
		t.Fatal(text, title, links, e)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureFetcher(t *testing.T, handler func(*http.Request) (int, string, string)) *HTTPFetcher {
	t.Helper()
	f := NewHTTPFetcher([]string{"www.phila.gov"})
	f.delay = 0
	f.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		status, body, location := handler(r)
		h := http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
		if location != "" {
			h.Set("Location", location)
		}
		if r.Header.Get("User-Agent") != agent {
			t.Error("missing agent")
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return f
}
func TestRobotsAndRedirectBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, robots string
		status       int
		want         string
	}{{"disallow", "User-agent: *\nDisallow: /free", 200, "disallows"}, {"unavailable", "", 503, "unavailable"}, {"redirected robots", "", 302, "unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtureFetcher(t, func(r *http.Request) (int, string, string) {
				if r.URL.Path != "/robots.txt" {
					t.Fatal("fetched disallowed page")
				}
				return tc.status, tc.robots, ""
			})
			if _, _, e := f.Fetch(context.Background(), "https://www.phila.gov/free"); e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Fatal(e)
			}
		})
	}
	f := fixtureFetcher(t, func(r *http.Request) (int, string, string) {
		if r.URL.Hostname() != "www.phila.gov" {
			t.Fatal("followed outside redirect")
		}
		if r.URL.Path == "/robots.txt" {
			return 404, "", ""
		}
		return 302, "", "https://outside.example/free"
	})
	if _, _, e := f.Fetch(context.Background(), "https://www.phila.gov/free"); e == nil || !strings.Contains(e.Error(), "host not approved") {
		t.Fatal(e)
	}
	f = fixtureFetcher(t, func(r *http.Request) (int, string, string) {
		switch r.URL.Path {
		case "/robots.txt":
			return 200, "User-agent: *\nDisallow: /private", ""
		case "/free":
			return 302, "", "/private"
		default:
			t.Fatal("redirect bypassed robots")
			return 500, "", ""
		}
	})
	if _, _, e := f.Fetch(context.Background(), "https://www.phila.gov/free"); e == nil || !strings.Contains(e.Error(), "disallows") {
		t.Fatal(e)
	}
}
func TestResponseLimitAndPolicies(t *testing.T) {
	f := fixtureFetcher(t, func(r *http.Request) (int, string, string) {
		if r.URL.Path == "/robots.txt" {
			return 404, "", ""
		}
		return 200, strings.Repeat("x", maxBytes+1), ""
	})
	if _, _, e := f.Fetch(context.Background(), "https://www.phila.gov/free"); e == nil || !strings.Contains(e.Error(), "byte limit") {
		t.Fatal(e)
	}
	f = fixtureFetcher(t, func(*http.Request) (int, string, string) {
		return 200, "User-agent: *\nCrawl-delay: 2\nRequest-rate: 1/4", ""
	})
	u, _ := url.Parse("https://www.phila.gov/free")
	p, e := f.rules(context.Background(), u)
	if e != nil || p.delay != 4*time.Second {
		t.Fatal(p.delay, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.last[u.Hostname()] = time.Now()
	if _, _, _, e = f.request(ctx, u.String(), time.Hour); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestBoundsAndDepth(t *testing.T) {
	f := &fakeFetcher{body: `<a href="/free">Free</a>`}
	config := Config{[]string{"example.com"}, []string{"https://example.com/"}}
	state := filepath.Join(t.TempDir(), "state.sqlite")
	for _, depth := range []int{0, 1} {
		r, e := Crawl(context.Background(), config, "../../seed/catalog.sql", state, 2, depth, f)
		if e != nil || r.Attempted != depth+1 {
			t.Fatal(r, e)
		}
	}
	if _, e := Crawl(context.Background(), config, "../../seed/catalog.sql", state, 0, 1, f); e == nil {
		t.Fatal("invalid bounds")
	}
}

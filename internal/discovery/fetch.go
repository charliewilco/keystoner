// Package discovery finds review candidates without modifying the public catalog.
package discovery

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/temoto/robotstxt"
	"golang.org/x/net/html/charset"
)

const agent = "KeystonerDiscovery/0.2 (+https://github.com/charliewilco/keystoner)"
const maxBytes = 2_000_000

func canonical(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("only HTTPS URLs without credentials or queries are supported")
	}
	u.Host = strings.ToLower(u.Hostname())
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

type Fetcher interface {
	Fetch(context.Context, string) (string, string, error)
}
type policy struct {
	group *robotstxt.Group
	delay time.Duration
}
type HTTPFetcher struct {
	client   *http.Client
	hosts    map[string]bool
	policies map[string]policy
	last     map[string]time.Time
	delay    time.Duration
}

func NewHTTPFetcher(hosts []string) *HTTPFetcher {
	allowed := map[string]bool{}
	for _, h := range hosts {
		allowed[h] = true
	}
	return &HTTPFetcher{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, hosts: allowed, policies: map[string]policy{}, last: map[string]time.Time{}, delay: time.Second}
}
func (f *HTTPFetcher) request(ctx context.Context, raw string, delay time.Duration) ([]byte, http.Header, int, error) {
	u, _ := url.Parse(raw)
	wait := time.Until(f.last[u.Hostname()].Add(delay))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
	f.last[u.Hostname()] = time.Now()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, nil, 0, e
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "text/html,text/plain")
	res, e := f.client.Do(req)
	if e != nil {
		return nil, nil, 0, e
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if e != nil {
		return nil, nil, 0, e
	}
	if len(body) > maxBytes {
		return nil, nil, 0, fmt.Errorf("response exceeds byte limit")
	}
	return body, res.Header, res.StatusCode, nil
}

var requestRate = regexp.MustCompile(`(?im)^\s*request-rate\s*:\s*([0-9]+)\s*/\s*([0-9]+)\s*(?:#.*)?$`)

func (f *HTTPFetcher) rules(ctx context.Context, u *url.URL) (policy, error) {
	if p, ok := f.policies[u.Hostname()]; ok {
		return p, nil
	}
	body, _, status, e := f.request(ctx, "https://"+u.Host+"/robots.txt", f.delay)
	if e != nil {
		return policy{}, fmt.Errorf("robots.txt unavailable: %w", e)
	}
	if status == 404 {
		body = nil
	} else if status != 200 {
		return policy{}, fmt.Errorf("robots.txt unavailable: HTTP %d", status)
	}
	rules, e := robotstxt.FromBytes(body)
	if e != nil {
		return policy{}, fmt.Errorf("invalid robots.txt: %w", e)
	}
	p := policy{group: rules.FindGroup("KeystonerDiscovery"), delay: f.delay}
	if p.group.CrawlDelay > p.delay {
		p.delay = p.group.CrawlDelay
	}
	// Apply the most conservative request-rate from any group. This extension is
	// not interpreted by robotstxt; applying all groups may slow us, never speed us up.
	for _, m := range requestRate.FindAllSubmatch(body, -1) {
		count, _ := strconv.ParseFloat(string(m[1]), 64)
		seconds, _ := strconv.ParseFloat(string(m[2]), 64)
		if count > 0 {
			d := time.Duration(seconds / count * float64(time.Second))
			if d > p.delay {
				p.delay = d
			}
		}
	}
	f.policies[u.Hostname()] = p
	return p, nil
}
func (f *HTTPFetcher) Fetch(ctx context.Context, raw string) (string, string, error) {
	for i := 0; i < 6; i++ {
		normalized, e := canonical(raw)
		if e != nil {
			return "", "", e
		}
		u, _ := url.Parse(normalized)
		if !f.hosts[u.Hostname()] {
			return "", "", fmt.Errorf("host not approved")
		}
		p, e := f.rules(ctx, u)
		if e != nil {
			return "", "", e
		}
		if !p.group.Test(u.EscapedPath()) {
			return "", "", fmt.Errorf("robots.txt disallows URL")
		}
		body, headers, status, e := f.request(ctx, normalized, p.delay)
		if e != nil {
			return "", "", e
		}
		switch status {
		case 301, 302, 303, 307, 308:
			loc, e := url.Parse(headers.Get("Location"))
			if e != nil || headers.Get("Location") == "" {
				return "", "", fmt.Errorf("invalid redirect")
			}
			raw = u.ResolveReference(loc).String()
			continue
		}
		if status != 200 {
			return "", "", fmt.Errorf("HTTP %d", status)
		}
		kind, _, e := mime.ParseMediaType(headers.Get("Content-Type"))
		if e != nil || (kind != "text/html" && kind != "application/xhtml+xml") {
			return "", "", fmt.Errorf("not an HTML page")
		}
		reader, e := charset.NewReader(strings.NewReader(string(body)), headers.Get("Content-Type"))
		if e != nil {
			return "", "", e
		}
		decoded, e := io.ReadAll(io.LimitReader(reader, maxBytes+1))
		if e != nil {
			return "", "", e
		}
		if len(decoded) > maxBytes {
			return "", "", fmt.Errorf("decoded response exceeds byte limit")
		}
		return normalized, string(decoded), nil
	}
	return "", "", fmt.Errorf("redirect limit exceeded")
}

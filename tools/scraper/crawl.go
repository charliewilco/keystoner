package scraper

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	_ "modernc.org/sqlite"
)

type Config struct {
	AllowedHosts []string `json:"allowed_hosts"`
	StartURLs    []string `json:"start_urls"`
}
type PageResult struct {
	URL       string  `json:"url"`
	FinalURL  string  `json:"final_url"`
	SourceID  *string `json:"source_id"`
	Status    string  `json:"status"`
	Title     string  `json:"title"`
	SHA256    string  `json:"sha256"`
	CheckedAt string  `json:"checked_at"`
}
type Candidate struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	DiscoveredOn string `json:"discovered_on"`
	FetchedAt    string `json:"fetched_at"`
	Status       string `json:"status"`
	CostTier     string `json:"cost_tier"`
	FundingClass string `json:"funding_class"`
}
type Failure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}
type Report struct {
	StartedAt      string       `json:"started_at"`
	ReviewRequired bool         `json:"review_required"`
	Pages          []PageResult `json:"pages"`
	Candidates     []Candidate  `json:"candidates"`
	Errors         []Failure    `json:"errors"`
	Attempted      int          `json:"attempted"`
	RemainingQueue int          `json:"remaining_queue"`
}
type link struct{ href, label string }

func short(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func parsePage(body string) (string, string, []link, error) {
	root, e := html.Parse(strings.NewReader(body))
	if e != nil {
		return "", "", nil, e
	}
	var text, title []string
	links := []link{}
	var walk func(*html.Node, bool, *[]string)
	walk = func(n *html.Node, inTitle bool, label *[]string) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			inTitle = true
		}
		anchor := n.Type == html.ElementNode && n.Data == "a"
		var words []string
		href := ""
		if anchor {
			label = &words
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
		}
		if n.Type == html.TextNode {
			v := strings.Join(strings.Fields(n.Data), " ")
			if v != "" {
				text = append(text, v)
				if inTitle {
					title = append(title, v)
				}
				if label != nil {
					*label = append(*label, v)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inTitle, label)
		}
		if anchor {
			links = append(links, link{href, strings.Join(words, " ")})
		}
	}
	walk(root, false, nil)
	return strings.Join(text, " "), short(strings.Join(title, " "), 200), links, nil
}

var terms = regexp.MustCompile(`(?i)\b(free|benefits?|assistance|health|food|housing|utilities|enroll|eligibility|services|programs)\b`)

// Crawl uses trusted source URLs and config. It only writes its own baseline state.
func Crawl(ctx context.Context, config Config, sources map[string]string, state string, maxPages, depth int, fetcher Fetcher) (Report, error) {
	report := Report{StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ReviewRequired: true, Pages: []PageResult{}, Candidates: []Candidate{}, Errors: []Failure{}}
	if maxPages < 1 || maxPages > 500 || depth < 0 || depth > 2 {
		return report, fmt.Errorf("max-pages must be 1–500; depth 0–2")
	}
	hosts := map[string]bool{}
	for _, h := range config.AllowedHosts {
		hosts[h] = true
	}
	if len(hosts) == 0 {
		return report, fmt.Errorf("allowed_hosts is empty")
	}
	if fetcher == nil {
		fetcher = NewHTTPFetcher(config.AllowedHosts)
	}
	known := map[string]string{}
	for raw, id := range sources {
		if u, e := canonical(raw); e == nil {
			known[u] = id
		}
	}
	var e error
	if e = os.MkdirAll(filepath.Dir(state), 0755); e != nil {
		return report, e
	}
	db, e := sql.Open("sqlite", state)
	if e != nil {
		return report, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Parser changes can change normalized text; use a new baseline namespace
	// instead of reporting legacy parser hashes as real content changes.
	if _, e = db.Exec("CREATE TABLE IF NOT EXISTS pages_go_v1(url TEXT PRIMARY KEY,sha256 TEXT NOT NULL,checked_at TEXT NOT NULL)"); e != nil {
		return report, e
	}
	checked := map[string]string{}
	rows, e := db.Query("SELECT url,checked_at FROM pages_go_v1")
	if e != nil {
		return report, e
	}
	for rows.Next() {
		var u, at string
		if e = rows.Scan(&u, &at); e != nil {
			rows.Close()
			return report, e
		}
		checked[u] = at
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return report, e
	}
	type item struct {
		url   string
		level int
	}
	queue := []item{}
	for _, u := range config.StartURLs {
		queue = append(queue, item{u, 0})
	}
	urls := []string{}
	for u := range known {
		urls = append(urls, u)
	}
	sort.Slice(urls, func(i, j int) bool {
		if checked[urls[i]] == checked[urls[j]] {
			return urls[i] < urls[j]
		}
		return checked[urls[i]] < checked[urls[j]]
	})
	for _, u := range urls {
		parsed, _ := url.Parse(u)
		if hosts[parsed.Hostname()] {
			queue = append(queue, item{u, 0})
		}
	}
	seen, candidates := map[string]bool{}, map[string]bool{}
	for len(queue) > 0 && len(seen) < maxPages {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		next := queue[0]
		queue = queue[1:]
		u, e := canonical(next.url)
		if e != nil {
			continue
		}
		parsed, _ := url.Parse(u)
		if seen[u] || !hosts[parsed.Hostname()] {
			continue
		}
		seen[u] = true
		final, body, e := fetcher.Fetch(ctx, u)
		if e != nil {
			report.Errors = append(report.Errors, Failure{u, short(e.Error(), 300)})
			continue
		}
		text, title, links, e := parsePage(body)
		if e != nil {
			report.Errors = append(report.Errors, Failure{u, short(e.Error(), 300)})
			continue
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		var previous string
		e = db.QueryRow("SELECT sha256 FROM pages_go_v1 WHERE url=?", u).Scan(&previous)
		status := "unchanged"
		if errors.Is(e, sql.ErrNoRows) {
			status = "first_seen"
		} else if e != nil {
			return report, e
		} else if previous != digest {
			status = "changed"
		}
		if _, e = db.Exec("INSERT OR REPLACE INTO pages_go_v1 VALUES(?,?,?)", u, digest, report.StartedAt); e != nil {
			return report, e
		}
		var sid *string
		if id, ok := known[u]; ok {
			sid = &id
		}
		report.Pages = append(report.Pages, PageResult{u, final, sid, status, title, digest, report.StartedAt})
		base, e := url.Parse(final)
		if e != nil {
			return report, e
		}
		for _, l := range links {
			ref, e := url.Parse(l.href)
			if e != nil {
				continue
			}
			target, e := canonical(base.ResolveReference(ref).String())
			if e != nil {
				continue
			}
			p, _ := url.Parse(target)
			if !hosts[p.Hostname()] || !terms.MatchString(l.label+" "+p.Path) {
				continue
			}
			if _, ok := known[target]; !ok && !candidates[target] {
				candidates[target] = true
				report.Candidates = append(report.Candidates, Candidate{target, short(l.label, 200), final, report.StartedAt, "unreviewed_link", "varies_or_unconfirmed", "unverified"})
			}
			if next.level < depth {
				queue = append(queue, item{target, next.level + 1})
			}
		}
	}
	report.Attempted = len(seen)
	report.RemainingQueue = len(queue)
	return report, nil
}

#!/usr/bin/env python3
"""Bounded public-source discovery; never writes the published catalog."""
import argparse
from collections import deque
from datetime import datetime, timezone
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import sqlite3
import time
from urllib.error import HTTPError
from urllib.parse import urljoin, urlsplit, urlunsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler
from urllib.robotparser import RobotFileParser

AGENT = 'KeystonerDiscovery/0.1 (+https://github.com/charliewilco/keystoner)'
MAX_BYTES = 2_000_000
TERMS = re.compile(r'\b(free|benefits?|assistance|health|food|housing|utilities|enroll|eligibility|services|programs)\b', re.I)

def canonical(url):
    p = urlsplit(url)
    if p.scheme != 'https' or not p.hostname or p.username or p.password or p.port not in (None, 443):
        raise ValueError('only public HTTPS source URLs are supported')
    # Query URLs are intentionally excluded to avoid calendars/search traps.
    if p.query:
        raise ValueError('query URLs are not crawled')
    return urlunsplit(('https', p.hostname, p.path or '/', '', ''))

class Page(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.hidden = 0
        self.text, self.links, self.title = [], [], []
        self.anchor = None
        self.in_title = False
    def handle_starttag(self, tag, attrs):
        if tag in ('script', 'style', 'noscript'):
            self.hidden += 1
        if tag == 'title': self.in_title = True
        if tag == 'a': self.anchor = [dict(attrs).get('href', ''), []]
    def handle_endtag(self, tag):
        if tag in ('script', 'style', 'noscript') and self.hidden: self.hidden -= 1
        if tag == 'title': self.in_title = False
        if tag == 'a' and self.anchor:
            self.links.append((self.anchor[0], ' '.join(self.anchor[1])))
            self.anchor = None
    def handle_data(self, data):
        if self.hidden: return
        value = ' '.join(data.split())
        if value:
            self.text.append(value)
            if self.in_title: self.title.append(value)
            if self.anchor: self.anchor[1].append(value)

class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

class Fetcher:
    def __init__(self, hosts, delay=1.0):
        self.hosts = set(hosts)
        self.delay = delay
        self.robots, self.last = {}, {}
        self.opener = build_opener(NoRedirect())
    def request(self, url, delay):
        host = urlsplit(url).hostname
        time.sleep(max(0, delay - (time.monotonic() - self.last.get(host, 0))))
        self.last[host] = time.monotonic()
        with self.opener.open(Request(url, headers={'User-Agent': AGENT, 'Accept': 'text/html,text/plain'}), timeout=20) as r:
            body = r.read(MAX_BYTES + 1)
            if len(body) > MAX_BYTES: raise ValueError('response exceeds byte limit')
            return body, r.headers.get_content_type(), r.headers.get_content_charset() or 'utf-8'
    def policy(self, url):
        host = urlsplit(url).hostname
        if host not in self.robots:
            robot = RobotFileParser()
            try:
                body, _, encoding = self.request('https://' + host + '/robots.txt', self.delay)
                robot.parse(body.decode(encoding, errors='replace').splitlines())
            except HTTPError as e:
                if e.code == 404: robot.parse([])
                else: raise ValueError('robots.txt unavailable; skipping host') from e
            self.robots[host] = robot
        robot = self.robots[host]
        if not robot.can_fetch(AGENT, url): raise ValueError('robots.txt disallows URL')
        rate = robot.request_rate(AGENT)
        return max(self.delay, robot.crawl_delay(AGENT) or 0, rate.seconds / rate.requests if rate and rate.requests else 0)
    def fetch(self, url):
        for _ in range(6):
            url = canonical(url)
            if urlsplit(url).hostname not in self.hosts: raise ValueError('host not approved')
            delay = self.policy(url)
            try:
                body, kind, encoding = self.request(url, delay)
                if kind not in ('text/html', 'application/xhtml+xml'): raise ValueError('not an HTML page')
                return url, body.decode(encoding, errors='replace')
            except HTTPError as e:
                if e.code in (301, 302, 303, 307, 308) and e.headers.get('Location'):
                    url = urljoin(url, e.headers['Location'])
                else: raise
        raise ValueError('redirect limit exceeded')

def crawl(config, catalog, state, max_pages=30, depth=1, fetcher=None):
    # Configuration and seed are repository-maintained trusted inputs, not public API inputs.
    hosts = set(config['allowed_hosts'])
    fetcher = fetcher or Fetcher(hosts)
    with sqlite3.connect(':memory:') as db:
        db.executescript(Path(catalog).read_text())
        known = {}
        for sid, url in db.execute('SELECT source_id,url FROM sources'):
            try: known[canonical(url)] = sid
            except ValueError: pass
    queue = deque((url, 0, None) for url in config['start_urls'])
    state = Path(state)
    state.parent.mkdir(parents=True, exist_ok=True)
    now = datetime.now(timezone.utc).isoformat()
    report = {'started_at': now, 'review_required': True, 'pages': [], 'candidates': [], 'errors': []}
    seen, candidates = set(), set()
    with sqlite3.connect(state) as db:
        db.execute('CREATE TABLE IF NOT EXISTS pages(url TEXT PRIMARY KEY, sha256 TEXT NOT NULL, checked_at TEXT NOT NULL)')
        checked = dict(db.execute('SELECT url,checked_at FROM pages'))
        queue.extend((u, 0, None) for u in sorted(known, key=lambda u: (checked.get(u, ''), u)) if urlsplit(u).hostname in hosts)
        while queue and len(seen) < max_pages:
            raw, level, parent = queue.popleft()
            try: url = canonical(raw)
            except ValueError: continue
            if url in seen or urlsplit(url).hostname not in hosts: continue
            seen.add(url)
            try:
                final, html = fetcher.fetch(url)
                page = Page(); page.feed(html)
                digest = hashlib.sha256(' '.join(page.text).encode()).hexdigest()
                previous = db.execute('SELECT sha256 FROM pages WHERE url=?', (url,)).fetchone()
                status = 'first_seen' if previous is None else ('unchanged' if previous[0] == digest else 'changed')
                report['pages'].append(dict(url=url, final_url=final, source_id=known.get(url), status=status,
                                            title=' '.join(page.title)[:200], sha256=digest, checked_at=now))
                db.execute('INSERT OR REPLACE INTO pages VALUES(?,?,?)', (url, digest, now))
                for href, label in page.links:
                    try: target = canonical(urljoin(final, href))
                    except ValueError: continue
                    if urlsplit(target).hostname not in hosts or not TERMS.search(label + ' ' + urlsplit(target).path): continue
                    if target not in known and target not in candidates:
                        candidates.add(target)
                        report['candidates'].append(dict(url=target, title=label[:200], discovered_on=final,
                            fetched_at=now, status='unreviewed_link', cost_tier='varies_or_unconfirmed', funding_class='unverified'))
                    if level < depth: queue.append((target, level + 1, final))
            except (OSError, ValueError, LookupError) as e:
                report['errors'].append(dict(url=url, error=str(e)[:300]))
        report['attempted'] = len(seen)
        report['remaining_queue'] = len(queue)
    return report

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config', default='discovery/sources.json')
    p.add_argument('--seed', default='seed/catalog.sql')
    p.add_argument('--state', default='work/discovery/state.sqlite')
    p.add_argument('--output', default='work/discovery/report.json')
    p.add_argument('--max-pages', type=int, default=30)
    p.add_argument('--depth', type=int, default=1)
    a = p.parse_args()
    if not 1 <= a.max_pages <= 500 or not 0 <= a.depth <= 2: p.error('max-pages must be 1–500; depth 0–2')
    report = crawl(json.loads(Path(a.config).read_text()), a.seed, a.state, a.max_pages, a.depth)
    output = Path(a.output); output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({k: len(report[k]) for k in ('pages', 'candidates', 'errors')}))
    # Partial results remain useful; make a completely unsuccessful run visible to CI.
    return 0 if report['pages'] else 1

if __name__ == '__main__': raise SystemExit(main())

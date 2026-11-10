import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError
from discover import canonical, crawl, Fetcher, Page

class FakeFetcher:
    html = '<title>Benefits</title><script>noise</script><a href="/new-assistance#apply">Free assistance</a><a href="https://other.example/free">Free</a>'
    def fetch(self, url): return url, self.html

class DiscoveryTests(unittest.TestCase):
    def test_normalization_and_hidden_text(self):
        self.assertEqual(canonical('https://www.phila.gov/free#apply'),'https://www.phila.gov/free')
        for url in ['http://www.phila.gov/','https://user:pass@www.phila.gov/','https://www.phila.gov/?q=x']:
            with self.assertRaises(ValueError): canonical(url)
        page=Page();page.feed(FakeFetcher.html)
        self.assertNotIn('noise',' '.join(page.text))
    def test_candidates_changes_and_failure_preserves_baseline(self):
        config={'allowed_hosts':['www.phila.gov'],'start_urls':['https://www.phila.gov/services/']}
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp)/'state.sqlite';fetch=FakeFetcher()
            first=crawl(config,'seed/catalog.sql',state,1,0,fetch)
            self.assertEqual(first['pages'][0]['status'],'first_seen')
            self.assertEqual(len(first['candidates']),1)
            self.assertEqual(first['candidates'][0]['funding_class'],'unverified')
            self.assertEqual(first['candidates'][0]['cost_tier'],'varies_or_unconfirmed')
            second=crawl(config,'seed/catalog.sql',state,1,0,fetch)
            self.assertEqual(second['pages'][0]['status'],'unchanged')
            fetch.html += '<p>Enrollment has closed.</p>'
            self.assertEqual(crawl(config,'seed/catalog.sql',state,1,0,fetch)['pages'][0]['status'],'changed')
            with patch.object(fetch,'fetch',side_effect=OSError('timeout')):
                failed=crawl(config,'seed/catalog.sql',state,1,0,fetch)
                self.assertEqual(len(failed['errors']),1)
            self.assertEqual(crawl(config,'seed/catalog.sql',state,1,0,fetch)['pages'][0]['status'],'unchanged')
    def test_robots_disallow_and_fail_closed(self):
        f=Fetcher(['www.phila.gov'],delay=0)
        with patch.object(f,'request',return_value=(b'User-agent: *\nDisallow: /private','text/plain','utf-8')):
            with self.assertRaisesRegex(ValueError,'disallows'):f.fetch('https://www.phila.gov/private')
        f=Fetcher(['www.phila.gov'],delay=0)
        with patch.object(f,'request',side_effect=HTTPError('url',503,'unavailable',{},None)):
            with self.assertRaisesRegex(ValueError,'unavailable'):f.fetch('https://www.phila.gov/free')
    def test_redirect_outside_allowlist_not_requested(self):
        f=Fetcher(['www.phila.gov'],delay=0)
        with patch.object(f,'policy',return_value=0),patch.object(f,'request',side_effect=HTTPError('url',302,'redirect',{'Location':'https://outside.example/free'},None)) as request:
            with self.assertRaisesRegex(ValueError,'host not approved'):f.fetch('https://www.phila.gov/free')
            self.assertEqual(request.call_count,1)
    def test_crawl_budget(self):
        with tempfile.TemporaryDirectory() as temp:
            report=crawl({'allowed_hosts':['www.phila.gov'],'start_urls':['https://www.phila.gov/']},'seed/catalog.sql',Path(temp)/'state.sqlite',2,2,FakeFetcher())
            self.assertEqual(report['attempted'],2)
            self.assertEqual(len(report['candidates']),1)

if __name__=='__main__':unittest.main()

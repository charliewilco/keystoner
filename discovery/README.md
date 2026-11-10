# Discovering new data

Run with Go 1.26+; no API key or Python runtime:

```sh
go run ./cmd/discover --max-pages 30 --depth 1
```

Open `work/discovery/report.json`. Run again with the same state file to detect changes. The separate `work/discovery/state.sqlite` stores URL, normalized-text hash, and last successful fetch time. It never writes the seed or published catalog, and fetched timestamps never replace human review dates.

The initial allowlist covers `www.phila.gov` and `www.pa.gov`. The crawler starts at configured service/program directories and checks existing catalog source URLs on those hosts, oldest checked first. With the default budget, repeated runs rotate through existing sources. Additional relevant links are followed when the budget permits. Increase `--max-pages` (up to 500) to reach discovery links; depth is limited to 0–2. Coverage is bounded, not exhaustive. Add provider hosts and directory URLs to `sources.json` after checking their terms and suitability. Do not add private/internal hosts; this configuration is trusted maintainer input.

The report contains:

- `pages`: successful fetches with source IDs where known, titles, hashes and `first_seen`, `changed` or `unchanged` status. A first fetch establishes a baseline, not proof that the source changed since the original research. Text includes page navigation, so site-wide edits may create false positives.
- `candidates`: deduplicated links whose titles or paths match broad service/benefit terms, excluding URLs already cited by the catalog. Each has its discovery page and fetch timestamp. A link is **not** a verified program or necessarily fetched target. Multiple URLs can refer to the same program; navigation and irrelevant pages can appear. All start as `unreviewed_link`, `varies_or_unconfirmed` cost and `unverified` funding.
- `errors`: blocked, unavailable, oversized or unsupported pages. Failures do not overwrite the last successful hash, mark a benefit closed, or remove anything from the catalog. A run with no successful pages exits nonzero; partial runs retain both results and errors.

Only normalized content hashes and short titles are saved, not full third-party page text. The crawler identifies itself, checks robots.txt, observes crawl delay and conservatively applies the slowest request-rate directive across all groups, and waits at least one second per host. Each request has a 20-second timeout and a 2 MB body limit. Redirect targets are rechecked against the host allowlist and robots rules. A missing robots.txt (404) permits fetching; other robots errors fail closed. It does not bypass login, CAPTCHA, blocks, or robots restrictions. JavaScript-only pages, PDFs, query URLs, feeds and non-HTML formats are not supported in this first version.

## Review and publish

1. Inspect changed sources and candidate links; check provider ownership, geography, whether this is genuinely a new pathway, and whether it duplicates an existing program/component.
2. Read the actual source and record eligibility, itemized costs/conditions, funding evidence, availability and the human review date. Do not infer free access from a keyword, public operation, or an old description.
3. Update `seed/catalog.sql` in a draft PR. Preserve IDs for existing pathways; allocate new permanent IDs only for new pathways. Add citations and qualifying language. New sources should explain any uncertainty or contradictory evidence.
4. Run tests and the catalog publisher with a new release ID as documented in the root README. Review the resulting snapshot before merging/deploying. Discovery never publishes changes automatically.

## GitHub Actions

After this workflow is merged into the default branch, open **Actions → Discover catalog sources → Run workflow**. Choose a page budget and download the `discovery-review` artifact when it finishes. Reports are retained for 30 days. A GitHub Actions cache preserves baseline hashes between runs when available; cache expiry causes fresh baselines, not spurious changes. Download reports you need to retain. Runs are serialized to avoid baseline races.

This workflow is manually triggered; no recurring schedule is enabled. It uses no secrets, commits no changes, opens no issues and does not publish a release. Network failures may leave a partial report; check its errors and remaining queue before interpreting coverage. For a durable baseline, run locally with a backed-up state file.

The Go HTML parser uses its own `pages_go_v1` baseline table inside the state database. Existing Python baseline data remains untouched; the first Go run establishes fresh baselines to avoid false change reports caused by parser differences. The API and scraper are Go; the separate catalog publisher still uses Python.

The reusable crawler lives in `tools/scraper`, separate from `cli` argument/file handling and `core` catalog access. The CLI reads catalog source IDs through the core and passes them into the scraper. Other callers can provide their own source map without importing the CLI or opening the catalog.

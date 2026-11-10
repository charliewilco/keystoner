# Keystoner

Find free things that meet your needs in Philadelphia. Keystoner starts with an open, healthcare-first catalog of benefits and services, with taxpayer funding available as a filter. Free events come next; neighbor giveaways and a marketplace are later ideas.

**Initial research release:** 108 benefit pathways, 117 sources, 52 categories, researched November 9, 2026. Pathways are not distinct programs or a count of benefits any person qualifies for. Sources were reviewed online; providers have not confirmed availability or individual eligibility. Some evidence comes from official search excerpts. This is an early public data/API project, not an eligibility determination service.

## Run

Requires Go 1.25+; no database service, account, or API key.

```sh
go run ./cmd/keystoner
curl 'http://localhost:8080/v1/resources?q=dental&cost_tier=free'
curl 'http://localhost:8080/v1/resources/ks-000001'
curl 'http://localhost:8080/v1/catalog/manifest'
```

`PORT` defaults to `8080`; `CATALOG_DIR` defaults to `catalog`. The process verifies snapshot integrity, schema, metadata, size, and SHA-256 before listening. It opens SQLite read-only. Deployments do not require a writable persistent disk.

## Public API

| GET endpoint | Purpose |
| --- | --- |
| `/healthz` | Readiness and release |
| `/v1/resources` | Search and filter pathways |
| `/v1/resources/{id}` | Complete pathway with structured citations |
| `/v1/categories` | Category IDs and pathway counts |
| `/v1/catalog/manifest` | Active release, schema, hash, size and download path |
| `/v1/catalog/releases/{release}/catalog.sqlite` | Immutable offline database |

List parameters: `q` (case-insensitive substring across name/provider/benefit/category), exact `category`, `geography`, `cost_tier`, `funding_class`; `limit` 1–100 (default 20), `offset` 0–100000 (default 0). Combined filters use AND. Unknown/repeated parameters and invalid bounds return 400. Unmatched filters return an empty list. Results sort by stable benefit ID. Geography values are source labels, not geocoded service boundaries. Search is basic substring search, not fuzzy or eligibility matching.

Every response identifies its catalog release in `X-Catalog-Release`; JSON data envelopes also identify it. Pagination across an active-release deployment can change results: compare release IDs and restart paging if they change. For a consistent full catalog, download the snapshot. Downloads support ETag/conditional GET and range requests. Public GETs allow cross-origin access. [OpenAPI](docs/openapi.yaml) describes the contract and is served at `/openapi.yaml`.

**Read cost details together.** `free` can mean conditional eligibility or a selected component. Other tiers are `nearly_free`, `sliding_scale`, and `varies_or_unconfirmed`. Funding filters distinguish taxes, government programs with unspecified funding, utility/telecom ratepayers, insurance, charities/providers, mixed funding, and unverified funding. `price_notes`, `cost_class`, `eligibility`, `caveats`, `availability`, and review dates remain in results. Residence does not establish citizenship or eligibility. Seasonal, closed, or invitation-only pathways remain visible with their restrictions; a match does not mean currently open enrollment.

## Discover new sources

Run `python3 scripts/discover.py --max-pages 30` to find candidate service links and flag changes to existing government sources. Results go to a separate review report; they never automatically become published benefits. [Scraper usage, coverage and review process](discovery/README.md). A manual GitHub Actions workflow can produce downloadable reports.

## Publish a catalog update

Edit `seed/catalog.sql`, preserving existing benefit IDs and citations. Never renumber or reuse an ID; public ID `ks-000001` maps permanently to benefit 1. Run:

```sh
python3 scripts/publish.py --release 2026-11-10.1 --published-at 2026-11-10T15:00:00Z
python3 -m unittest discover -s scripts -v
go test -race ./...
go vet ./...
```

Use the real publication timestamp and a new release ID. The publisher checks integrity, foreign keys, required details and sourcing, exports with `VACUUM INTO`, writes a standalone versioned snapshot and release manifest, then atomically replaces the latest manifest. It refuses to overwrite releases. Run one publisher at a time; publish through reviewed Git changes and a new deployment, not inside the running service. Retain all `catalog/releases/` directories in deployments so old download URLs keep working. The API pins the active release at startup. [Architecture and offline sync](docs/architecture.md).

## Host on Render

The repository includes a Dockerfile and `render.yaml` for a **free preview**, with `/healthz` as the health check.

1. Sign in to Render and connect this GitHub repository.
2. Choose **New → Blueprint**, select `charliewilco/keystoner`, and review the `keystoner` web service. Confirm the **Free** instance plan before creating it.
3. Apply the blueprint. Render builds the Docker image and supplies an HTTPS service URL.
4. Open `https://YOUR-SERVICE.onrender.com/healthz`, then `/v1/catalog/manifest` and `/v1/resources`. Verify the manifest download hash before connecting clients.

No secrets, database add-on, or persistent disk are needed. Alternatively use **New → Web Service**, Docker runtime, Free instance, and `/healthz`. Local container: `docker build -t keystoner .` then `docker run --rm -p 8080:8080 --read-only keystoner`.

Render free services sleep after 15 idle minutes and may take about a minute to wake. The workspace has a 750 free instance-hour monthly allowance plus bandwidth/build limits. Render advises against free production hosting. Use free hosting to evaluate this API; choose paid compute when reliable public access matters. File changes on running instances are ephemeral, which is why releases are bundled into the image. See [Render free-service limits](https://render.com/docs/free) and [Go hosting guide](https://render.com/docs/deploy-go-nethttp). **The project is not deployed by this repository.**

## Scope and contribution

No iOS app, accounts, marketplace, messaging, real-time availability feed, or automatic catalog publication is implemented. The future iOS sync contract is documented. Current data is preliminary and needs ongoing human verification. Before a production launch, set up monitoring and appropriate platform traffic controls, confirm key service listings with providers, and establish a correction/review cadence.

Open an issue for incorrect cost, eligibility, availability, or source information, including the resource ID, public source URL, and date checked. Do not post personal medical, income, immigration, or contact information. Use a draft pull request for catalog or code changes; describe evidence and tests. Never upgrade uncertain costs or funding to confirmed free/tax-funded claims without evidence.

Code is [MIT licensed](LICENSE). Original catalog summaries/compilation are offered under [CC BY 4.0](DATA-LICENSE.md); source websites retain their own rights. [Seed provenance](seed/README.md).

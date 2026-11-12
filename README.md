# Keystoner

Find free things that meet your needs in Philadelphia. Keystoner starts with an open, healthcare-first catalog of benefits and services, with taxpayer funding available as a filter. Free events come next; neighbor giveaways and a marketplace are later ideas.

**Initial research release:** 108 benefit pathways, 117 sources, 52 categories, researched November 9, 2026. Pathways are not distinct programs or a count of benefits any person qualifies for. Sources were reviewed online; providers have not confirmed availability or individual eligibility. Some evidence comes from official search excerpts. This is an early public data/API project, not an eligibility determination service.

## Project commands

Run `just` to list commands. Requires [just](https://just.systems/) plus the prerequisites for the chosen task (Go 1.26+, Python 3 for publishing, Apple `container` for local containers).

```sh
just run                         # native API
just build                       # API executable
just build-cli                   # separate scraper CLI
just discover --max-pages 30     # candidate review report
just check                       # tests and static checks
just up                # build and run with Apple container
just stop
```

`just container-start` starts Apple's service after initial installation/kernel setup. Use `just container-run` to reuse an existing image, or `just container-logs` to inspect it. `just publish RELEASE UTC_TIMESTAMP` runs the catalog publisher explicitly. Commands run from the repository root even when invoked in a subdirectory. Set `GO` to a Go executable path when it is not on `PATH`.

## Run

Requires Go 1.26+; no database service, account, or API key.

```sh
go run ./cmd/keystoner
curl 'http://localhost:8080/v1/resources?q=dental&cost_tier=free'
curl 'http://localhost:8080/v1/resources/ks-000001'
curl 'http://localhost:8080/v1/catalog/manifest'
```

`PORT` defaults to `8080`; `CATALOG_DIR` defaults to `catalog`. The process verifies snapshot integrity, schema, metadata, size, and SHA-256 before listening. It opens SQLite read-only. Deployments do not require a writable persistent disk.

## Run with Apple container on macOS

Install [Apple's container tool](https://github.com/apple/container) on an Apple silicon Mac running macOS 26 or later, then:

```sh
container system kernel set --recommended
container system start
./scripts/container.sh build
./scripts/container.sh run
curl http://127.0.0.1:8080/healthz
./scripts/container.sh logs
./scripts/container.sh stop
```

On a Mac without Rosetta, set `rosetta = false` in the `[build]` section of `~/.config/container/config.toml` before starting the service. Preserve any existing configuration. If the service is already running, restart it after the change. This project builds ARM64 images and does not need x86 translation. See [Apple's configuration reference](https://github.com/apple/container/blob/1.7.1/docs/container-system-config.md).

The build uses your local Go toolchain to cross-compile a static Linux ARM64 API executable. Apple `container build` packages it using `containers/Containerfile`. Only the executable and published catalogs enter the temporary build context. The image runs as a non-root user with a read-only filesystem and exposes port 8080 on loopback only. No Docker installation or daemon is needed; Apple's builder uses the standard container build-file syntax.

Set `GO` to a Go executable path if needed, `KEYSTONER_HOST_PORT` to change the local port, or `KEYSTONER_IMAGE` / `KEYSTONER_CONTAINER` to choose image/container names. The scraper CLI remains independently runnable with Go. `stop` removes only the named Keystoner container; it leaves Apple's shared service running. Fly.io receives a separately built Linux AMD64 image, and Linux CI verifies native builds; Apple container runtime checks require a compatible Mac.

## Components

`core/` owns catalog access and validation. `tools/scraper/` is a reusable crawler that accepts source URLs. `cli/` handles command arguments and report files; `cmd/discover/` is its executable. The API uses the core independently. They live in one repository with [explicit dependency boundaries](docs/architecture.md#component-boundaries).

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

Run `go run ./cmd/discover --max-pages 30` to find candidate service links and flag changes to existing government sources. Results go to a separate review report; they never automatically become published benefits. [Scraper usage, coverage and review process](discovery/README.md). A manual GitHub Actions workflow can produce downloadable reports.

## Publish a catalog update

Edit `seed/catalog.sql`, preserving existing benefit IDs and citations. Never renumber or reuse an ID; public ID `ks-000001` maps permanently to benefit 1. Run:

```sh
python3 scripts/publish.py --release 2026-11-10.1 --published-at 2026-11-10T15:00:00Z
python3 -m unittest discover -s scripts -v
go test -race ./...
go vet ./...
```

Use the real publication timestamp and a new release ID. The publisher checks integrity, foreign keys, required details and sourcing, exports with `VACUUM INTO`, writes a standalone versioned snapshot and release manifest, then atomically replaces the latest manifest. It refuses to overwrite releases. Run one publisher at a time; publish through reviewed Git changes and a new deployment, not inside the running service. Retain all `catalog/releases/` directories in deployments so old download URLs keep working. The API pins the active release at startup. [Architecture and offline sync](docs/architecture.md).

## Host on Fly.io

`fly.toml` configures HTTPS, port 8080, `/healthz` checks, a shared CPU with 256 MB memory, and automatic stop/start with zero minimum running Machines. The initial region is `iad`; change it if needed. The catalog ships inside the image, so no database service or persistent volume is needed.

Install [flyctl](https://fly.io/docs/flyctl/install/) and sign in with `fly auth login`. Choose a globally unique app name and create it in your intended organization with `fly apps create YOUR_APP --org YOUR_ORG`. App creation/deployment is an explicit hosting step; this repository has not deployed a service.

Build and publish with Apple's container tooling:

```sh
just fly-build YOUR_APP RELEASE_TAG
# Create a short-lived app deploy token and pass it directly to Apple's registry client:
fly tokens create deploy --app YOUR_APP --expiry 1h | container registry login --username x --password-stdin registry.fly.io
just fly-push YOUR_APP RELEASE_TAG
fly config validate --config fly.toml --app YOUR_APP --strict
just fly-deploy YOUR_APP RELEASE_TAG
```

Use a new image tag for each release. `fly-build` cross-compiles Linux AMD64 and packages it without executing AMD64 instructions, so no Rosetta or Docker daemon is needed. Local `just up` still uses ARM64. The deploy recipe uses the image already pushed to Fly's registry, with `--ha=false` to avoid provisioning a redundant second Machine on the initial deploy. It does not create an app, database or volume. Provision public addressing if the app has none: `fly ips allocate-v6 --app YOUR_APP` and `fly ips allocate-v4 --shared --app YOUR_APP`.

Verify `https://YOUR_APP.fly.dev/healthz`, `/v1/catalog/manifest` and its download checksum. Review [Fly's usage pricing](https://fly.io/docs/about/pricing/) before deploying; automatic stopping does not imply free hosting. Keep historical catalog snapshots in every image. See [Fly app configuration](https://fly.io/docs/reference/configuration/) and [prebuilt image deployment](https://fly.io/docs/blueprints/using-the-fly-docker-registry/).

## Scope and contribution

No iOS app, accounts, marketplace, messaging, real-time availability feed, or automatic catalog publication is implemented. The future iOS sync contract is documented. Current data is preliminary and needs ongoing human verification. Before a production launch, set up monitoring and appropriate platform traffic controls, confirm key service listings with providers, and establish a correction/review cadence.

Open an issue for incorrect cost, eligibility, availability, or source information, including the resource ID, public source URL, and date checked. Do not post personal medical, income, immigration, or contact information. Use a draft pull request for catalog or code changes; describe evidence and tests. Never upgrade uncertain costs or funding to confirmed free/tax-funded claims without evidence.

Code is [MIT licensed](LICENSE). Original catalog summaries/compilation are offered under [CC BY 4.0](DATA-LICENSE.md); source websites retain their own rights. [Seed provenance](seed/README.md).

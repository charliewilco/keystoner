# Catalog and offline sync contract

The API and SQLite download use the same immutable catalog. Schema version 1 is recorded in `PRAGMA user_version`, `release_metadata`, and the manifest. Release IDs identify content separately from schema. The current server accepts schema 1 only and fails closed on unsupported metadata, corrupt data, or hash mismatch. Legacy research `catalog_metadata.version=2.0` describes the imported research artifact, not the app schema.

The original benefits, details, sources, benefit_sources, locations, research_notes and metadata tables remain available. `resources` is a view adding stable text IDs to `expanded_catalog`. Do not infer structured relations between locations and benefits: the original seed has no such relation. Benefit IDs are permanent and never reused. Corrections preserve an ID. If a pathway ends, retain its record and explain closure in availability; do not delete it merely because enrollment ended.

The publisher imports SQL into a fresh private database, validates it, and uses `VACUUM INTO` to produce a standalone snapshot. This includes committed SQLite contents and avoids unsafe live-WAL file copying. The serving process never writes catalogs. Keep old release files in the deployment; historical downloads continue to work even though the latest API serves only its pinned current release. Before moving snapshots to object storage, migrate old URLs without losing their immutable bytes. Avoid concurrent publishing; a reviewed Git commit is the publication unit. A failed publisher may leave an unreferenced release directory, but never makes an incomplete file the active manifest.

## Future iOS client (not implemented)

1. Bundle a known-good catalog. Open it read-only and search locally so browsing works offline from first launch.
2. Store favorites and other user state separately in `user.sqlite`, keyed by stable resource ID. Never write user data into a downloaded catalog. An unavailable favorite keeps its ID and can show a saved label.
3. On foreground entry or manual refresh, fetch `/v1/catalog/manifest`. Background refresh is opportunistic, never required for correctness. Resolve its relative URL against the trusted API origin. If release is unchanged, stop.
4. Accept only explicitly supported `schema_version` values (initially exactly 1). An old app seeing a newer schema keeps its existing catalog and offers an app update. It must not attempt an unrecognized schema. Keep historical snapshots available for old installations.
5. Download to a temporary, versioned file. Enforce a client size ceiling (for example 50 MB), verify exact `byte_size` and SHA-256, open read-only, run SQLite integrity and FK checks, and compare `PRAGMA user_version` and `release_metadata` against the manifest. SHA-256 detects corrupt/mismatched bytes; HTTPS and the trusted origin establish transport trust.
6. Through a coordinated database owner, wait for active readers, close the old connection pool, atomically switch an app-owned active-file pointer, and open the new pool. Keep the previous valid file until the new one opens successfully; roll back on any failure. Never replace an open SQLite file underneath readers.
7. Show data review dates and uncertainty offline. Network failures must preserve the old usable catalog. Full snapshots are the initial protocol: there is no incremental or account sync.

Schema changes require a new schema_version, documented migration/read compatibility, and a compatible app release. Keep release/content IDs distinct from schema versions. The server startup verification prevents manifest/database mismatch. HTTP responses use short cache lifetimes; immutable snapshots use one-year caching and a hash ETag. Deployment is a process restart with a new pinned catalog, not an in-process manifest switch.

## Component boundaries

One repository and Go module contain independent packages; separate repositories or releases are not required to reuse them.

| Component | Responsibility | Depends on |
| --- | --- | --- |
| `core` | Open and verify immutable catalogs; read source IDs/URLs from trusted seed SQL | SQLite and standard library |
| `tools/scraper` | Fetch pages, parse links, track crawl baselines, return review reports | HTTP/HTML/robots libraries and its own SQLite state |
| `tools/publisher` | Validate trusted seed SQL, stage immutable releases, replace the active manifest | Core and SQLite |
| `cli` | Parse arguments, invoke discovery or publishing, write output | Core, scraper, and publisher |
| `internal/api` | HTTP routes, query parameters, JSON responses and downloads | Core and API docs |
| `cmd/discover`, `cmd/publish` | Process entry points and cancellation signals | CLI |
| `cmd/keystoner` | API process entry point and server lifecycle | API |

The scraper accepts a source URL-to-ID map: it does not load seed SQL or open public catalogs. It can run from another program without command-line parsing. The CLI joins catalog sources to scraping and owns report files; it accepts a context and output writers and never exits the process. The core imports neither the scraper nor CLI. The API binary likewise imports neither, and its native build compiles only its server dependency graph. They share a module lockfile, not runtime coupling. The Go catalog publisher (`tools/publisher`, invoked by `cmd/publish`) remains a separate release-preparation utility and is excluded from the API dependency graph.

Local macOS packaging uses Apple `container` and `containers/Containerfile`. A temporary context contains only the statically compiled Linux ARM64 API and immutable catalogs. The image has no shell or scraper and runs non-root with a read-only filesystem. Fly.io deployment uses a separate Linux AMD64 build of the same API and catalog, pushed with Apple container to Fly’s registry. Local containers remain ARM64.

# Release 2.3.0 pre-release runbook

> **RC4 note (2026-11-20).** Two source-scoped crawling options are part of the
> release candidate: `crawling_config.incognito` and
> `crawling_config.follow_redirects`. When running the scenarios below, add a
> source-level check for each: a source with `incognito: false` must still be
> isolated from a previous session, and a source with `follow_redirects: false`
> must refuse a redirected navigation instead of indexing the destination.
> See [`RELEASE_230_RC4_IMPLEMENTATION.md`](RELEASE_230_RC4_IMPLEMENTATION.md)
> for the exact contract and
> [`RELEASE_230_RC4_TEST_REPORT.md`](RELEASE_230_RC4_TEST_REPORT.md) for the
> executed evidence.
>
> The Docker image build no longer needs a temporary loopback proxy: the default
> module path completes given a realistic build timeout, and the documented
> `GOPROXY` build argument remains available for restricted networks. The
> Information Seed harness now honours `INFOSEED_GOPROXY` and supports
> `--fixture-only`.

> **RC3 note (2026-11-20).** The verified release candidate is
> `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`, which is hosted-CI green. The
> scenario identifiers and results that were actually executed for the release
> gate are recorded in [`RELEASE_230_RC3_TEST_REPORT.md`](RELEASE_230_RC3_TEST_REPORT.md);
> where this runbook's older S-numbering differs, the RC3 report is
> authoritative.
>
> Two practical corrections found during RC3:
>
> 1. The HTTP fixture used for refresh scenarios **must** send
>    `Cache-Control: no-store`. Without it the browser can re-serve a cached
>    body and a "refresh" becomes a no-op, which silently invalidates the
>    scenario.
> 2. A legacy (non-fenced) page-indexing failure can leave either
>    `status='error'` or `status='completed'` depending on where the error
>    surfaces, but `last_error` is populated in both cases. Assert on
>    `last_error` and on the stored page/object rows, not on `status` alone.

This is the repeatable sequence used by `WP-REL230-RC1` and intended to be
re-run before any tag. It assumes the 2.3.0 scope described in
`RELEASE_230_SCOPE_AND_GAP_MATRIX.md` has been accepted by the maintainer.

Nothing here authorises a tag, a push, a production migration or a change to
the default claim mode. It is a verification procedure.

## 0. Non-negotiable boundaries

- Do not run this against a shared, staging or production database.
- Do not touch an existing Compose project, volume or container. Use a new
  project name every time.
- Do not switch a production deployment to `fenced` as part of the runbook.
  The runbook switches mode only inside the disposable stack.
- `refresh_content` is page-scoped and orphan-only in both claim modes since
  WP-REL230-RC2. A refresh of a page linked to more than one source is refused
  by design; do not work around that refusal by changing the data model.

## 1. Prerequisites

| Requirement | Version used in this gate | Notes |
| --- | --- | --- |
| Go | 1.27.4 (`go.mod` requires 1.26.6) | `go version` |
| Docker Engine | 29.9.2-ce | rootless or rootful both fine |
| Docker Compose | 5.7.1 | `docker compose version` |
| PostgreSQL | 17.12 | disposable container |
| PostgreSQL client | 17.x for `pg_dump`/`pg_restore` | host client here was 18.6; use `scripts/select-postgres-client-major.sh 17` when the backup tools matter |
| CROWler VDI | `zfpsystems/crowler-vdi:latest` | pull, do not rebuild |
| Free host ports | `55432`, `18080`, `18082`, `14444-14445` | or edit the override |
| Optional | Ollama endpoint + tool-capable model | live-model chain is otherwise unverified |

## 2. Establish the baseline

```bash
git status --porcelain          # record; must be clean for a release candidate
git rev-parse HEAD
git branch --show-current
git log -5 --oneline
cat RELEASE_VERSION.txt
grep -E 'RequiredSchemaVersion|WriterGeneration' pkg/database/startup_compatibility.go
```

Expected: the release candidate commit, `v2.3.0`, `1.17`,
`verified-ownership-v1.17`.

Record the defaults that define the shipped surface:

```bash
grep -n 'refresh_content' config.default
grep -rn 'legacy' pkg/database/source_crawl_attempt.go | head
grep -n 'Mode' pkg/database/source_crawl_attempt_schema.go | head
```

Expected: `refresh_content: false`, default claim policy row seeded as `legacy`.

## 3. Static and unit gates

```bash
go test ./... -count=1 -timeout 90m -p 1
go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1
go vet ./...
go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
gofmt -l pkg cmd services
git diff --check
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/*.yml
```

Any failure is a stop. Do not proceed to the Docker stage with a red unit gate.

## 4. Disposable PostgreSQL 17

```bash
docker run -d --name rel230-pg \
  -e POSTGRES_DB=SitesIndex -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD='<disposable-password>' \
  -p 127.0.0.1:55917:5432 postgres:17
```

Fresh install as the runtime role:

```bash
export POSTGRES_DB=SitesIndex POSTGRES_USER=postgres POSTGRES_PASSWORD='<disposable-password>'
export CROWLER_DB_USER=crowler CROWLER_DB_PASSWORD=crowler
export CROWLER_DB_EXTRA_ARGS="--host 127.0.0.1 --port 55917"
bash ./pkg/database/postgresql-setup.sh
psql -h 127.0.0.1 -p 55917 -U crowler -d SitesIndex -At -c \
  "select version from DBSchemaVersion where is_current;
   select writer_generation from DatabaseWriterCompatibility where lock_id=1;
   select capability from SchemaCapabilities order by 1;"
```

Expected: `1.17`, `verified-ownership-v1.17`,
`mcp-mutation-authorization-v1` and `source-crawl-attempt-v1`.

Migration rehearsal (disposable only):

```bash
# 1. create a disposable 1.16 database, load the v1.16 setup, insert a fixture row
# 2. apply pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
# 3. verify version 1.17, writer generation, and that the fixture row survived
# 4. re-apply the same migration and verify it is idempotent
# 5. apply pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql twice
#    and verify the goal tables and rows are preserved
# 6. take a custom-format backup with pg_dump and verify it with pg_restore --list
# 7. restore into a fresh database and compare row counts for the fixture tables
```

Then the integration suites:

```bash
export THECROWLER_POSTGRES_INTEGRATION=1
export DOCKER_POSTGRES_DB_HOST=127.0.0.1 DOCKER_POSTGRES_DB_PORT=55917
export DOCKER_POSTGRES_DB_NAME=SitesIndex DOCKER_POSTGRES_DB_USER=crowler
export DOCKER_POSTGRES_PASSWORD=crowler DOCKER_POSTGRES_SSL_MODE=disable

go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawl(Attempt|Result|B2R)' -count=1 -v
go test -tags=integration ./pkg/agent -run '^TestPostgresGoalAsync' -count=1 -v
go test -tags=integration ./pkg/agent -run '^TestPostgresFullProposalApprovalSchedulerClaimAndObservation$' -count=1 -v
go test -tags=integration ./pkg/crawler -run '^TestPostgresFencedRefresh' -count=1 -v
go test -tags=integration ./pkg/crawler -run '^TestPostgresSharedObjectTrigger' -count=1 -v
go test -tags=integration ./pkg/crawler -run '^TestPostgresLegacyRefresh' -count=1 -v
go test -tags=integration ./pkg/crawler -run '^TestPostgresRefreshAndVacuumRemainConsistent$' -count=1 -v
go test -tags=integration ./pkg/crawler -run '^TestPostgresKeyword' -count=1 -v
go test -tags=integration ./services/events -run '^TestPostgresEventJanitor' -count=1 -v
go test -p 1 -tags=integration ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... ./services/api/... -count=1 -timeout 40m
```

Expected: every package passes. The shared-object and legacy refresh tests are
now **positive regressions**: they assert that a concurrent reference and a
shared object survive. If they fail, a regression has been introduced; do not
weaken them.

## 5. Disposable Docker stack with a real browser

Use a fresh project name. The override in this gate is
`docker-compose.rel230.yml` (not shipped in the repository; recreate it):

```bash
./scripts/generate-docker-compose.sh -e=1 -v=1 --postgres=yes --prometheus=no --no_jaeger
# create a disposable .env and config.yaml (see section 5.1)
docker compose -p rel230rc1 -f docker-compose.yml -f docker-compose.rel230.yml config --quiet
docker compose -p rel230rc1 -f docker-compose.yml -f docker-compose.rel230.yml build crowler-api crowler-events crowler-engine-1
docker compose -p rel230rc1 -f docker-compose.yml -f docker-compose.rel230.yml up -d
```

The override must:

- rename every container to `rel230-*`;
- retag images to `rel230-*:gate` so a maintainer's local `crowler-*` images are
  never overwritten;
- bind host ports on `127.0.0.1` with non-default numbers;
- use `postgres:17`;
- set `SE_ENABLE_TRACING=false` (the generated file enables tracing but the
  generator is invoked with `--no_jaeger`, so the exporter endpoint does not
  resolve);
- attach the fixture container to both `crowler-net` and `crowler-vdi-1`, so
  the engine and the browser can both reach it.

### 5.1 Disposable configuration

`config.yaml` is gitignored. Start from `config.default` and change only:

```text
database.host              crowler-db
crawler.workers            2
crawler.indexing_concurrency 1
crawler.max_depth          1
crawler.max_links          20
crawler.prevent_duplicate_urls false
crawler.crawling_interval  30 days
crawler.crawling_if_ok     30 days
crawler.crawling_if_error  5 minutes
crawler.create_event_when_done true
crawler.refresh_content    false   # flip per scenario in the database, not here
vdi[0].host                crowler-vdi-1
api.enable_console         true
events.automatic_events_removal never
```

`.env` must use disposable credentials and the standard variable names
(`DOCKER_DB_HOST`, `DOCKER_POSTGRES_PASSWORD`, `DOCKER_CROWLER_DB_USER`,
`DOCKER_CROWLER_DB_PASSWORD`, `DOCKER_SELENIUM_HOST`,
`DOCKER_SELENIUM_IMAGE`, `CROWLER_DB_*`, `POSTGRES_DB_HOST`).

### 5.2 Readiness before any scenario

```bash
curl -fsS http://127.0.0.1:18080/v1/health
curl -fsS http://127.0.0.1:18080/v1/ready
curl -fsS http://127.0.0.1:18082/v1/health
curl -fsS http://127.0.0.1:14444/status
docker compose -p rel230rc1 exec -T crowler-db psql -U postgres -d SitesIndex -At -c \
  "select version from DBSchemaVersion where is_current;
   select writer_generation from DatabaseWriterCompatibility where lock_id=1;
   select mode from SourceCrawlClaimPolicy where policy_id=1;"
docker compose -p rel230rc1 exec -T crowler-engine-1 ./healthCheck -service crowler
```

Expected: all healthy, `1.17`, `verified-ownership-v1.17`, mode `legacy`.

### 5.3 Scenarios

Run these in order, asserting database state rather than logs.

| ID | Scenario | Assertion |
| --- | --- | --- |
| S-01 | Legacy crawl of `http://rel230-fixture:8000/index.html` with `refresh_content` off | source `completed`; `SourceSearchIndex` link exists; `WebObjects` row exists; `crawl_completed` event exists when the flag is on; no `SourceCrawlClaims` row |
| S-02 | Legacy crawl of `shared.html`, then index the same content from a second page, then `refresh_content` on the first page | the second page keeps its link, object and attributes; only the refreshed page's link is replaced |
| S-03 | Switch mode to `fenced`, crawl the fixture | claim row + attempt row exist; attempt `completed`; source status per the unbound rule; single page object |
| S-04 | Fenced crawl of the same page after changing the fixture body, `refresh_content` on | one link, new hash, old object removed |
| S-05 | Fenced refresh with an injected `BEFORE DELETE ON WebObjects` failure | attempt `failed`; old link, object and content intact |
| S-06 | Fenced refresh of a page linked to a second source | attempt `failed`; page content and both links intact |
| S-07 | Fenced crawl of a `null:` source | no deletion; terminal failure recorded |
| S-08 | Legacy crawl of a `null:` source | source row deleted; no browser navigation |
| S-09 | Disable the source mid-crawl | attempt not `completed`; fence log line; no partial page |
| S-10 | Restart the engine during a fenced crawl | old attempt never `completed`; lease expiry reclaims with a new generation |

### 5.4 Negative guarantees

```bash
# No asynchronous resume: there is no worker to observe, and no run should
# transition by itself after the engine is idle.
docker compose -p rel230rc1 logs --since 5m crowler-engine-1 | grep -i resume || true

# No MCP mutation dispatch.
docker compose -p rel230rc1 exec -T crowler-db psql -U postgres -d SitesIndex -At -c \
  "select count(*) from MCPMutationDispatchAttempts;"
# must be 0
```

## 6. Shutdown

```bash
docker compose -p rel230rc1 -f docker-compose.yml -f docker-compose.rel230.yml down
docker compose -p rel230rc1 -f docker-compose.yml -f docker-compose.rel230.yml rm -f -s -v
docker rm -f rel230-pg
```

Never use `docker compose down -v` against a project you did not create, and
never prune globally.

## 7. Rollback and recovery of the release candidate

Nothing in this runbook changes a persistent database, so the rollback for the
gate itself is removal of the disposable resources above.

If a future deployment does apply the schema-1.17 migration:

1. stop every writer;
2. restore the verified backup taken before the migration;
3. confirm `DBSchemaVersion` and `DatabaseWriterCompatibility` match the
   restored generation;
4. start only writers compatible with the restored generation.

`dbDeploy -disable` removes only the MCP capability marker and preserves
history. It is not a substitute for restoring base-schema changes.

Rolling back fenced mode is `set_source_crawl_claim_mode('legacy')` after
draining fenced workers. Reverting the 2.3.0 code does not require a schema
change.

## 8. Exit criteria

The gate is green only when:

- every static/unit command in section 3 exits 0;
- every PostgreSQL suite in section 4 exits 0 on a fresh database;
- the migration rehearsal completes and the restored database matches;
- the Docker stack reaches readiness with the published VDI image;
- scenarios S-01, S-03, S-04, S-05, S-06, S-07, S-08, S-09 and S-10 pass;
- S-02 shows the shared object and the other page's link preserved (RC2-B);
- `MCPMutationDispatchAttempts` is 0 and no resume activity exists;
- the hosted CI state for the exact candidate commit is recorded, or explicitly
  marked unverified.

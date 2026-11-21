# Release 2.3.0 RC3 CI and release gate

Frozen commit: `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`
Branch: `develop` (= `origin/develop`)
Date: 2026-11-20
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

## 1. Hosted CI on the exact commit — VERIFIED

Check runs queried directly from the GitHub API for the frozen SHA:
`GET /repos/pzaino/thecrowler/commits/ef28d767.../check-runs` → `total_count: 8`,
every conclusion `success`.

| Check | Conclusion | URL |
| --- | --- | --- |
| `build` | success | https://github.com/pzaino/thecrowler/actions/runs/44671315041/job/136072703476 |
| `postgres-integration` | success | https://github.com/pzaino/thecrowler/actions/runs/44671315109/job/136072703779 |
| `SQL parser CGO=0` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703766 |
| `SQL parser CGO=1` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703875 |
| `Static binaries linux/amd64` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703752 |
| `Static binaries linux/arm64` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703910 |
| `Static binaries darwin/amd64` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703432 |
| `Static binaries darwin/arm64` | success | https://github.com/pzaino/thecrowler/actions/runs/44671314949/job/136072703701 |

Run window: 2026-11-20T03:59:00Z → 04:04:14Z.

### `postgres-integration` step inventory (all 18 steps success)

Retrieved from `GET /repos/pzaino/thecrowler/actions/jobs/136072703779`:

| # | Step | Result |
| --- | --- | --- |
| 5 | Install and select PostgreSQL 17 client | success |
| 6 | Create current schema | success |
| 7 | Verify CROWler runtime database role | success |
| 8 | Verify repeatable v1.17 migration | success |
| 9 | Verify v1.17 crawl-approval migration | success |
| 10 | Run fenced source-crawl attempt PostgreSQL integration | success |
| 11 | Run MCP mutation PostgreSQL integration | success |
| 12 | Run asynchronous goal store, observation and eligibility PostgreSQL integration | success |
| 13 | Run fenced crawler handoff PostgreSQL integration | success |
| 14 | Run fenced refresh PostgreSQL integration | success |
| **15** | **Run shared-object integrity PostgreSQL regressions** | **success** |
| **16** | **Run event retention PostgreSQL integration** | **success** |
| 17 | Run keyword suppression window PostgreSQL integration | success |
| 18 | Run PostgreSQL integration suite | success |

Steps 15 and 16 are the RC2 hardening gates. Both executed and passed on this
commit, so the two new integrity regressions and the event-retention assertion
are proven in hosted CI, not only locally.

This is the strongest available evidence class: hosted, exact-SHA, on the frozen
commit.

## 2. Local gates on the same tree

Executed in an isolated worktree at `ef28d767`; see
`RELEASE_230_RC3_TEST_REPORT.md` for commands, exit codes and log paths:

- full unit suite, race suite, `go vet` (both tag sets), `CGO_ENABLED=0/1`
  builds, `gofmt`, `git diff --check`, pinned `actionlint`;
- 14 agent manifests × (`lint` + `validate --strict`);
- disposable PostgreSQL 17: fresh install, `1.16 → 1.17` migration twice from a
  simulated pre-RC2 function, function-body equality, addons, `pg_dump`/
  `pg_restore` comparison;
- full tagged integration suite for `pkg/database`, `pkg/crawler`, `pkg/agent`,
  `services/api`, `services/events`;
- authentic shipping images from the repository Dockerfiles, then a real
  PostgreSQL + VDI acceptance run.

## 3. What is still not covered by any CI

| Gate | Status | Why |
| --- | --- | --- |
| `MCP authorization E2E (opt-in)` | **NOT RUN** | `workflow_dispatch`-only; the workflow is absent from the protected default branch `main`, so GitHub does not register it (see `E2E_DEFAULT_BRANCH_REGISTRATION.md`) |
| Live Ollama inference / tool calling | **NOT RUN** | No endpoint or tool-capable model configured |
| Live external MCP server | **NOT RUN** | Only local fixtures available |
| MySQL runtime | **NOT RUN** | No server; MySQL is legacy-only by contract |
| Information Seed Compose acceptance | **NOT RUN** | Needs its own image builds; blocked by the reproducible Docker module-download stall (§4) |
| Docker image build in CI | **Not part of the push checks** | The eight checks that run on a `develop` push are `go test`/build/static checks. The image-building workflows (`110_build_images.yml`, `111_build_vdi_images.yml`) are `workflow_dispatch`-only and did not run for this commit, so the shipping-image build is validated locally in this package |

None of these is a defect. They are named evidence gaps and are put to the
maintainer in `RELEASE_230_RC3_SIGNOFF_REQUEST.md` (§3).

## 4. Shipping image build environment finding

> **Corrected by RC4 (2026-11-20).** RC3 recorded that a plain `docker build`
> "stalls" during Go module download. That was a false negative: the same
> Dockerfile with the default module path and no `GOPROXY` build argument
> completed successfully in 248 s when given a realistic timeout. The build log
> is silent during compilation, which was mistaken for a hang. The `GOPROXY`
> build argument stays available and documented for restricted networks, and
> module integrity is unchanged (`GOSUMDB` untouched, `go.sum` unmodified).

Positive signal for reproducibility: the images produced with the proxy
workaround were byte-identical to RC2's for the same source, so the build is
deterministic for the same source and builder digests.

## 5. Release artifact integrity

| Item | Value |
| --- | --- |
| `RELEASE_VERSION.txt` | `v2.3.0` |
| `RequiredSchemaVersion` | `1.17` |
| `WriterGeneration` | `verified-ownership-v1.17` |
| Default claim policy | `legacy` |
| Default `refresh_content` | `false` |
| Migration files for this release | `postgresql-migration-v1.17.pgsql`, `sqlite-migration-v1.17.sqlite3`, `mysql-migration-v1.17.mysql` + the additive capability scripts |
| New schema version in this release | none |

The version string is consistent across `RELEASE_VERSION.txt`, the documentation
set and the Compose generation scripts. The running acceptance stack reported
schema `1.17` and writer generation `verified-ownership-v1.17` from the database
itself. No temporary test file, credential or generated `config.yaml` belongs in
the release artifact; `docker-compose.yml` and `.env` are local, git-ignored
deployment inputs.

## 6. Decision

**GO FOR PRE-RELEASE TESTING** on `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`.

Rationale:

- the frozen SHA is hosted-CI green across all eight checks, including the two
  new shared-object integrity and event-retention gates;
- no P0/P1 defect is open;
- the RC2 data-integrity fixes are green in hosted and local PostgreSQL runs and
  in a real browser acceptance run on the shipping images.

**Not granted: GO TO TAG / PUBLISH.** That still requires the maintainer's
recorded scope acceptance and deferral acknowledgement
(`RELEASE_230_RC3_SIGNOFF_REQUEST.md`), plus a decision on the two P2 items and
on the named evidence gaps. RC3 adds documentation only; if the RC3 documents
are to be part of the tagged tree, hosted CI must be re-observed on the
resulting commit.

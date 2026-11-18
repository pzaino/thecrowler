# WP-05B3C3B2F1 test report

## 1. Status and scope

**IMPLEMENTATION NOT APPROVED. FENCED REFRESH NOT ENABLED. LIVE REF TESTS NOT
RUN.**

Decision 2 is explicitly recorded as **not approved** in
[`ASYNC_GOAL_MAINTAINER_DECISIONS.md`](ASYNC_GOAL_MAINTAINER_DECISIONS.md)
(Decision 1k: "`refresh_content` atomic replacement ... require separate
approval"). No later attributable decision exists in the tracked tree. The
work-package request is not approval, so this package contains:

- read-only baseline verification and reconciliation;
- a source-level refresh design review
  ([`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md));
- a corrected B3 ownership-continuity proposal;
- no runtime source change, no DDL, no test that enables fenced refresh and no
  destructive live test.

## 2. Environment and baseline

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Observed HEAD | `6a682ea6091fdb0a43b76e57616a2bc8a2c2edaa` |
| `origin/develop` | `6a682ea6091fdb0a43b76e57616a2bc8a2c2edaa` |
| Working tree at start | clean |
| Go | `go1.27.3 linux/amd64` |
| Host | Linux, Docker 29.7.2-ce, Compose 5.6.1 |
| Disposable PostgreSQL | `postgres:17` → PostgreSQL 17.12, host port 55505 |
| Runtime role | `crowler`; administrative role `postgres` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Default claim mode | `legacy` |

Hosted CI on the observed HEAD
(`6a682ea6091fdb0a43b76e57616a2bc8a2c2edaa`): **8/8 successful**, including
`postgres-integration`, `build`, both SQL-parser jobs and all static-binary
jobs. This is the actual current HEAD, not the older B2R commit.

### 2.1 B2V report reconciliation

| Reported discrepancy | Resolution |
| --- | --- |
| B2V test report prose says "four" defects while its table lists five | Documentation typo only; the five defects are B2V-01..B2V-05 and all five fixes are present at HEAD. The prose is corrected. |
| B2V review says "35 source-crawl tests"; test report says "36 PASS" | Both numbers refer to different moments. The current regex matches **36 top-level tests** (22 attempt/result claim tests, 8 result tests, 6 B2R/B2V tests). The 36 includes the B2V-added `TestPostgresSourceCrawlB2RClaimBindsProcessingOperation`. Exact rerun below produced 36 PASS, 0 FAIL, 0 SKIP. |
| B2V patch had not been pushed when the report was written | The B2V change set is now commit `6a682ea6` on `develop`, and hosted CI on that commit is 8/8 green. Recorded as post-publication status; the B2V report body is otherwise unchanged. |

## 3. Commands actually executed

All commands ran in `/home/pzaino/workspace/github/thecrowler` on the clean
observed HEAD. `TMPDIR=/dev/shm/opencode` was used for the Go suite.

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | **PASS** | `b2f1-full.log` |
| `go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawl(Attempt\|Result\|B2R)' -count=1 -v` | **36 PASS, 0 FAIL, 0 SKIP** | `b2f1-pg2.log` |
| `go test -tags=integration ./pkg/agent -run '^TestPostgresFullProposalApprovalSchedulerClaimAndObservation$' -count=1` | **PASS** | `b2f1-handoff.log` |
| `go test -race ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1` | **PASS**, no data race | `b2f1-race.log` |
| `go vet ./...` | **PASS** | `b2f1-vet.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | **PASS** | `b2f1-veti.log` |
| `CGO_ENABLED=0 go build ./...` | **PASS** | local |
| `CGO_ENABLED=1 go build ./...` | **PASS** | local |
| `gofmt -l pkg cmd services` | **PASS** (no output) | `b2f1-gofmt.log` |
| `git diff --check` | **PASS** | local |
| `actionlint .github/workflows/*.yml` | **PASS** | `b2f1-actionlint.log` |

No source file was modified, so these results confirm that the B2V fixes and
the rest of the tree are unchanged by this package.

### 3.1 Environment caveat observed during the first PostgreSQL attempt

The first PostgreSQL run reported 7 failures, all with the message
`source crawl claim API is disabled in legacy mode`. Cause: a previously
timed-out `go test` process was still running against the same disposable
database, and `SourceCrawlClaimPolicy` is a singleton global row. Two test
binaries with different tests concurrently flipping `legacy`/`fenced` make
unrelated tests fail. This is the documented "run claim/handoff suites
sequentially against one database" constraint, not a product regression. After
terminating the stray process and resetting the mode, the clean rerun produced
36 PASS, 0 FAIL, 0 SKIP. The failing run is retained as `b2f1-pg.log`; the
authoritative result is `b2f1-pg2.log`.

## 4. Mandatory refresh design verification (read-only)

The full analysis and proposal are in
[`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md).
This section records the executed evidence.

### 4.1 Disposable PostgreSQL schema load

```text
docker run -d --rm --name b2f1-pg -e POSTGRES_DB=SitesIndex \
  -e POSTGRES_PASSWORD=postgres -p 55505:5432 postgres:17
psql -f pkg/database/postgresql-setup.pgsql ...
```

Result: setup exit 0, database server 17.12.

### 4.2 Shared-object and shared-page experiment

Setup:

1. sources `https://a.example/` and `https://b.example/`;
2. one page row `https://b.example/` linked to **both** sources;
3. an unrelated page `https://other.example/dup`;
4. one `WebObjects` row (hash `aaaa…`) linked to both pages, with one
   `ObjectAttributes` row;
5. the exact `deleteWebObjects(index_id)` SQL executed for the shared page.

Observed before the delete:

```text
 source_id |        url         | index_id | object_id
-----------+--------------------+----------+-----------
         3 | https://a.example/ |        1 |         1
         4 | https://b.example/ |        1 |         1

 index_id | linked_sources
----------+----------------
        1 |              2
```

Observed after:

```text
 other_page | other_page_links
------------+------------------
          2 |                0

 shared_object_rows
--------------------
                  0

 shared_object_attributes
--------------------------
                        0
```

Conclusion: the current refresh delete destroys an unrelated page's link,
object and attributes, and a single page row can be linked to more than one
source. A source-scoped fenced attempt therefore cannot authorize the current
delete shape. The proposed fenced design must use a page row lock, a
cross-source page refusal, page-scoped link deletion and orphan-only object
deletion. These are design requirements, not implemented behavior.

## 5. Docker build and live REF tests

**NOT RUN — APPROVAL PENDING.**

The mandatory live gate (REF-01 through REF-10) is conditional on Decision 2
approval. Because Decision 2 is not approved, no Docker image was rebuilt for
this package, no isolated Compose project was started, no fenced mode was
enabled and no destructive refresh scenario was executed. Reporting a live
PASS here would be fabricated.

Previously established (B2V, commit `f0f4fc97`) and unchanged: the isolated
`b2v-e2e` stack build, startup and LIVE-01..LIVE-12 results, including the
B2V-04 `processing` binding and B2V-05 indexing-failure fixes. That evidence
does not exercise atomic refresh and is not a substitute for REF-01..REF-10.

## 6. Skipped and not-run items

| Item | Status | Reason |
| --- | --- | --- |
| REF-01..REF-10 live Docker/Selenium | **NOT RUN** | Decision 2 not approved |
| Fenced refresh implementation and tests | **NOT RUN** | Decision 2 not approved |
| `claim_owner_epoch` DDL and tests | **NOT RUN** | Decision 1 not approved; proposal only |
| `null:` exact-fence deletion | **NOT RUN** | Decision 3 not approved |
| MySQL fenced mode | **NOT RUN** | Legacy-only by contract |
| Shared-database or production migration | **NOT RUN** | Prohibited |
| Hosted CI for a new patch | **N/A** | No source change in this package |

Skipped tests: none in the executed Go suite (the full suite and PostgreSQL
runs report 0 skips for the selected tests).

## 7. Compatibility

- Release `v2.3.0` unchanged.
- Schema `1.17` and writer generation `verified-ownership-v1.17` unchanged.
- Default claim mode `legacy` unchanged.
- Fenced `refresh_content` remains refused.
- Fenced `null:` remains quarantined.
- Legacy refresh code path unchanged.
- No public API, event, configuration, manifest or MCP contract changed.

## 8. Evidence classification

Executed and observed: sections 3 and 4.1–4.2.

Not executed: every implementation and live-refresh item in sections 5 and 6.

The fenced refresh path is **not production-ready** and remains disabled.
Decision 2 requires an explicit maintainer approval before any implementation.

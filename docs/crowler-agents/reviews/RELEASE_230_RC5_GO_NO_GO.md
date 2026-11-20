# Release 2.3.0 RC5 — go / no-go

Work package: `WP-REL230-RC5`
Baseline: `906000f3871c988e24a65ee32e1ec452491357d6`
Decision owner: the repository maintainer
Prepared: 2026-11-20

**This document does not tag, merge, publish, deploy or approve anything.** It
separates the engineering recommendation from the maintainer's authorization.

## 1. Decision

# **GO FOR PRE-RELEASE TESTING**

# **TAG / PUBLISH: BLOCKED**

The engineering work is complete and green: the shared-artifact concern is
measured, the correction is proven safe and faster on the dominant path, the
migration converges, and the release scenarios were re-run on shipping images.
The tag is blocked by a single external fact: **hosted CI has not run on the RC5
commit**, because the patch has not been pushed.

## 2. What RC5 delivers

| Item | Outcome |
| --- | --- |
| Contention concern | Measured, not guessed. Baseline `FOR UPDATE` serialisation is real but concentrated in multi-link cleanup; normal keyword/metatag crawling never deletes links, so it is not serialised by the trigger at all |
| Correction | `handle_shared_entity_deletion()` is now `AFTER DELETE FOR EACH STATEMENT` with an `OLD TABLE` transition relation: one `FOR UPDATE` + re-check per distinct parent, ascending parent-id order |
| Integrity | RC2 guarantee preserved and re-proved per family (5 families × 6 properties) on the real schema |
| Two unsafe "simplifications" | Characterised and pinned so they cannot be reintroduced: the pre-RC2 body, and the single-statement `DELETE … AND NOT EXISTS` form |
| Throughput | 2.77× faster vacuum-shaped cleanup, 4.75× faster concurrent vacua, ~25% slower on the opt-in single-link refresh path |
| Schema | `1.17` unchanged, writer generation unchanged, no table/column/index/grant change |
| Migration | Fresh and migrated function bodies byte-identical (`5bdd88ef…`); five statement-level triggers; `pg_dump`/`pg_restore` converged |
| Unsafe rollback | The pre-RC2 / v2.2.8 body is documented as **not** a rollback; it was neither created nor applied |

## 3. Why the refresh regression is acceptable

The only measured regression is on statements that delete exactly one link, which
is the shape of the opt-in `refresh_content` path:

- `refresh_content` defaults to `false`, so default deployments do not execute it;
- that path already serialises page indexing through the
  `crowler:fenced-content-refresh` advisory lock, so bounded per-statement
  overhead does not change its concurrency class;
- the same change removes 2.8–4.8× of the cost from the paths that run on every
  source removal or update.

If the maintainer prefers zero regression on refresh over the cleanup gain, the
previous function is a safe rollback (row-level, RC2-protected) and the
measurement table quantifies exactly what that choice costs.

## 4. Blockers

| ID | Severity | Blocker | Required action |
| --- | --- | --- | --- |
| B1 | Release gate | Hosted CI has not run on the RC5 SHA | Push the patch and confirm `build` and `postgres-integration` green on the resulting commit; or record an explicit waiver |
| B2 | Deployment | A deployed 1.17 database needs the repeatable migration re-applied to converge the triggers | Follow `RELEASE_230_RC5_DEPLOYMENT_MONITORING.md` §3 in a bounded-lock window |

No P0 or P1 data-integrity defect is open.

## 5. Known limitations (unchanged or newly recorded)

| Item | Severity | Disposition |
| --- | --- | --- |
| Browser error documents can be indexed for a navigation Chromium cannot complete | P2, pre-existing | Not changed. No stable machine-readable signal through the WebDriver results the crawler consumes; a locale-dependent DOM heuristic was rejected. Documented in the RC4F record |
| `follow_redirects: false` cannot detect a redirect loop | P3, by design | Documented |
| `refresh_content: true` serialises page indexing | P2-1 | Unchanged; RC5 adds the single-link trigger overhead on that path |
| Legacy page-indexing failure can leave `completed` with `last_error` | P2-2 | Unchanged, accept/document recommended |
| SQLite Events janitor `now()`, cosmetic startup warnings, finite event TTL | P3 | Unchanged, no 2.3.0 regression |
| Application-level deadlocks between separate multi-parent statements | P3 | The trigger orders the work it sees; ordering across statements remains the caller's responsibility |
| `lock_wait_ratio` is a sampled proxy | Evidence limitation | Recorded in the test report |

## 6. Evidence gaps the maintainer must accept or close

| Gap | Status | Release effect |
| --- | --- | --- |
| Hosted CI on the RC5 commit | **NOT RUN** | Must be green before tagging, or explicitly waived |
| Live Ollama / live external MCP | **NOT RUN** | No endpoint/model; hermetic coverage passes |
| MySQL runtime | **NOT RUN** | Legacy-only by contract |
| Hosted `mcp-authorization-e2e` | **BLOCKED** | Dispatch-only workflow, absent from the protected default branch |
| Production telemetry / `pg_stat_statements` | **NOT RUN** | No production access, none assumed |
| Production/shared database migration | **NOT RUN** | Forbidden by the work package |

## 7. Maintainer sign-off items

1. Accept the statement-level trigger shape and the measured trade-off
   (2.8–4.8× faster cleanup, ~25% slower opt-in refresh).
2. Accept the two characterized unsafe shapes as permanent negative tests.
3. Push RC5 and confirm `build` and `postgres-integration` green on the exact
   SHA, or record an explicit waiver.
4. Accept or close the evidence gaps in §6.
5. Schedule the migration application window and the monitoring baseline
   described in `RELEASE_230_RC5_DEPLOYMENT_MONITORING.md`.
6. Confirm the release scope and deferrals: no asynchronous goal resumption, no
   MCP mutation dispatch, no ownership-epoch DDL, no fenced `null:` deletion,
   production `fenced` mode still operator-gated.

## 8. Engineer recommendation vs authorization

The engineering recommendation is **GO FOR TAG once hosted CI is green on the
RC5 commit**, with the 1.17 migration applied in a bounded-lock window and the
monitoring baseline captured. This recommendation is not an authorization: only
the maintainer can accept the trade-off, the evidence gaps and the deployment
window.

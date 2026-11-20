# Release 2.3.0 RC2 go / no-go

Work package: `WP-REL230-RC2`
Baseline: `e6a41d317835356b1ed7ebd3fd31c8aa781f272c` plus the RC2 patch
Decision owner: the repository maintainer
Prepared: 2026-11-20
**This document does not tag, merge, publish or deploy anything.**

## 1. Decision

# **PASS — recommend tag review**

RC2-A and RC2-B are approved, implemented and verified. The shared-object
integrity hazards are closed, the shipping images build from the repository
Dockerfiles and run the full stack, and the database migration paths converge.
No new P0 or P1 defect was observed.

Two conditions remain attached to the recommendation:

1. **Hosted CI is NOT RUN** on this patch. A local green suite is not hosted
   evidence.
2. `refresh_content` keeps its opt-in posture and its documented throughput
   limitation; `fenced` mode remains operator-gated and is still not activated
   by default.

## 2. Gate matrix

| Gate | Status | Evidence |
| --- | --- | --- |
| RC2-A / RC2-B approvals recorded | **PASS** | Decision 1o, with scope, rollback, schema compatibility and test gates |
| Pre-fix defects reproduced | **PASS** | both RC1 characterizations passed before any edit |
| RC2-A function fixed in fresh install | **PASS** | `handle_shared_entity_deletion` locks each parent before counting |
| RC2-A function fixed in v1.17 migration | **PASS** | simulated pre-fix body converged after two migration passes |
| RC2-A positive regression | **PASS** | concurrent reference preserved, attributes intact |
| RC2-A zero-reference deletion still works | **PASS** | three idempotent deletions |
| RC2-A stress | **PASS** | 25 iterations, no loss, bounded |
| RC2-A vs `VacuumSource` | **PASS** | consistent final state, no deadlock |
| RC2-B legacy path page-scoped | **PASS** | shared object, link and attributes preserved |
| RC2-B cross-source refusal | **PASS** | explicit error, zero writes |
| RC2-B rollback | **PASS** | injected failure restores old generation |
| Schema stays 1.17 | **PASS** | version, writer generation and table count unchanged |
| Fresh install | **PASS** | 1.17, writer, both capabilities, 78 tables |
| v1.16 → v1.17 migration, twice | **PASS** | both passes exit 0, triggers present |
| Backup / restore / data comparison | **PASS** | 677 KB dump, 77 data entries, counts match |
| Full unit suite | **PASS** | 35 packages, exit 0 |
| Race suite | **PASS** | 5 packages, exit 0, no race |
| vet / builds / gofmt / diff / actionlint | **PASS** | all exit 0 |
| Example manifests lint + strict | **PASS** | 28/28 |
| Full PostgreSQL integration suite | **PASS** | 4 packages, exit 0 |
| Shipping image build (repository Dockerfiles) | **PASS** | api/events/engine built, exit 0 |
| Shipping image tool fidelity | **PASS** | nmap 7.95, java 11, dig 9.18 |
| Full stack on shipping images, default posture | **PASS** | schema/writer/mode/function verified |
| Legacy crawl | **PASS** | rows and event |
| Legacy refresh with shared page | **PASS** | other page preserved |
| Fenced crawl | **PASS** | claim + completed attempt |
| Fenced refresh | **PASS** | replacement + completed attempt |
| Forced failure | **PASS (data)** | old generation preserved |
| Cross-source refusal | **PASS** | explicit error, zero writes |
| Legacy `null:` | **PASS** | deleted, no navigation |
| Fenced `null:` | **PASS** | retained, terminal failure |
| No MCP mutation dispatch | **PASS** | 0 rows |
| No autonomous async resume | **PASS** | 0 goal runs, no worker |
| Event retention semantics | **PASS** | documented and tested |
| Hosted GitHub CI on the patch | **NOT RUN** | not pushed |
| Live Ollama / live MCP | **NOT RUN** | not configured |

## 3. Prerelease-test permission vs publication permission

**Prerelease testing: permitted.** The default-mode gates and the shipping-image
full-stack run are green.

**Publication (tag/release): not granted here.** It requires the maintainer to
record the scope acceptance (below) and to observe hosted CI on the exact pushed
commit. This document is evidence, not approval.

## 4. Scope freeze and release-note wording

2.3.0 is **not** complete autonomous agentic execution. Intended scope:

> local Ollama provider, synchronous acceptance-driven goals, trusted read-only
> native tools, approval-gated native crawl scheduling, read-only MCP over stdio
> and Streamable HTTP, and record-only MCP mutation proposals. The fenced
> crawl-claim foundation is operator-gated and not activated by default.

Suggested release-note sentences for the deferrals:

- "Automatic asynchronous goal resumption is not part of 2.3.0. The durable
  checkpoint kernel, the transactional store and the read-only observer and
  resume-eligibility services ship inert; no worker, lease or resume loop is
  active."
- "MCP mutation execution is not part of 2.3.0. Proposals, approvals, denial,
  revocation, audit and retention are record-only; no dispatcher, execution
  backend or reconciliation loop exists."
- "Ownership-epoch schema and the unbound source-status and `null:` enablement
  that depends on it are deferred."
- "Production activation of `fenced` claim mode is not authorised by default.
  The mode remains an operator switch and defaults to `legacy`."
- "`refresh_content` remains opt-in (`false` by default). Since this release it
  is page-scoped and orphan-only in both claim modes: a refresh no longer
  deletes an object another page still references, and a page linked to more
  than one source is refused with an explicit error. Enabling it serialises page
  indexing across the deployment."

## 5. Blockers

No P0 or P1 blocker remains for the default configuration or for the shipping
images.

### P2-1 — `refresh_content` serialises page indexing

When `refresh_content: true`, every page takes the
`crowler:fenced-content-refresh` advisory lock, so indexing serialises across
sources. Correctness is unaffected; a large stale generation holds the lock for
its whole replacement (~0.7 s measured for 286 links). Default deployments are
unaffected. Recommendation: keep the option opt-in, document the cost, and
revisit only if a deployment needs refresh at scale.

### P2-2 — Legacy refresh failure still reports source `completed`

In legacy mode an injected page-indexing failure during a refresh left the old
generation intact (correct) but the source row showed `completed` with
`last_error` set. Fenced mode reports `failed` through the terminal receipt.
This is the pre-existing legacy error classification, which RC2-B was not
approved to change. Recommendation: a separate, narrowly scoped decision to
classify refresh-indexing failures as source errors, or accept and document it.

### P3-1 — SQLite Events janitor

`cleanupExpiredEvents` uses SQL `now()`, which SQLite does not provide; the
query fails, logs, and deletes nothing. Pre-existing, unrelated to the agentic
scope, not fixed because changing the janitor SQL is outside the RC2 approvals.
PostgreSQL and MySQL behave as documented.

### P3-2 — Startup warnings

The engine still logs
`Failed to read ruleset's schema file: ./schemas/crowler-rulesets-schema.json`
(the file is `crowler-ruleset-schema.json`) and
`Failed to load plugin from ./plugins/: is a directory`. Cosmetic and
pre-existing.

### P3-3 — Event TTL may surprise operators

`automatic_events_removal: never` stops consumer acknowledgement but not the
janitor: events carry a finite `expires_at` (two minutes for `crawl_completed`,
five by default). Documented and tested; operators who need permanent events
must set a longer `expires_at`.

## 6. What the maintainer must record before a tag

1. Acceptance of the scope and deferrals in section 4.
2. Acknowledgment that hosted CI has **not** run on this patch, or an explicit
   decision to proceed without it.
3. Confirmation that `refresh_content` and `fenced` mode stay opt-in for the
   release.
4. A decision on P2-1 and P2-2 (accept and document, or schedule).

## 7. Recommended next step

Push the patch through the normal review path, let the `Go` and
`postgres-integration` workflows run on the exact candidate SHA, confirm the new
mandatory steps (shared-object integrity and event retention) pass, then
schedule the maintainer-controlled pre-release procedure described in
`RELEASE_230_PRE_RELEASE_RUNBOOK.md`.

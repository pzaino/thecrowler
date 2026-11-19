# WP-05B3C3 Stage B3 approval request

## Status

**PARTIALLY DECIDED ON 2026-11-18 (WP-05B3C3B2F1 / B3).**

- Decision 1a (monotonic ownership epoch) is **approved as the ownership
  model**. No DDL or activation is authorised.
- Decision 2 (atomic fenced `refresh_content`) is **approved and implemented**
  in WP-05B3C3B2F1, subject to the corrected design in
  [`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md).
- Decision 3 (`null:`) is **deferred**; legacy `null:` semantics are preserved
  and fenced `null:` remains quarantined.
- Decisions 4 and 5 are **deferred**.

The rest of this document remains the decision request for the outstanding
items. A chat request or the existence of a work package is not approval for
anything still marked pending.

B1 Option A and B2 were approved by the repository maintainer on 2026-11-18
under `WP-05B3C3/Stage-B-2`. WP-05B3C3B2V found that the B2R unbound
owner-continuity rule was not sound and disabled it; the corrected ownership
model is Decision 1a above.

Each decision below is independent. Approving one does not approve another.

Schema remains **1.17** and writer generation remains
**`verified-ownership-v1.17`** until a decision says otherwise.

## 1. Why a decision is needed

### 1.1 Unbound source-status mutation is not provable in schema 1.17

`SourceCrawlAttempts` stores the operation principal only for a bound attempt.
An unbound attempt (an ordinary fenced source crawl with no submitted crawl
proposal) has no persisted owner. B2R tried to infer owner continuity from:

```text
SourceOwnership.user_id == Sources.usr_id
SourceOwnership.last_updated_at <= SourceCrawlClaims.claimed_at
```

That is not sound. The ownership upsert and the `BEFORE UPDATE` trigger write
`CURRENT_TIMESTAMP`, which PostgreSQL evaluates at **transaction start**, not
commit time:

- `pkg/database/source_ownership.go` — `last_updated_at = CURRENT_TIMESTAMP`
- `pkg/database/postgresql-setup.pgsql` — `update_last_updated_at_column()`
  sets `NEW.last_updated_at = CURRENT_TIMESTAMP`

A transfer transaction that begins before the claim and commits after it can
therefore leave `last_updated_at <= claimed_at` even though the owner changed
between the claim and the terminal report. `Sources.usr_id` is mutable and
unversioned; no claim-time owner is persisted anywhere.

Reproducible evidence:
`TestPostgresSourceCrawlB2RProvenanceTimestampIsNotContinuity` (disposable
PostgreSQL 17) starts the transfer transaction, claims the source, performs the
ownership update, commits, and proves:

- the recorded `SourceOwnership.last_updated_at` is **not later than**
  `claimed_at`;
- the owner is nevertheless `8`, not the owner at claim time.

Consequence today: unbound source-status mutation is suppressed. The source
stays `processing` and becomes claimable again only when the 30-minute claim
lease expires. That is safe but operationally poor and repeats the crawl.

**Decision 1 is required to restore correct unbound source lifecycle.** The
corrected Decision 1 below also records that a single owner ID does not prove
continuous tenure (A -> B -> A), and asks for an explicit ownership semantic
plus a monotonic epoch if continuous tenure is required.

### 1.2 `refresh_content` is refused in fenced mode

`deleteWebObjects` runs in an independent transaction before the replacement
page transaction. If the replacement fails or the fence is lost between them,
successfully indexed old content has already been deleted. B2R fails closed
(refuses `refresh_content` when a handoff is present), so fenced sources that
rely on refresh cannot be crawled.

An atomic replacement does not obviously require new schema: the delete can
move inside the already-fenced main indexing transaction. That must be proved
and separately approved as a code change.

### 1.3 `null:` deletion is quarantined in fenced mode

`cmd/thecrowler` queues `null:` sources for asynchronous deletion by source ID
after the claim. That cannot prove the deletion is still authorised. B2R
records a terminal failure and does not delete, so fenced `null:` sources
cannot be removed through the normal interface.

### 1.4 Optional durability gaps (not safety blockers)

- A terminal receipt is delivered once; a transient `unavailable` leaves the
  attempt claimed until lease expiry. Replay is read-only and exact, but no
  durable receipt digest/ledger exists.
- Claim-request replay cannot prove equality of limit, priority or timing
  fields, and an empty batch leaves no acknowledgement.
- The worker no longer supplies `ExpectedState`; the repository reads
  `accepted`/`processing` from `Status`. The originally observed state is not
  persisted.

These are idempotency/audit limitations, not current authorisation holes.

## 2. Decision 1 — durable claim-time owner and ownership continuity (RECOMMENDED, corrected)

> **Corrigendum WP-05B3C3B2F1.** The first version of this section proposed a
> single `claim_owner_user_id` and expected an away-and-back transfer to be
> suppressed by comparing that ID with `Sources.usr_id`. That expectation is
> **not established**: A -> B -> A restores equality while ownership was
> interrupted. The corrected design below separates "who owned the source at
> claim time" from "was that ownership continuous", and asks the maintainer to
> choose the ownership semantic explicitly.

### Problem

Authorise unbound source-status mutation (and exact-fence `null:` deletion)
using a durable owner captured when the claim is allocated, without trusting a
mutable `Sources.usr_id` alone or a transaction-start timestamp.

### What a single owner ID proves, and what it does not

`claim_owner_user_id = Sources.usr_id` plus a verified `SourceOwnership` row
for that user proves **only** that the recorded owner at terminal time equals
the owner at claim time. It does **not** prove continuous tenure:

```text
claim owner A  ->  transfer to B (commit)  ->  transfer back to A (commit)
terminal: Sources.usr_id = A, SourceOwnership.user_id = A  ->  equality holds
```

`SourceOwnership` has one row per source (`source_id` primary key), no history
and no version:
`pkg/database/postgresql-setup.pgsql:456`.
`Sources.usr_id` is mutable and unversioned: `:416`.
`SourceOwnership.last_updated_at` is written by
`update_last_updated_at_column()` with `CURRENT_TIMESTAMP`, which PostgreSQL
evaluates at transaction start, so it cannot order a transfer against a claim
(`:15`; see also B2V-01).

Therefore a single owner-ID column cannot satisfy the "away-and-back
suppresses" expectation. Either continuity must be made provable, or the
maintainer must explicitly define that reauthorization by the same principal is
sufficient.

### Option 1a (recommended): monotonic ownership epoch

Add the smallest durable fact that distinguishes "same owner, never changed"
from "same owner again":

```sql
-- Sources
ownership_epoch BIGINT NOT NULL DEFAULT 0
    CHECK (ownership_epoch >= 0)

-- SourceCrawlAttempts
claim_owner_user_id BIGINT
    CHECK (claim_owner_user_id IS NULL OR claim_owner_user_id > 0),
claim_owner_epoch  BIGINT
    CHECK (claim_owner_epoch IS NULL OR claim_owner_epoch >= 0),
CONSTRAINT chk_source_crawl_attempt_owner_pair CHECK (
    (claim_owner_user_id IS NULL AND claim_owner_epoch IS NULL)
    OR (claim_owner_user_id IS NOT NULL AND claim_owner_epoch IS NOT NULL)
)
```

The epoch is advanced by an ownership-change trigger on `Sources`:

```sql
CREATE TRIGGER trg_sources_ownership_epoch
BEFORE UPDATE OF usr_id ON Sources
FOR EACH ROW
WHEN (NEW.usr_id IS DISTINCT FROM OLD.usr_id)
EXECUTE FUNCTION bump_source_ownership_epoch();
```

`bump_source_ownership_epoch()` sets
`NEW.ownership_epoch = OLD.ownership_epoch + 1`. The trigger covers
application paths (`VerifySourceOwnershipContext`) **and** direct `UPDATE`
statements, so an administrator transfer cannot silently leave the epoch
unchanged. An `UPDATE` that writes the same `usr_id` is not an ownership change
and does not advance it.

### Ownership-path review for the selected Option 1a (2026-11-18)

All tracked ownership-changing paths were reviewed:

| Path | Location | Changes | Epoch rule |
| --- | --- | --- | --- |
| Admin verification / transfer | `pkg/database/source_ownership.go:396` | locks source, updates `Sources.usr_id`, upserts `SourceOwnership` | advances on `usr_id` change and on the provenance write |
| Authenticated source creation | `pkg/database/source_authenticated.go:190` | inserts source + provenance in one transaction | provenance INSERT advances the epoch |
| Information Seed source creation | `pkg/database/source_upsert_policy.go:247,271` | inserts source + provenance | provenance INSERT advances the epoch |
| Source insert | `pkg/database/source.go:402` | `usr_id` at creation | new row starts at epoch 0; provenance INSERT advances it |
| Direct administrative SQL | database owner | `UPDATE Sources SET usr_id`, `INSERT/UPDATE/DELETE SourceOwnership` | row triggers fire for direct DML as well |
| Source deletion | source lifecycle | removes the source row | a deleted source cannot satisfy terminal checks; historical attempts are stale |

The epoch must advance on **all** of the following, not only `usr_id` changes:

- `Sources.usr_id` change (its own trigger);
- `SourceOwnership` INSERT (first verification or re-grant after revocation);
- `SourceOwnership` DELETE (revocation);
- `SourceOwnership` UPDATE that changes `user_id` (transfer).

A metadata-only re-verification of the same principal (`verified_at`,
`verified_by`, method or reason) is not an ownership change and should **not**
advance the epoch: it would otherwise invalidate healthy in-flight attempts
without any authority change.

This closes the ABA gap: A -> B -> A changes `usr_id` twice (or deletes and
re-inserts provenance), so the epoch at terminal differs from the claim epoch
and the update is suppressed. A delete-then-reinsert of the same owner is also
caught by the INSERT trigger even when `usr_id` never changes. Restoring a
backup can move the epoch backwards; that is a deployment concern and restored
databases must be treated as a new authority baseline with no surviving
in-flight claims.

Claim time, under the existing source row lock:

- read the verified `SourceOwnership` row;
- persist `claim_owner_user_id = SourceOwnership.user_id` and
  `claim_owner_epoch = Sources.ownership_epoch`;
- when ownership is not verified, persist both `NULL`.

Terminal time, under the same source row lock:

- allow an unbound source-status mutation only when
  `claim_owner_user_id = Sources.usr_id`
  **and** `claim_owner_epoch = Sources.ownership_epoch`
  **and** a verified `SourceOwnership` row for that user exists;
- otherwise suppress (current fail-closed behaviour).

A -> B -> A advances the epoch twice, so the claim epoch differs and the
mutation is suppressed. An unchanged owner keeps the epoch and is allowed.

SQLite can express the equivalent with an `AFTER UPDATE OF usr_id` trigger
(`WHEN NEW.usr_id IS NOT OLD.usr_id`) that increments the column, or with an
application-maintained epoch under SQLite's writer serialization. MySQL keeps
the columns for parity but remains legacy-only and must never expose fenced
behaviour.

**Limits of 1a:**

- it proves "no ownership change since claim", not a full ownership audit
  history;
- a restore from backup can move the epoch backwards; that is a deployment
  concern and should be called out in the runbook;
- a database owner who drops the trigger can bypass it; that is outside the
  application threat model, as with every other owned-table invariant;
- it does not by itself authorise exact-fence `null:` deletion; that remains
  Decision 3 and must still verify the URL marker and terminal state.

### Option 1b (alternative): append-only ownership history

Add `SourceOwnershipHistory` with `(source_id, epoch)` primary key, user,
verifier, method, reason and timestamps; `SourceOwnership` gains
`current_epoch`. Claims store the epoch; terminal compares the current epoch.
This is more auditable and survives investigations ("who owned this source at
time T"), at the cost of one more table, one more write per transfer and its
own retention policy.

### Option 1c (semantic alternative): reauthorization is enough

If the platform contract defines that the same principal re-acquiring ownership
restores authority (no continuous-tenure requirement), then the single
`claim_owner_user_id` equality is the documented contract and A -> B -> A
**passes**. That is a legitimate product decision, but it must be explicit and
recorded; the current proposal text cannot quietly rely on it while claiming
"continuity". Choosing 1c requires deleting the away-and-back suppression test
expectation and documenting the ABA tolerance.

### Option 1d: keep suppression

No DDL. Unbound sources stay `processing` until the claim lease expires; safe
but repeats the crawl.

### Why the current suppression remains correct until a decision

Schema 1.17 contains neither continuous-tenure proof nor an epoch. Any
implementation before a decision would either repeat the unsound timestamp
inference or silently adopt option 1c without recording it.

### Lock order (all options)

Unchanged:

```text
policy lock -> Sources row -> current claim -> attempt history -> Status CAS
```

The claim already holds the source row and reads `SourceOwnership` under it;
the epoch is read from the same locked `Sources` row.

### Migration, backfill, rollback

- Additive: columns default `NULL`/`0`, plus one trigger.
- Existing attempts are **not** backfilled: their claim-time owner and epoch are
  unknown. They remain suppressed. No bulk ownership inference.
- Fresh install, v1.16 -> v1.17 migration and the same-version addon all add
  the objects.
- Rollback drops the trigger and columns and returns to suppression; ownership
  and historical rows are not rewritten.
- If schema 1.17 has already shipped when this is approved, the change becomes
  a later schema migration instead. The release state at approval time decides.

### Capability and version

Recommended: a named capability within 1.17 (for example
`source-crawl-claim-owner-v1`) with a checksum, a readiness check for the
columns, the pair CHECK and the trigger, and an explicit fail-closed path when
the capability is absent. `RequiredSchemaVersion` and `WriterGeneration` stay
unchanged while 1.17 is unreleased.

### Privileges

The runtime role needs no new table grants: it already inserts attempts and
selects ownership. It must not be able to change `Sources.ownership_epoch`
directly except through the trigger; the readiness check verifies column
visibility and the trigger definition.

### Tests

- Claim persists the verified owner and epoch; unverified claims persist
  `NULL`/`NULL`.
- Unbound terminal with unchanged owner and epoch updates source status.
- Transfer A -> B before terminal suppresses.
- Transfer A -> B -> A before terminal suppresses (the corrected ABA case).
- Re-verification of the same owner without a `usr_id` change keeps the epoch
  and remains allowed.
- Direct `UPDATE Sources SET usr_id` advances the epoch.
- A rolled-back transfer leaves the epoch unchanged.
- Concurrent transfer and claim serialize on the source row; either the claim
  sees the new epoch or the transfer waits.
- Bound operations are unchanged.
- Historical attempts with `NULL` remain suppressed.
- Terminal replay does not rewrite the stored owner/epoch.
- MySQL reports unsupported.
- PostgreSQL and SQLite integration, trigger definition/readiness checks,
  migration/addon convergence.

### Activation preconditions

- An explicit semantic choice (1a, 1b, 1c or 1d) with approver, UTC date and
  contract version.
- Independent review of the DDL and code change.
- Disposable PostgreSQL and SQLite migration rehearsal.
- Backup/restore rehearsal on a disposable database.
- No production activation in the same work package.

## 3. Decision 2 — atomic `refresh_content` (APPROVED, IMPLEMENTED, no DDL)

**Status: approved 2026-11-18 and implemented in WP-05B3C3B2F1.** The original
proposal below was necessary but not sufficient; the implemented contract adds
page-row locking, cross-source page refusal, page-scoped link replacement and
orphan-only object deletion. See
[`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md)
section 5.7 for the delivered behaviour and
[`ASYNC_GOAL_B3C3B2F1_TEST_REPORT.md`](ASYNC_GOAL_B3C3B2F1_TEST_REPORT.md) for
evidence.

### Problem

The destructive refresh delete and the replacement write are separate
transactions. `refresh_content` is therefore refused for fenced crawls.

### Existing-schema workaround

Refusal (current behaviour) is safe but blocks refresh-dependent sources.

### Proposal

No schema change. A separately approved code package:

1. remove the standalone `deleteWebObjects` transaction from the fenced path;
2. perform the delete inside the main indexing transaction, after
   `ValidateTx` and before the replacement writes;
3. keep the source-row lock held to commit, so a terminal report or a
   supersession cannot interleave;
4. rely on the existing statement-level cleanup triggers, which run inside the
   same transaction and roll back with it;
5. keep the local pre-check only as defence in depth.

### Invariants

- Old content is deleted only if the replacement transaction commits.
- A failure after the delete rolls the delete back.
- A supersession or lease loss before commit rolls everything back.
- The fence check remains inside the transaction.

### Alternatives

- Attempt provenance on objects (larger, B3 DDL).
- Keep refusal (safe, no refresh).

### Recommendation

Approve the atomic code change; do not add schema for it. This was not
implemented in B2V because it changes destructive behaviour and requires its
own review and rollback tests.

## 4. Decision 3 — exact-fence `null:` deletion (RECOMMENDED after Decision 1)

### Problem

Asynchronous deletion by source ID cannot prove the claim, owner or policy is
still current.

### Options

1. **After Decision 1:** delete the `null:` source inside the terminal
   transaction, only when the attempt is current, unbound with a matching
   `claim_owner_user_id` (or bound with the matching operation principal), and
   the URL is a `null:` marker. The source delete cascades the current claim;
   attempt history is retained because it has no FK to `Sources`.
2. **Without Decision 1:** allow bound attempts only, using the immutable
   operation principal.
3. Keep quarantine (current safe behaviour).

### Invariants

- No deletion after transfer, disablement, expiry or supersession.
- No deletion of a non-`null:` source.
- The terminal result, the source delete and the attempt CAS commit together.
- Legacy `null:` deletion is unchanged.

### Recommendation

Approve option 1, implemented as a separate code package after Decision 1.
No additional DDL is required beyond Decision 1.

## 5. Decision 4 — durable terminal receipt digest/ledger (OPTIONAL)

### Problem

A terminal receipt is not stored as a canonical digest. Replay compares the
persisted attempt identity, binding, outcome, reason and operation result.
`ExpectedState` and the exact observation time are not persisted, and a
transient report failure leaves the attempt claimed until lease expiry.

### Existing workaround

The attempt history and `Status` row are the durable record; replay is
read-only and conflicts on different content. No completion proof is emitted
on failure.

### Proposal if approved

A canonical receipt digest and a compact append-only receipt ledger with:

- attempt/operation identity, outcome, reason, operation result;
- canonical argument/decision bytes hash;
- trusted observation time;
- unique `(goal_run_id/operation_id, receipt_sha256)` semantics if goal
  correlation is later required.

This would strengthen audit and exact replay, but it is **not required** for
the safety fixes in this package.

### Recommendation

Defer. Revisit when async goal correlation needs a durable receipt identity.

## 6. Decision 5 — claim request fingerprint and empty-batch acknowledgement (OPTIONAL)

### Problem

Claim-request replay checks the persisted attempts, not the original request
parameters. Limit, priority and timing intervals are not persisted, so a
changed request with the same request ID is not detectable. An empty batch has
no durable acknowledgement.

### Proposal if approved

- `SourceCrawlClaimRequests(claim_request_id PK, engine_id, request_sha256,
  created_at)` and an empty-batch record;
- or a fingerprint column on a claim-request table.

### Recommendation

Defer. Current workers use a fresh 256-bit request ID per batch, and the
duplicate-claim protections are independent of this gap.

## 7. Decisions requested

Please record an explicit yes/no for each:

1. **Ownership semantic.** Choose continuous tenure (1a or 1b) or
   reauthorization-is-sufficient (1c), or keep suppression (1d).
2. **Decision 1a (recommended if continuous tenure):** additive
   `Sources.ownership_epoch` + `SourceCrawlAttempts.claim_owner_user_id` and
   `claim_owner_epoch`, with the ownership-change trigger.
3. **Decision 1b (alternative):** append-only ownership history.
4. **Decision 2:** atomic `refresh_content` code change, no DDL
   (recommended), subject to the corrected shared-object and cross-source page
   requirements in `ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`. The naive
   "move the delete into the main transaction" design is not sufficient.
5. **Decision 3:** exact-fence `null:` deletion after Decision 1
   (recommended; requires the chosen continuity proof).
6. **Decision 4:** terminal receipt digest/ledger (optional; recommend defer).
7. **Decision 5:** claim request fingerprint and empty-batch acknowledgement
   (optional; recommend defer).
8. If Decision 1a/1b is approved: confirm the additive change stays within
   1.17 (unreleased) or must become a later schema migration.

A chat message or the existence of this request is not approval. The decision
must be tracked with approver, UTC date, contract version and work-package
identifier.

## 8. Explicitly not authorised

- Any DDL before a recorded decision.
- Production fenced-mode activation or mode switching.
- Automatic asynchronous goal resumption.
- Public API, event, configuration or agent-manifest changes.
- MCP mutation execution, dispatchers, retries or reconciliation workers.
- Changes to the `legacy` default or MySQL legacy-only behaviour.

## 9. Async goal resume phase outline (NOT IMPLEMENTED)

After the crawl-attempt foundation is independently accepted, a separate
package must define:

- a trusted single-runner election: durable claim/lease with fencing token,
  heartbeat, expiry and stale-worker rejection;
- an event or bounded-poll trigger with coalescing and no unbounded retries;
- revalidation at resume: current account, agent identity/owner, policy,
  verified source ownership, cancellation and budget;
- cumulative budgets across slices, with the synchronous slice ceiling kept
  separate from the logical goal deadline;
- crash/replay recovery: exact transition replay via the goal store's unique
  ledger, uncertain-commit reconciliation, and no duplicate external effects;
- new privileged actions require a fresh approval; a historic approval is not
  an enduring grant;
- correlation to authoritative operation receipts (`Status` + attempt
  history), never to source-level events or model text;
- goal evidence evaluation remains the only completion authority.

## 10. Remaining MCP/agent manifest integration (NOT IMPLEMENTED)

Based on the current code inventory:

- the MCP client is read-only and operator-configured; mutation proposals are
  inert and record-only;
- production proposal registration requires an authenticated principal from the
  manual-run path; event/background runs remain principal-less and fail closed;
- no dispatcher, execution backend or reconciliation worker exists;
- operator approval/denial/revocation are record-only;
- agent manifests cannot supply policy, approval identity, endpoint or
  credentials.

Any expansion (streamable-HTTP read-only tools, mutation dispatch, goal
resume) must be a separately approved work package with its own threat model,
idempotency/reconciliation contract and evidence semantics. A tool advertised
by a remote MCP server is never authority.

## 11. Recommended next work packages

1. **B2F1 follow-up A (Decision 2):** atomic fenced `refresh_content` with
   cross-source page refusal, orphan-only object deletion, rollback and
   interleaving tests. See `ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`.
2. **B3 (Decision 1):** the chosen ownership semantic, then the monotonic
   epoch/history DDL + migration + readiness, then unbound source-status and
   exact-fence `null:` deletion.
3. **B3.1 (Decision 3):** exact-fence `null:` deletion using the chosen
   continuity proof.
4. **WP-05B3C3 C/D:** async goal resume/claim/lease, only after the attempt
   foundation is accepted.
5. **WP-04/05/06 remaining:** MCP execution/reconciliation and user-facing
   agent integration, separately approved.

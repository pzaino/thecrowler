# WP-05B3C3 Stage B3 approval request

## Status

**REQUEST ONLY. NOT APPROVED. NO DDL, MIGRATION OR RUNTIME WIRING IS
AUTHORISED BY THIS DOCUMENT.**

B1 Option A and B2 were approved by the repository maintainer on 2026-11-18
under `WP-05B3C3/Stage-B-2`. B3 was explicitly not requested. WP-05B3C3B2V is
the independent verification package; it found that the B2R unbound
owner-continuity rule is not sound and disabled it. This document asks the
maintainer to decide the follow-up options, separately and explicitly.

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

**Decision 1 is required to restore correct unbound source lifecycle.**

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

## 2. Decision 1 — durable claim-time owner (RECOMMENDED)

### Problem

Authorise unbound source-status mutation (and exact-fence `null:` deletion)
using a durable owner captured when the claim is allocated.

### Existing-schema workaround

None that is sound. Suppression is the current fail-closed behaviour.

### Proposed additive change

Add one nullable column to `SourceCrawlAttempts`:

```sql
claim_owner_user_id BIGINT
    CHECK (claim_owner_user_id IS NULL OR claim_owner_user_id > 0)
```

- The claim transaction already locks the `Sources` row and reads
  `SourceOwnership`. When verified ownership exists, persist that `user_id`
  on the new attempt row before commit.
- When ownership is not verified, persist `NULL`. Unbound attempts remain
  suppressed, exactly as today.
- The column is immutable for the attempt. A replay or terminal report never
  rewrites it.
- The terminal transaction, under the source row lock, may update source status
  for an unbound attempt only when
  `claim_owner_user_id = Sources.usr_id` and a verified `SourceOwnership` row
  for that user exists. The current owner may legitimately differ from the
  claim owner after a transfer; in that case the update is suppressed.

An alternative separate table
`SourceCrawlAttemptOwners(attempt_id, source_id, generation, owner_user_id,
recorded_at)` is functionally equivalent. A single column is smaller and keeps
the attempt as the authoritative record. **Recommended: the column.**

### Why this fixes the race

The owner is captured under the source-row lock in the claim transaction and
never changes. It does not depend on any transaction-start timestamp, on
`Sources.usr_id` history, or on a mutable provenance row.

### Write/read invariants

- `claim_owner_user_id` is written exactly once, at claim insertion.
- `NULL` means "owner not proven"; every consumer treats it as unauthorised.
- Terminal source-status mutation requires the value to equal the current
  source owner **and** a verified provenance row for that owner.
- Bound attempts continue to use the immutable operation binding; the new
  column is informational for them and never replaces the binding checks.

### Lock order

Unchanged:

```text
policy lock -> Sources -> current claim -> attempt history -> Status CAS
```

The claim already holds the source row; reading `SourceOwnership` under it is
the same order used by the existing claim ownership check.

### PostgreSQL and SQLite

- PostgreSQL: `BIGINT NULL` with a named CHECK; no trigger.
- SQLite: `INTEGER NULL` with the same CHECK; existing writer-transaction
  serialisation applies.
- MySQL: add the nullable column for schema parity, or omit it. MySQL remains
  legacy-only and MUST NOT expose fenced claim/source-status behaviour.

### Migration, backfill, rollback

- Additive column, default `NULL`.
- Existing attempts are **not** backfilled: their claim-time owner is unknown.
  They remain suppressed. No bulk ownership inference.
- Fresh install, v1.16 -> v1.17 migration and the same-version addon all add
  the column.
- Rollback drops the column and returns to suppression; no historical record is
  rewritten.
- If schema 1.17 has already shipped when this is approved, the same change
  must become a 1.18 migration instead. The release state at approval time
  decides.

### Capability and version

Recommended: a named capability within 1.17 (for example
`source-crawl-claim-owner-v1`) with a checksum, a readiness check that the
column and CHECK exist, and an explicit fail-closed path when the capability is
absent. `RequiredSchemaVersion` and `WriterGeneration` stay unchanged while
1.17 is unreleased; otherwise a new schema version is required.

### Privileges

The runtime role needs no new table grants: it already inserts attempts and
selects ownership. The readiness check verifies column visibility.

### Tests

- Claim persists the verified owner; unverified claim persists `NULL`.
- Unbound terminal with unchanged owner updates source status.
- Transfer before terminal suppresses the update.
- Transfer away and back (two provenance writes) suppresses the update.
- Bound operations are unchanged.
- Historical rows with `NULL` remain suppressed.
- Conservative replay: a terminal retry does not rewrite the owner.
- MySQL reports unsupported.
- PostgreSQL and SQLite integration, including concurrent transfer and claim.
- Readiness/version/addon/migration convergence.

### Activation preconditions

- Independent review of the DDL and the code change.
- Disposable PostgreSQL and SQLite migration rehearsal.
- Backup/restore rehearsal on a disposable database.
- No production activation in the same work package.

## 3. Decision 2 — atomic `refresh_content` (RECOMMENDED, no DDL)

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

1. **Decision 1:** additive durable claim-time owner (recommended).
2. **Decision 2:** atomic `refresh_content` code change, no DDL (recommended).
3. **Decision 3:** exact-fence `null:` deletion after Decision 1
   (recommended).
4. **Decision 4:** terminal receipt digest/ledger (optional; recommend defer).
5. **Decision 5:** claim request fingerprint and empty-batch acknowledgement
   (optional; recommend defer).
6. If Decision 1 is approved: confirm the additive change stays within 1.17
   (unreleased) or must become 1.18.

A chat message or the existence of this request is not approval. The decision
must be tracked with approver, UTC date and contract version.

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

1. **B2V follow-up A (Decision 2):** atomic fenced `refresh_content` with
   rollback and interleaving tests.
2. **B3 (Decision 1):** durable claim-time owner, DDL + migration + readiness,
   then unbound source-status and exact-fence `null:` deletion.
3. **B3.1 (Decision 3):** exact-fence `null:` deletion using the new owner.
4. **WP-05B3C3 C/D:** async goal resume/claim/lease, only after the attempt
   foundation is accepted.
5. **WP-04/05/06 remaining:** MCP execution/reconciliation and user-facing
   agent integration, separately approved.

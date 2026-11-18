# WP-05B3C3 Stage B approval request

## Status

**B1 implemented. B2 approved; B2 implementation pending. B3 not approved.**

WP-05B3C3A stopped at claim/replay repairs, a real proposal-to-claim test and a
pure handoff/terminal contract. WP-05B3C3B1 corrected the pure authority/replay
model and audited the crawler side effects. WP-05B3C3B1R closed the remaining
source-status, current/history, unbound-owner and claim-timing gaps.
WP-05B3C3B1S then closed the all-class final-freshness and observation-time
replay gaps.

The maintainer approved B1 Option A and B2 on 2026-11-18. B1 is implemented as
the internal, test-only repository in
[`ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md).
B2 remains approved but not implemented; it is the live crawler handoff and its
fencing/cancellation work. B3 remains not approved. No production activation,
automatic mode switch, migration, deployment or automatic goal resume is
authorized.

## Requested decisions (separate approvals)

The maintainer is asked to decide these independently. Approving one does not
approve the others.

### B1. Internal heartbeat and terminal repository (no crawler caller)

> APPROVE WP-05B3C3 Stage B1 only: implement internal, opt-in lease renewal and
> terminal-result methods using the existing schema 1.17 tables, invoked only by
> unit and disposable integration tests. The methods must use the authoritative
> locked fence, fresh database time, atomic compare-and-set, the persisted
> replay rules and source-owner/disablement restrictions in
> `ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md`. They must not be called by the crawler,
> `cmd/thecrowler`, an event consumer, an agent, MCP or a public interface.

### B2. Live crawler handoff caller

> APPROVE WP-05B3C3 Stage B2 only: construct the handoff from
> `ClaimSourceCrawlAttempts`, carry it through `cmd/thecrowler`,
> `pkg/crawler.Pars` and `ProcessContext`, call the approved internal heartbeat
> and terminal methods, and implement the approved fencing/cancellation
> requirements for crawler writes and external effects.

### B3. Optional additive schema (Option B)

> APPROVE a specific additive 1.17 extension only for: canonical persisted
> receipt digest/ledger, persisted `ExpectedState`, original unbound class,
> durable request fingerprint and/or empty-batch acknowledgement. The approval
> must include the exact DDL, fresh-install/v1.16-to-v1.17 migration,
> same-version addon, readiness check, capability checksum, rollback and
> retention design.

Option A, no new DDL, is sufficient for the minimal internal repository but
cannot prove the fields listed as limitations in
`ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md`.

## Recorded approval

- Approver identity: repository maintainer (user) in the OpenCode session.
- UTC approval date: 2026-11-18.
- Exact instruction: `this is the approval for B2.` followed by the recorded
  clarification selecting `Approve B1 + B2 (Recommended)`.
- Contract version: `WP-05B3C3/Stage-B-2`
- B1 internal repository (Option A): **approved**
- B1 approval explicitly includes per-source lease timing, final freshness
  rejection, exact current/history equality, source-status authority preflight
  and unbound owner-continuity denial: **yes**
- B2 live crawler caller: **approved**
- B3 additive schema (Option B): **not requested / not approved**
- B3 exact DDL/migration/addon/capability/rollback reference: not applicable
- Rollout/drain/rollback authorization: B1 is internal and test-only. B2 must
  preserve the legacy path, may run only when the operator has explicitly
  selected `fenced` mode, and must not add an automatic mode switch, migration,
  deployment or default-mode change.
- Automatic goal resume: **not approved**
- Public API/config/manifest contract: **not approved**

A chat instruction, a task prompt or the existence of this request is not
approval. The decision must be tracked and attributed.

## Scope if B1 is approved

1. Add an internal, opt-in heartbeat matching the exact current attempt.
2. Extend only under the documented bounds and fresh database clock.
3. Update current and history lease expiry together with affected-row checks.
4. Add the terminal reporter with the documented lock order, compare-and-set,
   closed result vocabulary, persisted replay/conflict handling and
   commit-uncertainty reconciliation.
5. Update source status only when the same fence, verified ownership and
   non-disabled/non-deleted source still permit it.
6. Keep the legacy path unchanged and add no production caller.
7. Test on SQLite and disposable PostgreSQL, including stale worker,
   supersession, lease expiry, owner transfer, disabled/deleted source,
   duplicate/conflicting receipts and lost-response reconciliation.

## Scope if B2 is approved

1. Carry the handoff without reading it from a URL, event, plugin, model or
   source configuration.
2. Preserve the legacy path when no handoff exists.
3. Implement the crawler side-effect decisions from the B3C3B1 audit: fence
   destructive/current-state writes, stop external work on lease loss where
   possible, and retain append-only output with attempt provenance where
   applicable.
4. Keep MySQL on its documented legacy path.

## Explicitly not authorized

- Automatic async goal resume or completion.
- A new public REST/Events/MCP route, event type, manifest field or `config.yaml`
  property.
- A new schema version, table, column or capability checksum without B3.
- Applying migrations to a shared, staging or production database.
- Treating a proposal, approval, claim, heartbeat or reported crawl completion as
  goal evidence.
- A remote mutating MCP call or a generic dispatcher.
- Any production crawler caller under B1 alone.

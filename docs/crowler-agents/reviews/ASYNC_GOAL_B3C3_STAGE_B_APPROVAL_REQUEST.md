# WP-05B3C3 Stage B approval request

## Status

**Request only. Not approved.**

WP-05B3C3A stops at claim/replay repairs, a real proposal-to-claim test and a
pure handoff/terminal contract. The following mutating stage requires a separate
explicit maintainer decision.

## Exact requested authorization

> APPROVE WP-05B3C3 Stage B only: implement the authenticated fenced crawler
> handoff, lease heartbeat, and terminal-result repository described in
> `ASYNC_GOAL_B3C3A_HANDOFF_AND_FENCED_RESULTS.md`. The implementation may carry
> the claim identity into the crawler, renew the exact current lease using
> database time, and record a terminal attempt/operation result through one
> bounded compare-and-set transaction. It must keep schema 1.17 and writer
> generation `verified-ownership-v1.17` unless a separately listed additive
> change is also approved. It must not auto-flip the claim mode, resume an
> agent, treat `Status='completed'` as goal acceptance, use a source-level
> `crawl_completed` event as operation proof, or expose a public API or
> configuration field.

## Required approval record

- Approver identity:
- UTC approval date:
- Contract version: `WP-05B3C3/Stage-B-1`
- Live crawler handoff: approved / rejected
- Heartbeat writer: approved / rejected
- Terminal-result repository and source-status update: approved / rejected
- Schema decision (choose one):
  - use the existing 1.17 columns only; or
  - approve an additive canonical request fingerprint and/or terminal-receipt
    ledger with migration, addon, checksum and rollback details.
- Rollout/drain/rollback authorization:
- Automatic goal resume: **not approved**

A chat instruction, a task prompt or the existence of this request is not
approval. The decision must be tracked and attributed.

## Scope if approved

1. Construct the handoff in the claim caller and carry it through
   `cmd/thecrowler`, `pkg/crawler.Pars` and `ProcessContext` without reading it
   from a URL, event, plugin, model or source configuration.
2. Add a heartbeat that matches the exact current attempt and extends the lease
   only under the documented limits and database clock.
3. Add the terminal reporter with the documented lock order, compare-and-set,
   closed result vocabulary, replay/conflict handling and commit-uncertainty
   reconciliation.
4. Update source status only when the same fence is still current. A transferred
   owner, disabled source or newer generation preserves the source row.
5. Keep the legacy path unchanged when no handoff exists and keep MySQL on its
   documented legacy path.
6. Add SQLite and disposable PostgreSQL tests, including stale worker,
   supersession, lease expiry, owner transfer, disabled/deleted source,
   duplicate/conflicting receipts and lost-response reconciliation.

## Explicitly not authorized

- Automatic async goal resume or completion.
- A new public REST/Events/MCP route, event type, manifest field or `config.yaml`
  property.
- A new schema version, table, column or capability checksum without the schema
  decision above.
- Applying migrations to a shared, staging or production database.
- Treating a proposal, approval, claim, heartbeat or reported crawl completion as
  goal evidence.
- A remote mutating MCP call or a generic dispatcher.

# WP-04C2I B5 Decision

## Status

**APPROVED — B5 only.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date: 2026-11-14 (UTC).
- Exact instruction: `OK, B4 applied, tested, reviewed and approved. Please
  continue with B5.`
- Baseline HEAD: `7f4db7f6d684ec51ec9968880735f52d30b0b509`.
- Scope: durable denied-admission audit, bounded reason codes, actor
  provenance, write-time redaction, retention, and fail-closed database-outage
  behaviour for MCP mutation proposal and registration denials.

The approval message states that B4 was applied, tested, reviewed and approved.
This checkout contains no B4-specific files, so B5 does not claim to have
verified B4 and does not reimplement approval-time current-policy
revalidation. B5 is independent of that milestone.

## Approved implementation

- Record durable `MCPMutationAudit` events for denied proposal admissions and
  denied production proposal-tool registrations.
- Record only bounded trusted fields: event type, actor type/ID/issuer, run,
  trace, agent, validated principal, server, tool alias, stable reason code and
  a small redacted detail object.
- Never store raw model arguments, URLs, tokens, credentials or arbitrary error
  text.
- Preserve the original denial when the audit write fails or the database is
  unavailable; log the audit failure without admitting the proposal.
- Add a bounded retention path integrated with the Events janitor and a
  documented redaction/expiry contract.
- Keep schema `1.17` and writer generation `verified-ownership-v1.17`.

## Not approved by B5

External dispatch, mutating MCP `tools/call`, dispatch claims,
`MCPMutationDispatchAttempts`, retry, reconciliation, automatic approval
execution, approval-time current-policy revalidation (B4), schema deployment
and SQLite foreign-key changes (G3), and any shared or production database
change remain out of scope.

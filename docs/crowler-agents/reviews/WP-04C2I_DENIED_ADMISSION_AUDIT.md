# WP-04C2I Denied-Admission Audit

## Scope

B5 adds a durable, bounded audit for MCP mutation admissions that are denied
before any proposal row exists. It does not add external mutation, dispatch,
retry, reconciliation, execution or approval automation.

## Event types

| Event type | Written at | Meaning |
| --- | --- | --- |
| `mcp_mutation_admission_denied` | proposal, tool loop, goal run | A proposal call was denied before admission. |
| `mcp_mutation_registration_denied` | production registration | A proposal-tool adapter was not registered. |

Both use `actor_type = agent`. `actor_id` is the agent ID (or `unattributed`
when no trusted agent identity exists), `actor_issuer` is the validated
principal issuer, and `principal_user_id` is set only when the principal passes
the existing `MCPMutationPrincipal.Validate` check. Run, trace, server and tool
alias are copied only from trusted runtime/descriptor values.

The `redacted_detail` object is flat and allowlisted:

```json
{"stage":"proposal","call_id":"call-1","policy_id":"operator-policy"}
```

`stage` is `proposal`, `registration`, `tool_loop` or `goal`. `call_id` is
included only when it is a safe token of at most 64 characters; a URL-like,
control-character, oversized or otherwise unsafe call ID is replaced with
`sha256:<16 hex>`.
`policy_id` is the operator policy identifier. A global registration denial may
also include `operation_count`.

## Stable reason codes

Existing tool-policy denials keep their existing codes:
`no_tool_loop_opt_in`, `missing_identity`, `identity_enforcement_required`,
`unknown_tool`, `not_allowlisted`, `missing_capability`, `insufficient_trust`,
`forbidden_by_contract`, `invalid_arguments`, `scope_denied`,
`budget_exhausted`, `context_expired`, `duplicate_call_id`,
`mutation_disabled`, `operation_not_allowlisted` and `unknown_tool_effect`.

B5 adds bounded admission/registration codes:
`missing_principal`, `missing_owner`, `missing_run_identity`, `not_approved`,
`descriptor_unavailable`, `descriptor_mismatch`, `agent_not_approved`,
`capability_missing`, `trust_insufficient`, `contract_forbidden`,
`transport_not_approved`, `source_scope_denied`, `resource_scope_conflict`,
`manager_unavailable`, `registry_denied`, `policy_invalid`, `store_unavailable`,
`proposal_conflict`, `quota_exceeded`, `proposal_expired`, `proposal_corrupt`,
`approval_invalid` and `admission_denied`.

Any unknown value is normalized to `admission_denied`. Raw error text is never
used as a reason.

## Write paths

1. **Proposal admission**: `MCPMutationProposer.Propose` installs a deferred
   denial audit after it has a descriptor and (when available) a trusted
   runtime. This covers authorization, policy-operation, agent/capability/
   trust/contract, transport, argument, scope, principal, fingerprint and
   durable-store failures. `AuthorizeToolCall` denials that prevent `Execute`
   are covered by the tool-loop or goal boundary instead.
2. **Tool loop**: `processToolCall` audits a denied proposal tool after the
   registry lookup and before every authorization/argument/budget/context
   denial. The descriptor identity is carried by the proposal marker and by
   `MCPMutationProposalAuditInfo`, which the frozen registry wrapper preserves.
   It also records a known operator-policy alias that was never registered for
   the run; the denial itself remains `unknown_tool`.
3. **Goal run**: `goalRunner.denyCallWithAudit` records the same boundary
   denial, including a known policy alias that was never registered. The
   `Execute` path that re-enters `denyCall` for a `ToolAuthError` skips the
   second audit because `Propose` already recorded it.
4. **Registration**: `registerMCPMutationProposalTools` records a global event
   for manager/principal/owner/store failures and a per-operation event for
   descriptor, agent, capability, trust, contract, transport, source/resource,
   constructor and registry failures.

## Database-outage contract

The audit write is deliberately best-effort with respect to availability but
never authoritative over the admission decision:

- The original denial is returned before and regardless of the audit result.
- The audit uses a two-second write budget derived with
  `context.WithoutCancel`, so a cancelled run can still record a denial but a
  down database cannot stall the denial indefinitely.
- A write failure is logged with bounded stage/reason and the error type only;
  raw database errors, arguments and credentials are not logged by this path.
- A proposal is never created and a tool is never registered because the audit
  failed.
- The durable proposal store is never replaced by an in-memory fallback.

B5 tests use both a fake audit sink that returns an error and a closed SQLite
database to prove the denial is preserved and no admission is attempted.

## Retention, expiry and redaction

- Configuration: `events.mcp_mutation_audit_retention`.
- Empty: 90 days.
- Values below 24 hours: raised to 24 hours.
- Invalid values: the existing duration parser returns a short fallback, which
  is then raised to the 24-hour floor.
- Cleanup runs hourly from the Events janitor, not on every event tick.
- One pass deletes at most `MCPMutationAuditCleanupMaxBatch` (1000) rows.
- Cleanup targets the known MCP mutation event types only and uses the existing
  `(event_type, occurred_at)` index. An unknown event family is retained rather
  than deleted by a future writer's mistake.
- Each cleanup query has a 30-second timeout so a database outage cannot stall
  the janitor goroutine.
- Redaction happens at write time. The database layer additionally validates
  event/actor/reason patterns, 64-hex identifiers, JSON object shape, flat
  detail values and size limits. It rejects rather than stores unsafe content.
- Expired rows are deleted, not rewritten. Existing schema-1.17 indexes and
  grants are sufficient; no migration is added.

## In-memory versus durable audit

- `AgentsEngine.appendAudit` remains a run-scoped in-memory operational trace
  used by the existing tool loop and goal runner.
- `MCPMutationAudit` is the durable compliance record. Proposal creation and
  approval transitions are written in their transaction; denied admissions and
  denied registrations use the standalone bounded writer.
- The two records are complementary. B5 does not move or duplicate the
  in-memory trace into the durable table.

## Schema and dispatch

No schema version, migration, capability checksum or writer generation changes.
No mutating MCP `tools/call`, dispatcher, dispatch claim,
`MCPMutationDispatchAttempts` row, retry, reconciliation worker, execution
endpoint or automatic approval execution is added. Approval-time current-policy
revalidation (B4) is outside this change.

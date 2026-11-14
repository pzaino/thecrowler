# WP-04C2I B5 Security Review

## Scope

Durable denied-admission audit, reason-code bounds, actor provenance, write-time
redaction, retention and database-outage behaviour for MCP mutation proposals
and production registrations. No external mutation or dispatch is in scope.

## Threats and controls

| Threat | Control |
| --- | --- |
| Raw model arguments leak into audit | `MCPAdmissionDeniedEvent` has no argument map; detail is an allowlisted flat object (`stage`, safe `call_id`, `policy_id`, bounded `operation_count`) |
| URL, token, credential or secret in a call ID | Unsafe or oversized call IDs are replaced with a short SHA-256 prefix |
| Arbitrary error text becomes the reason | Errors are mapped to a bounded allowlist; unknown values become `admission_denied` |
| Control characters or unbounded fields | Agent and database layers strip control characters and enforce 256-byte text, 64-byte reason and 128-byte event-type bounds |
| Nested or oversized redacted detail | Database layer requires a JSON object, flat scalar/string-list values, at most 32 keys, at most 4096 bytes |
| Malformed hash/identifier | Proposal/approval/attempt identifiers must be exactly 64 lowercase hex characters |
| Native crawl authority authorizes an MCP audit path | Audit identity is exposed only by the MCP proposal marker/interface; native tools return no audit identity |
| Double audit changes a decision | Audit writes return values that callers ignore after logging; the original denial is returned unchanged |
| Audit failure is treated as success | `Propose` returns the original error; registration returns the original registration error; tests assert no proposal/admission |
| Database outage blocks a denial indefinitely | Two-second audit write budget and 30-second janitor cleanup budget; the denial path proceeds after the bounded audit attempt |
| In-memory fallback hides a durable outage | Production registration still requires `DatabaseMCPMutationProposalStore`; no fallback is introduced |
| Retention deletes unrelated audit history | Cleanup targets a bounded known event-type list and leaves unknown event families untouched |
| Retention is unbounded | Default 90 days, 24-hour floor, hourly cadence and 1000-row batch cap |
| Retention scans the whole table per minute | Hourly cadence and `(event_type, occurred_at)` index usage |
| Audit rows expose tenant data | Only trusted runtime/descriptor fields are copied; principal is recorded only when validated; no request body, URL, token or argument is stored |
| Operator/agent confusion | Denied events use `actor_type=agent`; approval/proposal operator events keep their existing actor types |

## Live evidence

A disposable Compose stack used PostgreSQL 15-family, the final B5
Events/API/Engine images, a controlled stdio MCP fixture with a read-only
`echo` tool and two mutating tools, and a fake deterministic model.

- The denied proposal run returned HTTP 200 with a failed tool call and no
  proposal. The audit row contained
  `event_type=mcp_mutation_admission_denied`,
  `actor_id=live-mutation-agent`, a validated principal, run/trace IDs,
  `server_id=approved-server`,
  `tool_alias=mcp_approved-server_mutate_counter`,
  `reason_code=invalid_arguments` and a 91-byte detail object.
- The registration-denial run returned HTTP 502 and no tool run. The audit row
  contained `event_type=mcp_mutation_registration_denied`,
  `actor_id=live-registration-agent`,
  `tool_alias=mcp_approved-server_mutate_strict_counter`,
  `reason_code=capability_missing` and a 62-byte detail object.
- The unregistered-alias run returned HTTP 200 with a denied tool call and no
  execution. The audit row contained
  `event_type=mcp_mutation_admission_denied`,
  `actor_id=live-unknown-alias-agent`,
  `tool_alias=mcp_approved-server_mutate_counter`,
  `reason_code=unknown_tool` and a 92-byte `tool_loop` detail object.
- `MCPMutationProposals`, `MCPMutationApprovals` and
  `MCPMutationDispatchAttempts` stayed empty.
- The durable audit held exactly three denied-admission rows for the three
  runs.
- The controlled mutation counter file stayed absent.
- A secret-looking denied resource (`https://secret.example/denied?token=abc`)
  did not appear in any audit column or in the Events logs.
- The PostgreSQL integration test inserted, read and bounded-cleanup audited
  rows in a disposable database and left an unknown event type untouched.

## Residual risks

- B4 approval-time current-policy revalidation is not present in this checkout.
  B5 does not claim to verify it and does not reimplement it.
- The durable audit is not exposed through a new read API. Operators query the
  existing table through their database tooling.
- Retention intentionally leaves unknown event types untouched. A future writer
  must add its event type to the retention allowlist deliberately.
- During a database outage, the denial is preserved but the audit row cannot be
  written. The failure is logged with bounded fields. A future durable local
  buffer would be a separate, explicitly approved design.
- G3 schema deployment and SQLite foreign-key enforcement remain pending.
- The agent-side audit write can add up to two seconds to a denial while the
  database is unavailable. The batch/tool/consecutive-denial budgets bound how
  many such attempts a run can make.

## Conclusion

B5 records denied admissions durably without weakening the existing denial or
creating any external mutation path. The audit payload is bounded, redacted and
tenant-safe, and retention is bounded and operator-configurable.

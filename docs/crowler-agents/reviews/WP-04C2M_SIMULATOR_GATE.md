# WP-04C2M Simulator Gate

## Status

**Option A is not approved. No simulator production/dev code or tests were
implemented.**

`WP-04C2M_DISPATCH_APPROVAL_REQUEST.md` is a request, not approval. The OpenCode
task is not approval. Option B (limited external execution) remains pending.

## Required approval record

Before any simulator implementation, a tracked maintainer decision must record:

- approver;
- UTC date;
- contract/version;
- exact authorization text;
- confirmation that only read-only simulation is approved;
- confirmation that dispatch attempts, dispatch states, workers, leases,
  remote mutating calls and schema changes remain forbidden.

Suggested exact scope:

> APPROVE WP-04C2M Option A only: implement a local/test-development read-only
> MCP execution-profile validation simulator. It may read durable inert
> proposals and approvals through trusted authorized test boundaries,
> compute/version a canonical execution-profile identity, revalidate current
> policy, agent, accounts, ownership, transport and schema assumptions, and
> emit bounded refusal/simulation results. It must not write
> `MCPMutationDispatchAttempts`, transition proposals to dispatch states, claim
> leases, start a worker, call any remote mutating MCP tool, execute a backend,
> use production credentials, or change schema 1.17. It must not expose a public
> operator API without separate approval. Option B remains pending.

## Permitted work completed

- Reviewed existing read-only proposal/approval types and execution-profile
  gaps.
- Prepared the simulator design and negative-test specification.
- Preserved the 1.17 schema and `execution_backend: none`.
- No simulator code was added.

## Blocked work

- Execution-profile digest implementation.
- Pure evaluator tests.
- Any read-only simulation command or package.
- GitHub CI for simulator tests.

## Gate decision needed

Approve, reject or amend the Option A scope above. Do not infer approval from
this task or from the existing B1/B2/B3/B4/B5/G3 decisions.

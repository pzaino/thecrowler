# WP-04C2K Maintainer Decisions

## Recorded decisions

### B4 approval

The maintainer recorded in
`docs/crowler-agents/reviews/WP-04C2I_B5_DECISION.md`:

> `OK, B4 applied, tested, reviewed and approved. Please continue with B5.`

Date: 2026-11-14 (UTC). Baseline: `7f4db7f6d684ec51ec9968880735f52d30b0b509`.

WP-04C2K reconciles that decision against the actual checkout. No B4 source
exists at `180bd5f9` or in reachable history. The decision is treated as
authorization for the documented fail-closed contract, not as proof of an
existing implementation. The implementation is recorded in
`WP-04C2K_B4_DECISION.md`.

### No dispatcher or external mutation

The maintainer's work package explicitly excludes a dispatcher, automatic
execution, retry/reconciliation workers, new execution backends and remote
mutating MCP calls. None were implemented. Execution backend remains `none`.

### Schema and writer generation

Schema remains `1.17`. Writer generation remains
`verified-ownership-v1.17`. No migration was added.

### No commit or push

No commit, tag, push, merge, rebase or pull request was created. The deliverable
is a patch for review.

## Decisions made during implementation

### Approval enforcement in the durable store

The current-policy check is enforced in
`DatabaseMCPMutationApprovalStore.Issue`, not only in the HTTP handler. This
prevents a future production path from issuing approvals without the check. The
handler factory is a test seam; production continues to construct the durable
store.

### API startup loads its own authority

The API process now freezes its own MCP policy, inventory and agent registry at
startup. It does not rely on Events process state. Policy deployment requires
restarting both processes. SIGHUP intentionally does not hot-reload MCP
authority because hot reload was not approved.

### Approval normalization does not require local MCP files

`SetMCPOperatorConfigForApproval` validates policy and inventory structure but
skips local file existence checks for stdio commands, token files, CA files and
working directories. The API never launches MCP servers. Requiring those files
in the API container would break deployments where only Events hosts the MCP
execution environment. Events continues to use the strict
`SetMCPOperatorConfig`.

### Admission authority in the insert transaction

Principal and ownership revalidation runs as a callback inside the durable
admission transaction. The authorizer uses the existing source-lock protocol.
This minimizes the race between authorization and insertion without adding new
lock primitives.

### External-resource admission

Source-scoped proposals receive the full ownership recheck. External-resource
proposals revalidate the active principal and exact approved resource scope;
administrator-only external-resource approval remains enforced at the approval
boundary. Proposal creation itself remains inert and is not treated as an
execution grant.

### Context propagation

A trusted parent context is carried through the job engine, budgets, tool loop,
goal run, MCP initialization, provider calls, registration and admission.
Background event paths keep independent contexts and cannot manufacture a
mutation principal.

### Approval store authority split

`DatabaseMCPMutationApprovalStore.Issue` owns current-policy revalidation,
stored-proposal validation and the durable state transition. The authenticated
approver's account and source authority are enforced by the API handler before
`Issue`. There is one production caller today. A future in-process caller must
reuse the same authority checks; this is recorded as residual risk rather than
silently claiming the store enforces approver authority.

### Scope selector ambiguity

External-resource proposals reject a payload that carries both `resource` and
`resource_id`. The canonical fingerprint must bind one unambiguous resource, not
allow a future transport to read the other selector.

### Replay authority and lock order

Replayed proposal admission revalidates current principal and ownership before
returning the existing row. Source-scoped admission locks the source before the
user, matching the transfer path and avoiding a share-to-update upgrade.

### Delegated and AIInteraction cancellation

Delegated agents inherit the trusted caller context, and `AIInteraction` passes
it to the provider transport. Manual-run cancellation therefore reaches the
whole delegation and model-call path, not only tool loops.

### PostgreSQL 15 restore rehearsal

The restore test tolerates only the known client artifact emitted by a newer
`pg_restore` against PostgreSQL 15:
`unrecognized configuration parameter "transaction_timeout"`. It still verifies
the restored data. Any other restore error fails.

## Open decisions and follow-up work

- CI does not yet run the E2E authorization fixture. Wiring it into a dedicated
  workflow needs image-build and isolation decisions.
- The next milestone may design a first external execution backend, but only
  through a separate design, security review and explicit maintainer
  authorization.
- A future policy hot-reload design would need an explicit cross-service
  consistency protocol. This work package intentionally does not add one.
- Direct database mutations that bypass the application's source-lock protocol
  remain outside the admission serialization guarantee.

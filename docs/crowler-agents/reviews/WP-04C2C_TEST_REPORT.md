# WP-04C2-C Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Local branch: `develop`
- Local HEAD: `cc2461d407678b47bb3de5ed63891400b04c66a9`
- Reviewed MCP commit: `3aee6780b7b437d120ad2e8ca08adf878db3a330`
- Go: `go1.27.3 linux/amd64`
- OS: Linux
- Build/test temp root: `/home/pzaino/tmp/opencode-go` (persistent home volume)
- Release: `v2.3.0`
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`

The local checkout is one commit ahead of the reviewed MCP HEAD. `cc2461d4`
contains unrelated Information Seed changes and MCP documentation wording only;
no MCP Go code differs between the two commits.

The final applied-patch tree full suite passed. Two intermediate exact
`go test ./...` runs hit the pre-existing `pkg/ws`
`TestWebSocketConnectionMessageDeliveryAndCleanup` flake (`connections = 0`);
the test passed immediately in isolation and on the final retry. No MCP
mutation code is involved in that package.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| Baseline `go test ./... -count=1` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-baseline-full.log` |
| `go test ./pkg/database ./pkg/agent ./services/api -count=1` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-focused3.log` |
| `go test ./... -count=1` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-full4.log` |
| `go test -race ./pkg/database ./pkg/agent ./services/api -count=1` | PASS, no data race | `/home/pzaino/tmp/opencode/wp04c2c-final-race5.log` |
| `gofmt -l pkg services cmd` | PASS, empty output | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| `git diff --check` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| `go vet ./...` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| `go vet -tags integration ./pkg/database` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `/home/pzaino/tmp/opencode/wp04c2c-final-static3.log` |
| PostgreSQL disposable integration (`-tags integration`) | PASS | `/home/pzaino/tmp/opencode/wp04c2c-pg-integration10.log` |
| `git apply --check` against a clean `cc2461d4` archive | PASS | `/home/pzaino/tmp/opencode/wp04c2c-patchcheck-apply.log` |
| `go test ./... -count=1` in the clean patched tree | PASS | `/home/pzaino/tmp/opencode/wp04c2c-patchcheck-final2-full3.log` |
| `go test -race ./pkg/database ./pkg/agent ./services/api -count=1` in that tree | PASS, no data race | `/home/pzaino/tmp/opencode/wp04c2c-patchcheck-final-race.log` |
| SQLite clean-install/migration/restart tests | PASS | included in full suite |

## PostgreSQL disposable isolation

The integration test now:

- requires `CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1` **and**
  `CROWLER_MCP_POSTGRES_DSN`;
- creates a unique database through a maintenance connection;
- applies the additive migration twice and applies the clean-install MCP block;
- runs admission, external-scope, approval, revocation, list, capability
  negative, type negative and cross-connection concurrency tests;
- verifies a sentinel row in the maintenance database remains intact;
- drops only the database it created.

It never runs `TRUNCATE` or deletes rows from the caller-selected database.
Negative guard tests prove a DSN alone is insufficient.

The concurrency section uses two independent `sql.DB` pools and covers:

- concurrent distinct admissions at per-run quota 1;
- concurrent identical approval issuance;
- concurrent issue/revoke with a coherent final proposal/approval state.

## SQLite and quota coverage

- `TestMCPMutationCapabilityDetectsMissingIndex`
- `TestMCPMutationCapabilityDetectsMissingTable`
- `TestMCPMutationCapabilityDetectsMissingColumn`
- `TestMCPMutationCapabilityDetectsStaleMarker`
- `TestMCPMutationCapabilityFailsOnSchema117WithoutCapability`
- `TestMCPMutationUniqueColumnsProbe`
- `TestMCPMutationExpiredApprovalReapprovalReopensProposal`
- `TestMCPMutationRevocationReopensProposal`
- `TestMCPMutationDenyClassifiesExpiredApproval`
- `TestMCPMutationQuotaConsistencyAfterTransitions`
- `TestMCPMutationSQLiteRestartPreservesProposalAndQuota`
- `TestMCPMutationInMemoryExpirySweepReleasesQuota`

## Corrupt-record coverage

- `TestDatabaseMCPMutationStoreRejectsCorruptProposal` covers truncated
  canonical bytes, noncanonical whitespace, trailing JSON, duplicate keys and a
  tampered policy snapshot.
- `TestDatabaseMCPMutationStoreAcceptsNormalizedPolicySnapshot` covers
  PostgreSQL-style policy key normalization.
- `TestMCPMutationProposalToolUsesDurableStoreWithoutExecution` proves the
  proposal tool can use the durable store with a test-installed trusted
  principal, replays idempotently, leaves the mutation fixture counter at zero
  and creates zero dispatch attempts.
- `TestDatabaseMCPMutationApprovalRejectsCorruptProposal` proves approval is not
  created for a corrupt proposal.
- `TestMCPMutationApprovalAPIRejectsCorruptProposal` proves the API returns a
  bounded `503` and does not create an approval.

## Tenant pagination coverage

`TestMCPMutationApprovalAPIListTenantPagination` mixes unauthorized proposals
before, within and after the requested page, includes an external-resource
proposal, verifies admin visibility, revokes source ownership and checks
oversized cursors. The PostgreSQL integration test exercises the non-admin
source-ownership SQL predicate.

## Not run

- Live Ollama: no Ollama service was available. The opt-in live proposal test
  was not executed and is not counted as a pass.
- GitHub `postgres-integration` workflow: the reviewed run failed while
  installing `postgresql-client` because the Ubuntu package mirror DNS
  resolution failed. That is an environment/package failure, not a SQL test
  failure and not a pass. Local Docker PostgreSQL 15.10 was used for the
  disposable integration gate.
- Production/shared database migration: intentionally not run.
- Full Docker Compose stack: not run; no service topology changed.
- Remote MCP mutation dispatch: intentionally not implemented or executed.

## Zero-mutation invariant

- No code path in this patch calls a mutating remote MCP `tools/call`.
- No dispatcher, dispatch claim or execution backend exists.
- The dispatch-attempt table remains empty; the attempts endpoint reports
  `dispatch_enabled:false`.
- Existing fixture counter assertions remain in the full suite.
- Proposal and approval records are treated as inert; they do not satisfy goal
  completion evidence.

## Remaining risks

- SQLite runtime foreign-key enforcement is unresolved (see schema review).
- No production authenticated principal reaches the tool loop; producer
  registration remains disabled.
- No approved public MCP mutation policy loader; the full Go policy is not
  operator-configurable yet.
- Current-policy revalidation at approval/dispatch is not implemented.
- Denied-admission audit and audit retention are not implemented.
- A persisted `approved` proposal with an expired approval is reconciled on the
  next action, not by a background reaper.
- The capability marker plus object probes cannot inspect every CHECK expression
  or SQLite type affinity.

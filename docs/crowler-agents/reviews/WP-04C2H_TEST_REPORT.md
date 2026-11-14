# WP-04C2H Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `408fc80840527c53c3b301771a89c974fb8f42f6` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- PostgreSQL test stack: `postgres:15.10-bookworm`
- Release: `v2.3.0`
- Schema: `1.17`, writer generation `verified-ownership-v1.17`

Commands ran in `/home/pzaino/tmp/opencode/thecrowler-wp04c2h`.
Logs are outside the repository under `/home/pzaino/tmp/opencode/`.

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS | `wp04c2h-final-suite.log` |
| `go test ./... -count=1` | PASS | `wp04c2h-final-suite.log` |
| `go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS, no data race | `wp04c2h-final-race.log` |
| VDI negative controls `-count=20` | PASS | `wp04c2h-vdi.log` |
| `gofmt -l pkg services cmd` | PASS | `wp04c2h-final-static.log` |
| `git diff --check` | PASS | `wp04c2h-final-static.log` |
| `go vet ./...` | PASS | `wp04c2h-final-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2h-final-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2h-final-static.log` |

## B3 test coverage

`pkg/agent/mcp_authority_test.go` and existing MCP mutation tests cover:

- native crawl authority and MCP authority isolation in both directions;
- the frozen registry wrapper preserving the MCP proposal marker;
- exact multi-operation registration and ambiguous first-match rejection;
- missing principal, owner label, agent approval, capabilities, trust,
  contract, transport and run scope;
- missing durable schema-1.17 capability with no in-memory fallback;
- tool-loop registration and execution through the durable store;
- goal registration and execution through the durable store;
- inert proposal output and empty mutation fixture counter;
- existing proposal fingerprint, quota, replay, expiry, approval, denial and
  revocation tests.

## Container build

The final binaries were built from the B3 source in a Go Alpine builder with
the production `/app` source path:

- `services/events` -> `/app/events`;
- `services/api` -> `/app/api`;
- `cmd/thecrowler` -> `/app/thecrowler`.

They were layered into the existing CROWler images and tagged:

- `crowler-events:latest`
- `crowler-api:latest`
- `crowler-engine:latest`

A standard Dockerfile build path remains network-dependent on
`proxy.golang.org`; the host module cache was used for the final images to avoid
the transient Docker-internal TLS failure observed during earlier container
work.

## Live proposal-only acceptance

A disposable Compose project (`wp04c2h`) used:

- PostgreSQL 15.10 on host port `55481`, schema 1.17;
- API `18081`, Events `18083`;
- a controlled stdio MCP server with `echo` and `mutate_counter`;
- a fake OpenAI-compatible model that calls
  `mcp_approved-server_mutate_counter` once with
  `{"resource":"live-resource"}`;
- a manual agent with `owner: live-owner`, trust `trusted`, capability
  `tool_execution`, and the mutation alias in its explicit allowlist;
- a local auth user with `events:*` and superadmin role for the approval API.

The live tool loop returned HTTP 200 with:

```json
{
  "status": "success",
  "agent_id": "live-mutation-agent",
  "agent_name": "Live Mutation Agent",
  "principal_user_id": 1,
  "principal_username": "wp04c2g-operator",
  "principal_issuer": "crowler",
  "result": {
    "content": "proposal recorded",
    "model_rounds": 2,
    "status": "completed",
    "tool_call_count": 1,
    "tool_calls": [{
      "name": "mcp_approved-server_mutate_counter",
      "status": "executed",
      "reason_code": ""
    }]
  }
}
```

Durable state after the run:

- `MCPMutationProposals`: `1`, state `approval_required`;
- proposal detail exposed `argument_keys: ["resource"]` and
  `external_resource: "live-resource"`, with no raw argument values;
- approval API issued one active approval, then revoked it; both remained
  record-only;
- `MCPMutationDispatchAttempts`: `0`;
- attempts API: `{"items":[],"dispatch_enabled":false}`;
- controlled mutation counter file: absent;
- no mutating MCP `tools/call` occurred.

Live logs: `wp04c2h-live-success.log`, `wp04c2h-live-list.json`,
`wp04c2h-live-approve.log`, `wp04c2h-live-revoke.log` and
`wp04c2h-live-evidence.log`.

The first live attempt exposed two integration defects, both fixed before the
final run:

1. the agent action requires an `input` parameter; the request now supplies one;
2. the durable proposal row requires a non-empty agent `owner` audit label;
   registration and admission now reject a missing owner label before insert.

## Safety evidence

- No production dispatcher, dispatch claim, retry, reconciliation worker,
  automatic approval execution or external mutation is implemented.
- Proposal and approval records are inert and cannot satisfy deterministic goal
  evidence.
- Native crawl mutation policy remains separate.
- The live test used a disposable database and was torn down, including its
  volumes. The existing user stack was not touched.
- No schema change, migration, release change or shared/production database
  operation was performed.

## Skips and limitations

- Information Seed Compose acceptance suite: not run; separate opt-in suite.
- Live Ollama: not used; the deterministic model was a controlled fake provider.
- Full engine/VDI application stack: not run; the kernel of this change is the
  API/Events/DB path.
- B4 current-policy revalidation at approval is pending.
- B5 denied-admission audit/retention is pending.
- G3 schema deployment/SQLite FK is pending.
- The manual-run scope remains coarse `events:*`; external-resource approval
  used a superadmin test user.

Race timings:

```text
ok  github.com/pzaino/thecrowler/pkg/config      1.911s
ok  github.com/pzaino/thecrowler/pkg/agent     193.107s
ok  github.com/pzaino/thecrowler/pkg/database   40.919s
ok  github.com/pzaino/thecrowler/services/events 25.902s
ok  github.com/pzaino/thecrowler/services/api    4.461s
```

## Patch validation

The patch was generated with:

```sh
git diff --binary --full-index --no-ext-diff 408fc808
```

It was applied to a clean `git archive 408fc808` tree:

| Check | Result | Log |
| --- | --- | --- |
| `git apply --check` | PASS | `wp04c2h-patchcheck-apply.log` |
| Applied-tree `go test ./... -count=1` | PASS | `wp04c2h-patchcheck-full.log` |
| Applied-tree `go vet ./...` | PASS | `wp04c2h-patchcheck-full.log` |
| Applied-tree `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2h-patchcheck-full.log` |
| Applied-tree focused race | PASS, no data race | `wp04c2h-patchcheck-race.log` |

The final patch hash and changed-file list are reported in the handoff. The
tracked report intentionally does not embed the hash because adding it would
change the patch it describes.

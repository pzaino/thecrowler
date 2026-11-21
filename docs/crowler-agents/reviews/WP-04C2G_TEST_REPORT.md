# WP-04C2G Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `3946fda8510f7bfac2f3fafacfd1c6d40e543ea7` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- PostgreSQL test stack: `postgres:15.10-bookworm`
- Release: `v2.3.0`
- Schema: `1.17`, writer generation `verified-ownership-v1.17`

Commands ran in `/home/pzaino/tmp/opencode/thecrowler-wp04c2g`.
Logs are outside the repository under `/home/pzaino/tmp/opencode/`.

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS | `wp04c2g-final-suite.log` |
| `go test ./... -count=1` | PASS | `wp04c2g-final-suite.log` |
| `go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS, no data race | `wp04c2g-final-race.log` |
| VDI negative controls `-count=20` | PASS | `wp04c2g-vdi.log` |
| `gofmt -l pkg services cmd` | PASS | `wp04c2g-final-static.log` |
| `git diff --check` | PASS | `wp04c2g-final-static.log` |
| `go vet ./...` | PASS | `wp04c2g-final-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2g-final-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2g-final-static.log` |

Focused tests cover the manual endpoint, principal derivation, account
state/scope checks, forged-field rejection, typed runtime propagation,
manual-job filtering, event-run exclusion, concurrent-run principal isolation,
goal/tool-loop principal binding and the MCP mutation alias fail-closed guard.

## Container builds

The standard Compose build was attempted first:

```sh
docker compose -p wp04c2g -f docker-compose.yml \
  -f /tmp/opencode/wp04c2g-compose-override.yml \
  build --no-cache crowler-api crowler-events crowler-engine-1
```

It failed on Docker-internal TLS handshakes to `proxy.golang.org` while
fetching Go modules; compilation did not fail. The same Dockerfiles were then
built successfully with host networking:

```sh
docker build --network=host -f Dockerfile.events -t crowler-events .
docker build --network=host -f Dockerfile.searchapi -t crowler-api .
docker build --network=host -f Dockerfile.thecrowler -t crowler-engine .
```

All three returned 0. The Events binary was subsequently rebuilt from the final
B2 source with the production source path `/app`, layered into the Events image
and tagged `crowler-events`. The final live run used that image without any
test-only schema mount. Images: `crowler-api:latest`,
`crowler-events:latest`, `crowler-engine:latest`.

The initial compose build failure is an environment/network limitation, not a
code compilation failure. The final source also passes host `CGO_ENABLED=0/1`
builds.

## Live disposable stack

A separate Compose project (`wp04c2g`) used the generated 1.17 PostgreSQL
schema, unique container names and host ports `18080` (API), `18082` (Events),
`55480` (PostgreSQL). The existing user stack was not disturbed. The stack
used the built images and a test-only local auth user with `events:*`.

Live evidence:

| Check | Result |
| --- | --- |
| API `/v1/health` and `/v1/ready` | PASS |
| Events authenticated `/v1/health` | PASS |
| Local login through API | PASS, token issued |
| `POST /v1/agent/run` with `live-manual-agent` | PASS, HTTP 200 |
| Returned principal | `user_id=1`, `wp04c2g-operator`, issuer `crowler` |
| Manual command result | `live-manual-ok` |
| Event-triggered sibling job count | `0` (manual-only filtering held) |
| No authentication | HTTP 401 |
| Forged `principal_user_id` field | HTTP 400 |
| Disabled account | HTTP 401 |
| Companion API/Events readiness | PASS |

Response:

```json
{
  "status": "success",
  "agent_id": "live-manual-agent",
  "agent_name": "Live Manual Agent",
  "principal_user_id": 1,
  "principal_username": "wp04c2g-operator",
  "principal_issuer": "crowler",
  "result": " live-manual-ok\n"
}
```

Logs and output:
`wp04c2g-live-success-app2.log`,
`wp04c2g-live-evidence-final2.log`,
`wp04c2g-live-event-count-final.txt`.

The test agent was preloaded through a writable `/app/agents` mount because the
Events container root filesystem is read-only and the upload handler writes
under `./agents`. The endpoint itself was exercised normally. The final image
was built with the production source path `/app`, so it loaded the agent and
schema without any `/src` test mount.

## Safety evidence

- No mutating MCP `tools/call` was invoked.
- No production proposal tool was registered.
- No dispatcher, dispatch claim, `MCPMutationDispatchAttempts` insert, retry,
  reconciliation worker or execution endpoint was added.
- Event-triggered jobs did not run through the manual endpoint.
- The live stack was disposable and was removed after the test, including its
  named volumes.
- No schema change, migration, release change or shared/production database
  operation was performed.

## Skips and limitations

- Information Seed Compose acceptance suite: not run; separate opt-in suite.
- Live Ollama: not run; not required for B2.
- Full Docker Compose engine/VDI stack: not started; B2 targets the
  API/Events/DB path. API and Events containers were built and run.
- The standard `docker compose build --no-cache` path was blocked by Docker
  network TLS; host-network builds succeeded.
- The test stack used a local auth user and test-only mounts; these are not
  repository changes.

## Final race and patch validation

Race timings:

```text
ok  github.com/pzaino/thecrowler/pkg/config      3.547s
ok  github.com/pzaino/thecrowler/pkg/agent     247.289s
ok  github.com/pzaino/thecrowler/pkg/database   53.377s
ok  github.com/pzaino/thecrowler/services/events 12.882s
ok  github.com/pzaino/thecrowler/services/api    3.592s
```

The final race run passed with no data-race warning.

The live test found and fixed one real wiring defect: the handler checked a
local `AgentsEngine` variable while startup initialized `agt.AgentsEngine`.
The handler now uses the initialized agent-package engine. The request `input`
field also accepts any bounded JSON value, not only an object, because agent
actions such as `RunCommand` take scalar input. Both fixes are covered by the
endpoint tests and the successful live run.

Patch hash, application check and applied-tree results are reported in the
handoff.

## Patch validation

The patch was generated with:

```sh
git diff --binary --full-index --no-ext-diff 3946fda8510f7bfac2f3fafacfd1c6d40e543ea7
```

It was applied to a clean `git archive 3946fda8` tree:

| Check | Result | Log |
| --- | --- | --- |
| `git apply --check` | PASS | `wp04c2g-patchcheck-apply.log` |
| Applied-tree `go test ./... -count=1` | PASS | `wp04c2g-patchcheck-full.log` |
| Applied-tree `go vet ./...` | PASS | `wp04c2g-patchcheck-full.log` |
| Applied-tree `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2g-patchcheck-full.log` |
| Applied-tree focused race | PASS, no data race | `wp04c2g-patchcheck-race.log` |

The final patch hash and changed-file list are reported in the handoff. The
tracked report intentionally does not embed the hash because adding it would
change the patch it describes.

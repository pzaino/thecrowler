# WP-04C2F Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `b5e7d020183acfba0e4a7c10834cf6612e3e135d` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- SQLite driver: `github.com/mattn/go-sqlite3 v1.14.52`
- PostgreSQL integration: `postgres:17` disposable Docker container
- Release: `v2.3.0`
- Schema: `1.17`, writer generation `verified-ownership-v1.17`

Commands ran in `/home/pzaino/tmp/opencode/thecrowler-wp04c2f-impl`.
Logs are outside the repository under `/home/pzaino/tmp/opencode/`.

## Results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/config ./pkg/agent ./pkg/database ./services/api -count=1` | PASS | `wp04c2f-release-suite.log` |
| `go test ./... -count=1` | PASS | `wp04c2f-release-suite.log` |
| `go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/api -count=1` | PASS, no data race | `wp04c2f-race-release.log` |
| `go test ./pkg/vdi -run 'TestCamouflageControlsDetectLegacyDefects|TestCamouflageLegacyAudioControlIsDeterministic' -count=20` | PASS | `wp04c2f-vdi-release.log` |
| `go vet ./...` | PASS | `wp04c2f-static-release.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2f-static-release.log` |
| `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2f-static-release.log` |
| `git diff --check` | PASS | `wp04c2f-static-release.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2f-static-release.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2f-static-release.log` |
| MCP PostgreSQL exact CI step simulation | PASS | `wp04c2f-pg-final.log` |

Focused suite timings:

```text
ok  github.com/pzaino/thecrowler/pkg/config
ok  github.com/pzaino/thecrowler/pkg/agent
ok  github.com/pzaino/thecrowler/pkg/database
ok  github.com/pzaino/thecrowler/services/api
```

Race detector timings:

```text
ok  github.com/pzaino/thecrowler/pkg/config      5.096s
ok  github.com/pzaino/thecrowler/pkg/agent     129.781s
ok  github.com/pzaino/thecrowler/pkg/database   19.541s
ok  github.com/pzaino/thecrowler/services/api    1.776s
```

The full suite covered all packages, including Information Seed, VDI, native
crawl, goal evidence, API and the `tests/integration/infoseed-compose` helper
packages (the latter report `[no test files]`).

## B1 policy and parser tests

`pkg/config/mcp_mutation_policy_test.go` passes:

- valid local file, include-expanded, remote, YAML and JSON policy loading;
- absent and explicitly disabled policy;
- unknown policy/`mcp` keys, duplicate outer/nested keys and duplicate JSON
  keys;
- YAML anchor and merge rejection;
- null, bool-as-string, float-as-int and int-as-string rejection;
- invalid durations, unsafe transports/backends and false
  `require_approval`/`audit_required`;
- mixed scopes, source outside inventory scope, missing/disabled server,
  missing tool, wrong effect, missing proposal opt-in, agent/capability
  mismatch, empty operations and invalid server IDs;
- unrelated duplicate/unknown legacy `crawler` fields still accepted;
- JSON Schema positive/negative fixtures through the repository's pinned
  `qri-io/jsonschema` tooling;
- the tracked `examples/mcp-mutation-policy.yaml` example through the real
  `ParseConfig` loader (the example uses a placeholder command and does not
  deploy or execute a server).

`pkg/agent/mcp_mutation_policy_test.go` passes:

- parse -> normalize -> `SetMCPOperatorConfig` -> `CurrentMCPOperatorConfig` /
  `CurrentMCPMutationPolicy` round-trip;
- exact derived alias `mcp_approved-server_approved_tool`;
- stable SHA-256 runtime identity;
- caller and getter slice mutation cannot change the frozen snapshot;
- failed setter rollback preserves the previous policy;
- concurrent getters and setters are race-free;
- missing/disabled/malformed policy grants nothing;
- inventory mismatch and derived-alias collision rejection;
- conversion without global installation;
- `MCPConfig.String`/`GoString` redaction.

An earlier race run also hit a timing-sensitive existing test,
`TestAgentGoalRunMCPInitializationConsumesTheRunDeadline`, under concurrent
load: the slow-start fixture did not record its startup before the one-second
test budget. The final race run passed that test, and no production code in the
MCP startup/deadline path was changed by B1.

The race detector found two real snapshot issues during this work:

1. normalized server slices still shared caller backing arrays;
2. `normalizeMCPServerConfig` mutated caller-owned approved-tool slices while
   another setter read them.

Both were fixed with `cloneMCPServerConfig` at normalization input and output.
The targeted race test and the final full race matrix are recorded above.

## PostgreSQL integration

The dedicated MCP step was simulated against disposable `postgres:17` with the
explicit opt-in and the service credentials:

```text
=== RUN   TestPostgresMCPMutationDurableStore
--- PASS: TestPostgresMCPMutationDurableStore (1.87s)
PASS
ok  	github.com/pzaino/thecrowler/pkg/database	1.882s
```

`grep` confirmed `RUN`/`PASS`; no `SKIP` occurred. The test created and dropped
only its unique child database. No shared/production database was touched.

The reviewed SHA `b5e7d020` was reported by the maintainer as having eight
successful GitHub checks, including the dedicated MCP step. The `gh` CLI was
not available here, so no independent GitHub run URL is claimed. The patch was
not pushed; local evidence is not GitHub evidence.

## Zero mutation / zero dispatch

`TestMCPMutationClassificationAndProposalIsInert`,
`TestMCPMutationProposalToolUsesDurableStoreWithoutExecution`,
`TestMCPMutationSourceScopeIsEnforced`,
`TestMCPMutationProposalThroughToolLoopAndGoalStaysInert` and
`TestMCPMutationLifecycleNeverCreatesDispatchAttempts` pass. They assert:

- the isolated mutating fixture counter remains empty;
- a proposal is not goal-success evidence;
- no `MCPMutationDispatchAttempts` rows exist;
- no proposal enters dispatch, uncertain or reconciliation states.

The release evidence log is `wp04c2f-inert-release.log`. A non-test search
found no MCP proposal registration and no dispatch-attempt insert. The only production `RegisterMutation` use remains the native
crawl-request tool. B1 does not connect the new policy to a proposal tool.

## Information Seed boundary

No Information Seed source, migration, fixture, Compose runner or
`tests/integration/infoseed-compose/` file changed. The Information Seed
Compose acceptance suite was not run; it is a separate opt-in suite on shared
Docker infrastructure.

## Skips and limits

- Live Ollama mutation test: not run; no configured service was used.
- GitHub Actions: not rerun because the worktree was not pushed.
- Shared/production database migration: not run.
- Information Seed Compose acceptance: not run.
- Full Docker Compose application stack: not run.
- Full legacy JSON Schema compilation under the pinned `qri-io/jsonschema`
  remains a pre-existing tooling limitation; the policy sub-schema is validated
  directly and runtime validation is mandatory.

## Patch validation

The patch was generated with:

```sh
git diff --binary --full-index --no-ext-diff b5e7d020
```

It was applied to a clean `git archive b5e7d020` tree:

| Check | Result | Log |
| --- | --- | --- |
| `git apply --check` | PASS | `wp04c2f-patchcheck-apply.log` |
| Applied-tree `go test ./... -count=1` | PASS | `wp04c2f-patchcheck-full.log` |
| Applied-tree `go vet ./...` and integration-tag vet | PASS | `wp04c2f-patchcheck-full.log` |
| Applied-tree `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2f-patchcheck-full.log` |
| Applied-tree MCP PostgreSQL exact CI step | PASS (`RUN`/`PASS`, no `SKIP`) | `wp04c2f-patchcheck-pg.log` |
| Applied-tree focused race | PASS, no data race | `wp04c2f-patchcheck-race.log` |

The final patch hash and changed-file list are reported in the handoff. The
tracked report intentionally does not embed the hash, because adding it would
change the patch it describes.

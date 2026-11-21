# WP-04C2E Test Report

> Historical note: this report records pre-B1 parser behavior. WP-04C2F
> subsequently approved and implemented strict `mcp.mutation_policy` loading;
> see `WP-04C2F_TEST_REPORT.md`.

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `5846ef53bbcbf8172bdeca2e26a0476411a4d0c2` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- SQLite driver: `github.com/mattn/go-sqlite3 v1.14.52`
- PostgreSQL: `postgres:17` local disposable Docker container
- Release: `v2.3.0`
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`

All commands ran in the isolated worktree
`/home/pzaino/tmp/opencode/thecrowler-wp04c2e`. Logs are outside the
repository under `/home/pzaino/tmp/opencode/`.

## Results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/config ./pkg/agent ./pkg/database ./services/api -count=1` | PASS | `wp04c2e-final-suite.log` |
| `go test ./... -count=1` | PASS | `wp04c2e-final-suite.log` |
| `go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/api -count=1` | PASS, no data race | `wp04c2e-race-frozen.log` |
| `go test ./pkg/vdi -run 'TestCamouflageControlsDetectLegacyDefects|TestCamouflageLegacyAudioControlIsDeterministic' -count=20` | PASS | `wp04c2e-vdi.log` |
| `go vet ./...` | PASS | `wp04c2e-static-frozen.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2e-static-frozen.log` |
| `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2e-static-frozen.log` |
| `git diff --check` | PASS | `wp04c2e-static-frozen.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2e-static-frozen.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2e-static-frozen.log` |
| MCP PostgreSQL integration, exact CI step simulation | PASS | `wp04c2e-ci-mcp-pg-final.log` |

Focused suite timings from the frozen run:

```text
ok  github.com/pzaino/thecrowler/pkg/config      0.021s
ok  github.com/pzaino/thecrowler/pkg/agent      25.585s
ok  github.com/pzaino/thecrowler/pkg/database   11.508s
ok  github.com/pzaino/thecrowler/services/api    0.201s
```

Race detector timings:

```text
ok  github.com/pzaino/thecrowler/pkg/config      1.172s
ok  github.com/pzaino/thecrowler/pkg/agent     109.557s
ok  github.com/pzaino/thecrowler/pkg/database   15.993s
ok  github.com/pzaino/thecrowler/services/api    1.493s
```

The full suite covered all packages, including the Information Seed packages
and the `tests/integration/infoseed-compose` helper packages (the latter report
`[no test files]`; the Compose acceptance runner is a separate opt-in suite and
was not executed).

## Alias and policy tests

- `TestDocumentedMutationInventoryUsesNormalizedServerIdentity`: PASS. It runs
  the real `normalizeMCPOperatorConfig` path for `approved-server`, derives
  `mcp_approved-server_approved_tool`, validates the internal policy, and
  rejects `approved_server`.
- `TestDocumentedMCPMutationPolicyAliasAndValidation`: PASS with the corrected
  server ID and alias.
- `TestMCPMutationPolicyFailsClosed`: PASS with new cases for an invalid server
  ID (alias recomputed), a control character in the tool name, and a duplicate
  operation.
- Existing `TestMCPToolAliasIsStableAndBounded` and
  `TestMCPOperatorConfigValidation` alias-collision cases: PASS in the focused
  and full suites.

## Parser strictness tests

`TestMutationPolicyUnknownKeysAreCurrentlyIgnored`: PASS. It proves the actual
legacy behavior of:

- `getConfigFile`;
- `LoadRemoteConfig`;
- YAML `ParseConfig`;
- JSON input through `ParseConfig`.

The fixtures contain an unknown `mcp.mutation_policy` subtree, the typos
`require_aproval` and `allowed_resouces`, a duplicate nested
`require_aproval`, a duplicate `crawler.workers`, an invalid
`proposal_ttl: not-a-duration`, and a wrong scalar type for typed
`mcp.servers`. Unknown nested keys, typos, invalid durations and duplicate keys
are silently ignored (last value wins); the wrong scalar type is rejected.

## VDI regression

The WP-04C2D deterministic legacy-audio fix remains intact. The combined
negative-control and focused deterministic regression passed 20 consecutive
runs on Node 24.22.1. No VDI source or assertion was changed in WP-04C2E.

## PostgreSQL integration

The exact CI step was simulated against a disposable `postgres:17` container
with `POSTGRES_DB=SitesIndex`, `POSTGRES_USER=postgres`,
`POSTGRES_PASSWORD=postgres`, the explicit opt-in, and the service DSN. Output:

```text
=== RUN   TestPostgresMCPMutationDurableStore
--- PASS: TestPostgresMCPMutationDurableStore (2.36s)
PASS
ok  	github.com/pzaino/thecrowler/pkg/database	2.362s
```

`grep` confirmed `=== RUN` and `--- PASS`; no `--- SKIP` occurred. The test
created and dropped only its own randomly named child database. The
maintenance database received no sentinel DDL/DML. This is local evidence; the
GitHub workflow was not run because the patch was not pushed.

## Inert proposal and zero-mutation evidence

Targeted verbose run (`wp04c2e-inert.log`):

```text
--- PASS: TestMCPMutationProposalToolUsesDurableStoreWithoutExecution
--- PASS: TestMCPMutationClassificationAndProposalIsInert
--- PASS: TestMCPMutationSourceScopeIsEnforced
--- PASS: TestMCPMutationProposalThroughToolLoopAndGoalStaysInert
```

Those tests execute the proposal adapter/tool loop and assert the isolated
mutating fixture counter file remains empty. They also prove a proposal is not
treated as goal-success evidence. The source-scope test rejects an out-of-scope
source while leaving the mutating fixture uncalled.

`TestMCPMutationLifecycleNeverCreatesDispatchAttempts` (new): PASS. It admits a
proposal, issues an approval, denies the proposal, then asserts:

- `MCPMutationDispatchAttempts` has zero rows;
- no proposal entered `dispatching`, `uncertain` or
  `reconciliation_required`.

A repository-wide non-test search found no production call to
`NewMCPMutationProposalTool` or `RegisterMutation` for the MCP proposal tool.
The only production mutation registrations are the native crawl-request tool.
No MCP dispatcher, dispatch claim, retry, reconciliation worker or execution
endpoint exists. The attempts API remains an empty `dispatch_enabled:false`
response.

## Skips and environmental limits

- Live Ollama mutation test: not run; no configured live Ollama service was
  used. It is not counted as a pass.
- GitHub Actions: not run for this worktree; no push was made. The dedicated
  MCP PostgreSQL step and the VDI check have local evidence only.
- Shared/production database migration: not run.
- Information Seed Compose acceptance suite: not run; it is a separate opt-in
  suite, not part of the normal eight checks.
- Full Docker Compose application stack: not run.

## Patch validation

The patch was generated with:

```sh
git diff --binary --full-index --no-ext-diff 5846ef53
```

It was applied to a clean `git archive 5846ef53` tree:

| Check | Result | Log |
| --- | --- | --- |
| `git apply --check` | PASS | `wp04c2e-patchcheck-apply.log` |
| Applied-tree `go test ./... -count=1` | PASS | `wp04c2e-patchcheck-full.log` |
| Applied-tree `go vet ./...` and integration-tag vet | PASS | `wp04c2e-patchcheck-full.log` |
| Applied-tree `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2e-patchcheck-full.log` |
| Applied-tree MCP PostgreSQL exact CI step | PASS (`RUN`/`PASS`, no `SKIP`) | `wp04c2e-patchcheck-pg.log` |
| Applied-tree focused race | PASS, no data race | `wp04c2e-patchcheck-race.log` |

The final patch hash and changed-file list are reported in the handoff. The
tracked report intentionally does not embed the hash, because adding it would
change the patch it describes.

# WP-04C2L Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Actual HEAD: `904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp04c2l`
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker client/server: `29.7.2-ce` host; disposable dind `29.9.3`
- Docker Compose: `v5.6.1`
- PostgreSQL: `17` and `15.10-bookworm`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

Logs are under `/home/pzaino/tmp/opencode/`; E2E artifacts are under
`/tmp/opencode/wp04c2l-e2e-dind7/`.

## Required Go matrix

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/agent ./pkg/database ./services/api ./services/events -count=1` | PASS | `wp04c2l-final-suite3.log` |
| `go test ./... -count=1` | PASS | `wp04c2l-final-suite3.log` |
| `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1` | PASS, no data race | `wp04c2l-final-suite3.log` |
| `go vet ./...` | PASS | `wp04c2l-final-static3.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2l-final-static3.log` |
| `gofmt` over changed Go files | PASS | `wp04c2l-final-static3.log` |
| `git diff --check` | PASS | `wp04c2l-final-static3.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2l-final-static3.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2l-final-static3.log` |

## PostgreSQL tagged integration

Both versions ran all five required MCP tests:

- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- `TestPostgresMCPMutationBackupRestoreRehearsal`

| Version | Result | Log |
| --- | --- | --- |
| PostgreSQL 17 | 5/5 PASS | `wp04c2l-pg-final2.log` |
| PostgreSQL 15.10 | 5/5 PASS | `wp04c2l-pg-final2.log` |

No SKIP. The restore test verifies restored audit rows. The PostgreSQL 15
`transaction_timeout` allowance remains limited to that one known statement.

## SQLite and startup compatibility

Focused SQLite foreign-key, migration, startup and deployment tests: PASS.
Log: `wp04c2l-sqlite-vdi.log`.

## VDI deterministic controls

```sh
go test ./pkg/vdi \
  -run '^(TestCamouflageControlsDetectLegacyDefects|TestCamouflageLegacyAudioControlIsDeterministic)$' \
  -count=20
```

PASS on Node `v24.22.1`. Log: `wp04c2l-sqlite-vdi.log`.

## E2E harness safety

Host daemon: 9/9 PASS, `wp04c2l-safety-host2.log`.

Disposable dind: 9/9 PASS, `wp04c2l-safety-dind3.log`, structured result
`/home/pzaino/tmp/opencode/wp04c2l-safety-dind3/safety-results.json`.

Covered: project collision, populated work root, concurrent runs, unsafe
bindings, invalid project, exact cleanup/idempotency, stale PID immunity and
stale image rejection.

## Full isolated E2E

Run on disposable Docker-in-Docker:

`/home/pzaino/tmp/opencode/wp04c2l-e2e-dind7.log`

Structured result:
`/tmp/opencode/wp04c2l-e2e-dind7/a5384e2cdbeac8ee8a7ace7388e66ddc/results.json`

- 60 PASS
- 0 FAIL
- 0 BLOCKED
- 0 SKIP
- cleanup PASS
- no leftover containers, volumes, networks, images or work directories

The fixture built run-scoped API/Events/model images from the current source,
verified source labels at runtime, used dynamic loopback ports, kept PostgreSQL
and models internal, and asserted zero mutation calls / zero dispatch attempts.

## Tests not run

- GitHub Actions itself was not executed.
- Information Seed Compose acceptance suite was **not run**.
- Live Ollama mutation test was not run.
- No production/shared database or production MCP endpoint was contacted.

## Evidence matrix

| Layer | Evidence |
| --- | --- |
| Unit | agent/API approval authority tests |
| Race | `-race` agent/database/API/Events |
| Tagged PostgreSQL | 5 tests on PG17 and PG15 |
| Schema deployment | PostgreSQL add/disable, SQLite migration/FK |
| Full E2E | 60 checks on disposable dind |
| Harness safety | 8 negative tests on host and dind |
| Skipped | Information Seed, live Ollama, GitHub Actions |

## Patch validation

Final patch generation and applied-tree checks are recorded in the delivery
message after the reports are included in the patch.

## Patch applicability

The final patch was generated from the isolated worktree against
`904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`:

```sh
git diff --binary --full-index --no-ext-diff 904f5684 > thecrowler-wp04c2l.patch
```

A fresh shared clone passed `git apply --check`, applied the patch cleanly, and
contained all 14 WP-04C2L/WP-04C2M reports.

Applied-tree verification:

- `go test ./... -count=1` PASS (`wp04c2l-patchcheck-final3.log`)
- `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1`
  PASS (`wp04c2l-patchcheck-final3.log`)
- `go vet ./...`, `go vet -tags=integration ./pkg/database`, `gofmt`,
  `git diff --check`, CGO 0/1 builds PASS (`wp04c2l-patchcheck-static3.log`)

The final patch SHA-256 and file list are reported in the delivery message.

### Applied-tree transient

The first final applied-tree `go test ./...` run hit the known transient
`pkg/ws.TestWebSocketConnectionMessageDeliveryAndCleanup` connection-count
failure outside this change set. The test passed in isolation
(`wp04c2l-ws-rerun.log`) and the complete applied-tree suite passed on immediate
rerun (`wp04c2l-final-applied3.log`). The failing package is unchanged by
WP-04C2L.

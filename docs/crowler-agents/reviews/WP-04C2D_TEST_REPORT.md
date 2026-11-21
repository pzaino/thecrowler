# WP-04C2D Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `1c98d753d298dde4c42fa0555584e8aa863475c4`
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`; Node 20.20.2 and 22.23.5 via disposable Docker images
- PostgreSQL: 15.10 disposable Docker container
- SQLite: `mattn/go-sqlite3 v1.14.52`
- Release: `v2.3.0`
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`

## Commands and results

| Command / group | Result | Evidence |
| --- | --- | --- |
| `go test ./pkg/database ./pkg/agent ./services/api ./pkg/vdi -count=1` | PASS | `/home/pzaino/tmp/opencode/wp04c2d-focused1.log` |
| `go test ./... -count=1` | PASS | `/home/pzaino/tmp/opencode/wp04c2d-full1.log` |
| `go test -race ./pkg/database ./pkg/agent ./services/api -count=1` | PASS, no data race | `/home/pzaino/tmp/opencode/wp04c2d-race1.log` |
| `gofmt -l pkg services cmd`, `git diff --check` | PASS | `/home/pzaino/tmp/opencode/wp04c2d-static1.log` |
| `go vet ./...`, `go vet -tags integration ./pkg/database` | PASS | `/home/pzaino/tmp/opencode/wp04c2d-static1.log` |
| `CGO_ENABLED=0/1 go build ./...` | PASS | `/home/pzaino/tmp/opencode/wp04c2d-static1.log` |
| VDI legacy control `-count=20` (Node 24) | PASS | `/home/pzaino/tmp/opencode/wp04c2d-vdi.log` |
| VDI deterministic regression test | PASS | `/home/pzaino/tmp/opencode/wp04c2d-vdi.log` |
| VDI legacy control on Node 20 and Node 22 | PASS | `/home/pzaino/tmp/opencode/wp04c2d-vdi-node20-22.log` |
| PostgreSQL disposable integration (`-tags integration`) | PASS | `/home/pzaino/tmp/opencode/wp04c2d-pg-integration2.log` |
| SQLite FK runtime behavior test | PASS | included in focused/full suite |
| SQLite clean install, migration repeat and restart tests | PASS | included in full suite |
| `git apply --check` against a clean `1c98d753` archive | PASS | `/home/pzaino/tmp/opencode/wp04c2d-patchcheck-apply.log` |
| `go test ./... -count=1` in the clean patched tree | PASS | `/home/pzaino/tmp/opencode/wp04c2d-patchcheck-full.log` |
| `go test -race ./pkg/database ./pkg/agent ./services/api -count=1` in that tree | PASS, no data race | `/home/pzaino/tmp/opencode/wp04c2d-patchcheck-race.log` |

## VDI triage

The reviewed commit's negative control failed intermittently because the legacy
AudioBuffer noise (`Math.random()*1e-7`) was below float32 resolution at the
probe's `1.0` baseline. The harness now resets the live store to `REFBASE`
(`0.123456789`) before the snapshot. The assertion remains intact; a focused
regression test runs the legacy control five times.

Local reproduction now passes 20 consecutive runs on Node 24, and single runs
on Node 20/22. Docker-backed `-count=10` Node 20/22 attempts exceeded the Go
test 10-minute limit due per-invocation container startup; this is environmental
overhead and is not reported as a test failure.

## PostgreSQL safety

The integration test creates a uniquely named database, applies the additive
migration and clean-install MCP block, creates its sentinel table inside that
disposable database, closes test pools, and drops only that database. It never
creates sentinel tables in the maintenance database, never truncates a
caller-selected database, and reports cleanup failure as a test error.

## Zero-mutation invariant

- No code calls a mutating remote MCP `tools/call`.
- No dispatcher, dispatch claim, execution backend, retry or reconciliation
  worker was added.
- `MCPMutationDispatchAttempts` remains unused; tests assert zero rows where
  relevant.
- Proposal/approval records remain inert and cannot satisfy goal completion.

## Not run / environmental

- Live Ollama proposal test: no configured Ollama service; skipped, not counted
  as a pass.
- GitHub CI: not rerun because the worktree is not pushed. The reviewed
  `build` check remains unverified on GitHub; local reproduction and fix are
  recorded in `WP-04C2D_CI_TRIAGE.md`. Do not claim green CI.
- Shared/production schema migration: not run.
- Full Docker Compose stack: not run.

## Remaining risks

- Public mutation policy contract is not approved; producer registration is
  disabled.
- Authenticated principal propagation is not approved/implemented; background
  runs have no service identity.
- SQLite runtime foreign-key enforcement remains disabled by default.
- Current-policy revalidation at approval/dispatch and denied-admission audit
  remain future work.
- Dispatch/execution is a separate milestone.

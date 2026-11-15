# Async Goal B2A Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base/working SHA: `cd7198caa9c576056c546b428b737915ca278da8`
- Platform: `linux/amd64`
- Go: `go1.27.3`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

## Focused async kernel

```text
go test ./pkg/agent -run '^TestAsyncGoal' -count=1 -json
```

PASS, exit 0.

- 110 test/subtest cases
- 111 PASS events
- 34 top-level tests
- 0 FAIL
- 0 SKIP

Log: `/home/pzaino/tmp/opencode/crowler-async-focused.json`

The matrix covers:

- logical lifetime longer than two minutes with bounded slices;
- exact logical-deadline refusal and no deadline extension;
- counter preservation across waiting/resume/complete;
- negative, oversized, backwards and extended lifetime inputs;
- strict v2 wire round trips and digest determinism;
- malformed source/tool/evidence/verifier/audit arrays rejected, not normalized;
- duplicate JSON keys, unknown fields, unknown versions, bad timestamps,
  trailing data and oversized input;
- trusted scheduling prerequisite admission;
- bare work reference, wrong run/goal/spec/agent/owner/principal/source/
  request/proposal, invalid approval, completed-at-scheduling and missing
  operation rejection;
- proposal `approval_required` representation without a submitted operation;
- submission-valid approval expiring before later completion;
- completion after approval expiry but before logical deadline;
- completion after logical deadline refused;
- fabricated observed time and mismatched source/proposal/operation rejected;
- replay/revision conflicts and terminal closure;
- secret/raw-content leak checks;
- defensive copying and concurrency.

## Race

```text
go test -race ./pkg/agent -run '^TestAsyncGoal' -count=1
```

PASS, exit 0, no data race.

Log: `/home/pzaino/tmp/opencode/crowler-async-race.log`

## Package regressions

```text
go test ./pkg/agent/... -count=1
go test ./pkg/config/... -count=1
go test ./pkg/database/... -count=1
```

PASS, exit 0 for all.

Log: `/home/pzaino/tmp/opencode/crowler-async-agent-config-db.log`

## Full repository

```text
go test ./... -count=1 -json
```

PASS, exit 0.

- 4445 PASS
- 0 FAIL
- 20 SKIP

The skips are pre-existing optional/live or package-level skips:

- 9 live Ollama agent tests;
- 1 MCP fixture process test;
- fuzz seed/corpus skip events in `pkg/config` and `pkg/ruleset`;
- executable packages with no test files.

No new async-goal test skips.

Log: `/home/pzaino/tmp/opencode/crowler-async-full.json`

## Static and build gates

```text
go vet ./...
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
gofmt -l pkg/agent/goal_async*.go
git diff --check
```

PASS, exit 0.

Log: `/home/pzaino/tmp/opencode/crowler-async-static.log`

## Explicitly not run

- No PostgreSQL/SQLite goal-checkpoint store, migration, CAS or restart test.
  No store exists in this patch.
- No worker, claim, lease, event consumer or resume engine.
- No live Ollama, real MCP endpoint, crawler or hosted GitHub CI.
- No production or shared database operation.
- No remote mutating MCP call.

## Evidence classification

All results above are local on the stated SHA and platform. No GitHub workflow
run exists for this uncommitted patch. Unit/race tests do not prove database
atomicity, crash recovery or live resume authorization.

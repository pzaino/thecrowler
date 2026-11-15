# Async Goal Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Baseline and working SHA: `57bbf41b0da2fc7df132d9b95f7a37a464219742`
- Platform: `linux/amd64`
- Go: `go1.27.3`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

## Focused kernel tests

```text
go test ./pkg/agent -run 'TestAsyncGoal' -count=1 -v
```

Result: PASS, exit 0.

- 69 test/subtest cases
- 70 PASS events (69 tests plus the package result)
- 26 top-level tests
- 0 FAIL
- 0 SKIP

Log: `/home/pzaino/tmp/opencode/crowler-async-focused-v.log`

The focused tests cover:

- created -> running -> awaiting approval;
- proposal versus approved/scheduled operation;
- scheduled operation versus observed completed operation;
- verifier-backed `criteria_met`;
- no model text or boolean can complete a goal;
- all terminal and illegal transitions;
- unknown states and transition kinds;
- replay and revision conflict;
- owner/agent/run/goal/spec/ledger/principal/policy/source binding;
- work kind/state/identity contradictions;
- evidence identity, scope, size and verifier-evidence binding;
- cumulative counters, ceilings, slices, retries, scheduled work, consecutive
  denials and deadline;
- cancellation and post-deadline behavior;
- serialization round-trip, unknown fields, version mismatch, oversize and
  trailing data;
- defensive copying and request/checkpoint immutability;
- concurrent pure transitions;
- no tenant/token/prompt/argument/payload fields;
- secret leakage checks;
- no side-effect imports.

## Race test

```text
go test -race ./pkg/agent -run 'TestAsyncGoal' -count=1
```

Result: PASS, exit 0, no data race.

Log: `/home/pzaino/tmp/opencode/crowler-async-race.log`

## Existing agent tests

```text
go test ./pkg/agent/... -count=1
go vet ./pkg/agent/...
```

Result: PASS, exit 0.

Logs:

- `/home/pzaino/tmp/opencode/crowler-async-agent-config.log`
- `/home/pzaino/tmp/opencode/crowler-async-baseline-vet.log` (baseline vet
  produced no output; exit 0 recorded in the shell)

The existing goal/acceptance, tool-loop, MCP proposal/approval, crawl
authorization and evidence tests remain unchanged and pass.

## Configuration and full repository

```text
go test ./pkg/config/... -count=1
go test ./... -count=1
```

Result: PASS, exit 0.

Full-suite JSON accounting:

- 4404 PASS
- 0 FAIL
- 20 SKIP

The 20 skips are pre-existing optional/live or package-level skips:

- 9 live Ollama agent tests (`*LiveOllama*Optional*` and Ollama connectivity);
- 1 MCP fixture process test;
- fuzz seed/corpus skip events in `pkg/config` and `pkg/ruleset`;
- executable packages with no test files (`rebuildTimeSeriesCardinality`,
  fixture-server, hashpw, probe, fake-model, fixture-mcp).

No new async-goal test skips.

Logs:

- `/home/pzaino/tmp/opencode/crowler-async-full-json.log`

## Static verification

```text
go vet ./...
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
gofmt -l pkg/agent/goal_async_state.go pkg/agent/goal_async_state_test.go
git diff --check
```

Result: PASS, exit 0.

Log: `/home/pzaino/tmp/opencode/crowler-async-static.log`

## What was not run

- No database migration, store, PostgreSQL/SQLite persistence test or restart
  rehearsal. This package adds no durable store.
- No background worker, claim, lease, event consumer, scheduler or resume test.
  Those are later approved phases.
- No live Ollama, real MCP endpoint, crawler, Docker E2E or hosted GitHub CI.
- No the later persistence, resume and public-contract phases implementation.

## Evidence classification

These are local test results on the stated SHA and platform. No GitHub CI run
exists for this uncommitted change. A local pass is not a hosted CI claim.

## Patch applicability

The final patch is generated from this worktree against
`57bbf41b0da2fc7df132d9b95f7a37a464219742`. The exact patch SHA-256 and
fresh-clone `git apply --check` result are in the delivery message because this
report is contained in the patch.

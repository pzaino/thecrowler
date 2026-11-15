# Async Goal B2A Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Checked HEAD: `cd7198caa9c576056c546b428b737915ca278da8`
- Previous reviewed HEAD: `57bbf41b0da2fc7df132d9b95f7a37a464219742`
- Working tree at inspection: clean
- Go: `go1.27.3 linux/amd64`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

The checked HEAD is the commit that adds the prior internal async goal kernel.
It is byte-identical to the reviewed prior implementation.

The named file
The independent-review file named in the task is not present in the repository
or the local task directory. The four defects in this assignment
were therefore confirmed directly against the committed source; no review text
was invented.

## Baseline checks

```text
go test ./pkg/agent -run '^TestAsyncGoal' -count=1   PASS
go test ./pkg/agent/... -count=1                     PASS
go vet ./pkg/agent/...                               PASS
git diff --check                                     PASS
```

Logs:

- `/home/pzaino/tmp/opencode/crowler-async-baseline-focused.log`
- `/home/pzaino/tmp/opencode/crowler-async-baseline-agent.log`
- `/home/pzaino/tmp/opencode/crowler-async-baseline-vet.log`

## Confirmed defects at HEAD

### 1. Whole-goal deadline inherited from the synchronous slice ceiling

At `cd7198c`, `newGoalAsyncCheckpoint` lines 687-702 computed the logical
deadline from `spec.Budgets.MaxElapsed`, whose hard maximum is two minutes.
A crawl or operator wait longer than two minutes could not be represented even
though the checkpoint is supposed to model a long-lived logical goal.

### 2. Read-boundary normalization

`parseGoalAsyncCheckpoint` lines 1967-1984 called
`normalizeGoalAsyncCheckpoint` before `validateGoalAsyncCheckpoint`. The
normalizer sorts and deduplicates allowed sources/tools, evidence source IDs,
verifier evidence IDs and audit refs. A malformed persisted snapshot could be
silently transformed into a different valid checkpoint. Standard
`DisallowUnknownFields` also does not reject duplicate JSON object keys.

### 3. Scheduling admission accepted a bare work reference

`applyGoalAsyncTransition` lines 1696-1701 admitted
`awaiting_external_work` from a structurally valid `goalAsyncWorkRef` without a
separate trusted scheduling admission. A durable-looking operation ID alone
could therefore be represented as scheduled work.

### 4. Completion was incorrectly bounded by submission expiry

`goalAsyncWorkRef.validate` lines 479-481 required a completed operation's
`ObservedAt` to be before `ExpiresAt`. `ExpiresAt` was one ambiguous field; in
the actual repository it corresponds to proposal/approval/request submission
validity, not to the later crawler completion. A crawl that completed after
approval expiry but before the logical goal deadline could not be represented.

## Existing interfaces inspected

- `pkg/agent/goal_async_state.go` and its tests.
- `pkg/agent/goal_types.go`, `goal_spec.go`, `goal_eval.go`,
  `goal_evidence.go`, `action_goal_run.go`.
- `pkg/agent/crawl_request.go`, `crawl_request_tool.go`,
  `crawl_request_tool_contract.go`.
- `pkg/database/crawl_proposal.go`, `crawl_approval.go`,
  `source_crawl_schedule_request.go`.
- `services/events/agent_run.go` and `pkg/crawler/crawler.go` event emission.
- `docs/crowler-agents/reviews/ASYNC_GOAL_*.md`.
- `docs/crowler-agents/crawl_request_authorization.md`,
  `crawl_scheduling_foundation.md`.
- `schemas/crowler-agent-schema.json` and the agent examples (read-only).

## Confirmed repository timestamp semantics

- `CrawlProposals.ExpiresAt`: proposal validity, required to still be in the
  future when a new schedule is submitted.
- `CrawlApprovals.ProposalExpiresAt`, `ApprovedAt`, `ExpiresAt`: approval
  validity at submission. `validateCrawlApprovalForScheduling` requires the
  approval, proposal and request expiry to be valid at scheduling time.
- `CrawlScheduleRequest.ExpiresAt`: the request's submit-by expiry, constrained
  by the proposal and approval expiry.
- `Status.created_at`: durable operation creation time (the accepted-at fact).
- `Status.last_updated_at` and source state: later operation progress, not
  proof that a specific operation completed.
- `crawl_completed` events carry `SourceID` and crawler status details, not the
  goal run ID, request ID, proposal hash or operation ID. They are not direct
  proof of the same durable operation.

## Gate state

No database migration, table, worker, event route, public manifest field, MCP
dispatch or remote mutation exists. The persistence and resume phases remain
pending maintainer approval.

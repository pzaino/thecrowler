# Async Goal Foundation Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Checked HEAD: `57bbf41b0da2fc7df132d9b95f7a37a464219742`
- Working tree at inspection: clean
- Go: `go1.27.3 linux/amd64`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Default operator mutation policy: disabled
- Execution backend: `none`

No commit exists between the task's inspected SHA and the actual checkout. No
reconciliation was required.

## Existing interfaces reused

The kernel reuses, rather than redefines:

- `AgentGoalSpec`, including `SpecHash`, `AllowedSources`, `Allowlist`,
  `Evidence`, `Budgets` and `Acceptance` (`pkg/agent/goal_types.go`,
  `pkg/agent/goal_spec.go`).
- `GoalBudgets` and its hard bounds (`pkg/agent/goal_types.go`).
- `GoalEvalInput`, `EvaluationResult`, `CriterionResult` and `EvaluateGoal`
  (`pkg/agent/goal_eval.go`).
- `EvidenceRecord`, `EvidenceSummary` semantics, `selectorHash`, `recordHash`
  and the trusted trust-class vocabulary (`pkg/agent/goal_evidence.go`).
- `AgentIdentity` (`pkg/agent/setup.go`).
- `MCPMutationPrincipal` and `CrawlPrincipal` identity shapes
  (`pkg/agent/mcp_mutation.go`, `pkg/agent/crawl_authorization.go`).
- `CrawlRequestProposal` and `CrawlRequestApproval` proposal/approval
  identities (`pkg/agent/crawl_request.go`).
- `CrawlProposalRow`, `CrawlApprovalRow`, `CrawlOperationState`,
  `CrawlScheduleRequest` and `CrawlScheduleResult`
  (`pkg/database/crawl_proposal.go`, `pkg/database/crawl_approval.go`,
  `pkg/database/source_crawl_schedule_request.go`).
- The existing `Status` operation record keyed by `status_sha256`, whose
  `details` JSON carries `request_id`, `run_id`, `principal_user_id`,
  `agent_id`, `owner`, `source_id`, `operation_type`, `approval_id`,
  `approval_sha256`, `approved_by_user_id`, `approved_at`, `expires_at` and
  state information.

The kernel does not call any database, network, scheduler, crawler, MCP or
model interface. Its only evaluator call is the existing pure `EvaluateGoal`.
It accepts the other trusted values through the pure checkpoint constructor,
verifier decision and resume grant.

## Existing behavior preserved

No changes were made to:

- `pkg/agent/action_goal_run.go`;
- `pkg/agent/goal_types.go`;
- `pkg/agent/goal_spec.go`;
- `pkg/agent/goal_eval.go`;
- `pkg/agent/goal_evidence.go`;
- `pkg/agent/action_tool_loop.go` or `AIInteraction`;
- `schemas/crowler-agent-schema.json` or agent example manifests;
- `services/events`, `services/api`, the crawler, scheduler, MCP client or
  database packages;
- migrations, schema version, writer generation or deployment files.

`AgentGoalRun` remains the bounded synchronous loop. It still maps only
`GoalCriteriaMet` to action success and every other outcome to a typed error.
`EvaluateGoal` remains the sole deterministic acceptance evaluator.

## Plan requirements addressed by this chunk

The original plan's asynchronous Layer B requirements include durable
goal-run state, checkpoints, total-life budgets, authorization on resume and
safe recovery. This chunk establishes the frozen internal contract and pure
transition rules for those properties. It deliberately stops before:

- durable storage;
- migration/schema changes;
- resume workers, claims, leases, timers or poll loops;
- event consumers or event correlation;
- public manifest, API or configuration changes.

## Baseline verification

Before implementation:

```text
go test ./pkg/agent/... -count=1   PASS
go vet ./pkg/agent/...             PASS
```

Logs:

- `/home/pzaino/tmp/opencode/crowler-async-baseline-agent.log`
- `/home/pzaino/tmp/opencode/crowler-async-baseline-vet.log`

The full final verification is in `ASYNC_GOAL_TEST_REPORT.md`.

## Report location

The tracked reports are under `docs/crowler-agents/reviews/`. The repository's
`artifacts/agentic/` directory is ignored/untracked in this checkout; it is not
part of the reviewable patch and was not modified. No duplicate ignored status
document was created.

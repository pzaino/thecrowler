# WP-04C2K Context Propagation

## Problem

The manual-run HTTP handler checked `r.Context().Err()` before starting, then
called `ExecuteManualAgentResult` without the request context. Group budgets,
tool-loop deadlines and goal-run deadlines were derived from
`context.Background()`. A disconnected client could therefore leave agent work,
model calls, MCP initialization and proposal admission running.

## Trusted parent context

`AgentExecutionContext` now carries an unexported-to-serialization
`Context context.Context` field
(`pkg/agent/execution_runtime.go`). It is supplied by the trusted caller:

- `ExecuteManualAgentResultContext` receives the HTTP request context
  (`services/events/agent_run.go:126`);
- background event paths set `context.Background()`, preserving prior behavior;
- `ExecuteManualAgentResult` remains as a compatibility wrapper that supplies
  `context.Background()`.

The context is never serialized into job parameters, manifests, event payloads
or model output.

## Action dispatch

`ContextAction` (`pkg/agent/agent.go:270`) is an optional action extension:

```go
ExecuteContext(ctx context.Context, params map[string]interface{})
```

`executeActionWithContext` (`pkg/agent/setup.go:1308`) uses it when present and
falls back to `Execute` for legacy actions. The action context comes from
`actionExecutionContext` (`pkg/agent/setup.go:1298`), which prefers the group
budget context and otherwise uses the execution context.

Both the normal action path and the retry path use the context. Retry backoff now
selects on the parent context instead of sleeping unconditionally
(`executeWithRetryBudget`, `pkg/agent/setup.go:1328`).

## Budgets

`newConstraintBudgetManagerWithContext`
(`pkg/agent/execution_runtime.go:72`) derives the group context from the
trusted parent. A group time budget uses `context.WithDeadline(parent, ...)`,
so the effective deadline is the earlier of the parent and the group budget.
`newConstraintBudgetManager` remains as a background-context wrapper.

## Tool loop and goal run

- `AgentToolLoopAction.ExecuteContext`
  (`pkg/agent/action_tool_loop.go:46`) rejects an already-cancelled context,
  passes it to `normalizeToolLoopConfigWithContext`
  (`pkg/agent/action_tool_loop.go:128`) and builds the run deadline from that
  parent.
- The tool loop's MCP manager initialization, provider calls, tool execution and
  proposal registration/admission all use the resulting `runCtx`.
- `AgentGoalRunAction.ExecuteContext`
  (`pkg/agent/action_goal_run.go:48`) builds its run deadline from the parent
  and propagates it through goal steps and MCP registration.
- `DecisionAction.ExecuteContext` delegates through
  `ExecuteAgentResultContext`, and `ExecuteAgentResultContext` (`pkg/agent/setup.go:728`) sets the parent
  on the delegated `AgentExecutionContext`.
- `AIInteractionAction.ExecuteContext` calls the provider through
  `executeProviderCall`, so the model transport receives the trusted context.

The effective deadline is never later than the earliest trusted deadline:
request cancellation, agent/group time budget, tool-loop timeout, goal-run
timeout and proposal TTL constraints are all expressed as derived contexts.

## Cancellation points covered

- before agent execution starts;
- while an action is running;
- while a model provider request is in flight;
- during MCP manager initialization;
- during proposal tool registration;
- immediately before durable proposal admission;
- while waiting to retry a failed action.

## Tests

`pkg/agent/context_propagation_test.go` covers:

- `TestManualAgentContextCancelledBeforeExecution`
- `TestManualAgentContextCancellationReachesAction`
- `TestManualAgentContextCancellationReachesProvider`
- `TestManualAgentConcurrentContextsDoNotLeak`
- `TestMCPProposalCancelledBeforeAdmissionDoesNotInsert`
- `TestMCPManagerInitializationHonoursCancellation`
- `TestRegistrationHonoursCancelledContext`
- `TestDatabaseAdmissionCancelledBeforeCommitDoesNotInsert`
- `TestAIInteractionCancellationReachesProvider`
- `TestExecuteAgentResultContextReachesDelegatedAgent`

The live E2E fixture disconnects a slow manual run while the fake model is
delayed. The fake model's `/stats` endpoint records a cancelled request, and the
database confirms no proposal was inserted. The same slow agent is then run
without cancellation and must admit exactly one inert proposal, proving the
cancellation assertion is not vacuous.

The work package does not claim that a transaction which already committed can
be retrospectively undone. The tests assert that cancellation observed before
commit prevents insertion.

package agent

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// flakyAction fails failFirst times, then succeeds. calls counts attempts.
type flakyAction struct {
	name      string
	failFirst int
	calls     int
	errMsg    string
}

func (a *flakyAction) Name() string { return a.name }

func (a *flakyAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	a.calls++
	if a.calls <= a.failFirst {
		if a.errMsg == "" {
			a.errMsg = "flaky failure"
		}
		return map[string]interface{}{StrStatus: StatusError, StrResponse: nil}, stringErr(a.errMsg)
	}
	return map[string]interface{}{StrStatus: StatusSuccess, StrResponse: map[string]interface{}{}}, nil
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

// TestRetryAttemptsConsumeGroupBudget: retries within a group must consume
// that group's max_steps instead of retrying without bound. The failed
// initial attempt consumes the single unit, so no retry may even start.
func TestRetryAttemptsConsumeGroupBudget(t *testing.T) {
	engine := NewJobEngine()
	flaky := &flakyAction{name: "AIInteraction", failFirst: 100}
	engine.RegisterAction(flaky)

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "retry-agent", Name: "Retry Agent", TrustLevel: "trusted",
			Capabilities: []string{"ai_reasoning"},
			Constraints:  &AgentConstraints{MaxSteps: 1},
		},
		Jobs: []Job{{Name: "Retry Agent", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{{
				"action": "AIInteraction",
				"params": map[string]interface{}{},
				"retry":  map[string]interface{}{"max_retries": 5, "base_delay": "1ms"},
			}}}},
	}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected max_steps exhaustion from retries, got: %v", err)
	}
	// Only the charged initial attempt ran; the budget denied every retry.
	if flaky.calls != 1 {
		t.Fatalf("expected 1 attempt (initial only), got %d", flaky.calls)
	}
}

// budgetTestAgent builds a single-group enforced agent for budget tests.
func budgetTestAgent(id, name string, caps []string, maxSteps int, contract *AgentContract, steps ...map[string]any) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: id, Name: name, TrustLevel: "trusted",
			Capabilities: caps,
			Constraints:  &AgentConstraints{MaxSteps: maxSteps},
			Contract:     contract,
		},
		Jobs: []Job{{
			Name: name, Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: steps,
		}},
	}
}

// budgetStep builds one step; extra pairs attach keys like retry/fallback.
func budgetStep(action string, extra ...any) map[string]any {
	step := map[string]any{"action": action, "params": map[string]any{}}
	for i := 0; i+1 < len(extra); i += 2 {
		if key, ok := extra[i].(string); ok {
			step[key] = extra[i+1]
		}
	}
	return step
}

// TestFailedInitialWithContinueChargesBudget: a failed initial call with
// failure handling that continues must still consume its attempt, denying
// the next action once max_steps is exhausted.
func TestFailedInitialWithContinueChargesBudget(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&flakyAction{name: "AIInteraction", failFirst: 100})
	engine.RegisterAction(&testStepAction{name: "APIRequest"})
	agentCfg := budgetTestAgent("cont-agent", "Cont Agent",
		[]string{"ai_reasoning", "api_request"}, 1,
		&AgentContract{FailurePolicy: "continue"},
		budgetStep("AIInteraction"), budgetStep("APIRequest"))
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected the second action to be denied, got: %v", err)
	}
}

// TestRetryConsumesTwoUnitsThenDeniesThird: with MaxSteps=2 a failed initial
// call plus one retry consumes both units; the next retry is denied.
func TestRetryConsumesTwoUnitsThenDeniesThird(t *testing.T) {
	engine := NewJobEngine()
	flaky := &flakyAction{name: "AIInteraction", failFirst: 100}
	engine.RegisterAction(flaky)
	agentCfg := budgetTestAgent("retry2-agent", "Retry2 Agent",
		[]string{"ai_reasoning"}, 2, nil,
		budgetStep("AIInteraction", "retry", map[string]any{"max_retries": 5, "base_delay": "1ms"}))
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected max_steps exhaustion, got: %v", err)
	}
	if flaky.calls != 2 {
		t.Fatalf("expected 2 attempts (initial + 1 retry), got %d", flaky.calls)
	}
}

// TestRetrySuccessChargesEachAttempt: one failure plus one successful retry
// consumes two units; no double charge on the successful retry.
func TestRetrySuccessChargesEachAttempt(t *testing.T) {
	engine := NewJobEngine()
	flaky := &flakyAction{name: "AIInteraction", failFirst: 1}
	engine.RegisterAction(flaky)
	agentCfg := budgetTestAgent("ok-agent", "OK Agent",
		[]string{"ai_reasoning"}, 2, nil,
		budgetStep("AIInteraction", "retry", map[string]any{"max_retries": 3, "base_delay": "1ms"}))
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected success via one retry, got %v", err)
	}
	if flaky.calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", flaky.calls)
	}
}

// TestFailedCreateEventConsumesRateLimit: failed emissions count against
// event_rate_limit so errors cannot bypass it.
func TestFailedCreateEventConsumesRateLimit(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&flakyAction{name: "CreateEvent", failFirst: 100})
	agentCfg := budgetTestAgent("ev-agent", "Ev Agent",
		[]string{"emit_event"}, 10, &AgentContract{FailurePolicy: "continue"},
		budgetStep("CreateEvent"), budgetStep("CreateEvent"))
	agentCfg.AgentIdentity.Constraints.EventRateLimit = 1
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "event_rate_limit exceeded") {
		t.Fatalf("expected rate-limit denial after a failed emission, got: %v", err)
	}
}

// TestFallbackSharesGroupBudget: fallback steps must be charged to the same
// group budget instead of getting a fresh one.
func TestFallbackSharesGroupBudget(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	engine.RegisterAction(&flakyAction{name: "APIRequest", failFirst: 100})

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "fb-agent", Name: "FB Agent", TrustLevel: "trusted",
			Capabilities: []string{"ai_reasoning", "api_request"},
			Constraints:  &AgentConstraints{MaxSteps: 2},
		},
		Jobs: []Job{{Name: "FB Agent", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{}},
				{
					"action": "APIRequest",
					"params": map[string]interface{}{},
					"fallback": []map[string]interface{}{
						{"action": "AIInteraction", "params": map[string]interface{}{}},
						{"action": "AIInteraction", "params": map[string]interface{}{}},
					},
				},
			}}},
	}

	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected fallback to hit the shared group budget, got: %v", err)
	}
}

// TestParallelGroupsIndependentBudgets: each parallel group owns its budget;
// sibling execution must not consume it.
func TestParallelGroupsIndependentBudgets(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})

	mkGroup := func(name string) Job {
		return Job{Name: name, Process: "parallel", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{}},
				{"action": "AIInteraction", "params": map[string]interface{}{}},
			}}
	}
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "par-agent", Name: "Par Agent", TrustLevel: "trusted",
			Capabilities: []string{"ai_reasoning"},
			Constraints:  &AgentConstraints{MaxSteps: 2},
		},
		Jobs: []Job{mkGroup("Group A"), mkGroup("Group B")},
	}

	// A global budget of 2 could not cover 4 steps; per-group budgets can.
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected parallel groups to use independent budgets, got %v", err)
	}
}

// TestCallerDelegationChargedOnce: the delegation step consumes exactly one
// unit of the caller's group budget; a second local step then exhausts it.
func TestCallerDelegationChargedOnce(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	callee := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "callee", Name: "Callee", TrustLevel: "trusted",
			Capabilities: []string{"all"}, Constraints: &AgentConstraints{MaxSteps: 5}},
		Jobs: []Job{{Name: "Callee", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}}}},
	}
	caller := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "caller", Name: "Caller", TrustLevel: "trusted",
			Capabilities: []string{"delegate", "ai_reasoning"}, Constraints: &AgentConstraints{MaxSteps: 1}},
		Jobs: []Job{{Name: "Caller", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{"condition": map[string]interface{}{
					"condition_type": "if", "expression": "true",
					"on_true": map[string]interface{}{"agent_id": "callee"},
				}}},
				{"action": "AIInteraction", "params": map[string]interface{}{}},
			}}},
	}
	AgentsRegistry.RegisterAgent(caller)
	AgentsRegistry.RegisterAgent(callee)

	err := engine.ExecuteAgent("caller", runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected caller budget to be exhausted after delegation, got: %v", err)
	}
}

// TestCalleeGroupsIndependentlyMetered: the callee's groups are metered by
// the callee's own budget under propagated enforcement flags.
func TestCalleeGroupsIndependentlyMetered(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	callee := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "callee2", Name: "Callee2", TrustLevel: "trusted",
			Capabilities: []string{"all"}, Constraints: &AgentConstraints{MaxSteps: 1}},
		Jobs: []Job{{Name: "Callee2", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{}},
				{"action": "AIInteraction", "params": map[string]interface{}{}},
			}}},
	}
	caller := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "caller2", Name: "Caller2", TrustLevel: "trusted",
			Capabilities: []string{"delegate"}, Constraints: &AgentConstraints{MaxSteps: 5}},
		Jobs: []Job{{Name: "Caller2", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "Decision", "params": map[string]interface{}{"condition": map[string]interface{}{
					"condition_type": "if", "expression": "true",
					"on_true": map[string]interface{}{"agent_id": "callee2"},
				}}},
			}}},
	}
	AgentsRegistry.RegisterAgent(caller)
	AgentsRegistry.RegisterAgent(callee)

	err := engine.ExecuteAgent("caller2", runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "max_steps exceeded") {
		t.Fatalf("expected callee group budget to be enforced under delegation, got: %v", err)
	}
}

// TestV2EmptyCapabilitiesDenied: an explicit v2 identity without capabilities
// must not default to omnipotent `all` under enforcement.
func TestV2EmptyCapabilitiesDenied(t *testing.T) {
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "bare", Name: "Bare", TrustLevel: "trusted"},
		Jobs: []Job{{Name: "Bare", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}}}},
	}
	def, err := agentCfg.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "test"})
	if err != nil {
		t.Fatalf("normalization failed: %v", err)
	}
	for _, c := range def.Identity.Capabilities {
		if strings.ToLower(strings.TrimSpace(c)) == "all" {
			t.Fatalf("v2 identity without capabilities must not gain `all`")
		}
	}
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err == nil ||
		!strings.Contains(err.Error(), "capability gate denied action AIInteraction") {
		t.Fatalf("expected capability denial for bare v2 identity, got: %v", err)
	}
}

// TestV1DerivedIdentityKeepsAll: legacy jobs-only manifests keep working.
func TestV1DerivedIdentityKeepsAll(t *testing.T) {
	agentCfg := &JobConfig{
		Jobs: []Job{{Name: "Legacy", Process: "serial", TriggerType: "manual", TriggerName: "legacy",
			Steps: []map[string]interface{}{{"action": "AIInteraction", "params": map[string]interface{}{}}}}},
	}
	def, err := agentCfg.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "test"})
	if err != nil {
		t.Fatalf("normalization failed: %v", err)
	}
	if def.FormatVersion != AgentFormatVersionV1 {
		t.Fatalf("expected v1 format version, got %q", def.FormatVersion)
	}
	found := false
	for _, c := range def.Identity.Capabilities {
		if c == "all" {
			found = true
		}
	}
	if !found {
		t.Fatalf("v1 derived identity must retain `all` for compatibility")
	}
}

// TestGroupDeadlineContext: a group's cancellation context enforces the
// elapsed-time deadline even between steps.
func TestGroupDeadlineContext(t *testing.T) {
	bm, err := newConstraintBudgetManager(AgentIdentity{Constraints: &AgentConstraints{TimeBudget: "2ms"}})
	if err != nil {
		t.Fatalf("budget setup failed: %v", err)
	}
	defer bm.release()
	time.Sleep(10 * time.Millisecond)
	if err := bm.preStepCheck("AIInteraction"); err == nil || !strings.Contains(err.Error(), "time_budget exceeded") {
		t.Fatalf("expected time_budget denial from group deadline, got: %v", err)
	}
}

// TestSystemTrustRank: the schema `system` level must rank at the top tier.
func TestSystemTrustRank(t *testing.T) {
	if trustLevelRank("system") != 3 {
		t.Fatalf("expected system rank 3, got %d", trustLevelRank("system"))
	}
	if trustLevelRank("untrusted") != 1 || trustLevelRank("restricted") != 1 {
		t.Fatalf("expected untrusted/restricted rank 1")
	}
	if !trustAllowed(AgentIdentity{TrustLevel: "system"}, "DBQuery") {
		t.Fatalf("system must satisfy sensitive-action trust gates")
	}
	caller := AgentIdentity{AgentID: "s", TrustLevel: "system", Capabilities: []string{"delegate"}}
	callee := AgentIdentity{AgentID: "p", TrustLevel: "privileged"}
	if err := delegationPolicyCheck(caller, callee); err != nil {
		t.Fatalf("system caller must outrank privileged callee, got %v", err)
	}
}

// TestAIUsagePolicyUsesIdentitySnapshot: provider/model contract policies must
// apply to the struct-form snapshot the runtime actually stores.
func TestAIUsagePolicyUsesIdentitySnapshot(t *testing.T) {
	params := map[string]interface{}{
		StrConfig: map[string]interface{}{
			cfgKeyAgentRuntime: map[string]interface{}{
				"identity_enforcement": true,
				"identity_snapshot": AgentIdentity{
					AgentID: "ai", TrustLevel: "trusted",
					Contract: &AgentContract{ForbiddenActions: []string{"provider:bad*"}},
				},
			},
		},
		StrRequest: "hello",
		"provider": "bad-provider",
		"url":      "https://example.com/v1/chat",
		"prompt":   "hi",
	}
	a := &AIInteractionAction{}
	_, err := a.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "AI policy denied provider") {
		t.Fatalf("expected contract provider denial from struct snapshot, got: %v", err)
	}
}

// fakeDBHandler implements cdb.Handler with an unreachable executor; only the
// authorization path is exercised (ExecuteQuery must never be reached).
type fakeDBHandler struct{}

func (f *fakeDBHandler) Connect(c cfg.Config) error { return nil }

func (f *fakeDBHandler) Close() error { return nil }

func (f *fakeDBHandler) Ping() error { return nil }

func (f *fakeDBHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return nil, stringErr("driver unreachable: authorization bypassed")
}

func (f *fakeDBHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return nil, stringErr("unreachable")
}

func (f *fakeDBHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return nil, stringErr("unreachable")
}

func (f *fakeDBHandler) DBMS() string { return "postgres" }

func (f *fakeDBHandler) Begin() (*sql.Tx, error) { return nil, stringErr("unreachable") }

func (f *fakeDBHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return nil, stringErr("unreachable")
}

func (f *fakeDBHandler) Commit(tx *sql.Tx) error { return stringErr("unreachable") }

func (f *fakeDBHandler) Rollback(tx *sql.Tx) error { return stringErr("unreachable") }

func (f *fakeDBHandler) QueryRow(query string, args ...interface{}) *sql.Row { return nil }

func (f *fakeDBHandler) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}

func (f *fakeDBHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, stringErr("unreachable")
}

func (f *fakeDBHandler) CheckConnection(c cfg.Config) error { return nil }

func (f *fakeDBHandler) WaitForConnection(c cfg.Config, totalTimeout time.Duration) error {
	return nil
}

func (f *fakeDBHandler) NewListener() cdb.Listener { return nil }

// TestDBQueryMissingIdentityRejected: enforcement on without a caller identity
// snapshot must deny before touching the driver.
func TestDBQueryMissingIdentityRejected(t *testing.T) {
	var handler cdb.Handler = &fakeDBHandler{}
	params := map[string]interface{}{
		StrConfig: map[string]interface{}{
			"db_handler": handler,
			cfgKeyAgentRuntime: map[string]interface{}{
				"identity_enforcement": true,
			},
		},
		StrRequest: "hello",
		"query":    "SELECT 1",
	}
	a := &DBQueryAction{}
	_, err := a.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "missing caller identity") {
		t.Fatalf("expected missing caller identity denial, got: %v", err)
	}
}

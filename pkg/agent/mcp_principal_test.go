package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	yaml "gopkg.in/yaml.v3"
)

func testPrincipal() MCPMutationPrincipal {
	return MCPMutationPrincipal{UserID: 7, Username: "owner-1", Issuer: "crowler"}
}

func TestMCPMutationPrincipalFromAuthIdentity(t *testing.T) {
	principal, err := MCPMutationPrincipalFromAuthIdentity(auth.Identity{
		Subject: "7", Username: "owner-1", Issuer: "crowler",
	})
	if err != nil || principal != testPrincipal() {
		t.Fatalf("principal = %+v err=%v", principal, err)
	}
	for _, identity := range []auth.Identity{
		{Subject: "", Username: "owner-1", Issuer: "crowler"},
		{Subject: "0", Username: "owner-1", Issuer: "crowler"},
		{Subject: "not-a-number", Username: "owner-1", Issuer: "crowler"},
		{Subject: "7", Username: "", Issuer: "crowler"},
		{Subject: "7", Username: "owner-1", Issuer: ""},
	} {
		if _, err := MCPMutationPrincipalFromAuthIdentity(identity); err == nil {
			t.Fatalf("invalid identity accepted: %+v", identity)
		}
	}
}

func TestWithMCPMutationPrincipalIsTypedAndFailClosed(t *testing.T) {
	input := map[string]any{
		cfgKeyMCPMutationPrincipal: map[string]any{"user_id": float64(99), "username": "forged"},
	}
	WithMCPMutationPrincipal(input, MCPMutationPrincipal{})
	if _, ok := input[cfgKeyMCPMutationPrincipal]; ok {
		t.Fatalf("invalid principal was retained")
	}
	WithMCPMutationPrincipal(input, testPrincipal())
	if got := mcpMutationPrincipalFromInput(input); got != testPrincipal() {
		t.Fatalf("typed principal = %+v", got)
	}
	input[cfgKeyMCPMutationPrincipal] = "forged-string"
	if got := mcpMutationPrincipalFromInput(input); got.Validate() == nil {
		t.Fatalf("string principal was accepted")
	}
}

func TestApplyExecutionContextPublishesOnlyTypedPrincipal(t *testing.T) {
	for _, forged := range []any{
		map[string]any{"user_id": float64(99)},
		"7:owner-1:crowler",
		MCPMutationPrincipal{UserID: 99, Username: "forged", Issuer: "forged"},
	} {
		params := map[string]any{StrConfig: map[string]any{
			cfgKeyAgentRuntime: map[string]any{cfgKeyMCPMutationPrincipal: forged},
		}}
		applyExecutionContext(params, AgentExecutionContext{MCPMutationPrincipal: testPrincipal()}, cfg.AgentRuntimeConfig{IdentityEnforcement: true})
		runtime := mapStringAny(params[StrConfig].(map[string]any)[cfgKeyAgentRuntime])
		got, ok := runtime[cfgKeyMCPMutationPrincipal].(MCPMutationPrincipal)
		if !ok || got != testPrincipal() {
			t.Fatalf("forged principal %#v survived: %#v", forged, runtime[cfgKeyMCPMutationPrincipal])
		}
	}

	params := map[string]any{StrConfig: map[string]any{
		cfgKeyAgentRuntime: map[string]any{cfgKeyMCPMutationPrincipal: testPrincipal()},
	}}
	applyExecutionContext(params, AgentExecutionContext{}, cfg.AgentRuntimeConfig{IdentityEnforcement: true})
	runtime := mapStringAny(params[StrConfig].(map[string]any)[cfgKeyAgentRuntime])
	if _, ok := runtime[cfgKeyMCPMutationPrincipal]; ok {
		t.Fatalf("zero principal left a forged runtime value")
	}
}

func TestMCPMutationAliasRequiresPrincipal(t *testing.T) {
	config := validMCPMutationPolicyConfig()
	if err := SetMCPOperatorConfig(config); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	alias := MCPToolAlias("approved-server", "approved_tool")
	if err := denyMCPMutationAliasesWithoutPrincipal([]string{alias}, MCPMutationPrincipal{}); !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("alias without principal error = %v", err)
	}
	if err := denyMCPMutationAliasesWithoutPrincipal([]string{alias}, testPrincipal()); err != nil {
		t.Fatalf("alias with principal rejected: %v", err)
	}
	if err := denyMCPMutationAliasesWithoutPrincipal([]string{"read-only-alias"}, MCPMutationPrincipal{}); err != nil {
		t.Fatalf("unrelated alias rejected: %v", err)
	}
}

func TestManualJobConfigFiltersNonManualJobs(t *testing.T) {
	var config JobConfig
	manifest := `format_version: v2
agent_identity:
  agent_id: manual-agent
  name: Manual Job
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Manual Job
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
  - name: Event Job
    process: serial
    trigger_type: event
    trigger_name: test_event
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
`
	if err := yaml.Unmarshal([]byte(manifest), &config); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	def, err := config.NormalizeToAgentDefinition(AgentSourceMetadata{Location: "test"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	manual, ok := def.ManualJobConfig()
	if !ok || len(manual.Jobs) != 1 || manual.Jobs[0].Name != "Manual Job" {
		t.Fatalf("manual jobs = %+v ok=%v", manual.Jobs, ok)
	}
}

type principalCaptureAction struct {
	name  string
	mu    sync.Mutex
	calls int
	last  MCPMutationPrincipal
}

func (a *principalCaptureAction) Name() string { return a.name }
func (a *principalCaptureAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	runtime := mapStringAny(mapStringAny(params[StrConfig])[cfgKeyAgentRuntime])
	a.last, _ = runtime[cfgKeyMCPMutationPrincipal].(MCPMutationPrincipal)
	return map[string]interface{}{StrResponse: map[string]any{"ok": true}, StrStatus: StatusSuccess}, nil
}

func TestExecuteManualAgentResultPropagatesPrincipal(t *testing.T) {
	oldRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = oldRegistry })

	var config JobConfig
	manifest := `format_version: v2
agent_identity:
  agent_id: manual-agent
  name: Manual Job
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Manual Job
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
  - name: Event Job
    process: serial
    trigger_type: event
    trigger_name: test_event
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
`
	if err := yaml.Unmarshal([]byte(manifest), &config); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if err := AgentsRegistry.RegisterAgentE(&config); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	engine := NewJobEngine()
	action := &principalCaptureAction{name: "APIRequest"}
	engine.RegisterAction(action)
	iCfg := WithMCPMutationPrincipal(map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
	}, testPrincipal())
	result, err := engine.ExecuteManualAgentResult("manual-agent", iCfg)
	if err != nil {
		t.Fatalf("manual run: %v", err)
	}
	if result == nil {
		t.Fatalf("manual run returned no result")
	}
	action.mu.Lock()
	calls, got := action.calls, action.last
	action.mu.Unlock()
	if calls != 1 {
		t.Fatalf("manual job calls = %d, want 1", calls)
	}
	if got != testPrincipal() {
		t.Fatalf("principal not propagated: %+v", got)
	}
}

func TestEventInputConfigHasNoPrincipal(t *testing.T) {
	iCfg := map[string]any{"event": map[string]any{"detail": "x"}}
	if got := mcpMutationPrincipalFromInput(iCfg); got.Validate() == nil {
		t.Fatalf("event input unexpectedly produced a principal")
	}
	// A forged typed value outside the trusted helper must still be absent
	// unless the trusted caller deliberately installs it.
	if got := mcpMutationPrincipalFromInput(map[string]any{}); got.Validate() == nil {
		t.Fatalf("empty input unexpectedly produced a principal")
	}
	_ = context.Background()
}

type principalCaptureGoalTool struct {
	principal MCPMutationPrincipal
}

func (t *principalCaptureGoalTool) Name() string { return "get_source_status" }
func (t *principalCaptureGoalTool) Description() string {
	return "Capture the trusted runtime principal."
}
func (t *principalCaptureGoalTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *principalCaptureGoalTool) RequiredCapabilities() []string { return nil }
func (t *principalCaptureGoalTool) EvidenceKind() EvidenceKind     { return EvidenceToolObservation }
func (t *principalCaptureGoalTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }
func (t *principalCaptureGoalTool) IsReadOnly() bool               { return true }
func (t *principalCaptureGoalTool) Execute(ctx context.Context, _ map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if ok {
		t.principal = runtime.Principal
	}
	return map[string]any{"observed": true}, nil
}

func TestGoalRunPropagatesTrustedPrincipal(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":7}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)
	if got, ok := getLLMProvider("goal-scripted"); !ok || got != provider {
		t.Fatalf("scripted provider not registered: %#v ok=%v", got, ok)
	}

	registry := NewAgentToolRegistry()
	tool := &principalCaptureGoalTool{}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	spec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	params := goalActionParams(spec, goalTestIdentity(), nil, nil)
	configMap := params[StrConfig].(map[string]any)
	configMap["goal_tool_registry"] = registry
	runtime := configMap[cfgKeyAgentRuntime].(map[string]any)
	runtime[cfgKeyMCPMutationPrincipal] = testPrincipal()
	result, err := (&AgentGoalRunAction{}).Execute(params)
	if err != nil {
		t.Fatalf("goal run: %v (%v) provider_calls=%d output=%+v", err, result[StrMessage], provider.providerCalls(), result[StrResponse])
	}
	if provider.providerCalls() == 0 {
		t.Fatalf("provider was never called")
	}
	if tool.principal != testPrincipal() {
		t.Fatalf("goal tool principal = %+v", tool.principal)
	}
}

func TestToolLoopConfigReadsTrustedPrincipal(t *testing.T) {
	_ = SetMCPOperatorConfig(cfg.MCPConfig{})
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
	params := loopTestParams()
	configMap := params[StrConfig].(map[string]interface{})
	runtime := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	runtime[cfgKeyMCPMutationPrincipal] = testPrincipal()
	identity := AgentIdentity{AgentID: "op", TrustLevel: "trusted", Capabilities: []string{"tool_execution", "db_read"}}
	loop, err := normalizeToolLoopConfig(params, configMap, NewInputContext(params), identity)
	if err != nil {
		t.Fatalf("normalize tool loop: %v", err)
	}
	defer loop.cancel()
	if loop.Principal != testPrincipal() {
		t.Fatalf("tool loop principal = %+v", loop.Principal)
	}
}

func TestExecuteManualAgentResultManualJobNeedNotBeFirst(t *testing.T) {
	oldRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = oldRegistry })

	var config JobConfig
	manifest := `format_version: v2
agent_identity:
  agent_id: reordered-agent
  name: Event First
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Event First
    process: serial
    trigger_type: event
    trigger_name: test_event
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
  - name: Manual Second
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
`
	if err := yaml.Unmarshal([]byte(manifest), &config); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if err := AgentsRegistry.RegisterAgentE(&config); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	engine := NewJobEngine()
	action := &principalCaptureAction{name: "APIRequest"}
	engine.RegisterAction(action)
	iCfg := WithMCPMutationPrincipal(map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
	}, testPrincipal())
	if _, err := engine.ExecuteManualAgentResult("reordered-agent", iCfg); err != nil {
		t.Fatalf("manual run: %v", err)
	}
	if action.calls != 1 {
		t.Fatalf("manual job calls = %d, want 1", action.calls)
	}
}

type principalIsolationAction struct {
	mu   sync.Mutex
	seen map[string]MCPMutationPrincipal
}

func (a *principalIsolationAction) Name() string { return "APIRequest" }
func (a *principalIsolationAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	name, _ := params["input"].(string)
	runtime := mapStringAny(mapStringAny(params[StrConfig])[cfgKeyAgentRuntime])
	principal, _ := runtime[cfgKeyMCPMutationPrincipal].(MCPMutationPrincipal)
	a.mu.Lock()
	a.seen[name] = principal
	a.mu.Unlock()
	return map[string]interface{}{StrResponse: name, StrStatus: StatusSuccess}, nil
}

func TestManualAgentRunsDoNotSharePrincipals(t *testing.T) {
	oldRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = oldRegistry })

	var config JobConfig
	manifest := `format_version: v2
agent_identity:
  agent_id: isolation-agent
  name: Isolation Agent
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Isolation Agent
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
`
	if err := yaml.Unmarshal([]byte(manifest), &config); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if err := AgentsRegistry.RegisterAgentE(&config); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	action := &principalIsolationAction{seen: map[string]MCPMutationPrincipal{}}
	engine := NewJobEngine()
	engine.RegisterAction(action)
	runs := []struct {
		name      string
		principal MCPMutationPrincipal
	}{
		{"run-a", MCPMutationPrincipal{UserID: 7, Username: "owner-a", Issuer: "crowler"}},
		{"run-b", MCPMutationPrincipal{UserID: 8, Username: "owner-b", Issuer: "crowler"}},
	}
	var wait sync.WaitGroup
	for _, run := range runs {
		run := run
		wait.Add(1)
		go func() {
			defer wait.Done()
			iCfg := WithMCPMutationPrincipal(map[string]any{
				"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
				"input":         run.name,
			}, run.principal)
			if _, err := engine.ExecuteManualAgentResult("isolation-agent", iCfg); err != nil {
				t.Errorf("manual run %s: %v", run.name, err)
			}
		}()
	}
	wait.Wait()
	for _, run := range runs {
		if got := action.seen[run.name]; got != run.principal {
			t.Fatalf("run %s principal = %+v, want %+v", run.name, got, run.principal)
		}
	}
}

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func mcpDeadlineServers(t *testing.T, startLog string) (cfg.MCPConfig, []string) {
	t.Helper()
	makeServer := func(id, mode string) cfg.MCPServerConfig {
		server := mcpFixtureServerConfig(t, mode, cfg.MCPApprovedTool{Name: "echo", ReadOnly: true})
		server.ID = id
		server.MaxStartupSeconds = 30
		server.MaxDiscoverySeconds = 30
		server.Environment["CROWLER_MCP_FIXTURE_START_LOG"] = startLog
		server.Environment["CROWLER_MCP_FIXTURE_SERVER_ID"] = id
		return server
	}
	alpha := makeServer("alpha", "normal")
	beta := makeServer("beta", "slowstart")
	gamma := makeServer("gamma", "normal")
	return cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta, gamma}},
		[]string{MCPToolAlias("alpha", "echo"), MCPToolAlias("beta", "echo"), MCPToolAlias("gamma", "echo")}
}

type mcpStartEntry struct {
	PID  int
	Mode string
	ID   string
}

func mcpStartLogEntries(t *testing.T, path string) map[string][]mcpStartEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MCP start log: %v", err)
	}
	out := map[string][]mcpStartEntry{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		id := ""
		if len(fields) > 2 {
			id = fields[2]
		}
		out[id] = append(out[id], mcpStartEntry{PID: pid, Mode: fields[1], ID: id})
	}
	return out
}

func waitForMCPProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP fixture process %d survived cleanup: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMCPInitializationSharesDeadlineCleansUpPreviousServersAndStartsNoThird(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	config, allowlist := mcpDeadlineServers(t, startLog)
	setMCPTestConfig(t, config)

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	manager, err := NewMCPToolManager(ctx, config, mcpTestIdentity(), allowlist, nil)
	if err == nil {
		_ = manager.Close()
		t.Fatalf("multi-server initialization unexpectedly succeeded past the run deadline")
	}
	if !errors.Is(err, ErrMCPTimeout) {
		t.Fatalf("deadline failure = %v, want ErrMCPTimeout", err)
	}
	if manager != nil {
		_ = manager.Close()
		t.Fatalf("partial MCP manager was returned after failed initialization")
	}

	starts := mcpStartLogEntries(t, startLog)
	if len(starts["alpha"]) == 0 || len(starts["beta"]) == 0 {
		t.Fatalf("expected first and second servers to start: %+v", starts)
	}
	if len(starts["gamma"]) != 0 {
		t.Fatalf("third server was started after the deadline: %+v", starts)
	}
	for _, entries := range starts {
		for _, entry := range entries {
			waitForMCPProcessExit(t, entry.PID)
		}
	}
}

func TestMCPManagerCloseIsIdempotent(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	manager, err := NewMCPToolManager(context.Background(), config, mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("first MCP close: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("repeated MCP close: %v", err)
	}
	select {
	case <-manager.ctx.Done():
	default:
		t.Fatalf("manager context was not canceled on close")
	}
}

func TestAgentToolLoopMCPInitializationConsumesTheRunDeadline(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	config, allowlist := mcpDeadlineServers(t, startLog)
	setMCPTestConfig(t, config)

	resetLLMProvidersForTest()
	provider := &countingProvider{name: defaultLLMProvider}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	params := loopTestParams()
	params["allowlist"] = stringsToAny(allowlist)
	params["limits"] = map[string]any{"timeout": "1s"}
	result, err := (&AgentToolLoopAction{}).Execute(params)
	if err == nil {
		t.Fatalf("tool loop unexpectedly succeeded after MCP initialization deadline")
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("provider was called %d times after initialization failure", provider.providerCalls())
	}
	if result[StrResponse] != nil {
		t.Fatalf("unexpected tool loop response after initialization failure: %+v", result[StrResponse])
	}
	starts := mcpStartLogEntries(t, startLog)
	if len(starts["alpha"]) == 0 || len(starts["beta"]) == 0 || len(starts["gamma"]) != 0 {
		t.Fatalf("unexpected server startup set: %+v", starts)
	}
	for _, entries := range starts {
		for _, entry := range entries {
			waitForMCPProcessExit(t, entry.PID)
		}
	}
}

func TestAgentGoalRunMCPInitializationConsumesTheRunDeadline(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	config, allowlist := mcpDeadlineServers(t, startLog)
	setMCPTestConfig(t, config)

	resetLLMProvidersForTest()
	provider := &countingProvider{name: "goal-scripted"}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	identity := goalTestIdentity()
	spec := mcpGoalSpecFor(t, allowlist[0], map[string]any{}, false)
	spec.Allowlist = allowlist
	spec.Provider = "goal-scripted"
	spec.Model = "scripted"
	spec.Budgets.MaxElapsed = time.Second
	spec.SpecHash = computeGoalSpecHash(spec)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, identity, nil, nil))
	if err == nil {
		t.Fatalf("goal run unexpectedly succeeded after MCP initialization deadline")
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("goal provider was called %d times after initialization failure", provider.providerCalls())
	}
	output, ok := result[StrResponse].(map[string]any)
	if !ok {
		t.Fatalf("missing goal failure result: %+v", result)
	}
	if output["status"] != string(GoalBudgetExhausted) || output["reason_code"] != "budget_elapsed" {
		t.Fatalf("unexpected goal deadline result: %+v", output)
	}
	starts := mcpStartLogEntries(t, startLog)
	if len(starts["alpha"]) == 0 || len(starts["beta"]) == 0 || len(starts["gamma"]) != 0 {
		t.Fatalf("unexpected server startup set: %+v", starts)
	}
	for _, entries := range starts {
		for _, entry := range entries {
			waitForMCPProcessExit(t, entry.PID)
		}
	}
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func TestAgentToolLoopPassesRemainingRunBudgetToProvider(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	setMCPTestConfig(t, mcpTestConfigFor(server))
	resetLLMProvidersForTest()
	var captured time.Duration
	provider := &scriptedProvider{name: defaultLLMProvider, responses: []any{chatWithText("done")}}
	provider.block = func(ctx context.Context) {
		if deadline, ok := ctx.Deadline(); ok {
			captured = time.Until(deadline)
		}
	}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	params := loopTestParams()
	params["allowlist"] = []any{MCPToolAlias("fixture", "echo")}
	params["limits"] = map[string]any{"timeout": "1s"}
	if _, err := (&AgentToolLoopAction{}).Execute(params); err != nil {
		t.Fatalf("tool loop with MCP and a valid budget: %v", err)
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.providerCalls())
	}
	if captured <= 0 || captured > 1100*time.Millisecond {
		t.Fatalf("provider received a reset deadline (%s), want remaining <= 1.1s", captured)
	}
}

func TestAgentGoalRunPassesRemainingRunBudgetToProvider(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	setMCPTestConfig(t, mcpTestConfigFor(server))
	resetLLMProvidersForTest()
	var captured time.Duration
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
	provider.block = func(ctx context.Context) {
		if deadline, ok := ctx.Deadline(); ok {
			captured = time.Until(deadline)
		}
	}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	identity := goalTestIdentity()
	spec := mcpGoalSpecFor(t, MCPToolAlias("fixture", "echo"), map[string]any{}, false)
	spec.Provider = "goal-scripted"
	spec.Model = "scripted"
	spec.Budgets.MaxElapsed = time.Second
	spec.SpecHash = computeGoalSpecHash(spec)
	if _, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, identity, nil, nil)); err == nil {
		t.Fatalf("goal without evidence unexpectedly succeeded")
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("goal provider calls = %d, want 1", provider.providerCalls())
	}
	if captured <= 0 || captured > 1100*time.Millisecond {
		t.Fatalf("goal provider received a reset deadline (%s), want remaining <= 1.1s", captured)
	}
}

func TestMCPDiscoveryFailureCleansUpEarlierServers(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	alpha := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "echo", ReadOnly: true})
	alpha.ID = "alpha"
	alpha.Environment["CROWLER_MCP_FIXTURE_START_LOG"] = startLog
	alpha.Environment["CROWLER_MCP_FIXTURE_SERVER_ID"] = "alpha"
	alpha.MaxStartupSeconds = 30
	alpha.MaxDiscoverySeconds = 30
	beta := mcpFixtureServerConfig(t, "badschema", cfg.MCPApprovedTool{Name: "bad_schema", ReadOnly: true})
	beta.ID = "beta"
	beta.Environment["CROWLER_MCP_FIXTURE_START_LOG"] = startLog
	beta.Environment["CROWLER_MCP_FIXTURE_SERVER_ID"] = "beta"
	beta.MaxStartupSeconds = 30
	beta.MaxDiscoverySeconds = 30
	config := cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta}}
	setMCPTestConfig(t, config)
	manager, err := NewMCPToolManager(context.Background(), config, mcpTestIdentity(),
		[]string{MCPToolAlias("alpha", "echo"), MCPToolAlias("beta", "bad_schema")}, nil)
	if err == nil {
		_ = manager.Close()
		t.Fatalf("invalid discovery unexpectedly succeeded")
	}
	if manager != nil {
		_ = manager.Close()
		t.Fatalf("partial manager returned after discovery failure")
	}
	starts := mcpStartLogEntries(t, startLog)
	if len(starts["alpha"]) == 0 || len(starts["beta"]) == 0 {
		t.Fatalf("expected both servers to start before discovery failure: %+v", starts)
	}
	for _, entries := range starts {
		for _, entry := range entries {
			waitForMCPProcessExit(t, entry.PID)
		}
	}
}

func TestMCPInitializationCancellationPropagatesAndCleansUp(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	config, allowlist := mcpDeadlineServers(t, startLog)
	setMCPTestConfig(t, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		manager, err := NewMCPToolManager(ctx, config, mcpTestIdentity(), allowlist, nil)
		if manager != nil {
			_ = manager.Close()
		}
		errs <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(startLog)
		if strings.Contains(string(data), "slowstart") {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("second MCP server did not start before cancellation test timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errs:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled initialization error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled MCP initialization did not return")
	}
	starts := mcpStartLogEntries(t, startLog)
	for _, entries := range starts {
		for _, entry := range entries {
			waitForMCPProcessExit(t, entry.PID)
		}
	}
}

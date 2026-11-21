package agent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestMCPAdmissionReasonCodeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"tool authority", denyTool(ToolDenyScope, "outside scope"), ToolDenyScope},
		{"disabled", ErrMCPMutationDisabled, ToolDenyMutationDisabled},
		{"policy", ErrMCPMutationPolicyInvalid, "policy_invalid"},
		{"not approved", ErrMCPMutationNotApproved, "not_approved"},
		{"arguments", ErrMCPMutationArguments, ToolDenyArguments},
		{"run identity", ErrMCPMutationRunIdentity, "missing_run_identity"},
		{"principal", ErrMCPMutationPrincipal, "missing_principal"},
		{"store", ErrMCPMutationStoreUnavailable, "store_unavailable"},
		{"conflict", ErrMCPMutationProposalConflict, "proposal_conflict"},
		{"quota", ErrMCPMutationQuota, "quota_exceeded"},
		{"expired", ErrMCPMutationProposalExpired, "proposal_expired"},
		{"corrupt", ErrMCPMutationProposalCorrupt, "proposal_corrupt"},
		{"approval", ErrMCPMutationApproval, "approval_invalid"},
		{"context", context.DeadlineExceeded, ToolDenyContext},
		{"unknown", errors.New("raw token=secret"), "admission_denied"},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpAdmissionReasonCode(tc.err); got != tc.want {
				t.Fatalf("reason code = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeMCPAuditCallID(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"safe", "call-1", "call-1"},
		{"empty", "", ""},
		{"url", "https://secret.example/token=abc", "sha256:"},
		{"control", "call\x00id", "sha256:"},
		{"too long", strings.Repeat("a", 65), "sha256:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeMCPAuditCallID(tc.value)
			if tc.want == "sha256:" {
				if !strings.HasPrefix(got, "sha256:") || strings.Contains(got, "secret") {
					t.Fatalf("call ID = %q, want a bounded hash", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("call ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func newMCPAuthorityManager(t *testing.T) (*MCPToolManager, MCPMutationPolicy, []string, AgentIdentity, func()) {
	t.Helper()
	counterPath := filepath.Join(t.TempDir(), "authority-counter.log")
	config, aliases, policy, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, aliases, []uint64{42})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return manager, policy, aliases, identity, func() { _ = manager.Close() }
}

func newDurableMCPProposalTool(t *testing.T, handler cdb.Handler) (*MCPMutationProposalTool, string, AgentIdentity, func()) {
	t.Helper()
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		cleanup()
		t.Fatalf("new durable store: %v", err)
	}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		cleanup()
		t.Fatalf("new exact proposal tool: %v", err)
	}
	return tool, aliases[0], identity, cleanup
}

func TestMCPProposalDenialWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	tool, alias, identity, cleanup := newDurableMCPProposalTool(t, handler)
	defer cleanup()
	ctx := mcpMutationRuntime(identity, alias, "audit-denied-run", "audit-denied-call")
	secret := "https://secret.example/token=abc"
	result, err := tool.Execute(ctx, map[string]any{"resource": secret})
	if err == nil || !errors.Is(err, ErrMCPMutationArguments) {
		t.Fatalf("denied proposal result=%v err=%v", result, err)
	}

	db := handler.(*mcpMutationSQLiteHandler).db
	var proposals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("denied admission created %d proposals", proposals)
	}
	var eventType, actorType, actorID, actorIssuer, runID, traceID string
	var agentID, serverID, toolAlias, reason, detail string
	var principal sql.NullInt64
	if err := db.QueryRow(`SELECT event_type, actor_type, actor_id, actor_issuer,
		run_id, trace_id, agent_id, principal_user_id, server_id, tool_alias,
		reason_code, redacted_detail FROM MCPMutationAudit`).Scan(
		&eventType, &actorType, &actorID, &actorIssuer, &runID, &traceID,
		&agentID, &principal, &serverID, &toolAlias, &reason, &detail); err != nil {
		t.Fatalf("read denied admission audit: %v", err)
	}
	if eventType != MCPAuditEventAdmissionDenied || actorType != "agent" ||
		actorID != identity.AgentID || actorIssuer != "test-issuer" ||
		runID != "audit-denied-run" || traceID != "mutation-trace" ||
		agentID != identity.AgentID || !principal.Valid || principal.Int64 != 7 ||
		serverID != "fixture" || toolAlias != alias || reason != ToolDenyArguments {
		t.Fatalf("unexpected audit provenance: %s %s %s %s %s %s %s %v %s %s %s %s",
			eventType, actorType, actorID, actorIssuer, runID, traceID, agentID,
			principal, serverID, toolAlias, reason, detail)
	}
	for _, fragment := range []string{`"stage":"proposal"`, `"call_id":"audit-denied-call"`, `"policy_id":"authority-policy"`} {
		if !strings.Contains(detail, fragment) {
			t.Fatalf("audit detail missing %s: %s", fragment, detail)
		}
	}
	if strings.Contains(detail, secret) || strings.Contains(detail, "token=") || strings.Contains(detail, "secret.example") {
		t.Fatalf("audit detail leaked denied arguments: %s", detail)
	}
}

type auditFailureProposalStore struct {
	admitCalls int
	auditCalls int
	auditErr   error
	audits     []MCPAdmissionDeniedEvent
}

func (s *auditFailureProposalStore) Admit(context.Context, MCPMutationProposal, int, int) (MCPMutationProposal, error) {
	s.admitCalls++
	return MCPMutationProposal{}, nil
}

func (s *auditFailureProposalStore) Get(context.Context, string) (MCPMutationProposal, bool, error) {
	return MCPMutationProposal{}, false, nil
}

func (s *auditFailureProposalStore) RecordAdmissionDenied(_ context.Context, event MCPAdmissionDeniedEvent) error {
	s.auditCalls++
	s.audits = append(s.audits, event)
	return s.auditErr
}

func TestMCPProposalDenialAuditFailureKeepsDenial(t *testing.T) {
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store := &auditFailureProposalStore{auditErr: errors.New("audit database is down")}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("new exact proposal tool: %v", err)
	}
	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:          map[string]bool{aliases[0]: true},
			MCPMutationAllowed: true, MCPAllowedOperations: map[string]bool{aliases[0]: true},
			RunID: "audit-failure-run", TraceID: "audit-failure-trace",
		},
		CallID: "audit-failure-call",
	})
	result, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if result != nil || !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("audit failure changed denial: result=%v err=%v", result, err)
	}
	if store.admitCalls != 0 {
		t.Fatalf("audit failure attempted admission %d times", store.admitCalls)
	}
	if store.auditCalls != 1 || len(store.audits) != 1 {
		t.Fatalf("audit calls = %d events=%d", store.auditCalls, len(store.audits))
	}
	if store.audits[0].ReasonCode != "missing_principal" || store.audits[0].Stage != MCPAuditStageProposal {
		t.Fatalf("unexpected audit event: %+v", store.audits[0])
	}
}

func TestMCPProposalDBOutageKeepsDenial(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	if err := handler.(*mcpMutationSQLiteHandler).db.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("new exact proposal tool: %v", err)
	}
	result, err := tool.Execute(mcpMutationRuntime(identity, aliases[0], "outage-run", "outage-call"),
		map[string]any{"resource": "fixture-resource"})
	if result != nil || !errors.Is(err, ErrMCPMutationStoreUnavailable) {
		t.Fatalf("DB outage changed denial: result=%v err=%v", result, err)
	}
}

func TestRegistrationDenialWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()

	if _, err := registerMCPMutationProposalTools(context.Background(), manager, NewAgentToolRegistry(), policy, identity,
		MCPMutationPrincipal{}, aliases, []uint64{42}, handler,
		"register-run", "register-trace"); !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("missing principal error = %v", err)
	}
	weak := identity
	weak.Capabilities = []string{"db_read"}
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, NewAgentToolRegistry(), policy, weak,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{42}, handler,
		"register-run", "register-trace"); err == nil {
		t.Fatalf("missing capability was accepted")
	}
	db := handler.(*mcpMutationSQLiteHandler).db
	rows, err := db.Query(`SELECT reason_code, tool_alias, actor_id, run_id, trace_id, redacted_detail
		FROM MCPMutationAudit WHERE event_type=? ORDER BY event_id`, MCPAuditEventRegistrationDenied)
	if err != nil {
		t.Fatalf("read registration audit: %v", err)
	}
	defer rows.Close()
	type row struct {
		reason, alias, actor, runID, traceID, detail string
	}
	var got []row
	for rows.Next() {
		var item row
		var alias sql.NullString
		if err := rows.Scan(&item.reason, &alias, &item.actor, &item.runID, &item.traceID, &item.detail); err != nil {
			t.Fatalf("scan registration audit: %v", err)
		}
		item.alias = alias.String
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate registration audit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("registration audit rows = %d, want 2: %+v", len(got), got)
	}
	if got[0].reason != "missing_principal" || got[0].alias != "" ||
		got[0].actor != identity.AgentID || got[0].runID != "register-run" ||
		got[0].traceID != "register-trace" ||
		!strings.Contains(got[0].detail, `"operation_count":2`) {
		t.Fatalf("unexpected global registration audit: %+v", got[0])
	}
	if got[1].reason != "capability_missing" || got[1].alias != aliases[0] ||
		got[1].actor != identity.AgentID || !strings.Contains(got[1].detail, `"stage":"registration"`) {
		t.Fatalf("unexpected operation registration audit: %+v", got[1])
	}
	var proposals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("registration denial created %d proposals", proposals)
	}
}

func TestRegistrationDBOutageKeepsDenial(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	handler.(*mcpMutationSQLiteHandler).db.Close()
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, NewAgentToolRegistry(), policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{42}, handler,
		"outage-register-run", "outage-register-trace"); !errors.Is(err, ErrMCPMutationStoreUnavailable) {
		t.Fatalf("registration DB outage error = %v", err)
	}
}

func TestToolLoopProposalDenialWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("new exact proposal tool: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := registry.RegisterMutation(tool); err != nil {
		t.Fatalf("register proposal tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("loop-denied-call", aliases[0], `{"resource":"fixture-resource"}`)),
		chatWithText("done"),
	}}
	loop := loopTestConfig(provider, registry)
	loop.Allowlist = []string{aliases[0]}
	loop.Identity = identity
	loop.Enforcement = true
	loop.MCPMutationAllowed = false
	loop.MCPAllowedOperations = nil
	loop.DB = handler
	loop.Principal = MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}
	loop.RunID = "loop-denied-run"
	loop.TraceID = "loop-denied-trace"
	result, err := RunToolLoop(loop)
	if err != nil {
		t.Fatalf("tool loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "denied" ||
		result.ToolCalls[0].ReasonCode != ToolDenyMutationDisabled {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	var eventType, reason, runID, traceID, alias, detail string
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT event_type, reason_code, run_id, trace_id, tool_alias, redacted_detail
		FROM MCPMutationAudit`).Scan(&eventType, &reason, &runID, &traceID, &alias, &detail); err != nil {
		t.Fatalf("read tool-loop audit: %v", err)
	}
	if eventType != MCPAuditEventAdmissionDenied || reason != ToolDenyMutationDisabled ||
		runID != "loop-denied-run" || traceID != "loop-denied-trace" || alias != aliases[0] ||
		!strings.Contains(detail, `"stage":"tool_loop"`) ||
		!strings.Contains(detail, `"call_id":"loop-denied-call"`) {
		t.Fatalf("unexpected tool-loop audit: %s %s %s %s %s %s", eventType, reason, runID, traceID, alias, detail)
	}
	var proposals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("tool-loop denial created %d proposals", proposals)
	}
}

func TestGoalProposalDenialWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("new exact proposal tool: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := registry.RegisterMutation(tool); err != nil {
		t.Fatalf("register proposal tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("goal-denied-call", aliases[0], `{"resource":"fixture-resource"}`)),
		chatWithText("done"),
	}}
	spec := mcpGoalSpecFor(t, aliases[0], map[string]any{"resource": "fixture-resource"}, false)
	result, err := RunGoal(goalRunConfig{
		Spec: spec, Identity: identity, DB: handler, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "goal-denied-run", TraceID: "goal-denied-trace",
		Principal:            MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
		MCPMutationAllowed:   false,
		MCPAllowedOperations: nil,
	})
	_ = result
	if err != nil && !strings.Contains(err.Error(), "goal") {
		t.Fatalf("goal run error: %v", err)
	}
	var eventType, reason, runID, traceID, alias, detail string
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT event_type, reason_code, run_id, trace_id, tool_alias, redacted_detail
		FROM MCPMutationAudit`).Scan(&eventType, &reason, &runID, &traceID, &alias, &detail); err != nil {
		t.Fatalf("read goal audit: %v", err)
	}
	if eventType != MCPAuditEventAdmissionDenied || reason != ToolDenyMutationDisabled ||
		runID != "goal-denied-run" || traceID != "goal-denied-trace" || alias != aliases[0] ||
		!strings.Contains(detail, `"stage":"goal"`) ||
		!strings.Contains(detail, `"call_id":"goal-denied-call"`) {
		t.Fatalf("unexpected goal audit: %s %s %s %s %s %s", eventType, reason, runID, traceID, alias, detail)
	}
	var proposals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("goal denial created %d proposals", proposals)
	}
}

func TestMCPAuditTextDropsControlCharacters(t *testing.T) {
	got := mcpAuditText("agent\nwith\tcontrol\x00chars", mcpAuditMaxTextBytes)
	if strings.ContainsAny(got, "\n\t\x00") {
		t.Fatalf("audit text retained control characters: %q", got)
	}
	if got != "agentwithcontrolchars" {
		t.Fatalf("audit text = %q", got)
	}
}

func TestToolLoopUnknownMCPAliasWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	_, _, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	registry := loopTestRegistry(t)
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("unknown-alias-call", aliases[0], `{"resource":"fixture-resource"}`)),
		chatWithText("done"),
	}}
	loop := loopTestConfig(provider, registry)
	loop.Allowlist = []string{"get_source_status"}
	loop.Identity = identity
	loop.Enforcement = true
	loop.DB = handler
	loop.Principal = MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}
	loop.RunID = "unknown-alias-run"
	loop.TraceID = "unknown-alias-trace"
	result, err := RunToolLoop(loop)
	if err != nil {
		t.Fatalf("tool loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "denied" ||
		result.ToolCalls[0].ReasonCode != ToolDenyUnknownTool {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	var eventType, reason, alias, server, detail string
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT event_type, reason_code, tool_alias, server_id, redacted_detail
		FROM MCPMutationAudit`).Scan(&eventType, &reason, &alias, &server, &detail); err != nil {
		t.Fatalf("read unknown-alias audit: %v", err)
	}
	if eventType != MCPAuditEventAdmissionDenied || reason != ToolDenyUnknownTool ||
		alias != aliases[0] || server != "fixture" ||
		!strings.Contains(detail, `"stage":"tool_loop"`) ||
		!strings.Contains(detail, `"policy_id":"authority-policy"`) {
		t.Fatalf("unexpected unknown-alias audit: %s %s %s %s %s", eventType, reason, alias, server, detail)
	}
	var proposals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("unknown-alias denial created %d proposals", proposals)
	}
}

func TestGoalUnknownMCPAliasWritesDurableAudit(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	_, _, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	registry := loopTestRegistry(t)
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("goal-unknown-alias-call", aliases[0], `{"resource":"fixture-resource"}`)),
		chatWithText("done"),
	}}
	spec := mcpGoalSpecFor(t, aliases[0], map[string]any{"resource": "fixture-resource"}, false)
	spec.Allowlist = []string{"get_source_status"}
	result, err := RunGoal(goalRunConfig{
		Spec: spec, Identity: identity, DB: handler, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "goal-unknown-alias-run", TraceID: "goal-unknown-alias-trace",
		Principal:            MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
		MCPMutationAllowed:   true,
		MCPAllowedOperations: map[string]bool{aliases[0]: true},
	})
	_ = result
	if err != nil && !strings.Contains(err.Error(), "goal") {
		t.Fatalf("goal run error: %v", err)
	}
	var eventType, reason, alias, server, detail string
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT event_type, reason_code, tool_alias, server_id, redacted_detail
		FROM MCPMutationAudit`).Scan(&eventType, &reason, &alias, &server, &detail); err != nil {
		t.Fatalf("read goal unknown-alias audit: %v", err)
	}
	if eventType != MCPAuditEventAdmissionDenied || reason != ToolDenyUnknownTool ||
		alias != aliases[0] || server != "fixture" ||
		!strings.Contains(detail, `"stage":"goal"`) ||
		!strings.Contains(detail, `"policy_id":"authority-policy"`) {
		t.Fatalf("unexpected goal unknown-alias audit: %s %s %s %s %s", eventType, reason, alias, server, detail)
	}
	var proposals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if proposals != 0 {
		t.Fatalf("goal unknown-alias denial created %d proposals", proposals)
	}
}

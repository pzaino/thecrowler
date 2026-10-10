package agent

import (
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// TestActionCapabilityParity asserts the 1:1 mapping between registered
// actions and canonical schema-aligned capability tokens.
func TestActionCapabilityParity(t *testing.T) {
	engine := NewJobEngine()
	RegisterActions(engine)

	expected := map[string]string{
		"APIRequest":      "api_request",
		"AIInteraction":   "ai_reasoning",
		"DBQuery":         "db_read", // base token; writes require db_write via classifier
		"RunCommand":      "command_execution",
		"PluginExecution": "plugin_execution",
		"CreateEvent":     "emit_event",
		// Decision is pure-control for local branching; delegation needs `delegate`.
		"Decision": "",
	}

	for actionName, wantCap := range expected {
		if _, ok := engine.GetAction(actionName); !ok {
			t.Fatalf("registered action %q missing from engine", actionName)
		}
		got, known := requiredCapabilityForAction(actionName)
		if !known {
			t.Fatalf("action %q not known to capability map", actionName)
		}
		if got != wantCap {
			t.Fatalf("action %q: expected capability %q, got %q", actionName, wantCap, got)
		}
	}

	// Every registered action must be known (fail-closed: no permissive default).
	for name := range engine.actions {
		if _, known := requiredCapabilityForAction(name); !known {
			t.Fatalf("registered action %q has no capability mapping", name)
		}
	}
}

// TestUnknownActionDenied ensures unknown/future actions are denied under enforcement.
func TestUnknownActionDenied(t *testing.T) {
	if _, known := requiredCapabilityForAction("FutureActionXYZ"); known {
		t.Fatalf("unknown action must not be known")
	}
	identity := AgentIdentity{Capabilities: []string{"all"}}
	// Note: `all` currently bypasses known-action checks, but unknown actions
	// must still be denied. capabilityAllowed denies unknown even with `all`.
	if capabilityAllowed(identity, "FutureActionXYZ") {
		t.Fatalf("unknown action must be denied even with `all`")
	}
	identity2 := AgentIdentity{Capabilities: []string{"api_request"}}
	if capabilityAllowed(identity2, "FutureActionXYZ") {
		t.Fatalf("unknown action must be denied")
	}
}

// TestCanonicalGrantDeny checks each action is authorized only with its grant.
func TestCanonicalGrantDeny(t *testing.T) {
	cases := []struct {
		action string
		grant  string
	}{
		{"APIRequest", "api_request"},
		{"AIInteraction", "ai_reasoning"},
		{"RunCommand", "command_execution"},
		{"PluginExecution", "plugin_execution"},
		{"CreateEvent", "emit_event"},
	}
	for _, tc := range cases {
		// Positive: canonical grant allows.
		if !capabilityAllowed(AgentIdentity{Capabilities: []string{tc.grant}}, tc.action) {
			t.Fatalf("%s should be allowed with %q", tc.action, tc.grant)
		}
		// Negative: unrelated grant denies.
		if capabilityAllowed(AgentIdentity{Capabilities: []string{"schedule_event"}}, tc.action) {
			t.Fatalf("%s should be denied with unrelated grant", tc.action)
		}
		// Negative: empty capabilities deny.
		if capabilityAllowed(AgentIdentity{}, tc.action) {
			t.Fatalf("%s should be denied with no capabilities", tc.action)
		}
	}
}

// TestAllWildcardPreserved ensures `all` still authorizes known actions.
func TestAllWildcardPreserved(t *testing.T) {
	for _, action := range []string{"APIRequest", "AIInteraction", "RunCommand", "PluginExecution", "CreateEvent", "DBQuery"} {
		if !capabilityAllowed(AgentIdentity{Capabilities: []string{"all"}}, action) {
			t.Fatalf("%s should be allowed with `all`", action)
		}
	}
	// `all` must be case-insensitive and whitespace-tolerant as before.
	if !capabilityAllowed(AgentIdentity{Capabilities: []string{" ALL "}}, "APIRequest") {
		t.Fatalf("`all` matching must remain case/space tolerant")
	}
}

// TestLegacyAliases ensures pre-PR-1 runtime spellings still work where accepted.
func TestLegacyAliases(t *testing.T) {
	if !capabilityAllowed(AgentIdentity{Capabilities: []string{"run_command"}}, "RunCommand") {
		t.Fatalf("legacy run_command alias must remain supported")
	}
	if !capabilityAllowed(AgentIdentity{Capabilities: []string{"create_event"}}, "CreateEvent") {
		t.Fatalf("legacy create_event alias must remain supported")
	}
	if !capabilityAllowed(AgentIdentity{Capabilities: []string{"ai_interaction"}}, "AIInteraction") {
		t.Fatalf("legacy ai_interaction alias must remain supported")
	}
	if !capabilityAllowed(AgentIdentity{Capabilities: []string{"call_plugin"}}, "PluginExecution") {
		t.Fatalf("call_plugin alias must remain supported for PluginExecution")
	}
	// Legacy db_query grants both read and write (preserves old omnipotent behavior).
	if !dbQueryAllowed(AgentIdentity{Capabilities: []string{"db_query"}}, "SELECT 1") {
		t.Fatalf("legacy db_query must allow reads")
	}
	if !dbQueryAllowed(AgentIdentity{Capabilities: []string{"db_query"}}, "UPDATE t SET a=1") {
		t.Fatalf("legacy db_query must allow writes (preserved behavior)")
	}
}

// TestDecisionLocalVsDelegation: local Decision branching needs no grant;
// delegation requires `delegate` (checked in delegationPolicyCheck).
func TestDecisionLocalVsDelegation(t *testing.T) {
	// Local branching: Decision is known and allowed without any grant.
	if _, known := requiredCapabilityForAction("Decision"); !known {
		t.Fatalf("Decision must be a known action")
	}
	if !capabilityAllowed(AgentIdentity{}, "Decision") {
		t.Fatalf("Decision local branching must not require an extra grant")
	}
	// Delegation requires `delegate`.
	caller := AgentIdentity{AgentID: "a", TrustLevel: "trusted", Capabilities: []string{"api_request"}}
	callee := AgentIdentity{AgentID: "b", TrustLevel: "restricted"}
	if err := delegationPolicyCheck(caller, callee); err == nil {
		t.Fatalf("delegation without `delegate` must be denied")
	}
	caller.Capabilities = []string{"delegate"}
	if err := delegationPolicyCheck(caller, callee); err != nil {
		t.Fatalf("delegation with `delegate` should pass policy check, got %v", err)
	}
}

// TestSQLClassifierTable is the required table-driven classifier contract.
func TestSQLClassifierTable(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want SQLClassificationType
	}{
		{"plain select", "SELECT id, name FROM users WHERE id = 1", SQLRead},
		{"select with trailing semicolon", "SELECT 1;", SQLRead},
		{"values", "VALUES (1, 'a'), (2, 'b')", SQLRead},
		{"select for update", "SELECT id FROM orders WHERE id = 1 FOR UPDATE", SQLWrite},
		{"select for share", "SELECT id FROM t FOR SHARE", SQLWrite},
		{"select into", "SELECT id, name INTO new_table FROM old_table", SQLWrite},
		{"with select", "WITH recent AS (SELECT id FROM t WHERE x > 1) SELECT * FROM recent", SQLRead},
		{"with modifying cte", "WITH moved AS (UPDATE t SET a = 1 RETURNING id) SELECT * FROM moved", SQLWrite},
		{"insert", "INSERT INTO t (a) VALUES (1)", SQLWrite},
		{"update", "UPDATE t SET a = 1 WHERE id = 2", SQLWrite},
		{"delete", "DELETE FROM t WHERE id = 3", SQLWrite},
		{"create", "CREATE TABLE foo (id INT)", SQLWrite},
		{"alter", "ALTER TABLE foo ADD COLUMN b TEXT", SQLWrite},
		{"drop", "DROP TABLE foo", SQLWrite},
		{"truncate", "TRUNCATE foo", SQLWrite},
		{"explain select", "EXPLAIN SELECT id FROM t", SQLRead},
		{"explain analyze update", "EXPLAIN ANALYZE UPDATE t SET a = 1", SQLWrite},
		{"comment prefix select", "-- fetch users\nSELECT id FROM users", SQLRead},
		{"block comment prefix", "/* hi */ SELECT 1", SQLRead},
		{"comment only", "-- just a comment", SQLRejected},
		{"multi statement", "SELECT 1; SELECT 2", SQLRejected},
		{"multi with trailing", "SELECT 1; DELETE FROM t", SQLRejected},
		{"malformed", "SELECT FROM WHERE", SQLRejected},
		{"empty", "", SQLRejected},
		{"unsupported", "LISTEN my_channel", SQLRead}, // parses; conservative read default
		{"select with function", "SELECT now(), count(*) FROM t", SQLRead},
		{"nested subselect", "SELECT * FROM (SELECT id FROM t WHERE x > 5) AS sub", SQLRead},
		{"union", "SELECT a FROM t1 UNION SELECT a FROM t2", SQLRead},
		{"select into in union branch", "SELECT a INTO t3 FROM t1 UNION SELECT a FROM t2", SQLWrite},
	}
	for _, tc := range cases {
		got := DBQueryClassifier.Classify(tc.sql)
		if got != tc.want {
			t.Errorf("%s: Classify(%q) = %q, want %q", tc.name, tc.sql, got, tc.want)
		}
	}
}

// TestDBQueryGateEnforcement checks dbQueryAllowed with fake identities (no live DB).
func TestDBQueryGateEnforcement(t *testing.T) {
	reader := AgentIdentity{Capabilities: []string{"db_read"}, TrustLevel: "trusted"}
	writer := AgentIdentity{Capabilities: []string{"db_write"}, TrustLevel: "trusted"}
	both := AgentIdentity{Capabilities: []string{"db_read", "db_write"}, TrustLevel: "trusted"}
	all := AgentIdentity{Capabilities: []string{"all"}, TrustLevel: "trusted"}
	none := AgentIdentity{Capabilities: []string{"api_request"}, TrustLevel: "trusted"}

	if !dbQueryAllowed(reader, "SELECT 1") {
		t.Fatalf("db_read must allow SELECT")
	}
	if dbQueryAllowed(reader, "UPDATE t SET a=1") {
		t.Fatalf("db_read must deny UPDATE")
	}
	if dbQueryAllowed(writer, "SELECT 1") {
		t.Fatalf("db_write alone must not allow SELECT (needs db_read)")
	}
	if !dbQueryAllowed(writer, "DELETE FROM t") {
		t.Fatalf("db_write must allow DELETE")
	}
	if !dbQueryAllowed(both, "WITH m AS (UPDATE t SET a=1 RETURNING id) SELECT * FROM m") {
		t.Fatalf("read+write must allow modifying CTE")
	}
	if dbQueryAllowed(reader, "WITH m AS (UPDATE t SET a=1 RETURNING id) SELECT * FROM m") {
		t.Fatalf("db_read must deny modifying CTE")
	}
	if !dbQueryAllowed(all, "SELECT 1") {
		t.Fatalf("`all` must allow SELECT")
	}
	if !dbQueryAllowed(all, "DROP TABLE t") {
		t.Fatalf("`all` must preserve wildcard for writes")
	}
	// `all` must not mask parse errors.
	if dbQueryAllowed(all, "SELECT FROM WHERE") {
		t.Fatalf("`all` must not mask parse errors")
	}
	if dbQueryAllowed(all, "SELECT 1; DROP TABLE t") {
		t.Fatalf("`all` must not allow multi-statement")
	}
	if dbQueryAllowed(none, "SELECT 1") {
		t.Fatalf("unrelated grant must deny SELECT")
	}
	// Rejected SQL denied even with db_write (except `all` path which also denies here).
	if dbQueryAllowed(writer, "SELECT FROM WHERE") {
		t.Fatalf("db_write must not allow unparseable SQL")
	}
}

// TestSchemaAcceptsDelegate ensures the `delegate` token validates under strict mode.
func TestSchemaAcceptsDelegate(t *testing.T) {
	manifest := []byte(`format_version: v2
agent_identity:
  name: Schema Delegate Agent
  trust_level: trusted
  capabilities: [api_request, delegate]
jobs:
  - name: Schema Delegate Agent
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params:
          url: http://localhost/health
          request_type: GET
`)
	if err := ValidateAgentConfig(manifest, "yaml", ValidationModeStrict, nil); err != nil {
		t.Fatalf("strict validation must accept `delegate` capability, got %v", err)
	}
}

// TestAPIRequestDeniedWithoutGrant ensures APIRequest is gated (was fallthrough).
func TestAPIRequestDeniedWithoutGrant(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "APIRequest"})
	agentCfg := makeIdentityAgent("APIRequest")
	agentCfg.AgentIdentity.Capabilities = []string{"emit_event"}
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "capability gate denied action APIRequest") {
		t.Fatalf("expected APIRequest denial, got: %v", err)
	}
	agentCfg.AgentIdentity.Capabilities = []string{"api_request"}
	// Will fail at execution (no real handler) only if gate passes; use test action.
	// testStepAction succeeds, so no error expected.
	if err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("expected APIRequest to pass gate with api_request, got %v", err)
	}
}

// TestEnforcementDisabledPreservesLegacy ensures rollout-off path is untouched.
func TestEnforcementDisabledPreservesLegacy(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})
	engine.RegisterAction(&testStepAction{name: "MysteryFutureAction"})
	legacy := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial", TriggerType: "manual", TriggerName: "legacy", Steps: []map[string]interface{}{
		{"action": "RunCommand", "params": map[string]interface{}{}},
		{"action": "MysteryFutureAction", "params": map[string]interface{}{}},
	}}}}
	if err := engine.ExecuteJobs(legacy, map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: false}}); err != nil {
		t.Fatalf("legacy execution with enforcement off must pass, got %v", err)
	}
}

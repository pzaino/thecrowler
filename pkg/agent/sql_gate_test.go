package agent

import (
	"strings"
	"testing"
)

func gateIdentities() (reader, writer, both, all, legacy, none AgentIdentity) {
	reader = AgentIdentity{Capabilities: []string{"db_read"}, TrustLevel: "trusted"}
	writer = AgentIdentity{Capabilities: []string{"db_write"}, TrustLevel: "trusted"}
	both = AgentIdentity{Capabilities: []string{"db_read", "db_write"}, TrustLevel: "trusted"}
	all = AgentIdentity{Capabilities: []string{"all"}, TrustLevel: "trusted"}
	legacy = AgentIdentity{Capabilities: []string{"db_query"}, TrustLevel: "trusted"}
	none = AgentIdentity{Capabilities: []string{"api_request"}, TrustLevel: "trusted"}
	return reader, writer, both, all, legacy, none
}

// TestGateDecisionsAcrossGrants pins the capability outcome matrix for
// plain statements.
func TestGateDecisionsAcrossGrants(t *testing.T) {
	reader, writer, both, all, legacy, none := gateIdentities()
	cases := []struct {
		name  string
		sql   string
		grant AgentIdentity
		want  bool
	}{
		{"read grants select", "SELECT id FROM t WHERE id = 1", reader, true},
		{"read denies update", "UPDATE t SET a = 1", reader, false},
		{"read denies call", "CALL do_something()", reader, false},
		{"read denies show", "SHOW server_version", reader, false},
		{"read denies unknown func", "SELECT mystery_fn(id) FROM t", reader, false},
		{"write denies select", "SELECT id FROM t", writer, false},
		{"write grants update", "UPDATE t SET a = 1", writer, true},
		{"write grants call", "CALL do_something()", writer, true},
		{"write grants unknown func", "SELECT mystery_fn(id) FROM t", writer, true},
		{"both grants cte read", "WITH r AS (SELECT id FROM t) SELECT * FROM r", both, true},
		{"both grants cte write", "WITH m AS (UPDATE t SET a = 1 RETURNING id) SELECT * FROM m", both, true},
		{"both deny multi", "SELECT 1; SELECT 2", both, false},
		{"both deny malformed", "SELECT FROM WHERE", both, false},
		{"all grants select", "SELECT id FROM t", all, true},
		{"all grants write", "DROP TABLE t", all, true},
		{"all denies malformed", "SELECT FROM WHERE", all, false},
		{"all denies multi", "SELECT 1; DROP TABLE t", all, false},
		{"legacy grants select", "SELECT id FROM t", legacy, true},
		{"legacy grants update", "UPDATE t SET a = 1", legacy, true},
		{"legacy denies malformed", "SELECT FROM WHERE", legacy, false},
		{"unrelated denies select", "SELECT id FROM t", none, false},
	}
	for _, tc := range cases {
		if got := dbQueryAllowed(tc.grant, tc.sql); got != tc.want {
			t.Errorf("%s: dbQueryAllowed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGateTemplatedReads keeps least privilege for value interpolation:
// a read-shaped template needs only db_read at the gate.
func TestGateTemplatedReads(t *testing.T) {
	reader, writer, both, _, _, _ := gateIdentities()
	templated := "SELECT asset_id FROM asset_inventory WHERE external_hostname = '{{TARGET_HOST}}' LIMIT 1"
	if !dbQueryAllowed(reader, templated) {
		t.Fatalf("db_read must allow read-shaped templates")
	}
	templatedResponse := "SELECT id FROM t WHERE id = $response.user_id"
	if !dbQueryAllowed(reader, templatedResponse) {
		t.Fatalf("db_read must allow $response value templates with read shape")
	}
	if dbQueryAllowed(reader, "UPDATE t SET a = $response.a") {
		t.Fatalf("db_read must deny write-shaped templates")
	}
	if !dbQueryAllowed(writer, "UPDATE t SET a = $response.a") {
		t.Fatalf("db_write must allow write-shaped templates")
	}
	_ = both
}

// TestGateInterpolationToWriteDenied proves a value that changes the shape
// cannot slip through: the resolved text is classified at execution.
func TestGateInterpolationToWriteDenied(t *testing.T) {
	reader, _, both, _, _, _ := gateIdentities()
	// Gate sees a read shape and grants db_read...
	raw := "SELECT id FROM t WHERE id = $response.user_id"
	if !dbQueryAllowed(reader, raw) {
		t.Fatalf("gate must grant read-shaped template to db_read")
	}
	// ...but a hostile interpolated value is rejected when classified.
	hostile := "SELECT id FROM t WHERE id = 1; DROP TABLE t"
	if DBQueryClassifier.Classify(hostile) != SQLRejected {
		t.Fatalf("hostile resolved text must classify rejected")
	}
	if dbQueryAllowed(reader, hostile) || dbQueryAllowed(both, hostile) {
		t.Fatalf("resolved multi-statement must be denied for every grant")
	}
	// Fully dynamic queries keep the conservative writer-only gate rule.
	if dbQueryAllowed(reader, "$response") {
		t.Fatalf("db_read must not gate-approve fully dynamic queries")
	}
	if !dbQueryAllowed(both, "$response") {
		t.Fatalf("db_read+db_write must gate-approve dynamic queries for execution re-check")
	}
}

// TestGateMissingIdentityDenied ensures the execution gate denies without a
// caller snapshot (fail-closed), covered end to end by the DBQuery action
// missing-identity test.
func TestGateCapabilityRequiredTable(t *testing.T) {
	cases := []struct {
		sql    string
		want   string
		wantOK bool
	}{
		{"SELECT 1", "db_read", true},
		{"UPDATE t SET a = 1", "db_write", true},
		{"CALL f()", "db_write", true},
		{"SELECT 1; SELECT 2", "", false},
		{"SELECT FROM WHERE", "", false},
		{"SELECT id FROM t WHERE id = $response.id", "db_read", true},
		{"$response", "db_write", true},
	}
	for _, tc := range cases {
		got, ok := dbQueryCapabilityRequired(tc.sql)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("dbQueryCapabilityRequired(%q) = (%q, %v), want (%q, %v)",
				tc.sql, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestGateErrorTextStable guards the denial messages callers match on.
func TestGateErrorTextStable(t *testing.T) {
	engine := NewJobEngine()
	identity := AgentIdentity{
		AgentID: "g", Name: "G", TrustLevel: "trusted",
		Capabilities: []string{"db_read"},
	}
	params := map[string]interface{}{"query": "UPDATE t SET a = 1"}
	err := enforceDBQueryGate(engine, params, identity,
		AgentExecutionContext{}, "g", "G", "")
	if err == nil || !strings.Contains(err.Error(), `"db_write" missing`) {
		t.Fatalf("expected db_write-missing denial, got %v", err)
	}
	params = map[string]interface{}{"query": "SELECT FROM WHERE"}
	err = enforceDBQueryGate(engine, params, identity,
		AgentExecutionContext{}, "g", "G", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported or ambiguous") {
		t.Fatalf("expected rejection denial, got %v", err)
	}
}

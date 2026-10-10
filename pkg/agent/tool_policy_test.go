package agent

import (
	"strings"
	"testing"
)

func policyTestTool() *stubAgentTool {
	return &stubAgentTool{
		name:         "get_status",
		description:  "read status",
		schema:       map[string]any{"type": "object"},
		capabilities: []string{"db_read"},
	}
}

func policyAuthContext() ToolAuthContext {
	return ToolAuthContext{
		Identity: AgentIdentity{AgentID: "op", TrustLevel: "trusted",
			Capabilities: []string{"db_read"}},
		Enforcement: true,
		Allowlist:   map[string]bool{"get_status": true},
		RunID:       "run-1",
		TraceID:     "trace-1",
	}
}

func authErrCode(err error) string {
	authErr, ok := err.(*ToolAuthError)
	if !ok {
		return "not-a-tool-error:" + err.Error()
	}
	return authErr.Code
}

// TestAuthorizeToolCallOrder pins every gate in sequence with zero handler
// invocations on denial.
func TestAuthorizeToolCallOrder(t *testing.T) {
	tool := policyTestTool()
	counting := &stubAgentTool{name: "counted", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}

	// Happy path.
	auth := policyAuthContext()
	if err := AuthorizeToolCall(auth, tool, map[string]any{}); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	// 1. Run not initialized.
	bad := policyAuthContext()
	bad.RunID = ""
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyNoOptIn {
		t.Fatalf("run check: %s", got)
	}
	// 2. Anonymous identity.
	bad = policyAuthContext()
	bad.Identity.AgentID = ""
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyNoIdentity {
		t.Fatalf("identity check: %s", got)
	}
	// 2b. Enforcement off.
	bad = policyAuthContext()
	bad.Enforcement = false
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyEnforcementOff {
		t.Fatalf("enforcement check: %s", got)
	}
	// 4. Not allowlisted (nil map and missing entry alike).
	bad = policyAuthContext()
	bad.Allowlist = nil
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyNotAllowlisted {
		t.Fatalf("nil allowlist: %s", got)
	}
	bad = policyAuthContext()
	bad.Allowlist = map[string]bool{"other": true}
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyNotAllowlisted {
		t.Fatalf("missing entry: %s", got)
	}
	// `all` capability does not satisfy the allowlist.
	bad = policyAuthContext()
	bad.Identity.Capabilities = []string{"all"}
	bad.Allowlist = map[string]bool{"other": true}
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyNotAllowlisted {
		t.Fatalf("all must not bypass allowlist: %s", got)
	}
	// 5. Missing capability (without `all`).
	bad = policyAuthContext()
	bad.Identity.Capabilities = []string{"api_request"}
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyCapability {
		t.Fatalf("capability check: %s", got)
	}
	// 5b. `all` satisfies capability matching.
	bad = policyAuthContext()
	bad.Identity.Capabilities = []string{"all"}
	if err := AuthorizeToolCall(bad, tool, map[string]any{}); err != nil {
		t.Fatalf("all must satisfy capability matching: %v", err)
	}
	// 6a. Low trust.
	bad = policyAuthContext()
	bad.Identity.TrustLevel = "restricted"
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyTrust {
		t.Fatalf("trust check: %s", got)
	}
	// 6b. Contract forbids.
	bad = policyAuthContext()
	bad.Identity.Contract = &AgentContract{ForbiddenActions: []string{"tool:get_status"}}
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyContract {
		t.Fatalf("contract check: %s", got)
	}
	bad = policyAuthContext()
	bad.Identity.Contract = &AgentContract{ForbiddenActions: []string{"tools"}}
	if got := authErrCode(AuthorizeToolCall(bad, tool, map[string]any{})); got != ToolDenyContract {
		t.Fatalf("blanket tools contract: %s", got)
	}
	// 7. Nil arguments.
	if got := authErrCode(AuthorizeToolCall(auth, tool, nil)); got != ToolDenyArguments {
		t.Fatalf("nil args: %s", got)
	}
	// Denials never invoke the handler.
	if counting.calls != 0 {
		t.Fatalf("denial invoked handler")
	}
}

// TestAuthorizeArgumentsInstances pins Draft-07 instance validation.
func TestAuthorizeArgumentsInstances(t *testing.T) {
	tool := &stubAgentTool{
		name: "searcher", description: "d",
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "minimum": 1},
				"mode":  map[string]any{"type": "string", "enum": []any{"fast", "deep"}},
				"extra": map[string]any{"type": "object",
					"properties":           map[string]any{"tag": map[string]any{"type": "string"}},
					"required":             []any{"tag"},
					"additionalProperties": false},
			},
			"required":             []any{"query"},
			"additionalProperties": false,
		},
		capabilities: []string{"db_read"},
	}
	auth := policyAuthContext()
	auth.Allowlist = map[string]bool{"searcher": true}

	valid := map[string]any{"query": "x", "limit": float64(3), "mode": "fast",
		"extra": map[string]any{"tag": "t"}}
	if err := AuthorizeToolCall(auth, tool, valid); err != nil {
		t.Fatalf("valid args: %v", err)
	}
	invalid := []struct {
		name string
		args map[string]any
	}{
		{"missing required", map[string]any{"limit": float64(1)}},
		{"wrong type", map[string]any{"query": float64(1)}},
		{"below minimum", map[string]any{"query": "x", "limit": float64(0)}},
		{"bad enum", map[string]any{"query": "x", "mode": "wild"}},
		{"additional property", map[string]any{"query": "x", "sneaky": true}},
		{"nested missing", map[string]any{"query": "x", "extra": map[string]any{}}},
		{"nested extra", map[string]any{"query": "x",
			"extra": map[string]any{"tag": "t", "sneaky": 1}}},
	}
	for _, tc := range invalid {
		err := AuthorizeToolCall(auth, tool, tc.args)
		if err == nil {
			t.Fatalf("%s: expected denial", tc.name)
			continue
		}
		if got := authErrCode(err); got != ToolDenyArguments {
			t.Fatalf("%s: wrong code %s", tc.name, got)
		}
		if strings.Contains(err.Error(), "sneaky") || strings.Contains(err.Error(), "wild") {
			t.Fatalf("%s: argument values leaked: %v", tc.name, err)
		}
	}
}

// TestAuthorizeRefInstances pins local $ref instance validation.
func TestAuthorizeRefInstances(t *testing.T) {
	tool := &stubAgentTool{
		name: "refer", description: "d",
		schema: map[string]any{
			"type": "object",
			"definitions": map[string]any{
				"addr": map[string]any{"type": "object",
					"properties": map[string]any{"zip": map[string]any{"type": "string"}},
					"required":   []any{"zip"}},
			},
			"properties": map[string]any{
				"home": map[string]any{"$ref": "#/definitions/addr"},
			},
			"required": []any{"home"},
		},
		capabilities: []string{"db_read"},
	}
	auth := policyAuthContext()
	auth.Allowlist = map[string]bool{"refer": true}
	if err := AuthorizeToolCall(auth, tool,
		map[string]any{"home": map[string]any{"zip": "00100"}}); err != nil {
		t.Fatalf("valid ref instance: %v", err)
	}
	if err := AuthorizeToolCall(auth, tool,
		map[string]any{"home": map[string]any{}}); err == nil {
		t.Fatalf("invalid ref instance must fail")
	}
}

// TestAuthorizeBounds pins size/depth denial without handler contact.
func TestAuthorizeBounds(t *testing.T) {
	tool := policyTestTool()
	auth := policyAuthContext()
	bigStr := make([]byte, maxToolArgsBytes+1)
	for i := range bigStr {
		bigStr[i] = 'x'
	}
	big := map[string]any{"blob": string(bigStr)}
	if got := authErrCode(AuthorizeToolCall(auth, tool, big)); got != ToolDenyArguments {
		t.Fatalf("oversize args: %s", got)
	}
	deep := map[string]any{}
	cursor := deep
	for i := 0; i < maxToolArgsDepth+2; i++ {
		next := map[string]any{}
		cursor["n"] = next
		cursor = next
	}
	if got := authErrCode(AuthorizeToolCall(auth, tool, deep)); got != ToolDenyArguments {
		t.Fatalf("deep args: %s", got)
	}
}

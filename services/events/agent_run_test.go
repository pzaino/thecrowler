package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	agt "github.com/pzaino/thecrowler/pkg/agent"
	"github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	yaml "gopkg.in/yaml.v3"
)

const agentRunTestManifest = `format_version: v2
agent_identity:
  agent_id: manual-agent
  name: Manual Agent
  trust_level: trusted
  capabilities: [api_request, emit_event]
jobs:
  - name: Manual Agent
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params: {url: "http://localhost/health", request_type: GET}
  - name: Event Agent
    process: serial
    trigger_type: event
    trigger_name: test_event
    steps:
      - action: CreateEvent
        params: {event_type: test_event, event_severity: info}
`

type agentRunCaptureAction struct {
	name      string
	calls     int
	principal agt.MCPMutationPrincipal
	runID     string
	input     any
}

func (a *agentRunCaptureAction) Name() string { return a.name }
func (a *agentRunCaptureAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	a.calls++
	configMap, _ := params[agt.StrConfig].(map[string]interface{})
	runtime, _ := configMap["agent_runtime"].(map[string]interface{})
	a.principal, _ = runtime["mcp_mutation_principal"].(agt.MCPMutationPrincipal)
	a.runID, _ = runtime["run_id"].(string)
	a.input = params["input"]
	return map[string]interface{}{agt.StrResponse: map[string]any{"ok": true}, agt.StrStatus: agt.StatusSuccess}, nil
}

func setupAgentRunTest(t *testing.T) (*agentRunCaptureAction, *agentRunCaptureAction) {
	t.Helper()
	oldDB := dbHandler
	oldRegistry := agt.AgentsRegistry
	oldEngine := agt.AgentsEngine
	oldAgentConfig := config.Agent
	oldPlugin := PluginRegister
	t.Cleanup(func() {
		dbHandler = oldDB
		agt.AgentsRegistry = oldRegistry
		agt.AgentsEngine = oldEngine
		configMutex.Lock()
		config.Agent = oldAgentConfig
		configMutex.Unlock()
		PluginRegister = oldPlugin
		if dbHandler != nil && dbHandler != oldDB {
			_ = dbHandler.Close()
		}
	})

	path := filepath.Join(t.TempDir(), "events-agent-run.db")
	handler, err := cdb.NewHandler(cfg.Config{Database: cfg.Database{
		Type: cdb.DBSQLiteStr, DBName: path, TransactionTimeout: 30,
	}})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if err := handler.Connect(cfg.Config{Database: cfg.Database{
		Type: cdb.DBSQLiteStr, DBName: path, TransactionTimeout: 30,
	}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	dbHandler = handler
	for _, statement := range []string{
		`CREATE TABLE Users (user_id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, disabled BOOLEAN NOT NULL DEFAULT FALSE, deleted_at TIMESTAMP)`,
		`CREATE TABLE AuthRoles (role_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE AuthScopes (scope_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE UserRoles (user_id INTEGER, role_id INTEGER, PRIMARY KEY (user_id, role_id))`,
		`CREATE TABLE RoleScopes (role_id INTEGER, scope_id INTEGER, PRIMARY KEY (role_id, scope_id))`,
		`CREATE TABLE UserScopes (user_id INTEGER, scope_id INTEGER, PRIMARY KEY (user_id, scope_id))`,
		`INSERT INTO Users (user_id, username, disabled, deleted_at) VALUES (7, 'owner-1', FALSE, NULL)`,
		`INSERT INTO AuthScopes (scope_id, name) VALUES (1, 'events:*')`,
		`INSERT INTO UserScopes (user_id, scope_id) VALUES (7, 1)`,
	} {
		if _, err := dbHandler.Exec(statement); err != nil {
			t.Fatalf("fixture statement: %v\n%s", err, statement)
		}
	}

	configMutex.Lock()
	config.Agent = cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true}
	configMutex.Unlock()
	agt.AgentsRegistry = agt.NewJobConfig()
	var config agt.JobConfig
	if err := yaml.Unmarshal([]byte(agentRunTestManifest), &config); err != nil {
		t.Fatalf("parse agent: %v", err)
	}
	if err := agt.AgentsRegistry.RegisterAgentE(&config); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	manualAction := &agentRunCaptureAction{name: "APIRequest"}
	eventAction := &agentRunCaptureAction{name: "CreateEvent"}
	agt.AgentsEngine = agt.NewJobEngine()
	agt.AgentsEngine.RegisterAction(manualAction)
	agt.AgentsEngine.RegisterAction(eventAction)
	return manualAction, eventAction
}

func runManualAgentRequest(t *testing.T, body string, identity *auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if identity != nil {
		req = req.WithContext(auth.ContextWithIdentity(context.Background(), *identity))
	}
	recorder := httptest.NewRecorder()
	agentRunHandler(recorder, req)
	return recorder
}

func TestManualAgentRunUsesAuthenticatedPrincipal(t *testing.T) {
	manual, event := setupAgentRunTest(t)
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	recorder := runManualAgentRequest(t, `{"agent_id":"manual-agent","input":"echo live"}`, &identity)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response agentRunResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.PrincipalUserID != 7 || response.PrincipalUsername != "owner-1" || response.PrincipalIssuer != "crowler" {
		t.Fatalf("response principal = %+v", response)
	}
	if manual.calls != 1 || event.calls != 0 {
		t.Fatalf("manual calls=%d event calls=%d", manual.calls, event.calls)
	}
	if manual.principal.UserID != 7 || manual.principal.Username != "owner-1" {
		t.Fatalf("runtime principal = %+v", manual.principal)
	}
	if manual.runID == "" {
		t.Fatalf("manual run missing trusted run ID")
	}
	if manual.input != "echo live" {
		t.Fatalf("string input not propagated: %#v", manual.input)
	}
}

func TestManualAgentRunRequiresAuthentication(t *testing.T) {
	setupAgentRunTest(t)
	if recorder := runManualAgentRequest(t, `{"agent_id":"manual-agent"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualAgentRunRequiresEventsScope(t *testing.T) {
	setupAgentRunTest(t)
	if _, err := dbHandler.Exec(`DELETE FROM UserScopes`); err != nil {
		t.Fatalf("remove scope: %v", err)
	}
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	if recorder := runManualAgentRequest(t, `{"agent_id":"manual-agent"}`, &identity); recorder.Code != http.StatusForbidden {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualAgentRunRejectsForgedPrincipalFields(t *testing.T) {
	setupAgentRunTest(t)
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	if recorder := runManualAgentRequest(t, `{"agent_id":"manual-agent","principal_user_id":99}`, &identity); recorder.Code != http.StatusBadRequest {
		t.Fatalf("forged principal field status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualAgentRunRejectsDisabledAccount(t *testing.T) {
	setupAgentRunTest(t)
	if _, err := dbHandler.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	if recorder := runManualAgentRequest(t, `{"agent_id":"manual-agent"}`, &identity); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("disabled account status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualAgentRunRejectsNonManualAgent(t *testing.T) {
	setupAgentRunTest(t)
	eventOnly := `format_version: v2
agent_identity:
  agent_id: event-agent
  name: Event Agent
  trust_level: trusted
  capabilities: [emit_event]
jobs:
  - name: Event Agent
    process: serial
    trigger_type: event
    trigger_name: test_event
    steps:
      - action: CreateEvent
        params: {event_type: test_event, event_severity: info}
`
	var config agt.JobConfig
	if err := yaml.Unmarshal([]byte(eventOnly), &config); err != nil {
		t.Fatalf("parse event agent: %v", err)
	}
	if err := agt.AgentsRegistry.RegisterAgentE(&config); err != nil {
		t.Fatalf("register event agent: %v", err)
	}
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	if recorder := runManualAgentRequest(t, `{"agent_id":"event-agent"}`, &identity); recorder.Code != http.StatusBadRequest {
		t.Fatalf("non-manual status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualAgentRunRejectsUnknownAgentAndMethod(t *testing.T) {
	setupAgentRunTest(t)
	identity := auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}
	if recorder := runManualAgentRequest(t, `{"agent_id":"missing-agent"}`, &identity); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown agent status %d: %s", recorder.Code, recorder.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/run", nil)
	recorder := httptest.NewRecorder()
	agentRunHandler(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status %d", recorder.Code)
	}
}

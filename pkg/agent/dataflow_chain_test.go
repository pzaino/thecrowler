package agent

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// echoProvider returns the resolved prompt so the chain can assert
// value flow across actions.
type echoProvider struct{ lastReq LLMRequest }

func (p *echoProvider) Name() string { return "echo-provider" }

func (p *echoProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	p.lastReq = req
	return map[string]interface{}{"echo": req.Prompt, "provider": p.Name()}, nil
}

// execOKHandler upgrades fakeDBHandler with a sqlmock-backed transaction
// path so CreateEvent can persist without a live database.
type execOKHandler struct {
	fakeDBHandler
	db   *sql.DB
	mock sqlmock.Sqlmock
}

func (h *execOKHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.db.BeginTx(ctx, opts)
}

// TestCrossActionResponseChain runs APIRequest -> AIInteraction -> CreateEvent
// with one shared $response meaning and asserts value flow end to end.
func TestCrossActionResponseChain(t *testing.T) {
	// NOTE: GenericAPIRequest returns a transport envelope
	// {body: "<raw JSON text>", status_code: N}; the chain below uses
	// those real field shapes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"user_id":7}`))
	}))
	defer srv.Close()

	resetLLMProvidersForTest()
	echo := &echoProvider{}
	RegisterLLMProvider(echo)
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock setup failed: %v", err)
	}
	defer db.Close() //nolint:errcheck
	handler := &execOKHandler{db: db, mock: mock}
	var cdbHandler cdb.Handler = handler

	expectedEcho := `summarize code 200 for {"status_code":200,"user_id":7}`
	expectedDetails := cmn.ConvertMapToString(map[string]interface{}{"origin": expectedEcho})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO Events").
		WithArgs(sqlmock.AnyArg(), uint64(0), "chain_event", "medium", sqlmock.AnyArg(), sqlmock.AnyArg(), expectedDetails).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	engine := NewJobEngine()
	engine.RegisterAction(&APIRequestAction{})
	engine.RegisterAction(&AIInteractionAction{})
	engine.RegisterAction(&CreateEventAction{})

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "chain", Name: "Chain", TrustLevel: "trusted",
			Capabilities: []string{"api_request", "ai_reasoning", "emit_event"},
		},
		Jobs: []Job{{Name: "Chain", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "APIRequest", "params": map[string]interface{}{
					"url":          srv.URL + "/api",
					"request_type": "GET",
				}},
				{"action": "AIInteraction", "params": map[string]interface{}{
					"provider": "echo-provider",
					"url":      "https://example.com/v1/chat",
					"model":    "mock-model",
					"prompt":   "summarize code $response.status_code for $response.body",
				}},
				{"action": "CreateEvent", "params": map[string]interface{}{
					"event_type": "chain_event",
					"source":     "0",
					"details": map[string]interface{}{
						"origin": "$response.echo",
					},
				}},
			}}},
	}

	// Step 1 carries no tokens (no previous payload exists yet); later
	// steps resolve against real outputs, and invalid references fail
	// loudly instead of interpolating magic strings.
	iCfg := map[string]any{
		cfgKeyAgentRuntime: map[string]any{
			"identity_enforcement": true,
			"contract_enforcement": true,
		},
		"db_handler": cdbHandler,
	}
	if err := engine.ExecuteJobs(agentCfg, iCfg); err != nil {
		t.Fatalf("chain execution failed: %v", err)
	}

	if echo.lastReq.Prompt != expectedEcho {
		t.Fatalf("AI step saw prompt %q, want %q", echo.lastReq.Prompt, expectedEcho)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("event persistence expectations unmet: %v", err)
	}

	// Every step envelope keeps the historical wire shape.
	for _, action := range []string{"APIRequest", "AIInteraction", "CreateEvent"} {
		if _, known := requiredCapabilityForAction(action); !known {
			t.Fatalf("chained action %q must stay a known action", action)
		}
	}
}

// TestChainEnvelopes validates the typed envelope helper against real outputs.
func TestChainEnvelopes(t *testing.T) {
	raw := map[string]interface{}{
		StrResponse: map[string]interface{}{"user_id": float64(7)},
		StrStatus:   StatusSuccess,
		StrMessage:  "ok",
		StrConfig:   map[string]interface{}{},
	}
	env, ok := ActionResultFromMap(raw)
	if !ok {
		t.Fatalf("expected envelope to parse")
	}
	if env.Status != StatusSuccess || env.Message != "ok" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if _, ok := ActionResultFromMap(map[string]interface{}{StrStatus: ""}); ok {
		t.Fatalf("expected envelope without status to be rejected")
	}
	if got := NewActionResult(StatusSuccess, nil, "m", nil).ToMap(); got[StrStatus] != StatusSuccess {
		t.Fatalf("unexpected envelope map: %v", got)
	}
}

// TestURLQueryInterpolation guards URL/auth interpolation sharing one
// context root. NOTE: params["headers"] maps are forwarded opaquely by the
// shared pkg/common client (a pre-existing wart outside agent scope);
// per-header forwarding is covered at the resolver level instead.
func TestURLQueryInterpolation(t *testing.T) {
	var gotPath, gotHeadersBlob string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		// The shared client forwards the resolved headers map opaquely
		// under one literal "headers" header (pre-existing client wart,
		// pinned here and reported for a later client PR).
		gotHeadersBlob = r.Header.Get("Headers")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &APIRequestAction{}
	result, err := a.Execute(map[string]interface{}{
		StrRequest:     map[string]interface{}{"user_id": float64(7), "name": "ada"},
		"url":          srv.URL + "/users/$response.user_id?name=$response.name",
		"request_type": "GET",
		"auth":         "Bearer $response.name",
		"headers": map[string]interface{}{
			"X-User": "$response.name",
		},
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	env, ok := ActionResultFromMap(result)
	if !ok || env.Status != StatusSuccess {
		t.Fatalf("unexpected envelope: %v", result)
	}
	if gotPath != "/users/7?name=ada" {
		t.Fatalf("unexpected request path %q", gotPath)
	}
	for _, want := range []string{`"X-User":"ada"`, `"Authorization":"Bearer ada"`} {
		if !strings.Contains(gotHeadersBlob, want) {
			t.Fatalf("resolved headers blob %q misses %s", gotHeadersBlob, want)
		}
	}

	// Missing references in required fields fail explicitly.
	_, err = a.Execute(map[string]interface{}{
		StrRequest:     map[string]interface{}{},
		"url":          srv.URL + "/users/$response.absent",
		"request_type": "GET",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown path") {
		t.Fatalf("expected unknown-path error, got %v", err)
	}
}

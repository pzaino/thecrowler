package agent

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// preflightTestDatabase bounds connection setup so bad integration
// credentials fail visibly instead of entering the service retry loop.
func preflightTestDatabase(t *testing.T, config cfg.Config) {
	t.Helper()
	endpoint := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(config.Database.Host, strconv.Itoa(config.Database.Port)),
		Path:   "/" + config.Database.DBName,
		User:   url.UserPassword(config.Database.User, config.Database.Password),
	}
	query := endpoint.Query()
	query.Set("sslmode", config.Database.SSLMode)
	endpoint.RawQuery = query.Encode()
	db, err := sql.Open("postgres", endpoint.String())
	if err != nil {
		t.Fatalf("database preflight open: %v", err)
	}
	defer db.Close() //nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("database preflight failed: %v", err)
	}
}

func liveOllamaDBConfig(t *testing.T) cfg.Config {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 and DOCKER_POSTGRES_* to run the live native-tool test")
	}
	value := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	port, err := strconv.Atoi(value("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	return cfg.Config{Database: cfg.Database{
		Type: cdb.DBPostgresStr, Host: value("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
		Port: port, User: value("DOCKER_POSTGRES_DB_USER", "postgres"),
		Password: value("DOCKER_POSTGRES_PASSWORD", "postgres"),
		DBName:   value("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode:  value("DOCKER_POSTGRES_SSL_MODE", "disable"),
		MaxConns: 10, MaxIdleConns: 5,
	}}
}

func liveOllamaFixtureSource(t *testing.T) (cdb.Handler, uint64) {
	t.Helper()
	config := liveOllamaDBConfig(t)
	preflightTestDatabase(t, config)
	db, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("create database handler: %v", err)
	}
	if err := db.Connect(config); err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	suffix := time.Now().UnixNano()
	source := &cdb.Source{
		URL:  fmt.Sprintf("https://wp03a-live-%d.example/", suffix),
		Name: fmt.Sprintf("wp03a-live-%d", suffix), Priority: "high", Restricted: 1,
	}
	sourceID, err := cdb.CreateSourceContext(context.Background(), &db, source, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create fixture source: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID) })
	return db, sourceID
}

func liveCrawlStatusSpec(t *testing.T, baseURL, model string, sourceID uint64, prompt string) *AgentGoalSpec {
	t.Helper()
	args := map[string]any{"source_id": float64(sourceID)}
	params := goalTestParams()
	params["provider"] = "ollama"
	params["url"] = baseURL
	params["model"] = model
	params["prompt"] = prompt
	params["allowlist"] = []any{"get_source_crawl_status"}
	params["allowed_sources"] = []any{float64(sourceID)}
	params["evidence"] = map[string]any{
		"allow":             []any{"tool_observation"},
		"default_max_age":   "15m",
		"max_payload_bytes": float64(4096),
	}
	params["budgets"] = map[string]any{
		"max_model_rounds": float64(3), "max_tool_calls": float64(2),
		"max_evidence_records": float64(4), "max_evidence_bytes": float64(16384),
		"max_total_result_bytes": float64(32768), "max_elapsed": "60s",
		"max_consecutive_denials": float64(2),
	}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{map[string]any{
			"id":          "crawl_status_observed",
			"description": "The pinned crawl-status tool observed the fixture source.",
			"predicate": map[string]any{
				"kind": "evidence_field_equals", "evidence": "tool_observation",
				"where": map[string]any{
					"tool":             "get_source_crawl_status",
					"arguments_sha256": argumentsHash(args),
				},
				"path": "status", "value": "new",
			},
		}},
	}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "WP03A Live Agent", 0)
	if err != nil {
		t.Fatalf("parse live goal spec: %v", err)
	}
	return spec
}

// TestAgentGoalRunLiveOllamaNativeToolOptional exercises the real registered
// crawl-status tool through a real Ollama model and a disposable database
// source. It requires both Ollama and THECROWLER_POSTGRES_INTEGRATION=1.
func TestAgentGoalRunLiveOllamaNativeToolOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	db, sourceID := liveOllamaFixtureSource(t)
	resetLLMProvidersForTest()
	RegisterLLMProvider(NewOllamaProvider([]string{model}))
	t.Cleanup(func() { resetLLMProvidersForTest() })
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = nil })

	prompt := fmt.Sprintf("Call the get_source_crawl_status tool exactly once with source_id %d. After the tool result, reply with the single word done.", sourceID)
	spec := liveCrawlStatusSpec(t, baseURL, model, sourceID, prompt)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), db, nil))
	if err != nil {
		if output, ok := result[StrResponse].(map[string]any); ok {
			t.Logf("live native-tool failure output: %v", output)
		}
		t.Fatalf("live native-tool goal failed: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("live native-tool goal did not meet criteria: %v", output)
	}
	toolCalls, ok := output["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		t.Fatalf("missing live tool call: %v", output["tool_calls"])
	}
	executed := false
	for _, item := range toolCalls {
		call := item.(map[string]any)
		if call["name"] == "get_source_crawl_status" && call["status"] == "executed" {
			executed = true
		}
	}
	if !executed {
		t.Fatalf("native tool was not executed: %v", toolCalls)
	}
	evidence, ok := output["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		t.Fatalf("missing native-tool evidence: %v", output["evidence"])
	}
	record := evidence[0].(map[string]any)
	if record["kind"] != string(EvidenceToolObservation) || record["trust_class"] != string(TrustToolObservation) {
		t.Fatalf("unexpected native-tool evidence: %v", record)
	}
	if fmt.Sprint(record["source_ids"]) != fmt.Sprintf("[%d]", sourceID) {
		t.Fatalf("native-tool evidence source scope wrong: %v", record["source_ids"])
	}
	decision := output["completion_decision"].(map[string]any)
	if decision["criteria_met"] != true || decision["model_claim_used"] != false {
		t.Fatalf("verifier did not decide native-tool completion: %v", decision)
	}
	seenEvidence := false
	for _, event := range AgentsEngine.AuditEvents() {
		if event.Action != "AgentGoalRun" {
			continue
		}
		if event.ReasonCode == "evidence_recorded" {
			seenEvidence = true
		}
	}
	if !seenEvidence {
		t.Fatal("native-tool evidence audit missing")
	}
	t.Logf("live native-tool result: status=%v model_rounds=%v tool_calls=%d evidence=%d",
		output["status"], output["model_rounds"], len(toolCalls), len(evidence))
}

// TestAgentGoalRunLiveOllamaNativeToolScopeDenialOptional proves a real model
// proposal outside allowed_sources is denied before the tool reaches the DB.
func TestAgentGoalRunLiveOllamaNativeToolScopeDenialOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	db, sourceID := liveOllamaFixtureSource(t)
	resetLLMProvidersForTest()
	RegisterLLMProvider(NewOllamaProvider([]string{model}))
	t.Cleanup(func() { resetLLMProvidersForTest() })
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = nil })

	args := map[string]any{"source_id": float64(sourceID + 1)}
	params := goalTestParams()
	params["provider"] = "ollama"
	params["url"] = baseURL
	params["model"] = model
	params["prompt"] = fmt.Sprintf("Call the get_source_crawl_status tool exactly once with source_id %d. Do not call any other tool.", sourceID+1)
	params["allowlist"] = []any{"get_source_crawl_status"}
	params["allowed_sources"] = []any{float64(sourceID)}
	params["evidence"] = map[string]any{
		"allow": []any{"tool_observation"}, "default_max_age": "15m",
		"max_payload_bytes": float64(4096),
	}
	params["budgets"] = map[string]any{
		"max_model_rounds": float64(1), "max_tool_calls": float64(1),
		"max_evidence_records": float64(4), "max_evidence_bytes": float64(16384),
		"max_total_result_bytes": float64(32768), "max_elapsed": "30s",
		"max_consecutive_denials": float64(1),
	}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{map[string]any{
			"id": "status", "description": "The tool observation must not exist.",
			"predicate": map[string]any{
				"kind": "evidence_exists", "evidence": "tool_observation",
				"where": map[string]any{
					"tool":             "get_source_crawl_status",
					"arguments_sha256": argumentsHash(args),
				},
			},
		}},
	}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "WP03A Live Scope Agent", 0)
	if err != nil {
		t.Fatalf("parse live scope spec: %v", err)
	}

	// The real source exists, so a tool that reaches the DB would return a
	// result. The assertion below also checks the denied proposal summary.
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), db, nil))
	if err == nil {
		t.Fatal("out-of-scope live proposal must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] == string(GoalCriteriaMet) {
		t.Fatalf("out-of-scope live proposal produced criteria_met: %v", output)
	}
	if toolCalls, ok := output["tool_calls"].([]any); ok {
		for _, item := range toolCalls {
			call := item.(map[string]any)
			if call["status"] == "executed" {
				t.Fatalf("out-of-scope live tool executed: %v", call)
			}
		}
	}
}

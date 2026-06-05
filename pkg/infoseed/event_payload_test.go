package infoseed

import (
	"encoding/json"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestInformationSeedEventPayloadStableShape(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.ProviderCounts["allowed"] = 3
	stats.addProviderMetric("allowed", "candidates", 3)
	stats.CandidatesFound = 3
	stats.CandidatesAccepted = 2
	stats.addRejectedAtStage(CandidateRejectionStageBuiltInFilters, CandidateRejectionMinimumScore, 1)
	stats.SourcesCreated = 1
	stats.SourcesLinked = 2
	stats.SourceIDsCreated = []uint64{42}
	stats.SourceIDsLinked = []uint64{42, 41}

	payload := informationSeedEventPayload(cdb.InformationSeed{ID: 7, InformationSeed: "seed text", Attempts: 2}, 42, stats)

	assertPayloadKey(t, payload, "schema_version")
	assertPayloadKey(t, payload, "orchestration_model")
	assertPayloadKey(t, payload, "agent")
	assertPayloadKey(t, payload, "phase_catalog")
	assertPayloadKey(t, payload, "information_seed_id")
	assertPayloadKey(t, payload, "information_seed")
	assertPayloadKey(t, payload, "run_id")
	assertPayloadKey(t, payload, "run_attempt")
	assertPayloadKey(t, payload, "provider_counts")
	assertPayloadKey(t, payload, "provider_metrics")
	assertPayloadKey(t, payload, "candidate_counts")
	assertPayloadKey(t, payload, "candidate_rejection_counts")
	assertPayloadKey(t, payload, "candidate_rejection_stages")
	assertPayloadKey(t, payload, "sources_created")
	assertPayloadKey(t, payload, "sources_linked")
	assertPayloadKey(t, payload, "source_ids_created")
	assertPayloadKey(t, payload, "source_ids_linked")
	assertPayloadKey(t, payload, "error_summaries")

	if payload["run_id"] != "information-seed-7-attempt-2" || payload["run_attempt"] != 2 {
		t.Fatalf("unexpected run correlation fields: %#v", payload)
	}
	agent := payload["agent"].(map[string]interface{})
	if agent["agent_id"] != InformationSeedBuiltInAgentID || agent["agent_type"] != InformationSeedBuiltInAgentType || agent["origin"] != InformationSeedBuiltInAgentOrigin {
		t.Fatalf("unexpected default agent identity: %#v", agent)
	}
	phaseCatalog := payload["phase_catalog"].([]map[string]interface{})
	if len(phaseCatalog) == 0 {
		t.Fatal("expected phase catalog to distinguish built-in and user/plugin phases")
	}
	foundBuiltIn := false
	foundPlugin := false
	for _, phase := range phaseCatalog {
		if phase["origin"] == "built_in" {
			foundBuiltIn = true
		}
		if phase["origin"] == "user_or_plugin" {
			foundPlugin = true
		}
	}
	if !foundBuiltIn || !foundPlugin {
		t.Fatalf("expected built-in and user/plugin phases, got %#v", phaseCatalog)
	}
	candidateCounts := payload["candidate_counts"].(map[string]interface{})
	if candidateCounts["found"] != 3 || candidateCounts["accepted"] != 2 || candidateCounts["rejected"] != 1 {
		t.Fatalf("unexpected candidate counts: %#v", candidateCounts)
	}
	byStage := candidateCounts["by_stage"].(map[string]int)
	if byStage[CandidateRejectionStageBuiltInFilters] != 1 || byStage["source_persistence"] != 2 {
		t.Fatalf("unexpected stage counts: %#v", byStage)
	}
	linked := payload["source_ids_linked"].([]uint64)
	if len(linked) != 2 || linked[0] != 41 || linked[1] != 42 {
		t.Fatalf("expected stable sorted linked source ids, got %#v", linked)
	}
}

func TestInformationSeedEventPayloadRedactsProviderConfigHeadersAndParameters(t *testing.T) {
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, nil, informationSeedEventPayloadOptions{
		ProviderConfigs: map[string]cfg.InformationSeedProviderConfig{
			"allowed": {
				Provider:   "http_json",
				Host:       "https://example.invalid/search?client_secret=SHOULD_NOT_LEAK&safe=value",
				Parameters: map[string]string{"api_key": "SHOULD_NOT_LEAK", "safe": "value"},
				Headers:    map[string]string{"Authorization": "Bearer SECRET_TOKEN", "X-Trace": "trace"},
			},
		},
	})
	encoded := mustJSON(t, payload)
	assertNoSecret(t, encoded)
	providerConfigs := payload["provider_configs"].(map[string]interface{})
	allowed := providerConfigs["allowed"].(map[string]interface{})
	parameters := allowed["parameters"].(map[string]string)
	headers := allowed["headers"].(map[string]string)
	if parameters["api_key"] != informationSeedRedactedValue || parameters["safe"] != "value" {
		t.Fatalf("unexpected redacted parameters: %#v", parameters)
	}
	if headers["Authorization"] != informationSeedRedactedValue || headers["X-Trace"] != "trace" {
		t.Fatalf("unexpected redacted headers: %#v", headers)
	}
	if !strings.Contains(encoded, "client_secret=REDACTED") {
		t.Fatalf("expected redacted host query parameter, got %s", encoded)
	}
}

func TestInformationSeedEventPayloadRedactsPluginMetadata(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.PluginMetadata = []map[string]interface{}{{
		"plugin": "policy",
		"metadata": map[string]interface{}{
			"decision":      "accept",
			"api_token":     "SHOULD_NOT_LEAK",
			"nested_secret": map[string]interface{}{"password": "SECRET_PASSWORD"},
		},
	}}
	payload := informationSeedEventPayload(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, 0, stats)
	encoded := mustJSON(t, payload)
	assertNoSecret(t, encoded)
	if !strings.Contains(encoded, `"api_token":"REDACTED"`) || !strings.Contains(encoded, `"nested_secret":"REDACTED"`) {
		t.Fatalf("expected redacted plugin metadata, got %s", encoded)
	}
}

func TestInformationSeedEventPayloadRedactsErrors(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addError(providerQueryError{SeedID: 7, Failures: []providerFailure{{Provider: "bad", Summary: `bad https://example.invalid/?client_secret=SHOULD_NOT_LEAK authorization: SECRET_TOKEN password=SECRET_PASSWORD`}}})
	payload := informationSeedEventPayload(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, 0, stats)
	encoded := mustJSON(t, payload)
	assertNoSecret(t, encoded)
	if !strings.Contains(encoded, "client_secret=REDACTED") || !strings.Contains(strings.ToLower(encoded), "authorization:redacted") {
		t.Fatalf("expected redaction markers in error summary, got %s", encoded)
	}
}

func assertPayloadKey(t *testing.T, payload map[string]interface{}, key string) {
	t.Helper()
	if _, ok := payload[key]; !ok {
		t.Fatalf("payload missing key %q: %#v", key, payload)
	}
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(encoded)
}

func assertNoSecret(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"SHOULD_NOT_LEAK", "SECRET_TOKEN", "SECRET_PASSWORD"} {
		if strings.Contains(value, secret) {
			t.Fatalf("payload leaked %s: %s", secret, value)
		}
	}
}

func TestInformationSeedEventPayloadUsesExplicitAgentIdentity(t *testing.T) {
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, nil, informationSeedEventPayloadOptions{
		AgentIdentity: AgentIdentity{
			ID:          "system.infoseed.test",
			Name:        "Test Seed Agent",
			Type:        "system",
			TrustLevel:  "system",
			Origin:      "built_in",
			RuntimePath: "infoseed.Runner.test",
		},
	})
	agent := payload["agent"].(map[string]interface{})
	if agent["agent_id"] != "system.infoseed.test" || agent["runtime_path"] != "infoseed.Runner.test" {
		t.Fatalf("expected explicit agent identity in payload, got %#v", agent)
	}
}

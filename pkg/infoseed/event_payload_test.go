package infoseed

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qri-io/jsonschema"

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
	assertPayloadKey(t, payload, "provider_failures")
	assertPayloadKey(t, payload, "plugin_failures")

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

func TestInformationSeedEventPayloadRedactsProviderAndPluginFailures(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addProviderFailure("bad", "api_key=SHOULD_NOT_LEAK")
	stats.addPluginFailure("policy", "password=SECRET_PASSWORD")
	payload := informationSeedEventPayload(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, 0, stats)
	encoded := mustJSON(t, payload)
	assertNoSecret(t, encoded)
	if !strings.Contains(encoded, `"provider_failures"`) || !strings.Contains(encoded, `"plugin_failures"`) {
		t.Fatalf("expected provider and plugin failures in diagnostics, got %s", encoded)
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

func loadEventDefinitionSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile("../../schemas/crowler-event-schema.json")
	if err != nil {
		t.Fatalf("read event schema: %v", err)
	}
	var document struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse event schema: %v", err)
	}
	raw := document.Definitions[name]
	if len(raw) == 0 {
		t.Fatalf("event schema definition %q not found", name)
	}
	schema := &jsonschema.Schema{}
	if err := schema.UnmarshalJSON(raw); err != nil {
		t.Fatalf("compile event schema definition %q: %v", name, err)
	}
	return schema
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

func TestInformationSeedEventPayloadRedactsWebDriverProviderDiagnostics(t *testing.T) {
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, nil, informationSeedEventPayloadOptions{
		ProviderConfigs: map[string]cfg.InformationSeedProviderConfig{
			"browser": {
				Provider:    "browser_search",
				Transport:   "webdriver",
				Host:        "https://search.example.invalid/?api_token=SHOULD_NOT_LEAK",
				Parameters:  map[string]string{"token": "SHOULD_NOT_LEAK", "q": "safe"},
				Headers:     map[string]string{"Authorization": "Bearer SHOULD_NOT_LEAK", "Cookie": "session=SHOULD_NOT_LEAK", "X-Trace": "trace"},
				MaxRequests: 2,
				MaxPages:    1,
				PageSize:    10,
				Browser: cfg.InformationSeedBrowserConfig{
					NavigationTimeout:      45,
					PageReadinessTimeout:   5,
					HBSEnabled:             true,
					SeleniumFallback:       true,
					InitialActions:         []string{"init"},
					ConsentActions:         []string{"consent"},
					QueryActions:           []string{"query"},
					PaginationActions:      []string{"next"},
					ScrapingRules:          []string{"extract"},
					AllowedNavigationHosts: []string{"example.invalid"},
					MaxPages:               1,
					MaxRequests:            2,
					MaxCandidates:          10,
					ScreenshotOnError:      true,
				},
			},
		},
	})
	encoded := mustJSON(t, payload)
	assertNoSecret(t, encoded)
	providerConfigs := payload["provider_configs"].(map[string]interface{})
	browserProvider := providerConfigs["browser"].(map[string]interface{})
	if browserProvider["transport"] != "webdriver" {
		t.Fatalf("expected webdriver transport diagnostic, got %#v", browserProvider["transport"])
	}
	browser := browserProvider["browser"].(map[string]interface{})
	if browser["screenshot_on_error"] != true || browser["max_candidates"] != 10 {
		t.Fatalf("unexpected browser diagnostics: %#v", browser)
	}
	headers := browserProvider["headers"].(map[string]string)
	if headers["Authorization"] != informationSeedRedactedValue || headers["Cookie"] != informationSeedRedactedValue || headers["X-Trace"] != "trace" {
		t.Fatalf("unexpected redacted headers: %#v", headers)
	}
}

func TestRedactedProviderConfigSchemaMatchesRuntimeDiagnostics(t *testing.T) {
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 7, InformationSeed: "seed"}, nil, informationSeedEventPayloadOptions{
		ProviderConfigs: map[string]cfg.InformationSeedProviderConfig{
			"browser": {
				Provider:    "browser_search",
				Transport:   "webdriver",
				Host:        "https://search.example.invalid/?api_token=SHOULD_NOT_LEAK",
				Parameters:  map[string]string{"token": "SHOULD_NOT_LEAK", "q": "safe"},
				Headers:     map[string]string{"Authorization": "Bearer SHOULD_NOT_LEAK", "X-Trace": "trace"},
				MaxRequests: 2,
				MaxPages:    1,
				PageSize:    10,
				Browser: cfg.InformationSeedBrowserConfig{
					NavigationTimeout:      45,
					PageReadinessTimeout:   5,
					HBSEnabled:             true,
					SeleniumFallback:       true,
					InitialActions:         []string{"init"},
					ConsentActions:         []string{"consent"},
					QueryActions:           []string{"query"},
					PaginationActions:      []string{"next"},
					ScrapingRules:          []string{"extract"},
					AllowedNavigationHosts: []string{"example.invalid"},
					MaxPages:               1,
					MaxRequests:            2,
					MaxCandidates:          10,
					ScreenshotOnError:      true,
				},
			},
			"http": {
				Provider:    "http_json",
				Host:        "https://search.example.invalid/?api_key=SHOULD_NOT_LEAK",
				Parameters:  map[string]string{"api_key": "SHOULD_NOT_LEAK", "q": "safe"},
				Headers:     map[string]string{"X-Trace": "trace"},
				MaxRequests: 1,
				MaxPages:    1,
				PageSize:    10,
			},
		},
	})
	providerConfigs := payload["provider_configs"].(map[string]interface{})
	schema := loadEventDefinitionSchema(t, "RedactedProviderConfig")
	for name, raw := range providerConfigs {
		runtimeConfig, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("runtime provider config %q has type %T", name, raw)
		}
		encoded := mustJSON(t, runtimeConfig)
		assertNoSecret(t, encoded)
		issues, err := schema.ValidateBytes(context.Background(), []byte(encoded))
		if err != nil {
			t.Fatalf("validate runtime provider diagnostics for %q: %v", name, err)
		}
		if len(issues) != 0 {
			t.Fatalf("runtime provider diagnostics for %q do not match schema: %v", name, issues)
		}
	}
	httpConfig, ok := providerConfigs["http"].(map[string]interface{})
	if !ok {
		t.Fatalf("runtime HTTP provider config has type %T", providerConfigs["http"])
	}
	if httpConfig["transport"] != "http" {
		t.Fatalf("empty provider transport = %#v, want default http", httpConfig["transport"])
	}
	runtimeConfig, ok := providerConfigs["browser"].(map[string]interface{})
	if !ok {
		t.Fatalf("runtime provider config has type %T", providerConfigs["browser"])
	}

	cloneConfig := func() map[string]interface{} {
		clone := make(map[string]interface{}, len(runtimeConfig))
		for key, value := range runtimeConfig {
			clone[key] = value
		}
		return clone
	}

	withUnknownField := cloneConfig()
	withUnknownField["unexpected_diagnostic"] = "value"
	issues, err := schema.ValidateBytes(context.Background(), []byte(mustJSON(t, withUnknownField)))
	if err != nil {
		t.Fatalf("validate provider diagnostics with unknown field: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("schema accepted an unknown provider diagnostic field")
	}

	withInvalidTransport := cloneConfig()
	withInvalidTransport["transport"] = "browser"
	issues, err = schema.ValidateBytes(context.Background(), []byte(mustJSON(t, withInvalidTransport)))
	if err != nil {
		t.Fatalf("validate provider diagnostics with invalid transport: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("schema accepted an invalid provider transport")
	}

	withInvalidBrowser := cloneConfig()
	withInvalidBrowser["browser"] = "not-an-object"
	issues, err = schema.ValidateBytes(context.Background(), []byte(mustJSON(t, withInvalidBrowser)))
	if err != nil {
		t.Fatalf("validate provider diagnostics with invalid browser: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("schema accepted a non-object browser diagnostic")
	}
}

func TestInformationSeedBrowserDiagnosticsAreCountOnlyAndSecretSafe(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.BrowserDiagnostics["browser"] = map[string]map[string]int{
		"navigation": {"success:none": 1},
		"url_rejections": {
			"skipped:denied_host": 2,
		},
	}
	stats.BrowserDurationsMS["browser"] = map[string]int64{"lease_wait": 12, "navigation": 34}
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 8, InformationSeed: "seed"}, stats, informationSeedEventPayloadOptions{
		ProviderConfigs: map[string]cfg.InformationSeedProviderConfig{
			"browser": {
				Provider:   "browser_search",
				APIKey:     "configured-api-secret",
				APIToken:   "configured-token-secret",
				Token:      "configured-bearer-secret",
				Headers:    map[string]string{"Authorization": "Bearer configured-header-secret", "X-API-Key": "configured-key-secret"},
				Parameters: map[string]string{"password": "configured-password-secret"},
			},
		},
	})
	encoded := mustJSON(t, payload)
	for _, forbidden := range []string{
		"configured-api-secret", "configured-token-secret", "configured-bearer-secret",
		"configured-header-secret", "configured-key-secret", "configured-password-secret",
		"<html", "page_html", "screenshot_bytes", "data:image",
	} {
		if strings.Contains(strings.ToLower(encoded), strings.ToLower(forbidden)) {
			t.Fatalf("event diagnostics leaked forbidden value %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(encoded, `"url_rejections":{"skipped:denied_host":2}`) {
		t.Fatalf("event missing bounded browser diagnostics: %s", encoded)
	}
}

func TestInformationSeedEventPayloadMatchesFullEventSchema(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addProviderFailure("provider-a", "provider returned status 404")
	stats.CandidatesFound = 1
	stats.CandidatesRejected = 1
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 9, InformationSeed: "schema seed", Attempts: 2}, stats, informationSeedEventPayloadOptions{SourceID: 0})
	data, err := os.ReadFile("../../schemas/crowler-event-schema.json")
	if err != nil {
		t.Fatalf("read event schema: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse event schema: %v", err)
	}
	definitions, _ := document["definitions"].(map[string]interface{})
	detailsSchema := resolveLocalSchemaRefs(definitions["InformationSeedRunDiagnosticsDetails"], definitions)
	compiled := &jsonschema.Schema{}
	raw, err := json.Marshal(detailsSchema)
	if err != nil {
		t.Fatalf("serialize details schema: %v", err)
	}
	if err := compiled.UnmarshalJSON(raw); err != nil {
		t.Fatalf("compile details schema: %v", err)
	}
	issues, err := compiled.ValidateBytes(context.Background(), []byte(mustJSON(t, payload)))
	if err != nil {
		t.Fatalf("validate details: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("information seed details do not match schema: %v", issues)
	}
}

func resolveLocalSchemaRefs(value interface{}, definitions map[string]interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		if reference, ok := typed["$ref"].(string); ok && strings.HasPrefix(reference, "#/definitions/") {
			name := strings.TrimPrefix(reference, "#/definitions/")
			return resolveLocalSchemaRefs(definitions[name], definitions)
		}
		resolved := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			resolved[key] = resolveLocalSchemaRefs(child, definitions)
		}
		return resolved
	case []interface{}:
		resolved := make([]interface{}, len(typed))
		for index, child := range typed {
			resolved[index] = resolveLocalSchemaRefs(child, definitions)
		}
		return resolved
	default:
		return value
	}
}

func TestProviderFailureClassificationSurvivesEventPayload(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addError(providerQueryError{SeedID: 1, Failures: []providerFailure{{Provider: "p", Class: "not_found", StatusCode: 404, Retryable: false, Summary: "provider p returned status 404"}}})
	payload := informationSeedEventPayloadWithOptions(cdb.InformationSeed{ID: 1, InformationSeed: "schema"}, stats, informationSeedEventPayloadOptions{})
	failures := payload["provider_failures"].([]map[string]interface{})
	if len(failures) != 1 || failures[0]["class"] != "not_found" || failures[0]["status_code"] != 404 || failures[0]["retryable"] != false {
		t.Fatalf("failure classification was lost: %#v", failures)
	}
}

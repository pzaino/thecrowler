package infoseed

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const informationSeedRedactedValue = "REDACTED"

type informationSeedEventPayloadOptions struct {
	SourceID        uint64
	ProviderConfigs map[string]cfg.InformationSeedProviderConfig
	AgentIdentity   AgentIdentity
}

func buildInformationSeedEventPayload(seed cdb.InformationSeed, stats *seedDiscoveryStats, options informationSeedEventPayloadOptions) map[string]interface{} {
	runAttempt := seed.Attempts
	payload := map[string]interface{}{
		"schema_version":             "information_seed.run_diagnostics.v1",
		"orchestration_model":        "built_in_infoseed_runner",
		"agent":                      informationSeedAgentPayload(options.AgentIdentity),
		"phase_catalog":              informationSeedPhaseCatalog(),
		"information_seed_id":        seed.ID,
		"information_seed":           seed.InformationSeed,
		"run_id":                     informationSeedRunID(seed.ID, runAttempt),
		"run_attempt":                runAttempt,
		"source_id":                  options.SourceID,
		"provider_counts":            map[string]int{},
		"provider_metrics":           map[string]map[string]int{},
		"provider_configs":           redactProviderConfigs(options.ProviderConfigs),
		"candidate_counts":           defaultCandidateCounts(),
		"candidates_found":           0,
		"candidates_accepted":        0,
		"candidates_rejected":        0,
		"sources_created":            0,
		"sources_linked":             0,
		"source_ids_created":         []uint64{},
		"source_ids_linked":          []uint64{},
		"error_summaries":            []string{},
		"candidate_rejection_counts": map[string]int{},
		"candidate_rejection_stages": map[string]map[string]int{},
		"plugin_metadata":            []map[string]interface{}{},
	}
	if stats == nil {
		return payload
	}
	providerCounts := copyIntMap(stats.ProviderCounts)
	providerMetrics := copyNestedIntMap(stats.ProviderMetrics)
	rejections := copyIntMap(stats.RejectionCounts)
	stages := copyNestedIntMap(stats.RejectionStages)
	errors := make([]string, 0, len(stats.ErrorSummaries))
	for _, summary := range stats.ErrorSummaries {
		errors = append(errors, trimEventString(redactInformationSeedError(summary), 512))
	}
	pluginMetadata := make([]map[string]interface{}, 0, len(stats.PluginMetadata))
	for _, entry := range stats.PluginMetadata {
		if safe, ok := redactInformationSeedDiagnosticValue(entry).(map[string]interface{}); ok {
			pluginMetadata = append(pluginMetadata, safe)
		}
	}

	payload["provider_counts"] = providerCounts
	payload["provider_metrics"] = providerMetrics
	payload["candidate_counts"] = informationSeedCandidateCounts(stats, stages)
	payload["candidates_found"] = nonNegativeInt(stats.CandidatesFound)
	payload["candidates_accepted"] = nonNegativeInt(stats.CandidatesAccepted)
	payload["candidates_rejected"] = nonNegativeInt(stats.CandidatesRejected)
	payload["sources_created"] = nonNegativeInt(stats.SourcesCreated)
	payload["sources_linked"] = nonNegativeInt(stats.SourcesLinked)
	payload["source_ids_created"] = copyUint64Slice(stats.SourceIDsCreated)
	payload["source_ids_linked"] = copyUint64Slice(stats.SourceIDsLinked)
	payload["error_summaries"] = errors
	payload["candidate_rejection_counts"] = rejections
	payload["candidate_rejection_stages"] = stages
	payload["plugin_metadata"] = pluginMetadata
	return payload
}

func informationSeedAgentPayload(identity AgentIdentity) map[string]interface{} {
	identity = normalizeInformationSeedAgentIdentity(identity)
	return map[string]interface{}{
		"agent_id":     identity.ID,
		"agent_name":   identity.Name,
		"agent_type":   identity.Type,
		"trust_level":  identity.TrustLevel,
		"origin":       identity.Origin,
		"runtime_path": identity.RuntimePath,
	}
}

func informationSeedPhaseCatalog() []map[string]interface{} {
	return []map[string]interface{}{
		{"phase": "provider_discovery", "origin": "built_in", "runtime": "infoseed.Runner.queryProviders"},
		{"phase": CandidateRejectionStageNormalization, "origin": "built_in", "runtime": "infoseed.Runner.normalizeCandidates"},
		{"phase": CandidateRejectionStageBuiltInFilters, "origin": "built_in", "runtime": "infoseed.Runner.applyBuiltInCandidateFilters"},
		{"phase": CandidateRejectionStageUserPlugins, "origin": "user_or_plugin", "runtime": "infoseed.CandidateProcessor"},
		{"phase": CandidateRejectionStageSourceOverrideValidation, "origin": "user_or_plugin", "runtime": "infoseed.CandidateProcessor.source_override"},
		{"phase": "source_persistence", "origin": "built_in", "runtime": "pkg/database information-seed helpers"},
	}
}

func informationSeedRunID(seedID uint64, attempt int) string {
	if attempt < 0 {
		attempt = 0
	}
	return fmt.Sprintf("information-seed-%d-attempt-%d", seedID, attempt)
}

func defaultCandidateCounts() map[string]interface{} {
	return map[string]interface{}{
		"found":    0,
		"accepted": 0,
		"rejected": 0,
		"by_stage": map[string]int{
			"provider_discovery":                            0,
			CandidateRejectionStageNormalization:            0,
			CandidateRejectionStageBuiltInFilters:           0,
			CandidateRejectionStageUserPlugins:              0,
			CandidateRejectionStageSourceOverrideValidation: 0,
			"source_persistence":                            0,
		},
	}
}

func informationSeedCandidateCounts(stats *seedDiscoveryStats, stages map[string]map[string]int) map[string]interface{} {
	counts := defaultCandidateCounts()
	counts["found"] = nonNegativeInt(stats.CandidatesFound)
	counts["accepted"] = nonNegativeInt(stats.CandidatesAccepted)
	counts["rejected"] = nonNegativeInt(stats.CandidatesRejected)
	byStage := counts["by_stage"].(map[string]int)
	byStage["provider_discovery"] = nonNegativeInt(stats.CandidatesFound)
	byStage["source_persistence"] = nonNegativeInt(stats.SourcesLinked)
	for stage, reasons := range stages {
		total := 0
		for _, count := range reasons {
			total += nonNegativeInt(count)
		}
		if strings.TrimSpace(stage) != "" {
			byStage[stage] = total
		}
	}
	return counts
}

func redactProviderConfigs(configs map[string]cfg.InformationSeedProviderConfig) map[string]interface{} {
	safe := map[string]interface{}{}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		providerCfg := configs[name]
		safe[name] = map[string]interface{}{
			"provider":     providerCfg.Provider,
			"host":         redactInformationSeedURL(providerCfg.Host),
			"max_requests": providerCfg.MaxRequests,
			"max_pages":    providerCfg.MaxPages,
			"page_size":    providerCfg.PageSize,
			"parameters":   redactStringMap(providerCfg.Parameters),
			"headers":      redactStringMap(providerCfg.Headers),
		}
	}
	return safe
}

func redactStringMap(values map[string]string) map[string]string {
	safe := map[string]string{}
	for key, value := range values {
		if isInformationSeedSensitiveKey(key) {
			safe[key] = informationSeedRedactedValue
		} else {
			safe[key] = redactInformationSeedError(value)
		}
	}
	return safe
}

func redactInformationSeedDiagnosticValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		safe := map[string]interface{}{}
		for key, child := range typed {
			if isInformationSeedSensitiveKey(key) {
				safe[key] = informationSeedRedactedValue
			} else {
				safe[key] = redactInformationSeedDiagnosticValue(child)
			}
		}
		return safe
	case map[string]string:
		return redactStringMap(typed)
	case []interface{}:
		safe := make([]interface{}, 0, len(typed))
		for _, child := range typed {
			safe = append(safe, redactInformationSeedDiagnosticValue(child))
		}
		return safe
	case []map[string]interface{}:
		safe := make([]map[string]interface{}, 0, len(typed))
		for _, child := range typed {
			safe = append(safe, redactInformationSeedDiagnosticValue(child).(map[string]interface{}))
		}
		return safe
	case string:
		return redactInformationSeedURL(redactInformationSeedError(typed))
	case json.RawMessage:
		var decoded interface{}
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return nil
		}
		return redactInformationSeedDiagnosticValue(decoded)
	default:
		return typed
	}
}

func redactInformationSeedURL(message string) string {
	parsed, err := url.Parse(message)
	if err != nil || parsed.RawQuery == "" {
		return message
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if isInformationSeedSensitiveKey(key) {
			query.Set(key, informationSeedRedactedValue)
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return message
}

func isInformationSeedSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "api-key", "apikey", "key", "token", "api-token", "subscription-key", "ocp-apim-subscription-key", "x-subscription-token", "authorization", "password", "secret", "api-secret", "client-secret", "access-token", "refresh-token", "credential", "credentials":
		return true
	default:
		return strings.Contains(normalized, "secret") || strings.Contains(normalized, "token") || strings.Contains(normalized, "password") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "api-key") || strings.Contains(normalized, "authorization")
	}
}

func copyIntMap(values map[string]int) map[string]int {
	safe := map[string]int{}
	for k, v := range values {
		if strings.TrimSpace(k) != "" {
			safe[k] = nonNegativeInt(v)
		}
	}
	return safe
}
func copyNestedIntMap(values map[string]map[string]int) map[string]map[string]int {
	safe := map[string]map[string]int{}
	for k, inner := range values {
		if strings.TrimSpace(k) == "" {
			continue
		}
		safe[k] = copyIntMap(inner)
	}
	return safe
}
func copyUint64Slice(values []uint64) []uint64 {
	if len(values) == 0 {
		return []uint64{}
	}
	safe := append([]uint64(nil), values...)
	sort.Slice(safe, func(i, j int) bool { return safe[i] < safe[j] })
	return safe
}
func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

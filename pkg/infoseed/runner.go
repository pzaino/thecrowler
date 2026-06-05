// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"text/template"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

// CandidateProcessor can transform or reject a discovered candidate. Returning
// false drops the candidate without failing the whole seed run.
type CandidateProcessor interface {
	ProcessCandidate(ctx context.Context, input CandidatePluginInput) (Candidate, bool, error)
}

// NamedCandidateProcessor can be selected by name from InformationSeed.config.
type NamedCandidateProcessor interface {
	CandidateProcessor
	ProcessorName() string
}

// Runner executes information-seed discovery work.
type Runner struct {
	DB         *cdb.Handler
	Config     cfg.InformationSeedConfig
	Providers  map[string]searchproviders.Provider
	Processors []CandidateProcessor
	Now        func() time.Time
}

// SeedRunConfig is read from InformationSeed.config. All fields are optional.
type SeedRunConfig struct {
	Queries            []string        `json:"queries" yaml:"queries"`
	QueryTemplates     []string        `json:"query_templates" yaml:"query_templates"`
	Providers          []string        `json:"providers" yaml:"providers"`
	TrackingParams     []string        `json:"tracking_params" yaml:"tracking_params"`
	DeduplicateHost    bool            `json:"deduplicate_host" yaml:"deduplicate_host"`
	MaxCandidates      int             `json:"max_candidates" yaml:"max_candidates"`
	SourceNameTemplate string          `json:"source_name_template" yaml:"source_name_template"`
	SourcePriority     string          `json:"source_priority" yaml:"source_priority"`
	Restricted         uint            `json:"restricted" yaml:"restricted"`
	Flags              uint            `json:"flags" yaml:"flags"`
	CandidatePlugins   []string        `json:"candidate_plugins" yaml:"candidate_plugins"`
	SourceConfig       json.RawMessage `json:"source_config" yaml:"source_config"`
}

// Result summarizes one seed processing run.
type Result struct {
	SeedID             uint64
	Candidates         int
	CandidatesFound    int
	CandidatesRejected int
	SourcesCreated     int
	Linked             int
}

const (
	informationSeedDiscoveryStarted   = "information_seed.discovery_started"
	informationSeedCandidateFound     = "information_seed.candidate_found"
	informationSeedCandidateRejected  = "information_seed.candidate_rejected"
	informationSeedSourceCreated      = "information_seed.source_created"
	informationSeedDiscoveryCompleted = "information_seed.discovery_completed"
	informationSeedDiscoveryFailed    = "information_seed.discovery_failed"
)

type seedDiscoveryStats struct {
	ProviderCounts     map[string]int
	CandidatesFound    int
	CandidatesAccepted int
	CandidatesRejected int
	SourcesCreated     int
	SourcesLinked      int
	RejectionCounts    map[string]int
	ErrorSummaries     []string
}

func newSeedDiscoveryStats() *seedDiscoveryStats {
	return &seedDiscoveryStats{ProviderCounts: map[string]int{}, RejectionCounts: map[string]int{}}
}

func (stats *seedDiscoveryStats) addRejected(reason string, count int) {
	if stats == nil || count <= 0 {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unspecified"
	}
	stats.CandidatesRejected += count
	stats.RejectionCounts[reason] += count
}

func (stats *seedDiscoveryStats) addError(err error) {
	if stats == nil || err == nil {
		return
	}
	stats.ErrorSummaries = append(stats.ErrorSummaries, trimEventString(err.Error(), 512))
}

// NewRunner constructs a runner with providers from configuration.
func NewRunner(db *cdb.Handler, config cfg.InformationSeedConfig) *Runner {
	return &Runner{
		DB:        db,
		Config:    config,
		Providers: BuildProviders(config),
		Now:       time.Now,
	}
}

// BuildProviders creates configured provider implementations.
func BuildProviders(config cfg.InformationSeedConfig) map[string]searchproviders.Provider {
	allowed := informationSeedAllowedProviders(config.ProviderAllowList)
	providers := make(map[string]searchproviders.Provider, len(config.Providers))
	for name, providerCfg := range config.Providers {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if len(allowed) == 0 {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		providers[key] = searchproviders.NewProvider(key, providerCfg.Provider)
	}
	return providers
}

func informationSeedAllowedProviders(allowList []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(allowList))
	for _, provider := range allowList {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider != "" {
			allowed[provider] = struct{}{}
		}
	}
	return allowed
}

func (r *Runner) emitInformationSeedEvent(ctx context.Context, seed cdb.InformationSeed, sourceID uint64, eventType, severity string, stats *seedDiscoveryStats) error {
	if r == nil || r.DB == nil {
		return nil
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	_, err := cdb.CreateEvent(ctx, r.DB, cdb.Event{
		SourceID:  sourceID,
		Type:      eventType,
		Severity:  severity,
		Timestamp: now().UTC().Format(time.RFC3339),
		Details:   informationSeedEventPayload(seed, sourceID, stats),
	})
	return err
}

func informationSeedEventPayload(seed cdb.InformationSeed, sourceID uint64, stats *seedDiscoveryStats) map[string]interface{} {
	payload := map[string]interface{}{
		"information_seed_id":        seed.ID,
		"information_seed":           seed.InformationSeed,
		"source_id":                  sourceID,
		"provider_counts":            map[string]int{},
		"candidates_found":           0,
		"candidates_accepted":        0,
		"candidates_rejected":        0,
		"sources_created":            0,
		"sources_linked":             0,
		"error_summaries":            []string{},
		"candidate_rejection_counts": map[string]int{},
	}
	if stats == nil {
		return payload
	}
	payload["provider_counts"] = stats.ProviderCounts
	payload["candidates_found"] = stats.CandidatesFound
	payload["candidates_accepted"] = stats.CandidatesAccepted
	payload["candidates_rejected"] = stats.CandidatesRejected
	payload["sources_created"] = stats.SourcesCreated
	payload["sources_linked"] = stats.SourcesLinked
	payload["error_summaries"] = stats.ErrorSummaries
	payload["candidate_rejection_counts"] = stats.RejectionCounts
	return payload
}

func trimEventString(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if maxLength <= 0 || len(value) <= maxLength {
		return value
	}
	if maxLength <= 3 {
		return value[:maxLength]
	}
	return value[:maxLength-3] + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RunSeed processes one already-claimed information seed and writes its final lifecycle status.
func (r *Runner) RunSeed(ctx context.Context, seed cdb.InformationSeed) (Result, error) {
	result := Result{SeedID: seed.ID}
	if r == nil || r.DB == nil {
		return result, fmt.Errorf("information seed runner is not configured")
	}
	if seed.Disabled {
		return result, cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "disabled", "")
	}

	stats := newSeedDiscoveryStats()
	runCfg, err := parseSeedRunConfig(seed)
	if err != nil {
		stats.addError(err)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}
	processingTimeout := ParseDurationOrDefault(r.Config.ProcessingTimeout, 30*time.Minute)
	if processingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, processingTimeout)
		defer cancel()
	}
	queries, err := renderQueries(seed, runCfg, r.Config.MaxQueriesPerSeed)
	if err != nil {
		stats.addError(err)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryStarted, cdb.EventSeverityInfo, stats)

	candidates, err := r.queryProviders(ctx, seed, runCfg, queries)
	for _, candidate := range candidates {
		stats.ProviderCounts[candidate.Provider]++
	}
	stats.CandidatesFound = len(candidates)
	result.CandidatesFound = stats.CandidatesFound
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedCandidateFound, cdb.EventSeverityInfo, stats)
	if err != nil {
		stats.addError(err)
	}
	if err != nil && len(candidates) == 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}

	trackingParams := append(defaultTrackingParams(), runCfg.TrackingParams...)
	normalized := NormalizeCandidates(candidates, CandidateOptions{TrackingParams: trackingParams, DeduplicateHost: runCfg.DeduplicateHost})
	stats.addRejected("normalization_or_deduplication", len(candidates)-len(normalized))
	limit := r.Config.MaxCandidatesPerSeed
	if runCfg.MaxCandidates > 0 && runCfg.MaxCandidates < limit {
		limit = runCfg.MaxCandidates
	}
	if limit > 0 && len(normalized) > limit {
		stats.addRejected("candidate_limit", len(normalized)-limit)
		normalized = normalized[:limit]
	}
	candidates = normalized

	beforeProcessors := len(candidates)
	candidates, err = r.applyProcessors(ctx, seed, runCfg, candidates, runCfg.CandidatePlugins)
	stats.addRejected("candidate_processor", beforeProcessors-len(candidates))
	if err != nil {
		stats.addError(err)
	}
	stats.CandidatesAccepted = len(candidates)
	stats.CandidatesRejected = maxInt(stats.CandidatesFound-stats.CandidatesAccepted, stats.CandidatesRejected)
	result.Candidates = len(candidates)
	result.CandidatesRejected = stats.CandidatesRejected
	if stats.CandidatesRejected > 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedCandidateRejected, cdb.EventSeverityInfo, stats)
	}
	if err != nil && len(candidates) == 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}

	linked, persistErr := r.persistCandidates(ctx, seed, runCfg, candidates, stats)
	result.Linked = linked
	result.SourcesCreated = stats.SourcesCreated
	if persistErr != nil {
		stats.addError(persistErr)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", persistErr.Error())
		return result, persistErr
	}
	if err := cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "completed", ""); err != nil {
		stats.addError(err)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		return result, err
	}
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryCompleted, cdb.EventSeverityInfo, stats)
	return result, err
}

func parseSeedRunConfig(seed cdb.InformationSeed) (SeedRunConfig, error) {
	runCfg := SeedRunConfig{}
	if seed.Config != nil && len(*seed.Config) > 0 && string(*seed.Config) != "null" {
		if err := json.Unmarshal(*seed.Config, &runCfg); err != nil {
			return runCfg, fmt.Errorf("invalid information seed config: %w", err)
		}
	}
	return runCfg, nil
}

func renderQueries(seed cdb.InformationSeed, runCfg SeedRunConfig, maxQueries int) ([]string, error) {
	templates := append([]string{}, runCfg.Queries...)
	templates = append(templates, runCfg.QueryTemplates...)
	if len(templates) == 0 {
		templates = []string{"{{ .Seed }}"}
	}
	if maxQueries > 0 && len(templates) > maxQueries {
		templates = templates[:maxQueries]
	}
	seen := map[string]struct{}{}
	queries := make([]string, 0, len(templates))
	data := map[string]interface{}{"Seed": seed.InformationSeed, "InformationSeed": seed.InformationSeed, "SeedID": seed.ID}
	for _, tmplText := range templates {
		tmpl, err := template.New("information-seed-query").Option("missingkey=zero").Parse(tmplText)
		if err != nil {
			return nil, fmt.Errorf("invalid information seed query template: %w", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("rendering information seed query: %w", err)
		}
		query := strings.TrimSpace(buf.String())
		if query == "" {
			continue
		}
		if _, ok := seen[query]; ok {
			continue
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
	}
	return queries, nil
}

func (r *Runner) queryProviders(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, queries []string) ([]Candidate, error) {
	providerNames := r.providerNames(runCfg)
	if len(providerNames) == 0 || len(queries) == 0 {
		return nil, nil
	}
	concurrency := r.Config.MaxConcurrentSeeds
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var candidates []Candidate
	var errs []string
	for _, providerName := range providerNames {
		provider := r.Providers[providerName]
		providerCfg := r.Config.Providers[providerName]
		providerQueries := cappedProviderQueries(queries, r.Config.MaxQueriesPerSeed, providerCfg)
		for _, query := range providerQueries {
			wg.Add(1)
			go func(providerName string, provider searchproviders.Provider, providerCfg cfg.InformationSeedProviderConfig, query string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				results, err := provider.Search(ctx, query, providerOptions(providerName, providerCfg))
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %s", providerName, redactInformationSeedError(err.Error())))
					return
				}
				for _, searchResult := range results {
					candidates = append(candidates, Candidate{URL: searchResult.URL, Title: searchResult.Title, Provider: providerName, Query: query, Rank: searchResult.Rank, Score: searchResult.Score, Metadata: searchResult.Metadata})
				}
			}(providerName, provider, providerCfg, query)
		}
	}
	wg.Wait()
	if len(errs) > 0 {
		return candidates, fmt.Errorf("information seed %d provider errors: %s", seed.ID, strings.Join(errs, "; "))
	}
	return candidates, nil
}

func (r *Runner) providerNames(runCfg SeedRunConfig) []string {
	configured := runCfg.Providers
	if len(configured) == 0 {
		configured = r.Config.ProviderAllowList
	}
	allowed := informationSeedAllowedProviders(r.Config.ProviderAllowList)
	names := make([]string, 0, len(configured))
	seen := map[string]struct{}{}
	for _, name := range configured {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || r.Providers[name] == nil {
			continue
		}
		if len(allowed) == 0 {
			continue
		}
		if _, ok := allowed[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func cappedProviderQueries(queries []string, globalMaxQueries int, providerCfg cfg.InformationSeedProviderConfig) []string {
	limit := len(queries)
	if globalMaxQueries > 0 && globalMaxQueries < limit {
		limit = globalMaxQueries
	}
	maxRequests := providerCfg.MaxRequests
	if maxRequests < 1 || maxRequests > limit {
		maxRequests = limit
	}
	maxPages := providerCfg.MaxPages
	if maxPages < 1 {
		maxPages = 1
	}
	searchesByRequests := maxRequests / maxPages
	if searchesByRequests < 1 {
		searchesByRequests = 1
	}
	if searchesByRequests < limit {
		limit = searchesByRequests
	}
	return queries[:limit]
}

func providerOptions(name string, providerCfg cfg.InformationSeedProviderConfig) searchproviders.Options {
	return searchproviders.Options{Name: name, Provider: providerCfg.Provider, Host: providerCfg.Host, Endpoint: providerCfg.Endpoint, APIKeyLabel: providerCfg.APIKeyLabel, APIKey: providerCfg.APIKey, APIToken: providerCfg.APIToken, Token: providerCfg.Token, Timeout: time.Duration(providerCfg.Timeout) * time.Second, MaxRequests: providerCfg.MaxRequests, Parameters: copyProviderMap(providerCfg.Parameters), Headers: copyProviderMap(providerCfg.Headers), PageSize: providerCfg.PageSize, MaxPages: providerCfg.MaxPages}
}

func copyProviderMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return copyValues
}

func redactInformationSeedError(message string) string {
	for _, marker := range []string{"api_key=", "apikey=", "key=", "token=", "secret=", "password=", "authorization:", "x-subscription-token:", "ocp-apim-subscription-key:"} {
		lower := strings.ToLower(message)
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		valueStart := idx + len(marker)
		for valueStart < len(message) && strings.ContainsRune(" \t", rune(message[valueStart])) {
			valueStart++
		}
		end := valueStart
		for end < len(message) && !strings.ContainsRune(" &;\n\t\r", rune(message[end])) {
			end++
		}
		message = message[:idx+len(marker)] + "REDACTED" + message[end:]
	}
	return message
}

func (r *Runner) applyProcessors(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, enabledNames []string) ([]Candidate, error) {
	processors := selectProcessors(r.Processors, enabledNames)
	if len(processors) == 0 {
		return candidates, nil
	}
	processed := make([]Candidate, 0, len(candidates))
	var errs []string
	for _, candidate := range candidates {
		keep := true
		current := candidate
		for _, processor := range processors {
			var err error
			input := candidatePluginInput(seed, runCfg, current)
			current, keep, err = processor.ProcessCandidate(ctx, input)
			if err != nil {
				errs = append(errs, err.Error())
				keep = false
				break
			}
			if !keep {
				break
			}
		}
		if keep {
			processed = append(processed, current)
		}
	}
	if len(errs) > 0 {
		return processed, fmt.Errorf("candidate processor errors: %s", strings.Join(errs, "; "))
	}
	return processed, nil
}

func selectProcessors(processors []CandidateProcessor, enabledNames []string) []CandidateProcessor {
	if len(enabledNames) == 0 {
		return processors
	}
	enabled := map[string]struct{}{}
	for _, name := range enabledNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			enabled[name] = struct{}{}
		}
	}
	selected := make([]CandidateProcessor, 0, len(processors))
	for _, processor := range processors {
		named, ok := processor.(NamedCandidateProcessor)
		if !ok {
			continue
		}
		if _, enabled := enabled[strings.ToLower(strings.TrimSpace(named.ProcessorName()))]; enabled {
			selected = append(selected, processor)
		}
	}
	return selected
}

func (r *Runner) persistCandidates(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, stats *seedDiscoveryStats) (int, error) {
	defaultSourceConfig, err := sourceConfigFromRaw(runCfg.SourceConfig)
	if err != nil {
		return 0, err
	}
	linked := 0
	for _, candidate := range candidates {
		name := candidate.Title
		if strings.TrimSpace(runCfg.SourceNameTemplate) != "" {
			rendered, err := renderSourceName(runCfg.SourceNameTemplate, seed, candidate)
			if err != nil {
				return linked, err
			}
			name = rendered
		}
		priority := runCfg.SourcePriority
		restricted := runCfg.Restricted
		flags := runCfg.Flags
		sourceConfig := defaultSourceConfig
		if candidate.SourceOverrides.Name != nil {
			name = strings.TrimSpace(*candidate.SourceOverrides.Name)
		}
		if candidate.SourceOverrides.Priority != nil {
			priority = strings.TrimSpace(*candidate.SourceOverrides.Priority)
		}
		if candidate.SourceOverrides.Restricted != nil {
			restricted = *candidate.SourceOverrides.Restricted
		}
		if candidate.SourceOverrides.Flags != nil {
			flags = *candidate.SourceOverrides.Flags
		}
		if len(candidate.SourceOverrides.SourceConfig) > 0 {
			if sourceConfig, err = sourceConfigFromRaw(candidate.SourceOverrides.SourceConfig); err != nil {
				return linked, err
			}
		}
		sourceID, err := cdb.CreateSource(r.DB, &cdb.Source{URL: candidate.URL, Name: name, Priority: priority, CategoryID: seed.CategoryID, UsrID: seed.UsrID, Restricted: restricted, Flags: flags}, sourceConfig)
		if err != nil {
			return linked, fmt.Errorf("creating source for seed %d candidate %s: %w", seed.ID, candidate.URL, err)
		}
		if stats != nil {
			stats.SourcesCreated++
		}
		_ = r.emitInformationSeedEvent(ctx, seed, sourceID, informationSeedSourceCreated, cdb.EventSeverityInfo, stats)
		metadata := discoveryMetadata(candidate)
		if err := cdb.LinkSourceToInformationSeedWithDiscoveryMetadata(r.DB, sourceID, seed.ID, metadata); err != nil {
			return linked, err
		}
		linked++
		if stats != nil {
			stats.SourcesLinked = linked
		}
	}
	return linked, nil
}

func sourceConfigFromRaw(raw json.RawMessage) (cfg.SourceConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return cfg.SourceConfig{}, nil
	}
	var sourceConfig cfg.SourceConfig
	if err := json.Unmarshal(raw, &sourceConfig); err != nil {
		return sourceConfig, fmt.Errorf("invalid information seed source_config: %w", err)
	}
	return sourceConfig, nil
}

func renderSourceName(tmplText string, seed cdb.InformationSeed, candidate Candidate) (string, error) {
	var buf bytes.Buffer
	tmpl, err := template.New("information-seed-source-name").Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return "", err
	}
	if err := tmpl.Execute(&buf, map[string]interface{}{"Seed": seed.InformationSeed, "Candidate": candidate, "URL": candidate.URL, "Host": candidate.Host}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func discoveryMetadata(candidate Candidate) cdb.InformationSeedDiscoveryMetadata {
	provider, query, reason := candidate.Provider, candidate.Query, candidate.Reason
	rank, score := candidate.Rank, candidate.Score
	var raw *json.RawMessage
	if len(candidate.Metadata) > 0 {
		if data, err := json.Marshal(candidate.Metadata); err == nil {
			msg := json.RawMessage(data)
			raw = &msg
		}
	}
	return cdb.InformationSeedDiscoveryMetadata{DiscoveryProvider: &provider, DiscoveryQuery: &query, DiscoveryRank: &rank, CandidateScore: &score, CandidateReason: &reason, DiscoveryMetadata: raw}
}

// CandidatePluginInput is the JSON payload passed to information-seed candidate plugins.
type CandidatePluginInput struct {
	Seed           map[string]interface{} `json:"seed"`
	Candidate      Candidate              `json:"candidate"`
	Metadata       CandidatePluginMeta    `json:"metadata"`
	SourceDefaults SourceDefaults         `json:"source_defaults"`
}

// CandidatePluginMeta describes where a candidate came from and its proposed ordering.
type CandidatePluginMeta struct {
	Provider string  `json:"provider"`
	Query    string  `json:"query"`
	Rank     int     `json:"rank"`
	Score    float64 `json:"score"`
}

// SourceDefaults describes the Source values that will be used unless a plugin
// returns safe source_overrides.
type SourceDefaults struct {
	Name         string          `json:"name"`
	Priority     string          `json:"priority"`
	CategoryID   uint64          `json:"category_id"`
	UsrID        uint64          `json:"usr_id"`
	Restricted   uint            `json:"restricted"`
	Flags        uint            `json:"flags"`
	SourceConfig json.RawMessage `json:"source_config,omitempty"`
}

type candidatePluginOutput struct {
	Accepted        *bool                  `json:"accepted"`
	Score           *float64               `json:"score"`
	Reason          *string                `json:"reason"`
	SourceOverrides map[string]interface{} `json:"source_overrides,omitempty"`
	Tags            []string               `json:"tags,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// JSPluginProcessor adapts an engine plugin into a candidate processor. The
// plugin receives seed, candidate, provider/query/rank metadata, and proposed
// source defaults under params.seed, params.candidate, params.metadata, and
// params.source_defaults. It must return JSON shaped as:
// {accepted: bool, score: number, reason: string, source_overrides?: object,
// tags?: string[], metadata?: object}.
type JSPluginProcessor struct {
	Plugin             plg.JSPlugin
	DB                 *cdb.Handler
	Timeout            int
	MaxOutputSizeBytes int
}

// ProcessorName returns the wrapped plugin name.
func (p JSPluginProcessor) ProcessorName() string {
	return p.Plugin.Name
}

// ProcessCandidate executes a JavaScript plugin for one candidate.
func (p JSPluginProcessor) ProcessCandidate(ctx context.Context, input CandidatePluginInput) (Candidate, bool, error) {
	candidate := input.Candidate
	select {
	case <-ctx.Done():
		return candidate, false, ctx.Err()
	default:
	}

	params := map[string]interface{}{
		"seed":            input.Seed,
		"candidate":       input.Candidate,
		"metadata":        input.Metadata,
		"source_defaults": input.SourceDefaults,
	}
	result, err := p.Plugin.Execute(nil, p.DB, p.Timeout, params)
	if err != nil {
		return candidate, false, fmt.Errorf("candidate plugin %q failed: %w", p.Plugin.Name, err)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return candidate, false, fmt.Errorf("candidate plugin %q returned non-json output: %w", p.Plugin.Name, err)
	}
	if p.MaxOutputSizeBytes > 0 && len(encoded) > p.MaxOutputSizeBytes {
		return candidate, false, fmt.Errorf("candidate plugin %q output exceeded %d bytes", p.Plugin.Name, p.MaxOutputSizeBytes)
	}

	output, err := validateCandidatePluginOutput(encoded)
	if err != nil {
		return candidate, false, fmt.Errorf("candidate plugin %q malformed output: %w", p.Plugin.Name, err)
	}
	if !*output.Accepted {
		candidate.Score = *output.Score
		candidate.Reason = *output.Reason
		return candidate, false, nil
	}

	candidate.Score = *output.Score
	candidate.Reason = strings.TrimSpace(*output.Reason)
	if len(output.Tags) > 0 || len(output.Metadata) > 0 {
		if candidate.Metadata == nil {
			candidate.Metadata = map[string]interface{}{}
		}
		if len(output.Tags) > 0 {
			candidate.Metadata["tags"] = output.Tags
		}
		if len(output.Metadata) > 0 {
			candidate.Metadata["plugin_metadata"] = output.Metadata
		}
	}
	if len(output.SourceOverrides) > 0 {
		overrides, err := validateSourceOverrides(output.SourceOverrides)
		if err != nil {
			return candidate, false, fmt.Errorf("candidate plugin %q unsafe source_overrides: %w", p.Plugin.Name, err)
		}
		candidate.SourceOverrides = overrides
	}
	return candidate, true, nil
}

func candidatePluginInput(seed cdb.InformationSeed, runCfg SeedRunConfig, candidate Candidate) CandidatePluginInput {
	name := candidate.Title
	if strings.TrimSpace(runCfg.SourceNameTemplate) != "" {
		if rendered, err := renderSourceName(runCfg.SourceNameTemplate, seed, candidate); err == nil {
			name = rendered
		}
	}
	return CandidatePluginInput{
		Seed: map[string]interface{}{
			"id":               seed.ID,
			"information_seed": seed.InformationSeed,
			"category_id":      seed.CategoryID,
			"usr_id":           seed.UsrID,
			"status":           seed.Status,
			"priority":         seed.Priority,
		},
		Candidate: candidate,
		Metadata:  CandidatePluginMeta{Provider: candidate.Provider, Query: candidate.Query, Rank: candidate.Rank, Score: candidate.Score},
		SourceDefaults: SourceDefaults{
			Name:         name,
			Priority:     runCfg.SourcePriority,
			CategoryID:   seed.CategoryID,
			UsrID:        seed.UsrID,
			Restricted:   runCfg.Restricted,
			Flags:        runCfg.Flags,
			SourceConfig: runCfg.SourceConfig,
		},
	}
}

func validateCandidatePluginOutput(data []byte) (candidatePluginOutput, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var output candidatePluginOutput
	if err := dec.Decode(&output); err != nil {
		return output, err
	}
	if output.Accepted == nil {
		return output, fmt.Errorf("accepted is required")
	}
	if output.Score == nil || math.IsNaN(*output.Score) || math.IsInf(*output.Score, 0) {
		return output, fmt.Errorf("score must be a finite number")
	}
	if output.Reason == nil || strings.TrimSpace(*output.Reason) == "" {
		return output, fmt.Errorf("reason is required")
	}
	for _, tag := range output.Tags {
		if strings.TrimSpace(tag) == "" {
			return output, fmt.Errorf("tags must be non-empty strings")
		}
	}
	return output, nil
}

func validateSourceOverrides(raw map[string]interface{}) (SourceOverrides, error) {
	var overrides SourceOverrides
	for key, value := range raw {
		switch key {
		case "name":
			str, ok := value.(string)
			if !ok || strings.TrimSpace(str) == "" {
				return overrides, fmt.Errorf("name must be a non-empty string")
			}
			overrides.Name = &str
		case "priority":
			str, ok := value.(string)
			if !ok {
				return overrides, fmt.Errorf("priority must be a string")
			}
			overrides.Priority = &str
		case "restricted":
			uintValue, err := jsonNumberToUint(value)
			if err != nil {
				return overrides, fmt.Errorf("restricted %w", err)
			}
			overrides.Restricted = &uintValue
		case "flags":
			uintValue, err := jsonNumberToUint(value)
			if err != nil {
				return overrides, fmt.Errorf("flags %w", err)
			}
			overrides.Flags = &uintValue
		case "source_config":
			data, err := json.Marshal(value)
			if err != nil {
				return overrides, err
			}
			if _, err := sourceConfigFromRaw(data); err != nil {
				return overrides, err
			}
			overrides.SourceConfig = json.RawMessage(data)
		default:
			return overrides, fmt.Errorf("field %q is not allowed", key)
		}
	}
	return overrides, nil
}

func jsonNumberToUint(value interface{}) (uint, error) {
	floatValue, ok := value.(float64)
	if !ok || math.Trunc(floatValue) != floatValue || floatValue < 0 || floatValue > float64(^uint(0)) {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return uint(floatValue), nil
}

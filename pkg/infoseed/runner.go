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
	"net/url"
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

// AgentIdentity identifies the system or user agent responsible for a seed run.
type AgentIdentity struct {
	ID          string
	Name        string
	Type        string
	TrustLevel  string
	Origin      string
	RuntimePath string
}

// Runner executes information-seed discovery work.
type Runner struct {
	DB            *cdb.Handler
	Config        cfg.InformationSeedConfig
	Providers     map[string]searchproviders.Provider
	Processors    []CandidateProcessor
	Now           func() time.Time
	AgentIdentity AgentIdentity
}

// SeedRunConfig is read from InformationSeed.config. All fields are optional.
type SeedRunConfig struct {
	Queries                    []string        `json:"queries" yaml:"queries"`
	QueryTemplates             []string        `json:"query_templates" yaml:"query_templates"`
	Providers                  []string        `json:"providers" yaml:"providers"`
	TrackingParams             []string        `json:"tracking_params" yaml:"tracking_params"`
	DeduplicateHost            bool            `json:"deduplicate_host" yaml:"deduplicate_host"`
	MaxCandidates              int             `json:"max_candidates" yaml:"max_candidates"`
	AllowedDomains             []string        `json:"allowed_domains" yaml:"allowed_domains"`
	DeniedDomains              []string        `json:"denied_domains" yaml:"denied_domains"`
	RequiredURLSchemes         []string        `json:"required_url_schemes" yaml:"required_url_schemes"`
	MinScore                   *float64        `json:"min_score" yaml:"min_score"`
	MaxCandidatesPerHost       int             `json:"max_candidates_per_host" yaml:"max_candidates_per_host"`
	MaxCandidatesPerDomain     int             `json:"max_candidates_per_domain" yaml:"max_candidates_per_domain"`
	SourceNameTemplate         string          `json:"source_name_template" yaml:"source_name_template"`
	SourcePriority             string          `json:"source_priority" yaml:"source_priority"`
	CreateSources              bool            `json:"create_sources" yaml:"create_sources"`
	LinkExistingSources        bool            `json:"link_existing_sources" yaml:"link_existing_sources"`
	UpdateExistingSourceConfig bool            `json:"update_existing_source_config" yaml:"update_existing_source_config"`
	Disabled                   bool            `json:"disabled" yaml:"disabled"`
	Status                     string          `json:"status" yaml:"status"`
	Restricted                 uint            `json:"restricted" yaml:"restricted"`
	Flags                      uint            `json:"flags" yaml:"flags"`
	CandidatePlugins           []string        `json:"candidate_plugins" yaml:"candidate_plugins"`
	SourceConfig               json.RawMessage `json:"source_config" yaml:"source_config"`
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
	InformationSeedBuiltInAgentID          = "system.infoseed.runner"
	InformationSeedBuiltInAgentName        = "Information Seed Agent"
	InformationSeedBuiltInAgentType        = "system"
	InformationSeedBuiltInAgentTrustLevel  = "system"
	InformationSeedBuiltInAgentOrigin      = "built_in"
	InformationSeedBuiltInAgentRuntimePath = "infoseed.Runner"

	informationSeedDiscoveryStarted   = "information_seed.discovery_started"
	informationSeedCandidateFound     = "information_seed.candidate_found"
	informationSeedCandidateRejected  = "information_seed.candidate_rejected"
	informationSeedSourceCreated      = "information_seed.source_created"
	informationSeedDiscoveryCompleted = "information_seed.discovery_completed"
	informationSeedDiscoveryFailed    = "information_seed.discovery_failed"
)

type candidateDecisionEvidence struct {
	Candidate Candidate
	Status    string
	Reason    string
	Stage     string
}

type seedDiscoveryStats struct {
	ProviderCounts       map[string]int
	ProviderMetrics      map[string]map[string]int
	CandidatesFound      int
	CandidatesAccepted   int
	CandidatesRejected   int
	SourcesCreated       int
	SourcesLinked        int
	RejectionCounts      map[string]int
	RejectionStages      map[string]map[string]int
	ErrorSummaries       []string
	SourceIDsCreated     []uint64
	SourceIDsLinked      []uint64
	PluginMetadata       []map[string]interface{}
	ProviderFailures     []providerFailure
	PluginFailures       map[string]int
	PluginFailureDetails []pluginFailure
}

func newSeedDiscoveryStats() *seedDiscoveryStats {
	return &seedDiscoveryStats{ProviderCounts: map[string]int{}, ProviderMetrics: map[string]map[string]int{}, RejectionCounts: map[string]int{}, RejectionStages: map[string]map[string]int{}, PluginFailures: map[string]int{}}
}

func (stats *seedDiscoveryStats) addProviderMetric(provider, metric string, count int) {
	if stats == nil || count <= 0 {
		return
	}
	provider = strings.TrimSpace(provider)
	metric = strings.TrimSpace(metric)
	if provider == "" || metric == "" {
		return
	}
	if stats.ProviderMetrics[provider] == nil {
		stats.ProviderMetrics[provider] = map[string]int{}
	}
	stats.ProviderMetrics[provider][metric] += count
}

func (stats *seedDiscoveryStats) addRejected(reason string, count int) {
	stats.addRejectedAtStage(CandidateRejectionStageBuiltInFilters, reason, count)
}

func (stats *seedDiscoveryStats) addRejectedAtStage(stage, reason string, count int) {
	if stats == nil || count <= 0 {
		return
	}
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "unspecified"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unspecified"
	}
	stats.CandidatesRejected += count
	stats.RejectionCounts[reason] += count
	if stats.RejectionStages[stage] == nil {
		stats.RejectionStages[stage] = map[string]int{}
	}
	stats.RejectionStages[stage][reason] += count
}

func (stats *seedDiscoveryStats) addRejectedMap(stage string, rejected map[string]int) {
	for reason, count := range rejected {
		stats.addRejectedAtStage(stage, reason, count)
	}
}

func (stats *seedDiscoveryStats) addRejectedDecisions(decisions []candidateDecisionEvidence) {
	for _, decision := range decisions {
		if decision.Status == cdb.InformationSeedCandidateDecisionRejected {
			stats.addRejectedAtStage(decision.Stage, decision.Reason, 1)
		}
	}
}

func (stats *seedDiscoveryStats) addPluginMetadataFromCandidates(candidates []Candidate) {
	if stats == nil {
		return
	}
	for _, candidate := range candidates {
		metadata, ok := candidate.Metadata["plugin_metadata"].(map[string]interface{})
		if !ok || len(metadata) == 0 {
			continue
		}
		stats.PluginMetadata = append(stats.PluginMetadata, map[string]interface{}{
			"provider": candidate.Provider,
			"host":     candidate.Host,
			"metadata": metadata,
		})
	}
}

func (stats *seedDiscoveryStats) addError(err error) {
	if stats == nil || err == nil {
		return
	}
	stats.ErrorSummaries = append(stats.ErrorSummaries, trimEventString(redactInformationSeedError(err.Error()), 512))
	if failures, ok := err.(interface{ ProviderFailures() []providerFailure }); ok {
		for _, failure := range failures.ProviderFailures() {
			stats.addProviderFailure(failure.Provider, failure.Summary)
		}
	}
}

func (stats *seedDiscoveryStats) addProviderFailure(provider, summary string) {
	if stats == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "unknown"
	}
	summary = trimEventString(redactInformationSeedError(summary), 512)
	stats.addProviderMetric(provider, "errors", 1)
	stats.ProviderFailures = append(stats.ProviderFailures, providerFailure{Provider: provider, Summary: summary})
}

type pluginFailure struct {
	Plugin  string
	Summary string
}

func (stats *seedDiscoveryStats) addPluginFailure(plugin, summary string) {
	if stats == nil {
		return
	}
	plugin = strings.TrimSpace(plugin)
	if plugin == "" {
		plugin = "unknown"
	}
	summary = trimEventString(redactInformationSeedError(summary), 512)
	if stats.PluginFailures == nil {
		stats.PluginFailures = map[string]int{}
	}
	stats.PluginFailures[plugin]++
	stats.PluginFailureDetails = append(stats.PluginFailureDetails, pluginFailure{Plugin: plugin, Summary: summary})
}

type providerFailure struct {
	Provider string
	Summary  string
}

type providerQueryError struct {
	SeedID   uint64
	Failures []providerFailure
}

func (e providerQueryError) Error() string {
	summaries := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		summaries = append(summaries, failure.Summary)
	}
	return fmt.Sprintf("information seed %d provider errors: %s", e.SeedID, strings.Join(summaries, "; "))
}

func (e providerQueryError) ProviderFailures() []providerFailure {
	return append([]providerFailure(nil), e.Failures...)
}

// NewRunner constructs a runner with providers from configuration. The optional
// options argument preserves the compatibility constructor used by tests and
// HTTP-only deployments while allowing application browser dependencies to be
// injected explicitly.
func NewRunner(db *cdb.Handler, config cfg.InformationSeedConfig, options ...RunnerOptions) *Runner {
	return &Runner{
		DB:            db,
		Config:        config,
		Providers:     BuildProviders(config, providerDependencies(options...)),
		Now:           time.Now,
		AgentIdentity: DefaultInformationSeedAgentIdentity(),
	}
}

// DefaultInformationSeedAgentIdentity returns the built-in system-agent identity
// used when operators have not provided an explicit test/runtime override.
func DefaultInformationSeedAgentIdentity() AgentIdentity {
	return AgentIdentity{
		ID:          InformationSeedBuiltInAgentID,
		Name:        InformationSeedBuiltInAgentName,
		Type:        InformationSeedBuiltInAgentType,
		TrustLevel:  InformationSeedBuiltInAgentTrustLevel,
		Origin:      InformationSeedBuiltInAgentOrigin,
		RuntimePath: InformationSeedBuiltInAgentRuntimePath,
	}
}

func normalizeInformationSeedAgentIdentity(identity AgentIdentity) AgentIdentity {
	defaultIdentity := DefaultInformationSeedAgentIdentity()
	identity.ID = strings.TrimSpace(identity.ID)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Type = strings.TrimSpace(identity.Type)
	identity.TrustLevel = strings.TrimSpace(identity.TrustLevel)
	identity.Origin = strings.TrimSpace(identity.Origin)
	identity.RuntimePath = strings.TrimSpace(identity.RuntimePath)
	if identity.ID == "" {
		identity.ID = defaultIdentity.ID
	}
	if identity.Name == "" {
		identity.Name = defaultIdentity.Name
	}
	if identity.Type == "" {
		identity.Type = defaultIdentity.Type
	}
	if identity.TrustLevel == "" {
		identity.TrustLevel = defaultIdentity.TrustLevel
	}
	if identity.Origin == "" {
		identity.Origin = defaultIdentity.Origin
	}
	if identity.RuntimePath == "" {
		identity.RuntimePath = defaultIdentity.RuntimePath
	}
	return identity
}

// BuildProviders creates configured provider implementations. Browser
// dependencies are optional so HTTP-only deployments remain VDI-independent.
func BuildProviders(config cfg.InformationSeedConfig, dependencies ...BrowserDependencies) map[string]searchproviders.Provider {
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
		provider := searchproviders.NewProvider(key, providerCfg.Provider)
		if browserProvider, ok := provider.(*searchproviders.BrowserSearchProvider); ok && strings.EqualFold(strings.TrimSpace(providerCfg.Transport), searchproviders.BrowserTransportWebDriver) {
			if len(dependencies) > 0 {
				browserProvider.Sessions = dependencies[0].Sessions
				browserProvider.Actions = dependencies[0].Actions
				browserProvider.Scraper = dependencies[0].Scraper
				browserProvider.Rules = dependencies[0].Rules
			}
		}
		providers[key] = provider
	}
	return providers
}

func providerDependencies(options ...RunnerOptions) BrowserDependencies {
	if len(options) == 0 {
		return BrowserDependencies{}
	}
	return options[0].Browser
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
		Details: informationSeedEventPayloadWithOptions(seed, stats, informationSeedEventPayloadOptions{
			SourceID:        sourceID,
			ProviderConfigs: r.Config.Providers,
			AgentIdentity:   normalizeInformationSeedAgentIdentity(r.AgentIdentity),
		}),
	})
	return err
}

func informationSeedEventPayload(seed cdb.InformationSeed, sourceID uint64, stats *seedDiscoveryStats) map[string]interface{} {
	return informationSeedEventPayloadWithOptions(seed, stats, informationSeedEventPayloadOptions{SourceID: sourceID})
}

func informationSeedEventPayloadWithOptions(seed cdb.InformationSeed, stats *seedDiscoveryStats, options informationSeedEventPayloadOptions) map[string]interface{} {
	return buildInformationSeedEventPayload(seed, stats, options)
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
	runStarted := time.Now()
	if r == nil || r.DB == nil {
		return result, fmt.Errorf("information seed runner is not configured")
	}
	if seed.Disabled {
		return result, cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "disabled", "")
	}

	stats := newSeedDiscoveryStats()
	defer func() { recordInformationSeedRunMetrics(stats, time.Since(runStarted)) }()
	runCfg, err := parseSeedRunConfig(seed)
	if err != nil {
		stats.addError(err)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(err.Error()))
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
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(err.Error()))
		return result, err
	}
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryStarted, cdb.EventSeverityInfo, stats)

	candidates, err := r.queryProvidersWithStats(ctx, seed, runCfg, queries, stats)
	for _, candidate := range candidates {
		stats.ProviderCounts[candidate.Provider]++
		stats.addProviderMetric(candidate.Provider, "candidates", 1)
	}
	stats.CandidatesFound = len(candidates)
	result.CandidatesFound = stats.CandidatesFound
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedCandidateFound, cdb.EventSeverityInfo, stats)
	partialProviderErr := error(nil)
	if err != nil {
		stats.addError(err)
		if len(candidates) > 0 {
			partialProviderErr = err
			err = nil
		}
	}
	if err != nil && len(candidates) == 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(err.Error()))
		return result, err
	}

	trackingParams := append(defaultTrackingParams(), runCfg.TrackingParams...)
	normalizedCandidates, normalizationDecisions := normalizeCandidatesWithDecisionEvidence(candidates, CandidateOptions{TrackingParams: trackingParams, DeduplicateHost: runCfg.DeduplicateHost})
	stats.addRejectedDecisions(normalizationDecisions)
	limit := r.Config.MaxCandidatesPerSeed
	if runCfg.MaxCandidates > 0 && (limit <= 0 || runCfg.MaxCandidates < limit) {
		limit = runCfg.MaxCandidates
	}
	filteredCandidates, filterDecisions := applyBuiltInCandidateFiltersWithDecisionEvidence(normalizedCandidates, CandidateFilters{AllowedDomains: runCfg.AllowedDomains, DeniedDomains: runCfg.DeniedDomains, RequiredSchemes: runCfg.RequiredURLSchemes, MinScore: runCfg.MinScore, MaxCandidatesPerHost: runCfg.MaxCandidatesPerHost, MaxCandidatesPerDomain: runCfg.MaxCandidatesPerDomain, MaxCandidates: limit})
	stats.addRejectedDecisions(filterDecisions)
	candidates = filteredCandidates

	candidates, processorDecisions, err := r.applyProcessorsWithDecisionEvidenceAndStats(ctx, seed, runCfg, candidates, runCfg.CandidatePlugins, stats)
	stats.addRejectedDecisions(processorDecisions)
	if err != nil {
		stats.addError(err)
	}
	stats.addPluginMetadataFromCandidates(candidates)
	stats.CandidatesAccepted = len(candidates)
	stats.CandidatesRejected = maxInt(stats.CandidatesFound-stats.CandidatesAccepted, stats.CandidatesRejected)
	result.Candidates = len(candidates)
	result.CandidatesRejected = stats.CandidatesRejected
	if stats.CandidatesRejected > 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedCandidateRejected, cdb.EventSeverityInfo, stats)
	}
	if err != nil && len(candidates) == 0 {
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(err.Error()))
		return result, err
	}

	acceptedDecisions := acceptedCandidateDecisions(candidates)
	allDecisions := append(append([]candidateDecisionEvidence{}, normalizationDecisions...), filterDecisions...)
	allDecisions = append(allDecisions, processorDecisions...)
	allDecisions = append(allDecisions, acceptedDecisions...)
	if auditErr := r.persistCandidateDecisionEvidence(seed, allDecisions); auditErr != nil {
		stats.addError(auditErr)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(auditErr.Error()))
		return result, auditErr
	}

	linked, persistErr := r.persistCandidates(ctx, seed, runCfg, candidates, stats)
	result.Linked = linked
	result.SourcesCreated = stats.SourcesCreated
	if persistErr != nil {
		stats.addError(persistErr)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", redactInformationSeedError(persistErr.Error()))
		return result, persistErr
	}
	if err := cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "completed", ""); err != nil {
		stats.addError(err)
		_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryFailed, cdb.EventSeverityError, stats)
		return result, err
	}
	severity := cdb.EventSeverityInfo
	if partialProviderErr != nil {
		severity = cdb.EventSeverityWarning
	}
	_ = r.emitInformationSeedEvent(ctx, seed, 0, informationSeedDiscoveryCompleted, severity, stats)
	return result, nil
}

func parseSeedRunConfig(seed cdb.InformationSeed) (SeedRunConfig, error) {
	runCfg := defaultSeedRunConfig()
	if seed.Config != nil && len(*seed.Config) > 0 && string(*seed.Config) != "null" {
		if err := json.Unmarshal(*seed.Config, &runCfg); err != nil {
			return runCfg, fmt.Errorf("invalid information seed config: %w", err)
		}
	}
	runCfg.Status = strings.TrimSpace(runCfg.Status)
	if runCfg.Status == "" {
		runCfg.Status = "new"
	}
	return runCfg, nil
}

func defaultSeedRunConfig() SeedRunConfig {
	return SeedRunConfig{
		CreateSources:              true,
		LinkExistingSources:        true,
		UpdateExistingSourceConfig: true,
		Disabled:                   false,
		Status:                     "new",
	}
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
	return r.queryProvidersWithStats(ctx, seed, runCfg, queries, nil)
}

func (r *Runner) queryProvidersWithStats(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, queries []string, stats *seedDiscoveryStats) ([]Candidate, error) {
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
	var failures []providerFailure
	for _, providerName := range providerNames {
		provider := r.Providers[providerName]
		providerCfg := r.Config.Providers[providerName]
		providerQueries := cappedProviderQueries(queries, r.Config.MaxQueriesPerSeed, providerCfg)
		perQueryMaxRequests := perQueryRequestLimit(providerCfg, len(providerQueries))
		for _, query := range providerQueries {
			wg.Add(1)
			go func(providerName string, provider searchproviders.Provider, providerCfg cfg.InformationSeedProviderConfig, query string, maxRequests int) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				results, err := provider.Search(ctx, query, providerOptionsWithMaxRequests(providerName, providerCfg, maxRequests))
				mu.Lock()
				defer mu.Unlock()
				if stats != nil {
					stats.addProviderMetric(providerName, "requests", 1)
				}
				if err != nil {
					summary := fmt.Sprintf("%s: %s", providerName, redactInformationSeedError(err.Error()))
					failures = append(failures, providerFailure{Provider: providerName, Summary: summary})
					return
				}
				for _, searchResult := range results {
					candidates = append(candidates, Candidate{URL: searchResult.URL, Title: searchResult.Title, Provider: providerName, Query: query, Rank: searchResult.Rank, Score: searchResult.Score, Metadata: searchResult.Metadata})
				}
			}(providerName, provider, providerCfg, query, perQueryMaxRequests)
		}
	}
	wg.Wait()
	if len(failures) > 0 {
		return candidates, providerQueryError{SeedID: seed.ID, Failures: failures}
	}
	return candidates, nil
}

func normalizeCandidatesWithDecisionEvidence(candidates []Candidate, options CandidateOptions) ([]Candidate, []candidateDecisionEvidence) {
	seenURL := map[string]struct{}{}
	seenHost := map[string]struct{}{}
	normalized := make([]Candidate, 0, len(candidates))
	decisions := []candidateDecisionEvidence{}
	for _, candidate := range candidates {
		normalizedURL, host, ok := NormalizeURL(candidate.URL, options.TrackingParams)
		if !ok {
			candidate.URL = strings.TrimSpace(candidate.URL)
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageNormalization, CandidateRejectionInvalidURL))
			continue
		}
		candidate.URL = normalizedURL
		candidate.Host = host
		if _, exists := seenURL[normalizedURL]; exists {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageNormalization, CandidateRejectionDuplicateURL))
			continue
		}
		if options.DeduplicateHost {
			if _, exists := seenHost[host]; exists {
				decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageNormalization, CandidateRejectionDuplicateHost))
				continue
			}
			seenHost[host] = struct{}{}
		}
		seenURL[normalizedURL] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized, decisions
}

func applyBuiltInCandidateFiltersWithDecisionEvidence(candidates []Candidate, filters CandidateFilters) ([]Candidate, []candidateDecisionEvidence) {
	allowedDomains := domainSet(filters.AllowedDomains)
	deniedDomains := domainSet(filters.DeniedDomains)
	requiredSchemes := schemeSet(filters.RequiredSchemes)
	perHost := map[string]int{}
	perDomain := map[string]int{}
	filtered := make([]Candidate, 0, len(candidates))
	decisions := []candidateDecisionEvidence{}
	for _, candidate := range candidates {
		u, err := url.Parse(candidate.URL)
		if (err != nil) || (u.Scheme == "") || (u.Host == "") {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionInvalidURL))
			continue
		}
		scheme := strings.ToLower(u.Scheme)
		host := strings.ToLower(strings.TrimSpace(candidate.Host))
		if host == "" {
			host = strings.ToLower(u.Hostname())
			candidate.Host = host
		}
		domain := registrableDomain(host)
		if len(requiredSchemes) > 0 {
			if _, ok := requiredSchemes[scheme]; !ok {
				decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionRequiredScheme))
				continue
			}
		}
		if (len(allowedDomains) > 0) && !matchesDomainSet(host, allowedDomains) && !matchesDomainSet(domain, allowedDomains) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionAllowedDomain))
			continue
		}
		if matchesDomainSet(host, deniedDomains) || matchesDomainSet(domain, deniedDomains) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionDeniedDomain))
			continue
		}
		if (filters.MinScore != nil) && (candidate.Score < *filters.MinScore) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionMinimumScore))
			continue
		}
		if (filters.MaxCandidatesPerHost > 0) && (perHost[host] >= filters.MaxCandidatesPerHost) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionMaxCandidatesPerHost))
			continue
		}
		if (filters.MaxCandidatesPerDomain > 0) && (perDomain[domain] >= filters.MaxCandidatesPerDomain) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionMaxCandidatesPerDomain))
			continue
		}
		if (filters.MaxCandidates > 0) && (len(filtered) >= filters.MaxCandidates) {
			decisions = append(decisions, rejectedCandidateDecision(candidate, CandidateRejectionStageBuiltInFilters, CandidateRejectionCandidateLimit))
			continue
		}
		perHost[host]++
		perDomain[domain]++
		filtered = append(filtered, candidate)
	}
	return filtered, decisions
}

func rejectedCandidateDecision(candidate Candidate, stage, reason string) candidateDecisionEvidence {
	if strings.TrimSpace(candidate.Reason) == "" {
		candidate.Reason = reason
	}
	return candidateDecisionEvidence{Candidate: candidate, Status: cdb.InformationSeedCandidateDecisionRejected, Stage: stage, Reason: reason}
}

func acceptedCandidateDecisions(candidates []Candidate) []candidateDecisionEvidence {
	decisions := make([]candidateDecisionEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		decisions = append(decisions, candidateDecisionEvidence{Candidate: candidate, Status: cdb.InformationSeedCandidateDecisionAccepted})
	}
	return decisions
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
	return providerOptionsWithMaxRequests(name, providerCfg, providerCfg.MaxRequests)
}

func providerOptionsWithMaxRequests(name string, providerCfg cfg.InformationSeedProviderConfig, maxRequests int) searchproviders.Options {
	browser := providerCfg.Browser
	return searchproviders.Options{
		Name: name, Provider: providerCfg.Provider, Host: providerCfg.Host, Endpoint: providerCfg.Endpoint,
		APIKeyLabel: providerCfg.APIKeyLabel, APIKey: providerCfg.APIKey, APIToken: providerCfg.APIToken, Token: providerCfg.Token,
		Timeout: time.Duration(providerCfg.Timeout) * time.Second, RateLimit: providerCfg.RateLimit, MaxRequests: maxRequests,
		Parameters: copyProviderMap(providerCfg.Parameters), Headers: copyProviderMap(providerCfg.Headers), PageSize: providerCfg.PageSize,
		MaxPages: providerCfg.MaxPages, Transport: providerCfg.Transport,
		Browser: searchproviders.BrowserOptions{
			VDIAllowList: append([]string(nil), browser.VDIAllowList...), NavigationTimeout: time.Duration(browser.NavigationTimeout) * time.Second,
			PageReadinessTimeout: time.Duration(browser.PageReadinessTimeout) * time.Second, HBSEnabled: browser.HBSEnabled,
			SeleniumFallback: browser.SeleniumFallback, InitialActions: append([]string(nil), browser.InitialActions...),
			ConsentActions: append([]string(nil), browser.ConsentActions...), QueryActions: append([]string(nil), browser.QueryActions...),
			PaginationActions: append([]string(nil), browser.PaginationActions...), ScrapingRules: append([]string(nil), browser.ScrapingRules...),
			AllowedNavigationHosts: append([]string(nil), browser.AllowedNavigationHosts...), MaxPages: browser.MaxPages,
			MaxRequests: min(browser.MaxRequests, maxRequests), MaxCandidates: browser.MaxCandidates,
		},
	}
}

func perQueryRequestLimit(providerCfg cfg.InformationSeedProviderConfig, queryCount int) int {
	if queryCount < 1 || providerCfg.MaxRequests < 1 {
		return providerCfg.MaxRequests
	}
	limit := providerCfg.MaxRequests / queryCount
	if limit < 1 {
		limit = 1
	}
	return limit
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
	message = redactInformationSeedURL(message)
	for _, marker := range []string{"api_key=", "apikey=", "key=", "token=", "secret=", "password=", "client_secret=", "access_token=", "refresh_token=", "authorization:", "x-subscription-token:", "ocp-apim-subscription-key:"} {
		searchFrom := 0
		for searchFrom < len(message) {
			lower := strings.ToLower(message[searchFrom:])
			relativeIdx := strings.Index(lower, marker)
			if relativeIdx < 0 {
				break
			}
			idx := searchFrom + relativeIdx
			valueStart := idx + len(marker)
			for valueStart < len(message) && strings.ContainsRune(" 	", rune(message[valueStart])) {
				valueStart++
			}
			end := valueStart
			terminators := " &;\n\t\r"
			if strings.HasSuffix(marker, ":") {
				terminators = ";\n\t\r"
			}
			for end < len(message) && !strings.ContainsRune(terminators, rune(message[end])) {
				end++
			}
			message = message[:idx+len(marker)] + informationSeedRedactedValue + message[end:]
			searchFrom = idx + len(marker) + len(informationSeedRedactedValue)
		}
	}
	return message
}

func (r *Runner) applyProcessors(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, enabledNames []string) ([]Candidate, error) {
	processed, _, err := r.applyProcessorsWithDecisionEvidence(ctx, seed, runCfg, candidates, enabledNames)
	return processed, err
}

func (r *Runner) applyProcessorsWithRejections(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, enabledNames []string) ([]Candidate, map[string]map[string]int, error) {
	processed, decisions, err := r.applyProcessorsWithDecisionEvidence(ctx, seed, runCfg, candidates, enabledNames)
	rejected := map[string]map[string]int{}
	for _, decision := range decisions {
		if decision.Status != cdb.InformationSeedCandidateDecisionRejected {
			continue
		}
		if rejected[decision.Stage] == nil {
			rejected[decision.Stage] = map[string]int{}
		}
		rejected[decision.Stage][decision.Reason]++
	}
	return processed, rejected, err
}

func (r *Runner) applyProcessorsWithDecisionEvidence(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, enabledNames []string) ([]Candidate, []candidateDecisionEvidence, error) {
	return r.applyProcessorsWithDecisionEvidenceAndStats(ctx, seed, runCfg, candidates, enabledNames, nil)
}

func (r *Runner) applyProcessorsWithDecisionEvidenceAndStats(ctx context.Context, seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate, enabledNames []string, stats *seedDiscoveryStats) ([]Candidate, []candidateDecisionEvidence, error) {
	processors := selectProcessors(r.Processors, enabledNames)
	if len(processors) == 0 {
		return candidates, nil, nil
	}
	processed := make([]Candidate, 0, len(candidates))
	decisions := []candidateDecisionEvidence{}
	var errs []string
	for _, candidate := range candidates {
		keep := true
		current := candidate
		for _, processor := range processors {
			var err error
			input := candidatePluginInput(seed, runCfg, current)
			current, keep, err = processor.ProcessCandidate(ctx, input)
			if err != nil {
				stage, reason := classifyCandidateProcessorError(err)
				decisions = append(decisions, rejectedCandidateDecision(current, stage, reason))
				redactedErr := redactInformationSeedError(err.Error())
				errs = append(errs, redactedErr)
				if stats != nil {
					stats.addPluginFailure(candidateProcessorMetricName(processor), redactedErr)
				}
				keep = false
				break
			}
			if !keep {
				decisions = append(decisions, rejectedCandidateDecision(current, CandidateRejectionStageUserPlugins, CandidateRejectionCandidateProcessor))
				break
			}
		}
		if keep {
			processed = append(processed, current)
		}
	}
	if len(errs) > 0 {
		return processed, decisions, fmt.Errorf("candidate processor errors: %s", strings.Join(errs, "; "))
	}
	return processed, decisions, nil
}

func candidateProcessorMetricName(processor CandidateProcessor) string {
	if named, ok := processor.(NamedCandidateProcessor); ok {
		if name := strings.TrimSpace(named.ProcessorName()); name != "" {
			return name
		}
	}
	return "unknown"
}

func classifyCandidateProcessorError(err error) (string, string) {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unsafe source_overrides"):
		return CandidateRejectionStageSourceOverrideValidation, CandidateRejectionSourceOverrideInvalid
	case strings.Contains(message, "malformed output"):
		return CandidateRejectionStageUserPlugins, CandidateRejectionPluginOutputInvalid
	default:
		return CandidateRejectionStageUserPlugins, CandidateRejectionCandidateProcessor
	}
}

// selectProcessors treats candidate_plugins as both an allow-list and the
// canonical ordered execution list. Omitted candidate_plugins runs registered
// processors in registration order. Present candidate_plugins runs only named
// registered processors, in the seed-provided order, ignoring duplicate or
// unknown names.
func selectProcessors(processors []CandidateProcessor, enabledNames []string) []CandidateProcessor {
	if len(enabledNames) == 0 {
		return processors
	}
	byName := map[string]CandidateProcessor{}
	for _, processor := range processors {
		named, ok := processor.(NamedCandidateProcessor)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(named.ProcessorName()))
		if name != "" {
			byName[name] = processor
		}
	}
	selected := make([]CandidateProcessor, 0, len(enabledNames))
	seen := map[string]struct{}{}
	for _, name := range enabledNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		processor, ok := byName[name]
		if !ok {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, processor)
	}
	return selected
}

func (r *Runner) persistCandidateDecisionEvidence(seed cdb.InformationSeed, decisions []candidateDecisionEvidence) error {
	if len(decisions) == 0 {
		return nil
	}
	rows := make([]cdb.InformationSeedCandidate, 0, len(decisions))
	for _, decision := range decisions {
		candidate := decision.Candidate
		metadata := candidateMetadata(candidate)
		rows = append(rows, cdb.InformationSeedCandidate{
			InformationSeedID: seed.ID,
			NormalizedURL:     auditCandidateURL(candidate),
			Host:              candidate.Host,
			Provider:          candidate.Provider,
			Query:             candidate.Query,
			Rank:              candidate.Rank,
			Score:             candidate.Score,
			DecisionStatus:    decision.Status,
			RejectionReason:   auditRejectionReason(decision.Stage, decision.Reason),
			Metadata:          metadata,
			RunAttempt:        seed.Attempts,
		})
	}
	return cdb.UpsertInformationSeedCandidateDecisions(r.DB, rows)
}

func auditCandidateURL(candidate Candidate) string {
	if strings.TrimSpace(candidate.URL) != "" {
		return candidate.URL
	}
	return "unknown"
}

func auditRejectionReason(stage, reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	if strings.TrimSpace(stage) == "" {
		return reason
	}
	return stage + ":" + reason
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
		upsertResult, err := cdb.UpsertSourceWithPolicy(r.DB, &cdb.Source{URL: candidate.URL, Name: name, Priority: priority, CategoryID: seed.CategoryID, UsrID: seed.UsrID, Restricted: restricted, Flags: flags}, sourceConfig, cdb.SourceUpsertPolicy{
			CreateSources:              runCfg.CreateSources,
			LinkExistingSources:        runCfg.LinkExistingSources,
			UpdateExistingSourceConfig: runCfg.UpdateExistingSourceConfig,
			Disabled:                   runCfg.Disabled,
			Status:                     runCfg.Status,
		})
		if err != nil {
			return linked, fmt.Errorf("creating source for seed %d candidate %s: %w", seed.ID, candidate.URL, err)
		}
		if upsertResult.SourceID == 0 {
			continue
		}
		if upsertResult.Created {
			if stats != nil {
				stats.SourcesCreated++
				stats.SourceIDsCreated = append(stats.SourceIDsCreated, upsertResult.SourceID)
			}
			_ = r.emitInformationSeedEvent(ctx, seed, upsertResult.SourceID, informationSeedSourceCreated, cdb.EventSeverityInfo, stats)
		}
		if upsertResult.Existing && !runCfg.LinkExistingSources {
			continue
		}
		metadata := discoveryMetadata(candidate)
		if err := cdb.LinkSourceToInformationSeedWithDiscoveryMetadata(r.DB, upsertResult.SourceID, seed.ID, metadata); err != nil {
			return linked, err
		}
		linked++
		if stats != nil {
			stats.SourcesLinked = linked
			stats.SourceIDsLinked = append(stats.SourceIDsLinked, upsertResult.SourceID)
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

func candidateMetadata(candidate Candidate) *json.RawMessage {
	if len(candidate.Metadata) == 0 {
		return nil
	}
	data, err := json.Marshal(candidate.Metadata)
	if err != nil {
		return nil
	}
	msg := json.RawMessage(data)
	return &msg
}

func discoveryMetadata(candidate Candidate) cdb.InformationSeedDiscoveryMetadata {
	provider, query, reason := candidate.Provider, candidate.Query, candidate.Reason
	rank, score := candidate.Rank, candidate.Score
	raw := candidateMetadata(candidate)
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

type candidatePluginDecision struct {
	candidatePluginOutput
	SourceOverrideValues SourceOverrides
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

	decision, err := validateCandidatePluginDecision(encoded)
	if err != nil {
		return candidate, false, fmt.Errorf("candidate plugin %q malformed output: %w", p.Plugin.Name, err)
	}
	output := decision.candidatePluginOutput
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
		candidate.SourceOverrides = decision.SourceOverrideValues
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
	decision, err := validateCandidatePluginDecision(data)
	return decision.candidatePluginOutput, err
}

func validateCandidatePluginDecision(data []byte) (candidatePluginDecision, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var output candidatePluginOutput
	if err := dec.Decode(&output); err != nil {
		return candidatePluginDecision{}, err
	}
	if output.Accepted == nil {
		return candidatePluginDecision{}, fmt.Errorf("accepted is required")
	}
	if output.Score == nil || math.IsNaN(*output.Score) || math.IsInf(*output.Score, 0) {
		return candidatePluginDecision{}, fmt.Errorf("score must be a finite number")
	}
	if output.Reason == nil || strings.TrimSpace(*output.Reason) == "" {
		return candidatePluginDecision{}, fmt.Errorf("reason is required")
	}
	for _, tag := range output.Tags {
		if strings.TrimSpace(tag) == "" {
			return candidatePluginDecision{}, fmt.Errorf("tags must be non-empty strings")
		}
	}
	decision := candidatePluginDecision{candidatePluginOutput: output}
	if len(output.SourceOverrides) > 0 {
		overrides, err := validateSourceOverrides(output.SourceOverrides)
		if err != nil {
			return candidatePluginDecision{}, fmt.Errorf("unsafe source_overrides: %w", err)
		}
		decision.SourceOverrideValues = overrides
	}
	return decision, nil
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

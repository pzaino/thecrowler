// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	ProcessCandidate(ctx context.Context, candidate Candidate) (Candidate, bool, error)
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
	SeedID     uint64
	Candidates int
	Linked     int
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
	providers := make(map[string]searchproviders.Provider, len(config.Providers))
	for name, providerCfg := range config.Providers {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		providers[key] = &searchproviders.JSONProvider{ProviderName: key}
		_ = providerCfg // reserved for provider-specific implementations.
	}
	return providers
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

	runCfg, err := parseSeedRunConfig(seed)
	if err != nil {
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
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}
	candidates, err := r.queryProviders(ctx, seed, runCfg, queries)
	if err != nil && len(candidates) == 0 {
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}

	trackingParams := append(defaultTrackingParams(), runCfg.TrackingParams...)
	candidates = NormalizeCandidates(candidates, CandidateOptions{TrackingParams: trackingParams, DeduplicateHost: runCfg.DeduplicateHost})
	limit := r.Config.MaxCandidatesPerSeed
	if runCfg.MaxCandidates > 0 && runCfg.MaxCandidates < limit {
		limit = runCfg.MaxCandidates
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result.Candidates = len(candidates)

	candidates, err = r.applyProcessors(ctx, candidates, runCfg.CandidatePlugins)
	if err != nil && len(candidates) == 0 {
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", err.Error())
		return result, err
	}

	linked, persistErr := r.persistCandidates(seed, runCfg, candidates)
	result.Linked = linked
	if persistErr != nil {
		_ = cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "error", persistErr.Error())
		return result, persistErr
	}
	if err := cdb.UpdateInformationSeedStatus(r.DB, seed.ID, "completed", ""); err != nil {
		return result, err
	}
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
		for _, query := range queries {
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
					errs = append(errs, fmt.Sprintf("%s: %v", providerName, err))
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
	names := make([]string, 0, len(configured))
	for _, name := range configured {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || r.Providers[name] == nil {
			continue
		}
		names = append(names, name)
	}
	return names
}

func providerOptions(name string, providerCfg cfg.InformationSeedProviderConfig) searchproviders.Options {
	return searchproviders.Options{Name: name, Provider: providerCfg.Provider, Host: providerCfg.Host, Endpoint: providerCfg.Endpoint, APIKeyLabel: providerCfg.APIKeyLabel, APIKey: providerCfg.APIKey, APIToken: providerCfg.APIToken, Token: providerCfg.Token, Timeout: time.Duration(providerCfg.Timeout) * time.Second, MaxRequests: providerCfg.MaxRequests}
}

func (r *Runner) applyProcessors(ctx context.Context, candidates []Candidate, enabledNames []string) ([]Candidate, error) {
	processors := selectProcessors(r.Processors, enabledNames)
	processed := make([]Candidate, 0, len(candidates))
	var errs []string
	for _, candidate := range candidates {
		keep := true
		current := candidate
		for _, processor := range processors {
			var err error
			current, keep, err = processor.ProcessCandidate(ctx, current)
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

func (r *Runner) persistCandidates(seed cdb.InformationSeed, runCfg SeedRunConfig, candidates []Candidate) (int, error) {
	sourceConfig, err := sourceConfigFromRaw(runCfg.SourceConfig)
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
		sourceID, err := cdb.CreateSource(r.DB, &cdb.Source{URL: candidate.URL, Name: name, Priority: runCfg.SourcePriority, CategoryID: seed.CategoryID, UsrID: seed.UsrID, Restricted: runCfg.Restricted, Flags: runCfg.Flags}, sourceConfig)
		if err != nil {
			return linked, fmt.Errorf("creating source for seed %d candidate %s: %w", seed.ID, candidate.URL, err)
		}
		metadata := discoveryMetadata(candidate)
		if err := cdb.LinkSourceToInformationSeedWithDiscoveryMetadata(r.DB, sourceID, seed.ID, metadata); err != nil {
			return linked, err
		}
		linked++
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

// JSPluginProcessor adapts an engine plugin into a candidate processor. The
// plugin receives the candidate as params.candidate and can return {accept:false}
// to reject it or {candidate:{...}} to update candidate fields.
type JSPluginProcessor struct {
	Plugin  plg.JSPlugin
	DB      *cdb.Handler
	Timeout int
}

// ProcessorName returns the wrapped plugin name.
func (p JSPluginProcessor) ProcessorName() string {
	return p.Plugin.Name
}

// ProcessCandidate executes a JavaScript plugin for one candidate.
func (p JSPluginProcessor) ProcessCandidate(_ context.Context, candidate Candidate) (Candidate, bool, error) {
	params := map[string]interface{}{"candidate": candidate}
	result, err := p.Plugin.Execute(nil, p.DB, p.Timeout, params)
	if err != nil {
		return candidate, false, err
	}
	if accept, ok := result["accept"].(bool); ok && !accept {
		return candidate, false, nil
	}
	if reason, ok := result["reason"].(string); ok {
		candidate.Reason = reason
	}
	if candidateMap, ok := result["candidate"].(map[string]interface{}); ok {
		applyCandidateMap(&candidate, candidateMap)
	}
	return candidate, true, nil
}

func applyCandidateMap(candidate *Candidate, updates map[string]interface{}) {
	if value, ok := updates["url"].(string); ok {
		candidate.URL = value
	}
	if value, ok := updates["title"].(string); ok {
		candidate.Title = value
	}
	if value, ok := updates["reason"].(string); ok {
		candidate.Reason = value
	}
}

// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ruleset implements the ruleset library for the Crowler and
// the scrapper.
package ruleset

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

///// ------------------------ RULESENGINE ---------------------------------- /////

/// --- Creation --- ///

// NewRuleEngine creates a new instance of RuleEngine with the provided site rules.
// It initializes the RuleEngine with the given sites and returns a pointer to the created RuleEngine.
func NewRuleEngine(schemaPath string, rulesets []Ruleset) *RuleEngine {
	// Create a new instance of RuleEngine
	ruleEngine := NewEmptyRuleEngine(schemaPath)
	if ruleEngine.Schema == nil {
		return &RuleEngine{
			Schema:   nil,
			Rulesets: rulesets,
			DetectionConfig: DetectionConfig{
				NoiseThreshold:    1.0,
				MaybeThreshold:    5.0,
				DetectedThreshold: 10.0,
			},
			Cache: Cache{
				Mu:               sync.RWMutex{},
				IsInvalid:        false,
				RuleGroups:       nil,
				ActiveRuleGroups: nil,
				Scraping:         nil,
				Action:           nil,
				Detection:        nil,
				Crawling:         nil,
			},
		}
	}

	// Parse the ruleset
	if ruleEngine.Schema != nil {
		for _, rs := range rulesets {
			err := ruleEngine.ValidateRuleset(rs)
			if err != nil {
				// Log the error, handle it, or return nil to indicate failure
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to validate ruleset: %v", err)
				return nil // or handle it as per your application's error handling strategy
			}
		}
	}

	// Set the rulesets
	ruleEngine.Rulesets = rulesets

	// Return the initialized RuleEngine
	return &ruleEngine
}

// NewEmptyRuleEngine creates a new instance of RuleEngine with an empty slice of site rules.
func NewEmptyRuleEngine(schemaPath string) RuleEngine {
	schema, err := LoadSchema(schemaPath)
	if err != nil {
		return RuleEngine{
			Schema: nil,
			Rulesets: []Ruleset{
				{},
			},
			DetectionConfig: DetectionConfig{
				NoiseThreshold:    1.0,
				MaybeThreshold:    5.0,
				DetectedThreshold: 10.0,
			},
			Cache: Cache{
				Mu:               sync.RWMutex{},
				IsInvalid:        false,
				RuleGroups:       nil,
				ActiveRuleGroups: nil,
				Scraping:         nil,
				Action:           nil,
				Detection:        nil,
				Crawling:         nil,
			},
		}
	}

	return RuleEngine{
		Schema: schema,
		Rulesets: []Ruleset{
			{},
		},
		DetectionConfig: DetectionConfig{
			NoiseThreshold:    1.0,
			MaybeThreshold:    5.0,
			DetectedThreshold: 10.0,
		},
		Cache: Cache{
			Mu:               sync.RWMutex{},
			IsInvalid:        false,
			RuleGroups:       nil,
			ActiveRuleGroups: nil,
			Scraping:         nil,
			Action:           nil,
			Detection:        nil,
			Crawling:         nil,
		},
	}
}

// NewRuleEngineWithParser creates a new instance of RuleEngine with the provided site rules.
func NewRuleEngineWithParser(parser RuleParser, file string) (*RuleEngine, error) {
	rulesets, err := parser.ParseRules(nil, file)
	if err != nil {
		return nil, err
	}
	return &RuleEngine{
		Schema:   nil,
		Rulesets: rulesets,
		DetectionConfig: DetectionConfig{
			NoiseThreshold:    1.0,
			MaybeThreshold:    5.0,
			DetectedThreshold: 10.0,
		},
	}, nil
}

// LoadRulesFromFile loads the rules from the provided file names list and returns
// a pointer to the created RuleEngine.
func (re *RuleEngine) LoadRulesFromFile(files []string) error {
	for _, file := range files {
		ruleset, err := BulkLoadRules(re.Schema, file)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "Failed to load rules from file: %v", err)
			return err
		}
		re.Rulesets = append(re.Rulesets, ruleset...)
	}

	return nil
}

// LoadRulesFromConfig loads the rules from the configuration file and returns
// a pointer to the created RuleEngine.
func (re *RuleEngine) LoadRulesFromConfig(config *cfg.Config) error {
	// Load Plugins from the configuration
	for _, plugin := range config.Plugins.Plugins {
		err := plg.LoadPluginsFromConfig(&re.JSPlugins, plugin, "")
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to load plugins from configuration: %v", err)
		}
	}

	// Load the rules from the configuration
	for _, rs := range config.Rulesets {
		rulesets, err := loadRulesFromConfig(re.Schema, rs)
		if err != nil {
			return err
		}
		re.Rulesets = append(re.Rulesets, *rulesets...)
	}

	return nil
}

/// --- Validation --- ///

// ValidateRuleset validates the provided ruleset against the RuleEngine's schema.
func (re *RuleEngine) ValidateRuleset(ruleset Ruleset) error {
	if re.Schema == nil {
		return fmt.Errorf("this RuleEngine has no validation schema")
	}

	// Transform the ruleset to JSON
	rulesetJSON, err := json.Marshal(ruleset)
	if err != nil {
		return fmt.Errorf("failed to marshal ruleset to JSON: %v", err)
	}

	// Create a context for the validation
	ctx := context.Background()

	// Validate the ruleset
	if keywordsErrors, err := re.Schema.ValidateBytes(ctx, []byte(rulesetJSON)); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to validate ruleset: %v", err)
		if len(keywordsErrors) > 0 {
			return fmt.Errorf("ruleset validation failed: %v", keywordsErrors)
		}
		return fmt.Errorf("ruleset validation failed: %v", err)
	}

	return nil
}

// MarshalJSON returns the JSON representation of the RuleEngine.
func (re *RuleEngine) MarshalJSON() ([]byte, error) {
	// transform the RuleEngine to JSON
	jsonDocument := map[string]interface{}{
		"schema":   re.Schema,
		"rulesets": re.Rulesets,
	}
	jsonData, err := json.MarshalIndent(jsonDocument, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RuleEngine to JSON: %v", err)
	}

	return jsonData, err
}

/// --- Ruleset Manipulation --- ///

// AddRuleset adds a new ruleset to the RuleEngine.
func (re *RuleEngine) AddRuleset(ruleset Ruleset) {
	re.Cache.Mu.Lock()
	defer re.Cache.Mu.Unlock()
	re.Rulesets = append(re.Rulesets, ruleset)
	re.Cache.IsInvalid = true
	re.cleanCache()
}

// RemoveRuleset removes a ruleset from the RuleEngine.
func (re *RuleEngine) RemoveRuleset(ruleset Ruleset) {
	re.Cache.Mu.Lock()
	defer re.Cache.Mu.Unlock()
	for i, r := range re.Rulesets {
		if r.Name == ruleset.Name {
			re.Rulesets = append(re.Rulesets[:i], re.Rulesets[i+1:]...)
			re.Cache.IsInvalid = true
			re.cleanCache()
			return
		}
	}
}

// UpdateRuleset updates a ruleset in the RuleEngine.
func (re *RuleEngine) UpdateRuleset(ruleset Ruleset) {
	re.Cache.Mu.Lock()
	defer re.Cache.Mu.Unlock()
	for i, r := range re.Rulesets {
		if r.Name == ruleset.Name {
			re.Rulesets[i] = ruleset
			re.Cache.IsInvalid = true
			re.cleanCache()
			return
		}
	}
}

// InvalidateCache invalidates the cached rule groups.
func (re *RuleEngine) cleanCache() {
	if re == nil {
		return
	}
	if re.Rulesets == nil {
		return
	}
	if re.Cache.IsInvalid {
		// Check if re.Cache.Mu is locked
		re.Cache.RuleGroups = nil
		re.Cache.ActiveRuleGroups = nil
		re.Cache.Scraping = nil
		re.Cache.Action = nil
		re.Cache.Detection = nil
		re.Cache.Crawling = nil
		re.Cache.IsInvalid = true
	}
}

/// --- Retrieving --- ///

// GetAllRulesets returns all rulesets in the RuleEngine.
func (re *RuleEngine) GetAllRulesets() []Ruleset {
	return re.Rulesets
}

// GetAllRuleGroups returns all the rule groups in the RuleEngine.
func (re *RuleEngine) GetAllRuleGroups() []*RuleGroup {
	if re == nil {
		return nil
	}
	if len(re.Rulesets) == 0 {
		return nil
	}

	// Check if the cache is valid
	re.Cache.Mu.RLock()
	if !re.Cache.IsInvalid && len(re.Cache.RuleGroups) != 0 {
		cachedGroups := re.Cache.RuleGroups
		re.Cache.Mu.RUnlock()
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Returning %d Rulesgroup from the cache.", len(cachedGroups))
		return cachedGroups
	}
	re.Cache.Mu.RUnlock()

	// Get all the rule groups
	var ruleGroups []*RuleGroup
	for i := 0; i < len(re.Rulesets); i++ {
		for i2 := 0; i2 < len(re.Rulesets[i].RuleGroups); i2++ {
			ruleGroups = append(ruleGroups, &(re.Rulesets[i].RuleGroups)[i2])
		}
	}

	// Update the cache
	re.Cache.Mu.Lock()
	re.Cache.RuleGroups = ruleGroups
	re.Cache.IsInvalid = false
	re.Cache.Mu.Unlock()
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Added %d Rulesgroup to the cache.", len(re.Cache.RuleGroups))

	return ruleGroups
}

// GetAllEnabledRuleGroups returns all the rule groups in the RuleEngine.
func (re *RuleEngine) GetAllEnabledRuleGroups() []*RuleGroup {
	if re == nil {
		return nil
	}

	// Check if the cache is valid for enabled rule groups
	re.Cache.Mu.RLock()
	if !re.Cache.IsInvalid && re.Cache.ActiveRuleGroups != nil {
		cachedEnabledGroups := re.Cache.ActiveRuleGroups
		re.Cache.Mu.RUnlock()
		return cachedEnabledGroups
	}
	re.Cache.Mu.RUnlock()

	var ruleGroups []*RuleGroup
	rgs := re.GetAllRuleGroups()
	for i := 0; i < len(rgs); i++ {
		if (rgs[i].IsEnabled) && (re.IsGroupValid(*rgs[i])) {
			ruleGroups = append(ruleGroups, rgs[i])
		}
	}

	// Update the cache
	re.Cache.Mu.Lock()
	re.Cache.ActiveRuleGroups = ruleGroups
	re.Cache.IsInvalid = false
	re.Cache.Mu.Unlock()

	return ruleGroups
}

// GetAllScrapingRules returns all the scraping rules in the RuleEngine.
func (re *RuleEngine) GetAllScrapingRules() []ScrapingRule {
	var scrapingRules []ScrapingRule
	for _, rg := range re.GetAllRuleGroups() {
		for i3 := 0; i3 < len(rg.ScrapingRules); i3++ {
			scrapingRules = append(scrapingRules, rg.ScrapingRules[i3])
		}
	}

	return scrapingRules
}

// GetAllActionRules returns all the action rules in the RuleEngine.
func (re *RuleEngine) GetAllActionRules() []ActionRule {
	var actionRules []ActionRule
	for _, rg := range re.GetAllRuleGroups() {
		actionRules = append(actionRules, rg.ActionRules...)
	}
	return actionRules
}

// GetAllCrawlingRules returns all the crawling rules in the RuleEngine.
func (re *RuleEngine) GetAllCrawlingRules() []CrawlingRule {
	var crawlingRules []CrawlingRule
	for _, rg := range re.GetAllRuleGroups() {
		crawlingRules = append(crawlingRules, rg.CrawlingRules...)
	}
	return crawlingRules
}

// GetAllDetectionRules returns all the detection rules in the RuleEngine.
func (re *RuleEngine) GetAllDetectionRules(CtxID string) []DetectionRule {
	var detectionRules []DetectionRule
	CID := strings.ToLower(strings.TrimSpace(CtxID))
	for _, rg := range re.GetAllRuleGroups() {
		if CID != "" {
			rg.SetEnv(CID)
		}
		detectionRules = append(detectionRules, rg.DetectionRules...)
	}
	return detectionRules
}

// GetAllEnabledScrapingRules returns all the enabled scraping rules in the RuleEngine.
func (re *RuleEngine) GetAllEnabledScrapingRules(CtxID string) []ScrapingRule {
	var scrapingRules []ScrapingRule
	CID := strings.ToLower(strings.TrimSpace(CtxID))
	for _, rg := range re.GetAllEnabledRuleGroups() {
		if CID != "" {
			rg.SetEnv(CID)
		}
		scrapingRules = append(scrapingRules, rg.ScrapingRules...)
	}
	return scrapingRules
}

// GetAllEnabledActionRules returns all the enabled action rules in the RuleEngine.
func (re *RuleEngine) GetAllEnabledActionRules(CtxID string) []ActionRule {
	var actionRules []ActionRule
	CID := strings.ToLower(strings.TrimSpace(CtxID))
	for _, rg := range re.GetAllEnabledRuleGroups() {
		if CID != "" {
			rg.SetEnv(CID)
		}
		actionRules = append(actionRules, rg.ActionRules...)
	}
	return actionRules
}

// GetAllEnabledCrawlingRules returns all the enabled crawling rules in the RuleEngine.
func (re *RuleEngine) GetAllEnabledCrawlingRules(CtxID string) []CrawlingRule {
	var crawlingRules []CrawlingRule
	CID := strings.ToLower(strings.TrimSpace(CtxID))
	for _, rg := range re.GetAllEnabledRuleGroups() {
		if CID != "" {
			rg.SetEnv(CID)
		}
		crawlingRules = append(crawlingRules, rg.CrawlingRules...)
	}
	return crawlingRules
}

// GetAllEnabledDetectionRules returns all the enabled detection rules in the RuleEngine.
func (re *RuleEngine) GetAllEnabledDetectionRules(CtxID string) []DetectionRule {
	var detectionRules []DetectionRule
	CID := strings.ToLower(strings.TrimSpace(CtxID))
	for _, rg := range re.GetAllEnabledRuleGroups() {
		if CID != "" {
			rg.SetEnv(CID)
		}
		detectionRules = append(detectionRules, rg.DetectionRules...)
	}
	return detectionRules
}

// GetAllScrapingRulesByURL returns all the scraping rules for the specified URL.
func (re *RuleEngine) GetAllScrapingRulesByURL(url, CtxID string) []ScrapingRule {
	// find all scraping rules with the specified URL
	rules := re.GetAllEnabledScrapingRules(CtxID)
	var scrapingRules []ScrapingRule

	// Prepare the URL for search
	var err error
	url, err = PrepareURLForSearch(url)
	if err != nil {
		return scrapingRules
	}

	for _, rule := range rules {
		if CheckURL(url, rule.RuleName) {
			scrapingRules = append(scrapingRules, rule)
		} else {
			for i2 := 0; i2 < len(rule.PreConditions); i2++ {
				if CheckURL(url, rule.PreConditions[i2].URL) {
					scrapingRules = append(scrapingRules, rule)
				}
			}
		}
	}
	return scrapingRules
}

/// --- Counting --- ///

// CountRulesets returns the number of rulesets in the RuleEngine.
func (re *RuleEngine) CountRulesets() int {
	return len(re.Rulesets)
}

// CountRuleGroups returns the number of RuleGroups in the RuleEngine.
func (re *RuleEngine) CountRuleGroups() int {
	var count int
	for _, rs := range re.Rulesets {
		count += len(rs.RuleGroups)
	}
	return count
}

// CountScrapingRules returns the number of ScrapingRules in the RuleEngine.
func (re *RuleEngine) CountScrapingRules() int {
	var count int
	for _, rg := range re.GetAllRuleGroups() {
		count += len(rg.ScrapingRules)
	}
	return count
}

// CountActionRules returns the number of ActionRules in the RuleEngine.
func (re *RuleEngine) CountActionRules() int {
	var count int
	for _, rg := range re.GetAllRuleGroups() {
		count += len(rg.ActionRules)
	}
	return count
}

// CountCrawlingRules returns the number of CrawlingRules in the RuleEngine.
func (re *RuleEngine) CountCrawlingRules() int {
	var count int
	for _, rg := range re.GetAllRuleGroups() {
		count += len(rg.CrawlingRules)
	}
	return count
}

// CountDetectionRules returns the number of DetectionRules in the RuleEngine.
func (re *RuleEngine) CountDetectionRules() int {
	var count int
	for _, rg := range re.GetAllRuleGroups() {
		count += len(rg.DetectionRules)
	}
	return count
}

// CountRules returns the number of rules in the RuleEngine.
func (re *RuleEngine) CountRules() int {
	return re.CountScrapingRules() + re.CountActionRules() + re.CountCrawlingRules() + re.CountDetectionRules()
}

// CountPlugins returns the number of loaded Plugins
func (re *RuleEngine) CountPlugins() int {
	return len(re.JSPlugins.Registry)
}

/// --- Searching --- ///

// GetRulesetByURL returns the ruleset for the specified URL.
func (re *RuleEngine) GetRulesetByURL(urlStr string) (*Ruleset, error) {
	if re == nil {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	if len((*re).Rulesets) == 0 {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return nil, fmt.Errorf("%s", errInvalidURL)
	}

	for i := 0; i < len((*re).Rulesets); i++ {
		rsName := strings.TrimSpace(re.Rulesets[i].Name)
		if rsName == "" || !IsURL(rsName) {
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Checking ruleset: '%s' == '%s'", rsName, parsedURL)
		if CheckURL(parsedURL, rsName) {
			return &re.Rulesets[i], nil
		}
	}

	// No ruleset found
	return nil, fmt.Errorf("%s", errRulesetNotFound)
}

// GetAllRulesetByURL returns all the rulesets who match the provided URL in the name.
func (re *RuleEngine) GetAllRulesetByURL(urlStr string) ([]*Ruleset, error) {
	if re == nil {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	if len((*re).Rulesets) == 0 {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return nil, fmt.Errorf("%s", errInvalidURL)
	}

	var ruleSets []*Ruleset
	for i := 0; i < len((*re).Rulesets); i++ {
		rsName := strings.TrimSpace(re.Rulesets[i].Name)
		if rsName == "" || !IsURL(rsName) {
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Checking ruleset: '%s' == '%s'", rsName, parsedURL)
		if CheckURL(parsedURL, rsName) {
			ruleSets = append(ruleSets, &re.Rulesets[i])
		}
	}

	return ruleSets, nil
}

// GetRulesetByName returns the ruleset for the specified name.
func (re *RuleEngine) GetRulesetByName(name string) (*Ruleset, error) {
	if re == nil {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	if len((*re).Rulesets) == 0 {
		return nil, fmt.Errorf("%s", errRulesetNotFound)
	}

	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%s", errEmptyName)
	}

	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rs := range re.Rulesets {
		if strings.ToLower(strings.TrimSpace(rs.Name)) == parsedName {
			return &rs, nil
		}
	}
	return nil, fmt.Errorf("%s", errRulesetNotFound)
}

// GetRulesGroupByURL returns the rules group for the specified URL.
func (re *RuleEngine) GetRulesGroupByURL(urlStr string) (*RuleGroup, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return nil, fmt.Errorf("%s", errInvalidURL)
	}

	for _, rg := range re.GetAllRuleGroups() {
		rgName := strings.TrimSpace(rg.GroupName)
		if rgName == "" || !IsURL(rgName) {
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Checking rules group: '%s' == '%s'", rgName, parsedURL)
		if rgName == "*" || CheckURL(parsedURL, rgName) || CheckURL(parsedURL, rg.URL) {
			if rg.IsValid() {
				return rg, nil
			}
		}
	}
	return nil, fmt.Errorf("%s", errRuleGroupNotFound)
}

// GetAllRulesGroupByURL returns all the rules groups which match the provided URL either in the name or the url filed.
func (re *RuleEngine) GetAllRulesGroupByURL(urlStr string) ([]*RuleGroup, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return nil, fmt.Errorf("%s", errInvalidURL)
	}

	var ruleGroups []*RuleGroup
	for _, rg := range re.GetAllRuleGroups() {
		rgName := strings.TrimSpace(rg.GroupName)
		if rgName == "" || !IsURL(rgName) {
			rgName = strings.TrimSpace(rg.URL)
		}
		if rgName == "" || !IsURL(rgName) {
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Checking rules group: '%s' == '%s'", rgName, parsedURL)
		if rgName == "*" || CheckURL(parsedURL, rgName) || CheckURL(parsedURL, rg.URL) {
			if rg.IsValid() {
				ruleGroups = append(ruleGroups, rg)
			}
		}
	}

	return ruleGroups, nil
}

// GetRuleGroupByName returns the rules group for the specified name.
func (re *RuleEngine) GetRuleGroupByName(name string) (*RuleGroup, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedName {
			if rg.IsValid() {
				return rg, nil
			}
			return nil, fmt.Errorf("RuleGroup '%s' is not valid", rg.GroupName)
		}
	}
	return nil, fmt.Errorf("%s", errRuleGroupNotFound)
}

// GetActionRuleByName returns the action rule with the specified name.
func (re *RuleEngine) GetActionRuleByName(name string) (*ActionRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		for _, r := range rg.ActionRules {
			//cmn.DebugMsg(cmn.DbgLvlDebug2, "Checking rule: '%s' == '%s'", r.RuleName, parsedName)
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf("%s", errActionNotFound)
}

// GetActionRuleByURL returns the action rule for the specified URL.
func (re *RuleEngine) GetActionRuleByURL(urlStr string) (*ActionRule, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return nil, err
	}

	for _, rg := range re.GetAllRuleGroups() {
		for _, r := range rg.ActionRules {
			if IsURL(r.URL) {
				reg := regexp.MustCompile(r.URL)
				if reg.MatchString(parsedURL) {
					return &r, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("%s", errActionNotFound)
}

// GetScrapingRuleByName returns the scraping rule with the specified name.
func (re *RuleEngine) GetScrapingRuleByName(name string) (*ScrapingRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		for _, r := range rg.ScrapingRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf("%s", errScrapingNotFound)
}

// GetScrapingRuleByPath returns the scraping rule for the specified path.
func (re *RuleEngine) GetScrapingRuleByPath(path string) (*ScrapingRule, error) {
	// Validate path
	parsedPath, err := PreparePathForSearch(path)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		if rule, err := findScrapingRuleByPath(parsedPath, rg.ScrapingRules); err == nil {
			return rule, nil
		}
	}
	return nil, fmt.Errorf("%s", errScrapingNotFound)
}

// GetCrawlingRuleByName returns the crawling rule with the specified name.
func (re *RuleEngine) GetCrawlingRuleByName(name string) (*CrawlingRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		for _, r := range rg.CrawlingRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf("%s", errCrawlingNotFound)
}

// GetDetectionRuleByName returns the detection rule with the specified name.
func (re *RuleEngine) GetDetectionRuleByName(name string) (*DetectionRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}
	for _, rg := range re.GetAllRuleGroups() {
		for _, r := range rg.DetectionRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf("%s", errDetectionNotFound)
}

// IsGroupValid checks if the provided RuleGroup is valid.
// It checks if the group is enabled and if the valid_from and valid_to dates are valid.
func (re *RuleEngine) IsGroupValid(group RuleGroup) bool {
	// Check if the group is enabled
	if !group.IsEnabled {
		return false
	}

	// Check if the rules group has a valid_from and valid_to date
	if (group.ValidFrom.IsEmpty()) && (group.ValidTo.IsEmpty()) {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Given no valid_from and valid_to fields were found for group '%s', it's always valid.", group.GroupName)
		return true
	}

	var validFrom, validTo CustomTime

	// Parse the 'valid_from' date if present
	if !group.ValidFrom.IsEmpty() {
		validFrom = group.ValidFrom
	}

	// Parse the 'valid_to' date if present
	if !group.ValidTo.IsEmpty() {
		validTo = group.ValidTo
	}

	// Get the current time
	now := time.Now()

	// Log the validation details
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Validating group: %s", group.GroupName)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Valid from: %s", validFrom)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Valid to: %s", validTo)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Current time: %s", now)

	// Check the range only if both dates are provided
	if (!group.ValidFrom.IsEmpty()) && (!group.ValidTo.IsEmpty()) {
		return now.After(validFrom.Time) && now.Before(validTo.Time)
	}

	// If only valid_from is provided
	if !group.ValidFrom.IsEmpty() {
		return now.After(validFrom.Time)
	}

	// If only valid_to is provided
	if !group.ValidTo.IsEmpty() {
		return now.Before(validTo.Time)
	}

	return false
}

// FindRulesForSite finds the rules for the provided URL.
// It returns a pointer to the SiteRules for the provided URL or an error if no rules are found.
func (re *RuleEngine) FindRulesForSite(inputURL string) (*Ruleset, error) {
	// Validate the input URL
	checkedURL, err := PrepareURLForSearch(inputURL)
	if err != nil {
		return nil, err
	}

	// Parse the input URL to extract the domain
	parsedURL, err := url.Parse(checkedURL)
	if err != nil {
		return nil, fmt.Errorf(errParsingURL, err)
	}
	inputDomain := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))

	// Iterate over the SiteRules to find a matching domain
	for _, siteRule := range re.Rulesets {
		siteRuleset, err := url.Parse(siteRule.Name)
		if err != nil {
			continue
		}
		rsHost := strings.TrimSpace(strings.ToLower(siteRuleset.Hostname()))
		if rsHost == inputDomain {
			return &siteRule, nil
		}
	}

	return nil, nil
}

// FindRulesetByName returns the ruleset with the provided name (if any).
func (re *RuleEngine) FindRulesetByName(name string) (*Ruleset, error) {
	// Validate the input name
	inputName, err := PrepareNameForSearch(name)
	if err != nil {
		return nil, err
	}

	// Iterate over the SiteRules to find a matching domain
	for _, ruleset := range re.Rulesets {
		rsName := strings.TrimSpace(strings.ToLower(ruleset.Name))
		if rsName == inputName {
			return &ruleset, nil
		}
	}

	return nil, nil
}

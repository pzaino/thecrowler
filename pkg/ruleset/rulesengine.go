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
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// NewRuleEngine creates a new instance of RuleEngine with the provided site rules.
// It initializes the RuleEngine with the given sites and returns a pointer to the created RuleEngine.
func NewRuleEngine(schemaPath string, ruleset []Ruleset) *RuleEngine {
	// Create a new instance of RuleEngine
	ruleEngine := NewEmptyRuleEngine(schemaPath)

	// Parse the ruleset
	if ruleEngine.Schema != nil {
		for _, rs := range ruleset {
			err := ruleEngine.ValidateRuleset(rs)
			if err != nil {
				return nil
			}
		}
	}

	// Set the rulesets
	ruleEngine.Rulesets = ruleset

	// Implementation of the RuleEngine initialization
	return &ruleEngine
}

// NewEmptyRuleEngine creates a new instance of RuleEngine with an empty slice of site rules.
func NewEmptyRuleEngine(schemaPath string) RuleEngine {
	schema, err := LoadSchema(schemaPath)
	if err != nil {
		return RuleEngine{
			Schema:   nil,
			Rulesets: []Ruleset{},
		}
	}

	return RuleEngine{
		Schema:   schema,
		Rulesets: []Ruleset{},
	}
}

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

// NewRuleEngineWithParser creates a new instance of RuleEngine with the provided site rules.
func NewRuleEngineWithParser(parser RuleParser, file string) (*RuleEngine, error) {
	rulesets, err := parser.ParseRules(nil, file)
	if err != nil {
		return nil, err
	}
	return &RuleEngine{
		Schema:   nil,
		Rulesets: rulesets,
	}, nil
}

func (re *RuleEngine) LoadRulesFromFile(files []string) error {
	for _, file := range files {
		ruleset, err := BulkLoadRules(re.Schema, file)
		if err != nil {
			return err
		}
		re.Rulesets = append(re.Rulesets, ruleset...)
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

// AddRuleset adds a new ruleset to the RuleEngine.
func (re *RuleEngine) AddRuleset(ruleset Ruleset) {
	re.Rulesets = append(re.Rulesets, ruleset)
}

// RemoveRuleset removes a ruleset from the RuleEngine.
func (re *RuleEngine) RemoveRuleset(ruleset Ruleset) {
	for i, r := range re.Rulesets {
		if r.Name == ruleset.Name {
			re.Rulesets = append(re.Rulesets[:i], re.Rulesets[i+1:]...)
			return
		}
	}
}

// UpdateRuleset updates a ruleset in the RuleEngine.
func (re *RuleEngine) UpdateRuleset(ruleset Ruleset) {
	for i, r := range re.Rulesets {
		if r.Name == ruleset.Name {
			re.Rulesets[i] = ruleset
			return
		}
	}
}

// GetScrapingRule returns a scraping rule with the specified name.
func (re *RuleEngine) GetScrapingRule(name string) (ScrapingRule, error) {
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			for _, r := range rg.ScrapingRules {
				if r.RuleName == name {
					return r, nil
				}
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf("rule not found")
}

// GetActionRule returns an action rule with the specified name.
func (re *RuleEngine) GetActionRule(name string) (ActionRule, error) {
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			for _, r := range rg.ActionRules {
				if r.RuleName == name {
					return r, nil
				}
			}
		}
	}
	return ActionRule{}, fmt.Errorf("rule not found")
}

// GetRuleGroup returns a rule group with the specified name.
func (re *RuleEngine) GetRuleGroup(name string) (RuleGroup, error) {
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			if rg.GroupName == name {
				return rg, nil
			}
		}
	}
	return RuleGroup{}, fmt.Errorf(errRuleGroupNotFound)
}

// GetRuleset returns a ruleset with the specified name.
func (re *RuleEngine) GetRuleset(name string) (Ruleset, error) {
	for _, rs := range re.Rulesets {
		if rs.Name == name {
			return rs, nil
		}
	}
	return Ruleset{}, fmt.Errorf(errRulesetNotFound)
}

// LoadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func (re *RuleEngine) LoadRulesFromConfig(config *cfg.Config) error {
	for _, rs := range config.Rulesets {
		rulesets, err := loadRulesFromConfig(re.Schema, rs)
		if err != nil {
			return err
		}
		re.Rulesets = append(re.Rulesets, *rulesets...)
	}
	return nil
}

// Return the number of rulesets in the RuleEngine.
func (re *RuleEngine) CountRulesets() int {
	return len(re.Rulesets)
}

// Return the number of RuleGroups in the RuleEngine.
func (re *RuleEngine) CountRuleGroups() int {
	var count int
	for _, rs := range re.Rulesets {
		count += len(rs.RuleGroups)
	}
	return count
}

// Return the number of ScrapingRules in the RuleEngine.
func (re *RuleEngine) CountScrapingRules() int {
	var count int
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			count += len(rg.ScrapingRules)
		}
	}
	return count
}

// Return the number of ActionRules in the RuleEngine.
func (re *RuleEngine) CountActionRules() int {
	var count int
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			count += len(rg.ActionRules)
		}
	}
	return count
}

// Return the number of rules in the RuleEngine.
func (re *RuleEngine) CountRules() int {
	return re.CountScrapingRules() + re.CountActionRules()
}

// GetRulesetByURL returns the ruleset for the specified URL.
func (re *RuleEngine) GetRulesetByURL(urlStr string) (*Ruleset, error) {
	// Validate URL
	if urlStr == "" {
		return nil, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rs := range re.Rulesets {
		if strings.ToLower(strings.TrimSpace(rs.Name)) == parsedURL || strings.ToLower(strings.TrimSpace(rs.Name)) == "*" {
			return &rs, nil
		}
	}
	return nil, fmt.Errorf(errRulesetNotFound)
}

// GetRulesGroupByURL returns the rules group for the specified URL.
func (re *RuleEngine) GetRuleGroupByURL(urlStr string) (*RuleGroup, error) {
	// Validate URL
	if urlStr == "" {
		return nil, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedURL {
				if rg.IsValid() {
					return &rg, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(errRuleGroupNotFound)
}

// GetRulesGroupByName returns the rules group for the specified name.
func (re *RuleEngine) GetRuleGroupByName(name string) (*RuleGroup, error) {
	// Validate name
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf(errEmptyName)
	}
	parsedName := strings.ToLower(name)
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedName {
				if rg.IsValid() {
					return &rg, nil
				} else {
					return nil, fmt.Errorf("RuleGroup '%s' is not valid", rg.GroupName)
				}
			}
		}
	}
	return nil, fmt.Errorf(errRuleGroupNotFound)
}

// GetRulesetByName returns the ruleset for the specified name.
func (re *RuleEngine) GetRulesetByName(name string) (*Ruleset, error) {
	// Validate name
	if name == "" {
		return nil, fmt.Errorf(errEmptyName)
	}
	parsedName := strings.ToLower(strings.TrimSpace(name))
	for _, rs := range re.Rulesets {
		if strings.ToLower(strings.TrimSpace(rs.Name)) == parsedName {
			return &rs, nil
		}
	}
	return nil, fmt.Errorf(errRulesetNotFound)
}

// GetActionRuleByName returns the action rule with the specified name.
func (re *RuleEngine) GetActionRuleByName(name string) (*ActionRule, error) {
	// Validate name
	if name == "" {
		return nil, fmt.Errorf(errEmptyName)
	}
	parsedName := strings.ToLower(strings.TrimSpace(name))
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			for _, r := range rg.ActionRules {
				//cmn.DebugMsg(cmn.DbgLvlDebug2, "Checking rule: '%s' == '%s'", r.RuleName, parsedName)
				if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
					return &r, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(errActionNotFound)
}

// GetActionRuleByURL returns the action rule for the specified URL.
func (re *RuleEngine) GetActionRuleByURL(urlStr string) (*ActionRule, error) {
	// Validate URL
	if urlStr == "" {
		return nil, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			for _, r := range rg.ActionRules {
				if strings.ToLower(strings.TrimSpace(r.URL)) == parsedURL {
					return &r, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(errActionNotFound)
}

// GetScrapingRuleByName returns the scraping rule with the specified name.
func (re *RuleEngine) GetScrapingRuleByName(name string) (*ScrapingRule, error) {
	// Validate name
	if name == "" {
		return nil, fmt.Errorf(errEmptyName)
	}
	parsedName := strings.ToLower(strings.TrimSpace(name))
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			for _, r := range rg.ScrapingRules {
				if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
					return &r, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(errScrapingNotFound)
}

// GetScrapingRuleByPath returns the scraping rule for the specified path.
func (re *RuleEngine) GetScrapingRuleByPath(path string) (*ScrapingRule, error) {
	// Validate path
	if path == "" {
		return nil, fmt.Errorf(errEmptyPath)
	}
	parsedPath := strings.ToLower(strings.TrimSpace(path))
	for _, rs := range re.Rulesets {
		for _, rg := range rs.RuleGroups {
			if rule, err := findScrapingRuleByPath(parsedPath, rg.ScrapingRules); err == nil {
				return rule, nil
			}
		}
	}
	return nil, fmt.Errorf(errScrapingNotFound)
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
		cmn.DebugMsg(cmn.DbgLvlError, "No valid_from and valid_to dates found for group: %s", group.GroupName)
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
	if inputURL == "" {
		return nil, fmt.Errorf(errEmptyURL)
	}

	// Parse the input URL to extract the domain
	parsedURL, err := url.Parse(inputURL)
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
	if name == "" {
		return nil, fmt.Errorf("empty ruleset name provided")
	}

	inputName := strings.ToLower(strings.TrimSpace(name))

	// Iterate over the SiteRules to find a matching domain
	for _, ruleset := range re.Rulesets {
		rsName := strings.TrimSpace(strings.ToLower(ruleset.Name))
		if rsName == inputName {
			return &ruleset, nil
		}
	}

	return nil, nil
}

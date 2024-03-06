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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"gopkg.in/yaml.v2"
)

// UnmarshalYAML parses date strings from the YAML file.
func (ct *CustomTime) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var dateStr string
	if err := unmarshal(&dateStr); err != nil {
		return err
	}

	// Parse date string in RFC3339 format, if it fails, try with the "2006-01-02" format
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		t, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return err
		}
	}

	ct.Time = t
	return nil
}

// IsEmpty checks if the CustomTime is empty.
func (ct *CustomTime) IsEmpty() bool {
	return ct.Time.IsZero()
}

// ParseRules parses a YAML file containing site rules and returns a slice of SiteRules.
// It takes a file path as input and returns the parsed site rules or an error if the file cannot be read or parsed.
func ParseRules(path string) ([]Ruleset, error) {
	files, err := filepath.Glob(path)
	if err != nil {
		fmt.Println("Error finding JSON files:", err)
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	var rulesets []Ruleset
	for _, file := range files {
		var ruleset []Ruleset
		yamlFile, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}

		err = yaml.Unmarshal(yamlFile, &ruleset)
		if err != nil {
			return nil, err
		}
		rulesets = append(rulesets, ruleset...)
	}

	return rulesets, nil
}

// ParseRules is an interface for parsing rules from a file.
func (p *DefaultRuleParser) ParseRules(file string) ([]Ruleset, error) {
	return ParseRules(file)
}

// InitializeLibrary initializes the library by parsing the rules from the specified file
// and creating a new rule engine with the parsed sites.
// It returns a pointer to the created RuleEngine and an error if any occurred during parsing.
func InitializeLibrary(rulesFile string) (*RuleEngine, error) {
	rules, err := ParseRules(rulesFile)
	if err != nil {
		return nil, err
	}

	engine := NewRuleEngine(rules)
	return engine, nil
}

// LoadRulesFromFile loads the rules from the specified file and returns a pointer to the created RuleEngine.
func LoadRulesFromFile(files []string) (*RuleEngine, error) {
	var rules []Ruleset
	for _, file := range files {
		r, err := ParseRules(file)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r...)
	}
	return NewRuleEngine(rules), nil
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
	return RuleGroup{}, fmt.Errorf("rule group not found")
}

// GetRuleset returns a ruleset with the specified name.
func (re *RuleEngine) GetRuleset(name string) (Ruleset, error) {
	for _, rs := range re.Rulesets {
		if rs.Name == name {
			return rs, nil
		}
	}
	return Ruleset{}, fmt.Errorf("ruleset not found")
}

// LoadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func (re *RuleEngine) LoadRulesFromConfig(config *cfg.Config) error {
	for _, rs := range config.Rulesets {
		rulesets, err := loadRulesFromConfig(rs)
		if err != nil {
			return err
		}
		re.Rulesets = append(re.Rulesets, *rulesets...)
	}
	return nil
}

// loadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func loadRulesFromConfig(config cfg.Ruleset) (*[]Ruleset, error) {
	if config.Path == nil {
		return nil, fmt.Errorf("empty path provided")
	}
	if config.Host == "" {
		// Rules are stored locally
		var ruleset []Ruleset
		for _, path := range config.Path {
			rules, err := ParseRules(path)
			if err != nil {
				return nil, err
			}
			ruleset = append(ruleset, rules...)
		}
		return &ruleset, nil
	}
	// Rules are stored remotely
	// TODO: implement remote ruleset loading
	return nil, fmt.Errorf("remote ruleset loading not implemented yet")

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

// NewRuleEngine creates a new instance of RuleEngine with the provided site rules.
// It initializes the RuleEngine with the given sites and returns a pointer to the created RuleEngine.
func NewRuleEngine(ruleset []Ruleset) *RuleEngine {
	// Implementation of the RuleEngine initialization
	return &RuleEngine{
		Rulesets: ruleset,
	}
}

// NewEmptyRuleEngine creates a new instance of RuleEngine with an empty slice of site rules.
func NewEmptyRuleEngine() RuleEngine {
	return RuleEngine{
		Rulesets: []Ruleset{},
	}
}

// NewRuleEngineWithParser creates a new instance of RuleEngine with the provided site rules.
func NewRuleEngineWithParser(parser RuleParser, file string) (*RuleEngine, error) {
	rulesets, err := parser.ParseRules(file)
	if err != nil {
		return nil, err
	}
	return &RuleEngine{Rulesets: rulesets}, nil
}

// GetEnabledRuleGroups returns a slice of RuleGroup containing only the enabled rule groups.
// It iterates over the RuleGroups in the SiteRules and appends the enabled ones to the result slice.
func (s *Ruleset) GetEnabledRuleGroups() []RuleGroup {
	var enabledRuleGroups []RuleGroup

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			enabledRuleGroups = append(enabledRuleGroups, rg)
		}
	}

	return enabledRuleGroups
}

// GetEnabledRules returns a slice of Rule containing only the enabled rules.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules to the result slice.
func (s *Ruleset) GetEnabledRules() []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			enabledRules = append(enabledRules, rg.ScrapingRules...)
		}
	}

	return enabledRules
}

// GetEnabledRulesByGroup returns a slice of Rule containing only the enabled rules for the specified group.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules for the specified group to the result slice.
func (s *Ruleset) GetEnabledRulesByGroup(groupName string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled && rg.GroupName == groupName {
			enabledRules = append(enabledRules, rg.ScrapingRules...)
		}
	}

	return enabledRules
}

// GetEnabledRulesByPath returns a slice of Rule containing only the enabled rules for the specified path.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules for the specified path to the result slice.
func (s *Ruleset) GetEnabledRulesByPath(path string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			for _, r := range rg.ScrapingRules {
				if r.Path == path {
					enabledRules = append(enabledRules, r)
				}
			}
		}
	}

	return enabledRules
}

// GetEnabledRulesByPathAndGroup returns a slice of Rule containing only the enabled rules for the specified path and group.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules for the specified path and group to the result slice.
func (s *Ruleset) GetEnabledRulesByPathAndGroup(path, groupName string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled && rg.GroupName == groupName {
			for _, r := range rg.ScrapingRules {
				if r.Path == path {
					enabledRules = append(enabledRules, r)
				}
			}
		}
	}

	return enabledRules
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
		return nil, fmt.Errorf("empty URL provided")
	}

	// Parse the input URL to extract the domain
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL: %s", err)
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

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
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"gopkg.in/yaml.v2"
)

// UnmarshalYAML parses date strings from the YAML file.
func (ct *CustomTime) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var dateStr string
	if err := unmarshal(&dateStr); err != nil {
		return err
	}
	fmt.Printf("Parsing date string: %s\n", dateStr) // Debug print
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return err
	}
	ct.Time = t
	return nil
}

func (ct *CustomTime) IsEmpty() bool {
	return ct.Time.IsZero()
}

// ParseRules parses a YAML file containing site rules and returns a slice of SiteRules.
// It takes a file path as input and returns the parsed site rules or an error if the file cannot be read or parsed.
func ParseRules(file string) ([]Ruleset, error) {
	var sites []Ruleset

	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(yamlFile, &sites)
	if err != nil {
		return nil, err
	}

	return sites, nil
}

// RuleParser is an interface for parsing rules from a file.
func (p *DefaultRuleParser) ParseRules(file string) ([]Ruleset, error) {
	return ParseRules(file)
}

// InitializeLibrary initializes the library by parsing the rules from the specified file
// and creating a new rule engine with the parsed sites.
// It returns a pointer to the created RuleEngine and an error if any occurred during parsing.
func InitializeLibrary(rulesFile string) (*RuleEngine, error) {
	sites, err := ParseRules(rulesFile)
	if err != nil {
		return nil, err
	}

	engine := NewRuleEngine(sites)
	return engine, nil
}

// NewRuleEngine creates a new instance of RuleEngine with the provided site rules.
// It initializes the RuleEngine with the given sites and returns a pointer to the created RuleEngine.
func NewRuleEngine(ruleset []Ruleset) *RuleEngine {
	// Implementation of the RuleEngine initialization
	return &RuleEngine{
		Rulesets: ruleset,
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
	inputDomain := parsedURL.Hostname()

	// Iterate over the SiteRules to find a matching domain
	for _, siteRule := range re.Rulesets {
		if strings.Contains(siteRule.Name, inputDomain) {
			return &siteRule, nil
		}
	}

	return nil, nil
}

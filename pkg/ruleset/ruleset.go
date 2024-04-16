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
	"strings"
)

///// ------------------------ RULESET ---------------------------------- /////

/// -- Creation -- ///

// NewRuleset creates a new Ruleset with the specified name.
func NewRuleset(name string) Ruleset {
	return Ruleset{
		Name:       name,
		RuleGroups: []RuleGroup{},
	}
}

/// --- Retrieving --- ///

// GetRuleGroups returns all the rule groups in a Ruleset.
func (rs *Ruleset) GetAllRuleGroups() []RuleGroup {
	return rs.RuleGroups
}

// GetAllENabledRuleGroups returns all the enabled rule groups in a Ruleset.
func (rs *Ruleset) GetAllEnabledRuleGroups() []RuleGroup {
	var enabledRuleGroups []RuleGroup
	for _, rg := range rs.RuleGroups {
		if rg.IsValid() {
			enabledRuleGroups = append(enabledRuleGroups, rg)
		}
	}
	return enabledRuleGroups
}

// GetActionRules returns all the action rules in a Ruleset.
func (rs *Ruleset) GetAllActionRules() []ActionRule {
	var actionRules []ActionRule
	for _, rg := range rs.RuleGroups {
		actionRules = append(actionRules, rg.ActionRules...)
	}
	return actionRules
}

// GetScrapingRules returns all the scraping rules in a Ruleset.
func (rs *Ruleset) GetAllScrapingRules() []ScrapingRule {
	var scrapingRules []ScrapingRule
	for _, rg := range rs.RuleGroups {
		scrapingRules = append(scrapingRules, rg.ScrapingRules...)
	}
	return scrapingRules
}

// GetCrawlingRules returns all the crawling rules in a Ruleset.
func (rs *Ruleset) GetAllCrawlingRules() []CrawlingRule {
	var crawlingRules []CrawlingRule
	for _, rg := range rs.RuleGroups {
		crawlingRules = append(crawlingRules, rg.CrawlingRules...)
	}
	return crawlingRules
}

// GetDetectionRules returns all the detection rules in a Ruleset.
func (rs *Ruleset) GetAllDetectionRules() []DetectionRule {
	var detectionRules []DetectionRule
	for _, rg := range rs.RuleGroups {
		detectionRules = append(detectionRules, rg.DetectionRules...)
	}
	return detectionRules
}

// GetAllEnabledRules returns a slice of Rule containing only the enabled rules.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules
// to the result slice.
func (s *Ruleset) GetAllEnabledScrapingRules() []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.GetAllEnabledRuleGroups() {
		enabledRules = append(enabledRules, rg.ScrapingRules...)
	}

	return enabledRules
}

// GetAllEnabledActionRules returns a slice of Rule containing only the enabled action rules.
// It iterates over the RuleGroups in the SiteRules and appends the enabled action rules
// to the result slice.
func (s *Ruleset) GetAllEnabledActionRules() []ActionRule {
	var enabledRules []ActionRule

	for _, rg := range s.GetAllEnabledRuleGroups() {
		enabledRules = append(enabledRules, rg.ActionRules...)
	}

	return enabledRules
}

// GetAllEnabledCrawlingRules returns a slice of Rule containing only the enabled crawling rules.
// It iterates over the RuleGroups in the SiteRules and appends the enabled crawling rules
// to the result slice.
func (s *Ruleset) GetAllEnabledCrawlingRules() []CrawlingRule {
	var enabledRules []CrawlingRule

	for _, rg := range s.GetAllEnabledRuleGroups() {
		enabledRules = append(enabledRules, rg.CrawlingRules...)
	}

	return enabledRules
}

// GetAllEnabledDetectionRules returns a slice of Rule containing only the enabled detection rules.
// It iterates over the RuleGroups in the SiteRules and appends the enabled detection rules
// to the result slice.
func (s *Ruleset) GetAllEnabledDetectionRules() []DetectionRule {
	var enabledRules []DetectionRule

	for _, rg := range s.GetAllEnabledRuleGroups() {
		enabledRules = append(enabledRules, rg.DetectionRules...)
	}

	return enabledRules
}

/// --- Searching --- ///

// GetRuleGroupByName returns the rule group with the specified name.
func (rs *Ruleset) GetRuleGroupByName(name string) (RuleGroup, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return RuleGroup{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedName {
			return rg, nil
		}
	}
	return RuleGroup{}, fmt.Errorf(errRuleGroupNotFound)
}

// GetRuleGroupByURL returns the rule group for the specified URL.
func (rs *Ruleset) GetRuleGroupByURL(urlStr string) (RuleGroup, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return RuleGroup{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedURL {
			return rg, nil
		}
	}
	return RuleGroup{}, fmt.Errorf(errRuleGroupNotFound)
}

// GetActionRuleByName returns the action rule with the specified name.
func (rs *Ruleset) GetActionRuleByName(name string) (ActionRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return ActionRule{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		for _, r := range rg.ActionRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return r, nil
			}
		}
	}
	return ActionRule{}, fmt.Errorf(errActionNotFound)
}

// GetActionRuleByURL returns the action rule for the specified URL.
func (rs *Ruleset) GetActionRuleByURL(urlStr string) (ActionRule, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return ActionRule{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		for _, r := range rg.ActionRules {
			if strings.ToLower(strings.TrimSpace(r.URL)) == parsedURL {
				return r, nil
			}
		}
	}
	return ActionRule{}, fmt.Errorf(errActionNotFound)
}

// GetScrapingRuleByName returns the scraping rule with the specified name.
func (rs *Ruleset) GetScrapingRuleByName(name string) (ScrapingRule, error) {
	// Validate name
	parsedName, err := PrepareNameForSearch(name)
	if err != nil {
		return ScrapingRule{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		for _, r := range rg.ScrapingRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == parsedName {
				return r, nil
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf(errScrapingNotFound)
}

// GetScrapingRuleByPath returns the scraping rule for the specified path.
func (rs *Ruleset) GetScrapingRuleByPath(path string) (ScrapingRule, error) {
	// Validate path
	parsedPath, err := PreparePathForSearch(path)
	if err != nil {
		return ScrapingRule{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		for _, r := range rg.ScrapingRules {
			for _, p := range r.PreConditions {
				if strings.ToLower(strings.TrimSpace(p.Path)) == parsedPath {
					return r, nil
				}
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf(errScrapingNotFound)
}

// GetScrapingRuleByURL returns the scraping rule for the specified URL.
func (rs *Ruleset) GetScrapingRuleByURL(urlStr string) (ScrapingRule, error) {
	// Validate URL
	parsedURL, err := PrepareURLForSearch(urlStr)
	if err != nil {
		return ScrapingRule{}, err
	}
	for _, rg := range rs.GetAllEnabledRuleGroups() {
		for _, r := range rg.ScrapingRules {
			for _, u := range r.PreConditions {
				if strings.ToLower(strings.TrimSpace(u.URL)) == parsedURL {
					return r, nil
				}
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf(errScrapingNotFound)
}

// GetEnabledRulesByGroup returns a slice of Rule containing only the enabled rules for the specified group.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules for the specified group to the result slice.
func (s *Ruleset) GetEnabledRulesByGroupName(groupName string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, rg := range s.GetAllEnabledRuleGroups() {
		if rg.GroupName == groupName {
			enabledRules = append(enabledRules, rg.ScrapingRules...)
		}
	}

	return enabledRules
}

// GetEnabledRulesByPath returns a slice of Rule containing only the enabled rules for the specified path.
// It iterates over the RuleGroups in the SiteRules and appends the enabled rules for the specified path to the result slice.
func (s *Ruleset) GetEnabledRulesByPath(path string) []ScrapingRule {
	var enabledRules []ScrapingRule
	parsedPath, err := PreparePathForSearch(path)
	if err != nil {
		return enabledRules
	}

	for _, rg := range s.GetAllEnabledRuleGroups() {
		if rg.IsEnabled {
			enabledRules = append(enabledRules, s.getEnabledScrapingRulesByPathHelper(rg, parsedPath)...)
		}
	}

	return enabledRules
}

func (s *Ruleset) getEnabledScrapingRulesByPathHelper(rg RuleGroup, parsedPath string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, r := range rg.ScrapingRules {
		for _, p := range r.PreConditions {
			if strings.TrimSpace(p.Path) == parsedPath {
				enabledRules = append(enabledRules, r)
			}
		}
	}

	return enabledRules
}

// GetEnabledRulesByPathAndGroup returns a slice of Rule containing only the
// enabled rules for the specified path and group. It iterates over the RuleGroups
// in the SiteRules and appends the enabled rules for the specified path and group
// to the result slice.
func (s *Ruleset) GetEnabledScrapingRulesByPathAndGroup(path, groupName string) []ScrapingRule {
	var enabledRules []ScrapingRule
	path = strings.TrimSpace(path)

	for _, rg := range s.GetAllEnabledRuleGroups() {
		if rg.GroupName == groupName {
			enabledRules = append(enabledRules, s.getEnabledScrapingRulesByPathAndGroupHelper(rg, path)...)
		}
	}

	return enabledRules
}

func (s *Ruleset) getEnabledScrapingRulesByPathAndGroupHelper(rg RuleGroup, path string) []ScrapingRule {
	var enabledRules []ScrapingRule

	for _, r := range rg.ScrapingRules {
		for _, p := range r.PreConditions {
			if strings.TrimSpace(p.Path) == path {
				enabledRules = append(enabledRules, r)
			}
		}
	}

	return enabledRules
}

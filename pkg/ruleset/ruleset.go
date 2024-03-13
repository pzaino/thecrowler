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
	"strings"
)

///// ------------------------ RULESET ---------------------------------- /////

// GetActionRules returns all the action rules in a Ruleset.
func (rs *Ruleset) GetActionRules() []ActionRule {
	var actionRules []ActionRule
	for _, rg := range rs.RuleGroups {
		actionRules = append(actionRules, rg.ActionRules...)
	}
	return actionRules
}

// GetActionRuleByName returns the action rule with the specified name.
func (rs *Ruleset) GetActionRuleByName(name string) (ActionRule, error) {
	// Validate name
	if name == "" {
		return ActionRule{}, fmt.Errorf(errEmptyName)
	}

	// prepare name
	name = strings.ToLower(strings.TrimSpace(name))
	for _, rg := range rs.RuleGroups {
		for _, r := range rg.ActionRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == name {
				return r, nil
			}
		}
	}
	return ActionRule{}, fmt.Errorf(errActionNotFound)
}

// GetActionRuleByURL returns the action rule for the specified URL.
func (rs *Ruleset) GetActionRuleByURL(urlStr string) (ActionRule, error) {
	// Validate URL
	if urlStr == "" {
		return ActionRule{}, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return ActionRule{}, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rg := range rs.RuleGroups {
		for _, r := range rg.ActionRules {
			if strings.ToLower(strings.TrimSpace(r.URL)) == parsedURL {
				return r, nil
			}
		}
	}
	return ActionRule{}, fmt.Errorf(errActionNotFound)
}

// GetRuleGroups returns all the rule groups in a Ruleset.
func (rs *Ruleset) GetRuleGroups() []RuleGroup {
	return rs.RuleGroups
}

// GetRuleGroupByName returns the rule group with the specified name.
func (rs *Ruleset) GetRuleGroupByName(name string) (RuleGroup, error) {
	// Validate name
	if name == "" {
		return RuleGroup{}, fmt.Errorf(errEmptyName)
	}

	// prepare name
	name = strings.ToLower(strings.TrimSpace(name))
	for _, rg := range rs.RuleGroups {
		if strings.ToLower(strings.TrimSpace(rg.GroupName)) == name {
			if !rg.IsValid() {
				return RuleGroup{}, fmt.Errorf("rule group not valid")
			}
			return rg, nil
		}
	}
	return RuleGroup{}, fmt.Errorf(errRuleGroupNotFound)
}

// GetRuleGroupByURL returns the rule group for the specified URL.
func (rs *Ruleset) GetRuleGroupByURL(urlStr string) (RuleGroup, error) {
	// Validate URL
	if urlStr == "" {
		return RuleGroup{}, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return RuleGroup{}, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rg := range rs.RuleGroups {
		if strings.ToLower(strings.TrimSpace(rg.GroupName)) == parsedURL {
			if !rg.IsValid() {
				return RuleGroup{}, fmt.Errorf("rule group not valid")
			}
			return rg, nil
		}
	}
	return RuleGroup{}, fmt.Errorf(errRuleGroupNotFound)
}

// GetScrapingRules returns all the scraping rules in a Ruleset.
func (rs *Ruleset) GetScrapingRules() []ScrapingRule {
	var scrapingRules []ScrapingRule
	for _, rg := range rs.RuleGroups {
		scrapingRules = append(scrapingRules, rg.ScrapingRules...)
	}
	return scrapingRules
}

// GetScrapingRuleByName returns the scraping rule with the specified name.
func (rs *Ruleset) GetScrapingRuleByName(name string) (ScrapingRule, error) {
	// Validate name
	if name == "" {
		return ScrapingRule{}, fmt.Errorf(errEmptyName)
	}

	// prepare name
	name = strings.ToLower(strings.TrimSpace(name))
	for _, rg := range rs.RuleGroups {
		for _, r := range rg.ScrapingRules {
			if strings.ToLower(strings.TrimSpace(r.RuleName)) == name {
				return r, nil
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf(errScrapingNotFound)
}

// GetScrapingRuleByPath returns the scraping rule for the specified path.
func (rs *Ruleset) GetScrapingRuleByPath(path string) (ScrapingRule, error) {
	// Validate path
	if path == "" {
		return ScrapingRule{}, fmt.Errorf(errEmptyPath)
	}

	// prepare path
	path = strings.ToLower(strings.TrimSpace(path))
	for _, rg := range rs.RuleGroups {
		for _, r := range rg.ScrapingRules {
			for _, p := range r.PreConditions {
				if strings.ToLower(strings.TrimSpace(p.Path)) == path {
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
	if urlStr == "" {
		return ScrapingRule{}, fmt.Errorf(errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return ScrapingRule{}, fmt.Errorf(errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, rg := range rs.RuleGroups {
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
	path = strings.TrimSpace(path)

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			for _, r := range rg.ScrapingRules {
				for _, p := range r.PreConditions {
					if strings.TrimSpace(p.Path) == path {
						enabledRules = append(enabledRules, r)
					}
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
	path = strings.TrimSpace(path)

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled && rg.GroupName == groupName {
			enabledRules = append(enabledRules, s.getEnabledRulesByPathAndGroupHelper(rg, path)...)
		}
	}

	return enabledRules
}

func (s *Ruleset) getEnabledRulesByPathAndGroupHelper(rg RuleGroup, path string) []ScrapingRule {
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

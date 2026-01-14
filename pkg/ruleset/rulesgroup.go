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
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

///// ---------------------- RuleGroup -------------------------------- /////

/// --- Actions --- ///

// SetEnv sets the environment variables for the RuleGroup.
func (rg *RuleGroup) SetEnv(CtxID string) {
	if rg.Env != nil {
		for i := 0; i < len(rg.Env); i++ {
			// Retrieve the environment variable key, value and properties
			key := rg.Env[i].Key
			values := rg.Env[i].Values
			properties := rg.Env[i].Properties

			// Set the environment properties
			envProperties := cmn.NewKVStoreProperty(properties.Persistent, properties.Static, properties.SessionValid, properties.Shared, properties.Source, CtxID, properties.Type)
			// Set the environment variable
			err := cmn.KVStore.Set(key, values, envProperties)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, fmt.Sprintf("setting environment variable %s: %s", key, err.Error()))
			}
		}
	}
}

/// --- Checks --- ///

// IsValid checks if the provided RuleGroup is valid.
// It checks if the group is enabled and if the valid_from and valid_to dates are valid.
func (rg *RuleGroup) IsValid() bool {
	// Check if the group is enabled
	if !rg.IsEnabled {
		return false
	}

	// Check if the rules group has a valid_from and valid_to date
	if (rg.ValidFrom.IsEmpty()) && (rg.ValidTo.IsEmpty()) {
		return true
	}

	var validFrom, validTo CustomTime

	// Parse the 'valid_from' date if present
	if !rg.ValidFrom.IsEmpty() {
		validFrom = rg.ValidFrom
	}

	// Parse the 'valid_to' date if present
	if !rg.ValidTo.IsEmpty() {
		validTo = rg.ValidTo
	}

	// Get the current time
	now := time.Now().UTC()

	// Log the validation details
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Validating group: %s", rg.GroupName)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Valid from: %s", validFrom)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Valid to: %s", validTo)
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Current time: %s", now)

	// Check the range only if both dates are provided
	if (!rg.ValidFrom.IsEmpty()) && (!rg.ValidTo.IsEmpty()) {
		return now.After(validFrom.Time) && now.Before(validTo.Time)
	}

	// If only valid_from is provided
	if !rg.ValidFrom.IsEmpty() {
		return now.After(validFrom.Time)
	}

	// If only valid_to is provided
	if !rg.ValidTo.IsEmpty() {
		return now.Before(validTo.Time)
	}

	return false
}

// IsEmpty checks if the RuleGroup is empty.
func (rg *RuleGroup) IsEmpty() bool {
	return len(rg.ActionRules) == 0 && len(rg.ScrapingRules) == 0 && len(rg.CrawlingRules) == 0 && len(rg.DetectionRules) == 0 && len(rg.GroupName) == 0
}

/// --- Getters --- ///

// GetActionRules returns all the action rules in a RuleGroup.
func (rg *RuleGroup) GetActionRules() []ActionRule {
	return rg.ActionRules
}

// GetScrapingRules returns all the scraping rules in a RuleGroup.
func (rg *RuleGroup) GetScrapingRules() []ScrapingRule {
	return rg.ScrapingRules
}

// GetActionRuleByName returns the action rule with the specified name.
func (rg *RuleGroup) GetActionRuleByName(name string) (ActionRule, error) {
	// Validate name
	if name == "" {
		return ActionRule{}, fmt.Errorf("%s", errEmptyName)
	}

	// prepare name
	name = strings.ToLower(strings.TrimSpace(name))
	for _, r := range rg.ActionRules {
		if strings.ToLower(strings.TrimSpace(r.RuleName)) == name {
			return r, nil
		}
	}
	return ActionRule{}, fmt.Errorf("%s", errActionNotFound)
}

// GetActionRuleByURL returns the action rule for the specified URL.
func (rg *RuleGroup) GetActionRuleByURL(urlStr string) (ActionRule, error) {
	// Validate URL
	if urlStr == "" {
		return ActionRule{}, fmt.Errorf("%s", errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return ActionRule{}, fmt.Errorf("%s: %v", errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, r := range rg.ActionRules {
		if strings.ToLower(strings.TrimSpace(r.URL)) == parsedURL {
			return r, nil
		}
	}
	return ActionRule{}, fmt.Errorf("%s", errActionNotFound)
}

// GetScrapingRuleByName returns the scraping rule with the specified name.
func (rg *RuleGroup) GetScrapingRuleByName(name string) (ScrapingRule, error) {
	// Validate name
	if name == "" {
		return ScrapingRule{}, fmt.Errorf("%s", errEmptyName)
	}

	// prepare name
	name = strings.ToLower(strings.TrimSpace(name))
	for _, r := range rg.ScrapingRules {
		if strings.ToLower(strings.TrimSpace(r.RuleName)) == name {
			return r, nil
		}
	}
	return ScrapingRule{}, fmt.Errorf("%s", errScrapingNotFound)
}

// GetScrapingRuleByPath returns the scraping rule for the specified path.
func (rg *RuleGroup) GetScrapingRuleByPath(path string) (ScrapingRule, error) {
	// Validate path
	if strings.TrimSpace(path) == "" {
		return ScrapingRule{}, fmt.Errorf("%s", errEmptyPath)
	}

	// prepare path
	path = strings.ToLower(strings.TrimSpace(path))
	for _, r := range rg.ScrapingRules {
		for _, p := range r.PreConditions {
			if strings.ToLower(strings.TrimSpace(p.Path)) == path {
				return r, nil
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf("%s", errScrapingNotFound)
}

// GetScrapingRuleByURL returns the scraping rule for the specified URL.
func (rg *RuleGroup) GetScrapingRuleByURL(urlStr string) (ScrapingRule, error) {
	// Validate URL
	if urlStr == "" {
		return ScrapingRule{}, fmt.Errorf("%s", errEmptyURL)
	}
	_, err := url.Parse(urlStr)
	if err != nil {
		return ScrapingRule{}, fmt.Errorf("%s: %v", errParsingURL, err)
	}
	parsedURL := strings.ToLower(strings.TrimSpace(urlStr))
	for _, r := range rg.ScrapingRules {
		for _, u := range r.PreConditions {
			if strings.ToLower(strings.TrimSpace(u.URL)) == parsedURL {
				return r, nil
			}
		}
	}
	return ScrapingRule{}, fmt.Errorf("%s", errScrapingNotFound)
}

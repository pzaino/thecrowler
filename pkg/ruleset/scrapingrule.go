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
	"strings"
)

///// ------------------------ ScrapingRule ---------------------------- /////

// GetRuleName returns the rule name for the specified scraping rule.
func (r *ScrapingRule) GetRuleName() string {
	return strings.TrimSpace(r.RuleName)
}

// GetPaths returns the path for the specified scraping rule.
func (r *ScrapingRule) GetPaths() []string {
	var paths []string
	for _, p := range r.PreConditions {
		if strings.TrimSpace(p.Path) == "" {
			continue
		}
		paths = append(paths, strings.TrimSpace(p.Path))
	}
	return paths
}

// GetURLs returns the URL for the specified scraping rule.
func (r *ScrapingRule) GetURLs() []string {
	var urls []string
	for _, u := range r.PreConditions {
		if strings.TrimSpace(u.URL) == "" {
			continue
		}
		urls = append(urls, strings.TrimSpace(u.URL))
	}
	return urls
}

// GetElements returns the elements for the specified scraping rule.
func (r *ScrapingRule) GetElements() []Element {
	return r.Elements
}

// GetJsFiles returns the js_files flag for the specified scraping rule.
func (r *ScrapingRule) GetJsFiles() bool {
	return r.JsFiles
}

// GetJSONFieldMappings returns the JSON field mappings for the specified scraping rule.
func (r *ScrapingRule) GetJSONFieldMappings() map[string]string {
	return r.JSONFieldMappings
}

// GetWaitConditions returns the wait conditions for the specified scraping rule.
func (r *ScrapingRule) GetWaitConditions() []WaitCondition {
	return r.WaitConditions
}

// GetPostProcessing returns the post-processing steps for the specified scraping rule.
func (r *ScrapingRule) GetPostProcessing() []PostProcessingStep {
	return r.PostProcessing
}

// GetConditionType returns the condition type for the specified wait condition.
func (w *WaitCondition) GetConditionType() string {
	return strings.ToLower(strings.TrimSpace(w.ConditionType))
}

// GetSelector returns the selector for the specified wait condition.
func (w *WaitCondition) GetSelector() Selector {
	return w.Selector
}

// GetStepType returns the step type for the specified post-processing step.
func (p *PostProcessingStep) GetStepType() string {
	return strings.ToLower(strings.TrimSpace(p.Type))
}

// GetDetails returns the details for the specified post-processing step.
func (p *PostProcessingStep) GetDetails() map[string]interface{} {
	return p.Details
}

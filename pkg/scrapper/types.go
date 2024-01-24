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

package scrapper

// SiteRules represents the structure of the site_rules.yaml file
type SiteRules struct {
	Site       string      `yaml:"site"`
	RuleGroups []RuleGroup `yaml:"rule_groups"`
}

// RuleGroup represents the structure of the rule_groups section in the site_rules.yaml file
type RuleGroup struct {
	GroupName string `yaml:"group_name"`
	ValidFrom string `yaml:"valid_from"`
	ValidTo   string `yaml:"valid_to"`
	IsEnabled bool   `yaml:"is_enabled"`
	Rules     []Rule `yaml:"rules"`
}

// Rule represents the structure of the rules section in the site_rules.yaml file
type Rule struct {
	Path               string    `yaml:"path"`
	Elements           []Element `yaml:"elements"`
	JsFiles            bool      `yaml:"js_files"`
	TechnologyPatterns []string  `yaml:"technology_patterns"` // If still needed
}

// Element represents the structure of the elements section in the site_rules.yaml file
type Element struct {
	Key          string `yaml:"key"`
	SelectorType string `yaml:"selector_type"`
	Selector     string `yaml:"selector"`
}

// RuleEngine represents the structure of the site_rules.yaml file
type RuleEngine struct {
	SiteRules []SiteRules
}

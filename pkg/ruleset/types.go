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
	"time"
)

// CustomTime wraps time.Time to provide custom parsing.
type CustomTime struct {
	time.Time
}

// / Ruleset represents the top-level structure of the rules YAML file
type Ruleset struct {
	Name       string      `yaml:"ruleset_name"`
	RuleGroups []RuleGroup `yaml:"rule_groups"`
}

// RuleGroup represents each group of rules within the ruleset
type RuleGroup struct {
	GroupName            string               `yaml:"group_name"`
	ValidFrom            CustomTime           `yaml:"valid_from"` // Assuming RFC3339 format for date-time
	ValidTo              CustomTime           `yaml:"valid_to"`
	IsEnabled            bool                 `yaml:"is_enabled"`
	ScrapingRules        []ScrapingRule       `yaml:"scraping_rules"`
	ActionRules          []ActionRule         `yaml:"action_rules"`
	EnvironmentSettings  EnvironmentSettings  `yaml:"environment_settings"`
	LoggingConfiguration LoggingConfiguration `yaml:"logging_configuration"`
}

// ScrapingRule represents a single scraping rule within a rule group
type ScrapingRule struct {
	RuleName           string               `yaml:"rule_name"`
	Path               string               `yaml:"path"`
	URL                string               `yaml:"url,omitempty"` // Optional
	Elements           []Element            `yaml:"elements"`
	JsFiles            bool                 `yaml:"js_files"`
	TechnologyPatterns []string             `yaml:"technology_patterns"`
	JsonFieldMappings  map[string]string    `yaml:"json_field_mappings"`
	WaitConditions     []WaitCondition      `yaml:"wait_conditions"`
	PostProcessing     []PostProcessingStep `yaml:"post_processing"`
}

// Elements represents a list of elements to be scraped
type Element struct {
	Key       string
	Selectors []Selector `yaml:"selectors"`
}

// Element represents a single element within a scraping rule
type Selector struct {
	SelectorType string `yaml:"selector_type"` // Enum: ["css", "xpath", "regex"]
	Selector     string `yaml:"selector"`
	Attribute    string `yaml:"attribute"`
}

// WaitCondition represents a condition to wait for before performing scraping
type WaitCondition struct {
	ConditionType string `yaml:"condition_type"`      // Enum: ["element_presence", "element_visible", "custom_js"]
	Selector      string `yaml:"selector,omitempty"`  // Optional
	CustomJS      string `yaml:"custom_js,omitempty"` // Optional
}

// PostProcessingStep represents a single step in post-processing scraped data
type PostProcessingStep struct {
	StepType string                 `yaml:"step_type"` // Enum: ["transform", "validate", "clean"]
	Details  map[string]interface{} `yaml:"details"`   // Flexible to accommodate different structures
}

// ActionRule represents a single action rule within a rule group
type ActionRule struct {
	RuleName       string          `yaml:"rule_name"`
	ActionType     string          `yaml:"action_type"` // Enum: ["click", "input_text", "submit_form", "scroll", "navigate", "screenshot"]
	Selector       string          `yaml:"selector"`
	Value          string          `yaml:"value,omitempty"` // Optional
	URL            string          `yaml:"url,omitempty"`   // Optional
	WaitConditions []WaitCondition `yaml:"wait_conditions"`
	Conditions     map[string]bool `yaml:"conditions"`
	ErrorHandling  ErrorHandling   `yaml:"error_handling"`
}

// ErrorHandling represents strategies for handling errors in action rules
type ErrorHandling struct {
	RetryCount int `yaml:"retry_count"`
	RetryDelay int `yaml:"retry_delay"`
}

// EnvironmentSettings represents settings for the WebDriver environment
type EnvironmentSettings struct {
	HeadlessMode         bool              `yaml:"headless_mode"`
	CustomBrowserOptions map[string]string `yaml:"custom_browser_options"`
}

// LoggingConfiguration represents configuration settings for logging
type LoggingConfiguration struct {
	LogLevel string `yaml:"log_level"`          // Enum: ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]
	LogFile  string `yaml:"log_file,omitempty"` // Optional
}

type RuleEngine struct {
	Rulesets []Ruleset
}

// RuleParser defines an interface for parsing rules.
type RuleParser interface {
	ParseRules(file string) ([]Ruleset, error)
}

type DefaultRuleParser struct{}

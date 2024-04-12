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

	"github.com/qri-io/jsonschema"
)

// RuleEngine represents the top-level structure for the rule engine
type RuleEngine struct {
	Schema   *jsonschema.Schema `yaml:"schema"`
	Rulesets []Ruleset          `yaml:"rulesets"`
}

// CustomTime wraps time.Time to provide custom parsing.
type CustomTime struct {
	time.Time
}

// Ruleset represents the top-level structure of the rules YAML file
type Ruleset struct {
	FormatVersion string      `yaml:"format_version"`
	Author        string      `yaml:"author"`
	CreatedAt     CustomTime  `yaml:"created_at"`
	Description   string      `yaml:"description"`
	Name          string      `yaml:"ruleset_name"`
	RuleGroups    []RuleGroup `yaml:"rule_groups"`
}

// RuleGroup represents a group of rules
type RuleGroup struct {
	GroupName            string               `yaml:"group_name"`
	ValidFrom            CustomTime           `yaml:"valid_from"`
	ValidTo              CustomTime           `yaml:"valid_to"`
	IsEnabled            bool                 `yaml:"is_enabled"`
	ScrapingRules        []ScrapingRule       `yaml:"scraping_rules"`
	ActionRules          []ActionRule         `yaml:"action_rules"`
	DetectionRules       []DetectionRule      `yaml:"detection_rules,omitempty"`
	CrawlingRules        []CrawlingRule       `yaml:"crawling_rules,omitempty"`
	EnvironmentSettings  EnvironmentSettings  `yaml:"environment_settings"`
	LoggingConfiguration LoggingConfiguration `yaml:"logging_configuration"`
}

type PreCondition struct {
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
}

// ScrapingRule represents a scraping rule
type ScrapingRule struct {
	RuleName           string                 `yaml:"rule_name"`
	PreConditions      []PreCondition         `yaml:"pre_conditions,omitempty"`
	Conditions         map[string]interface{} `yaml:"conditions"`
	WaitConditions     []WaitCondition        `yaml:"wait_conditions"`
	Elements           []Element              `yaml:"elements"`
	JsFiles            bool                   `yaml:"js_files"`
	TechnologyPatterns []string               `yaml:"technology_patterns"`
	JSONFieldMappings  map[string]string      `yaml:"json_field_mappings"`
	PostProcessing     []PostProcessingStep   `yaml:"post_processing"`
}

// ActionRule represents an action rule
type ActionRule struct {
	RuleName       string                 `yaml:"rule_name"`
	ActionType     string                 `yaml:"action_type"`
	Selectors      []Selector             `yaml:"selectors"`
	Value          string                 `yaml:"value,omitempty"`
	URL            string                 `yaml:"url,omitempty"`
	WaitConditions []WaitCondition        `yaml:"wait_conditions"`
	Conditions     map[string]interface{} `yaml:"conditions"`
	ErrorHandling  ErrorHandling          `yaml:"error_handling"`
}

// Element represents a single element to be scraped
type Element struct {
	Key       string     `yaml:"key"`
	Selectors []Selector `yaml:"selectors"`
}

// Selector represents a single selector
type Selector struct {
	SelectorType string `yaml:"selector_type"`
	Selector     string `yaml:"selector"`
	Attribute    struct {
		Name  string `yaml:"name"`
		Value string `yaml:"value"`
	} `yaml:"attribute,omitempty"`
	Value string `yaml:"value,omitempty"`
}

// WaitCondition represents a single wait condition
type WaitCondition struct {
	ConditionType string   `yaml:"condition_type"`
	Selector      Selector `yaml:"selector,omitempty"`
	CustomJS      string   `yaml:"custom_js,omitempty"`
	Value         string   `yaml:"value,omitempty"`
}

// PostProcessingStep represents a single post-processing step
type PostProcessingStep struct {
	Type    string                 `yaml:"step_type"`
	Details map[string]interface{} `yaml:"details"`
}

// DetectionRule represents a rule for detecting specific technologies or objects
type DetectionRule struct {
	RuleName            string            `yaml:"rule_name"`
	ObjectName          string            `yaml:"object_name"`
	ObjectVersion       string            `yaml:"object_version,omitempty"`
	HTTPHeaderFields    []HTTPHeaderField `yaml:"http_header_fields,omitempty"`
	PageContentPatterns []string          `yaml:"page_content_patterns,omitempty"`
	URLMicroSignatures  []string          `yaml:"url_micro_signatures,omitempty"`
	MetaTags            []MetaTag         `yaml:"meta_tags,omitempty"`
}

// HTTPHeaderField represents a pattern for matching HTTP header fields
type HTTPHeaderField struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

// MetaTag represents a pattern for matching HTML meta tags
type MetaTag struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

// CrawlingRule represents a crawling rule for URL fuzzing and form handling
type CrawlingRule struct {
	RuleName          string             `yaml:"rule_name"`
	RequestType       string             `yaml:"request_type"`
	TargetElements    []TargetElement    `yaml:"target_elements"`
	FuzzingParameters []FuzzingParameter `yaml:"fuzzing_parameters"`
}

// TargetElement represents a target element specified in a crawling rule
type TargetElement struct {
	SelectorType string `yaml:"selector_type"`
	Selector     string `yaml:"selector"`
}

// FuzzingParameter represents a parameter to be fuzzed as specified in a crawling rule
type FuzzingParameter struct {
	ParameterName string   `yaml:"parameter_name"`
	FuzzingType   string   `yaml:"fuzzing_type"`
	Selector      string   `yaml:"selector"`
	Values        []string `yaml:"values,omitempty"`
	Pattern       string   `yaml:"pattern,omitempty"`
}

// EnvironmentSettings represents the environment settings for the rule group
type EnvironmentSettings struct {
	HeadlessMode         bool              `yaml:"headless_mode"`
	CustomBrowserOptions map[string]string `yaml:"custom_browser_options"`
}

// LoggingConfiguration represents the logging configuration for the rule group
type LoggingConfiguration struct {
	LogLevel string `yaml:"log_level"`
	LogFile  string `yaml:"log_file,omitempty"`
}

// ErrorHandling represents the error handling configuration for the action rule
type ErrorHandling struct {
	RetryCount int `yaml:"retry_count"`
	RetryDelay int `yaml:"retry_delay"`
}

// RuleParser defines an interface for parsing rules.
type RuleParser interface {
	ParseRules(schema *jsonschema.Schema, file string) ([]Ruleset, error)
}

// DefaultRuleParser is the default implementation of the RuleParser interface.
type DefaultRuleParser struct{}

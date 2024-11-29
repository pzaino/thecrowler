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
	"encoding/json"
	"sync"
	"time"

	"github.com/qri-io/jsonschema"

	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

// RuleEngine represents the top-level structure for the rule engine
type RuleEngine struct {
	Schema          *jsonschema.Schema   `json:"schema" yaml:"schema"`
	Rulesets        []Ruleset            `json:"rulesets" yaml:"rulesets"`
	DetectionConfig DetectionConfig      `json:"detection_config" yaml:"detection_config"`
	JSPlugins       plg.JSPluginRegister `json:"js_plugins" yaml:"js_plugins"`

	// Not available in the YAML file (for internal use only)
	Cache Cache
}

// Cache represents the cache for the ruleset
type Cache struct {
	Mu               sync.RWMutex
	IsInvalid        bool
	RuleGroups       []*RuleGroup
	ActiveRuleGroups []*RuleGroup
	Scraping         []*ScrapingRule
	Action           []*ActionRule
	Detection        []*DetectionRule
	Crawling         []*CrawlingRule
}

// DetectionConfig represents the configuration for the detection engine
type DetectionConfig struct {
	NoiseThreshold    float32 `json:"noise_threshold" yaml:"noise_threshold"`
	MaybeThreshold    float32 `json:"maybe_threshold" yaml:"maybe_threshold"`
	DetectedThreshold float32 `json:"detected_threshold" yaml:"detected_threshold"`
}

// CustomTime wraps time.Time to provide custom parsing.
type CustomTime struct {
	time.Time
}

// Ruleset represents the top-level structure of the rules YAML file
type Ruleset struct {
	FormatVersion string      `json:"format_version" yaml:"format_version"`
	Author        string      `json:"author" yaml:"author"`
	CreatedAt     CustomTime  `json:"created_at" yaml:"created_at"`
	Description   string      `json:"description" yaml:"description"`
	Name          string      `json:"ruleset_name" yaml:"ruleset_name"`
	RuleGroups    []RuleGroup `json:"rule_groups" yaml:"rule_groups"`
}

// RuleGroup represents a group of rules
type RuleGroup struct {
	GroupName      string               `json:"group_name" yaml:"group_name"`
	ValidFrom      CustomTime           `json:"valid_from,omitempty" yaml:"valid_from,omitempty"`
	ValidTo        CustomTime           `json:"valid_to,omitempty" yaml:"valid_to,omitempty"`
	IsEnabled      bool                 `json:"is_enabled" yaml:"is_enabled"`
	URL            string               `json:"url" yaml:"url"`
	ScrapingRules  []ScrapingRule       `json:"scraping_rules,omitempty" yaml:"scraping_rules,omitempty"`
	ActionRules    []ActionRule         `json:"action_rules,omitempty" yaml:"action_rules,omitempty"`
	DetectionRules []DetectionRule      `json:"detection_rules,omitempty" yaml:"detection_rules,omitempty"`
	CrawlingRules  []CrawlingRule       `json:"crawling_rules,omitempty" yaml:"crawling_rules,omitempty"`
	PostProcessing []PostProcessingStep `json:"post_processing" yaml:"post_processing"`
	Env            []EnvSetting         `json:"environment_settings,omitempty" yaml:"environment_settings,omitempty"`
	LoggingConf    LoggingConfiguration `json:"logging_configuration,omitempty" yaml:"logging_configuration,omitempty"`
}

// EnvSetting represents the environment settings for the ruleset
type EnvSetting struct {
	Key        string        `json:"key" yaml:"key"`
	Values     interface{}   `json:"values" yaml:"values"`
	Properties EnvProperties `json:"properties" yaml:"properties"`
}

// EnvProperties represents the properties for the environment settings
type EnvProperties struct {
	Persistent   bool   `json:"persistent" yaml:"persistent"`
	Static       bool   `json:"static" yaml:"static"`
	SessionValid bool   `json:"session_valid" yaml:"session_valid"`
	Type         string `json:"type" yaml:"type"`
	Source       string `json:"source" yaml:"source"`
}

// UnmarshalJSON implements custom unmarshaling logic for EnvSetting
func (e *EnvSetting) UnmarshalJSON(data []byte) error {
	type Alias EnvSetting
	aux := &struct {
		Values json.RawMessage `json:"values"` // Read Values as raw JSON first
		*Alias
	}{
		Alias: (*Alias)(e),
	}

	// Unmarshal the raw data
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Now handle the "values" field, which can be multiple types
	var value interface{}
	if err := json.Unmarshal(aux.Values, &value); err != nil {
		return err
	}

	// Detect and process the type of "values"
	switch v := value.(type) {
	case string:
		e.Values = v
		e.Properties.Type = "string"
	case float64:
		e.Values = v
		e.Properties.Type = "number"
	case bool:
		e.Values = v
		e.Properties.Type = "boolean"
	case nil:
		e.Values = v
		e.Properties.Type = "null"
	case []interface{}:
		e.Values = processArray(v, e)
	default:
		e.Values = nil
		e.Properties.Type = "unknown"
	}

	return nil
}

// Helper function to handle array processing and set the type in EnvProperties
func processArray(arr []interface{}, e *EnvSetting) interface{} {
	if len(arr) == 0 {
		e.Properties.Type = "array"
		return arr
	}

	// Check the type of the first element to guess the array type
	switch arr[0].(type) {
	case string:
		e.Properties.Type = "[]string"
		var stringArray []string
		for _, elem := range arr {
			stringArray = append(stringArray, elem.(string))
		}
		return stringArray
	case float64:
		e.Properties.Type = "[]float64"
		var numberArray []float64
		for _, elem := range arr {
			numberArray = append(numberArray, elem.(float64))
		}
		return numberArray
	case bool:
		e.Properties.Type = "[]bool"
		var boolArray []bool
		for _, elem := range arr {
			boolArray = append(boolArray, elem.(bool))
		}
		return boolArray
	default:
		e.Properties.Type = "[]unknown"
		return arr
	}
}

// MarshalJSON is a custom MarshalJSON to ensure the correct format when marshaling the "values" field
func (e *EnvSetting) MarshalJSON() ([]byte, error) {
	type Alias EnvSetting
	aux := &struct {
		Values interface{} `json:"values"`
		*Alias
	}{
		Alias:  (*Alias)(e),
		Values: e.Values, // Directly assign without reflection
	}
	return json.Marshal(aux)
}

// PreCondition represents a pre-condition for a scraping rule
type PreCondition struct {
	URL  string `json:"url" yaml:"url"`
	Path string `json:"path" yaml:"path"`
}

// ScrapingRule represents a scraping rule
type ScrapingRule struct {
	RuleName          string                 `json:"rule_name" yaml:"rule_name"`
	PreConditions     []PreCondition         `json:"pre_conditions,omitempty" yaml:"pre_conditions,omitempty"`
	Conditions        map[string]interface{} `json:"conditions" yaml:"conditions"`
	WaitConditions    []WaitCondition        `json:"wait_conditions" yaml:"wait_conditions"`
	Elements          []Element              `json:"elements" yaml:"elements"`
	JsFiles           bool                   `json:"js_files" yaml:"js_files"`
	JSONFieldMappings map[string]string      `json:"json_field_mappings" yaml:"json_field_mappings"`
	PostProcessing    []PostProcessingStep   `json:"post_processing" yaml:"post_processing"`
}

// ActionRule represents an action rule
type ActionRule struct {
	RuleName       string                 `json:"rule_name" yaml:"rule_name"`
	ActionType     string                 `json:"action_type" yaml:"action_type"`
	Selectors      []Selector             `json:"selectors" yaml:"selectors"`
	Value          string                 `json:"value,omitempty" yaml:"value,omitempty"`
	URL            string                 `json:"url,omitempty" yaml:"url,omitempty"`
	WaitConditions []WaitCondition        `json:"wait_conditions" yaml:"wait_conditions"`
	Conditions     map[string]interface{} `json:"conditions" yaml:"conditions"`
	PostProcessing []PostProcessingStep   `json:"post_processing" yaml:"post_processing"`
	ErrorHandling  ErrorHandling          `json:"error_handling" yaml:"error_handling"`
}

// Element represents a single element to be scraped
type Element struct {
	Key       string     `json:"key" yaml:"key"`
	Selectors []Selector `json:"selectors" yaml:"selectors"`
	Critical  bool       `json:"critical" yaml:"critical"`
}

// Selector represents a single selector
type Selector struct {
	SelectorType string `json:"selector_type" yaml:"selector_type"`
	Selector     string `json:"selector" yaml:"selector"`
	Attribute    struct {
		Name  string `json:"name" yaml:"name"`
		Value string `json:"value" yaml:"value"`
	} `json:"attribute,omitempty" yaml:"attribute,omitempty"`
	Value                 string        `json:"value,omitempty" yaml:"value,omitempty"`
	Extract               ItemToExtract `json:"extract,omitempty" yaml:"extract,omitempty"`
	ExtractAllOccurrences bool          `json:"extract_all_occurrences" yaml:"extract_all_occurrences"`
	// Not available in the YAML file (for internal use only)
	ResolvedValue string
}

// ItemToExtract represents the item to extract from the selector
type ItemToExtract struct {
	Type    string `json:"type" yaml:"type"`
	Pattern string `json:"pattern" yaml:"pattern"`
}

// WaitCondition represents a single wait condition
type WaitCondition struct {
	ConditionType string   `json:"condition_type" yaml:"condition_type"`
	Selector      Selector `json:"selector,omitempty" yaml:"selector,omitempty"`
	CustomJS      string   `json:"custom_js,omitempty" yaml:"custom_js,omitempty"`
	Value         string   `json:"value,omitempty" yaml:"value,omitempty"`
}

// PostProcessingStep represents a single post-processing step
type PostProcessingStep struct {
	Type    string                 `json:"step_type" yaml:"step_type"`
	Details map[string]interface{} `json:"details" yaml:"details"`
}

// DetectionRule represents a rule for detecting specific technologies or objects
type DetectionRule struct {
	RuleName            string                 `yaml:"rule_name"`
	ObjectName          string                 `yaml:"object_name"`
	HTTPHeaderFields    []HTTPHeaderField      `yaml:"http_header_fields,omitempty"`
	PageContentPatterns []PageContentSignature `yaml:"page_content_patterns,omitempty"`
	SSLSignatures       []SSLSignature         `yaml:"ssl_patterns,omitempty"`
	URLMicroSignatures  []URLMicroSignature    `yaml:"url_micro_signatures,omitempty"`
	MetaTags            []MetaTag              `yaml:"meta_tags,omitempty"`
	Implies             []string               `yaml:"implies,omitempty"`
	PluginCalls         []PluginCall           `yaml:"plugin_calls,omitempty"`
	ExternalDetections  []ExternalDetection    `yaml:"external_detection,omitempty"`
}

// PluginCall represents a call to a plugin
type PluginCall struct {
	PluginName string         `yaml:"plugin_name"`
	PluginArgs []PluginParams `yaml:"plugin_args"`
}

// PluginParams represents the parameters for a plugin call
type PluginParams struct {
	ArgName    string                 `yaml:"parameter_name"`
	ArgValue   interface{}            `yaml:"parameter_value"`
	Properties PluginParamsProperties `yaml:"properties"`
}

// PluginParamsProperties represents the properties for the plugin parameters
type PluginParamsProperties struct {
	Type string `yaml:"type"`
}

// UnmarshalJSON implements custom unmarshaling logic for EnvSetting
func (e *PluginParams) UnmarshalJSON(data []byte) error {
	type Alias PluginParams
	aux := &struct {
		ArgValue json.RawMessage `json:"parameter_value"` // Read Values as raw JSON first
		*Alias
	}{
		Alias: (*Alias)(e),
	}

	// Unmarshal the raw data
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Now handle the "values" field, which can be multiple types
	var value interface{}
	if err := json.Unmarshal(aux.ArgValue, &value); err != nil {
		return err
	}

	// Detect and process the type of "values"
	switch v := value.(type) {
	case string:
		e.ArgValue = v
		e.Properties.Type = "string"
	case float64:
		e.ArgValue = v
		e.Properties.Type = "number"
	case bool:
		e.ArgValue = v
		e.Properties.Type = "boolean"
	case nil:
		e.ArgValue = v
		e.Properties.Type = "null"
	case []interface{}:
		e.ArgValue = processPlgArgArray(v, e)
	default:
		e.ArgValue = nil
		e.Properties.Type = "unknown"
	}

	return nil
}

// Helper function to handle array processing and set the type in PluginParamsProperties
func processPlgArgArray(arr []interface{}, e *PluginParams) interface{} {
	if len(arr) == 0 {
		e.Properties.Type = "array"
		return arr
	}

	// Check the type of the first element to guess the array type
	switch arr[0].(type) {
	case string:
		e.Properties.Type = "[]string"
		var stringArray []string
		for _, elem := range arr {
			stringArray = append(stringArray, elem.(string))
		}
		return stringArray
	case float64:
		e.Properties.Type = "[]float64"
		var numberArray []float64
		for _, elem := range arr {
			numberArray = append(numberArray, elem.(float64))
		}
		return numberArray
	case bool:
		e.Properties.Type = "[]bool"
		var boolArray []bool
		for _, elem := range arr {
			boolArray = append(boolArray, elem.(bool))
		}
		return boolArray
	default:
		e.Properties.Type = "[]unknown"
		return arr
	}
}

// MarshalJSON is a custom MarshalJSON to ensure the correct format when marshaling the "parameter_value" field
func (e *PluginParams) MarshalJSON() ([]byte, error) {
	type Alias PluginParams
	aux := &struct {
		ArgValue interface{} `json:"parameter_value"`
		*Alias
	}{
		Alias:    (*Alias)(e),
		ArgValue: e.ArgValue, // Directly assign without reflection
	}

	return json.Marshal(aux)
}

// ExternalDetection represents a call to an external detection service
type ExternalDetection struct {
	Provider string `yaml:"provider"`
}

// HTTPHeaderField represents a pattern for matching HTTP header fields
type HTTPHeaderField struct {
	Key        string   `yaml:"key"`
	Value      []string `yaml:"value,omitempty"`
	Confidence float32  `yaml:"confidence"`
}

// SSLSignature represents a pattern for matching SSL Certificate fields
type SSLSignature struct {
	Key        string   `yaml:"key"`
	Value      []string `yaml:"value,omitempty"`
	Confidence float32  `yaml:"confidence"`
}

// URLMicroSignature represents a pattern for matching URL micro-signatures
type URLMicroSignature struct {
	Signature  string  `yaml:"value"`
	Confidence float32 `yaml:"confidence"`
}

// PageContentSignature micro-signatures are patterns that can be found in the page content
type PageContentSignature struct {
	Key        string   `yaml:"key"`
	Attribute  string   `yaml:"attribute,omitempty"`
	Signature  []string `yaml:"value,omitempty"`
	Text       []string `yaml:"text,omitempty"`
	Confidence float32  `yaml:"confidence"`
}

// MetaTag represents a pattern for matching HTML meta tags
type MetaTag struct {
	Name       string  `yaml:"name"`
	Content    string  `yaml:"content"`
	Confidence float32 `yaml:"confidence"`
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
	Ignore     bool `yaml:"ignore"`
	RetryCount int  `yaml:"retry_count"`
	RetryDelay int  `yaml:"retry_delay"`
}

// RuleParser defines an interface for parsing rules.
type RuleParser interface {
	ParseRules(schema *jsonschema.Schema, file string) ([]Ruleset, error)
}

// DefaultRuleParser is the default implementation of the RuleParser interface.
type DefaultRuleParser struct{}

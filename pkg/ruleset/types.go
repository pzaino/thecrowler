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
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/qri-io/jsonschema"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

const (
	varTypeStr     = "string"
	varTypeNum     = "number"
	varTypeBool    = "boolean"
	varTypeNull    = "null"
	varTypeArr     = "array"
	varTypeUnknown = "unknown"
	arrTypeStr     = "[]string"
	arrTypeBool    = "[]bool"
	arrTypeFloat64 = "[]float64"
	arrTypeUnknown = "[]unknown"
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

// Ruleset represents the top-level structure of the rules YAML file
type Ruleset struct {
	FormatVersion string         `json:"format_version" yaml:"format_version"`
	Author        string         `json:"author" yaml:"author"`
	CreatedAt     cmn.CustomTime `json:"created_at" yaml:"created_at"`
	Description   string         `json:"description" yaml:"description"`
	Name          string         `json:"ruleset_name" yaml:"ruleset_name"`
	RuleGroups    []RuleGroup    `json:"rule_groups" yaml:"rule_groups"`
}

// RuleGroup represents a group of rules
type RuleGroup struct {
	GroupName      string               `json:"group_name" yaml:"group_name"`
	ValidFrom      cmn.CustomTime       `json:"valid_from,omitempty" yaml:"valid_from,omitempty"`
	ValidTo        cmn.CustomTime       `json:"valid_to,omitempty" yaml:"valid_to,omitempty"`
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
	Shared       bool   `json:"shared" yaml:"shared"`
	Type         string `json:"type" yaml:"type"`
	Source       string `json:"source" yaml:"source"`
}

// UnmarshalJSON accepts values (the canonical spelling) and the legacy value
// alias. Values are decoded without coercing every JSON number to float64.
func (e *EnvSetting) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	canonical, hasCanonical := fields["values"]
	legacy, hasLegacy := fields["value"]
	if hasCanonical && hasLegacy {
		return errors.New("environment setting cannot contain both values and value")
	}
	delete(fields, "values")
	delete(fields, "value")
	type plain EnvSetting
	remainder, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(remainder, (*plain)(e)); err != nil {
		return err
	}
	if hasLegacy {
		canonical = legacy
	}
	if hasCanonical || hasLegacy {
		value, err := decodeJSONValue(canonical)
		if err != nil {
			return err
		}
		e.Values = value
		e.Properties.Type = environmentValueType(value)
	}
	return nil
}

func decodeJSONValue(data []byte) (interface{}, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid trailing JSON data")
	}
	return normalizeEnvironmentValue(value)
}

func normalizeEnvironmentValue(value interface{}) (interface{}, error) {
	switch value := value.(type) {
	case json.Number:
		if integer, err := strconv.ParseInt(string(value), 10, 64); err == nil {
			return integer, nil
		}
		float, err := strconv.ParseFloat(string(value), 64)
		if err != nil {
			return nil, err
		}
		return float, nil
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		if uint64(value) > uint64(^uint64(0)>>1) {
			return nil, errors.New("environment integer exceeds int64")
		}
		return int64(value), nil
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return nil, errors.New("environment integer exceeds int64")
		}
		return int64(value), nil
	case []interface{}:
		for i := range value {
			normalized, err := normalizeEnvironmentValue(value[i])
			if err != nil {
				return nil, err
			}
			value[i] = normalized
		}
		return value, nil
	case map[string]interface{}:
		for key, item := range value {
			normalized, err := normalizeEnvironmentValue(item)
			if err != nil {
				return nil, err
			}
			value[key] = normalized
		}
		return value, nil
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(value))
		for rawKey, item := range value {
			key, ok := rawKey.(string)
			if !ok {
				return nil, errors.New("environment object keys must be strings")
			}
			normalized, err := normalizeEnvironmentValue(item)
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	default:
		return value, nil
	}
}

func environmentValueType(value interface{}) string {
	switch value.(type) {
	case string:
		return varTypeStr
	case int64, float64:
		return varTypeNum
	case bool:
		return varTypeBool
	case nil:
		return varTypeNull
	case []interface{}:
		return varTypeArr
	default:
		return varTypeUnknown
	}
}

// processArray remains for plugin-style typed parameters. Environment values
// deliberately do not use it, because heterogeneous arrays must remain intact.
func processArray(arr []interface{}, e *EnvSetting) interface{} {
	if len(arr) == 0 {
		e.Properties.Type = varTypeArr
		return arr
	}
	switch arr[0].(type) {
	case string:
		e.Properties.Type = arrTypeStr
		result := make([]string, len(arr))
		for i := range arr {
			result[i] = arr[i].(string)
		}
		return result
	case float64:
		e.Properties.Type = arrTypeFloat64
		result := make([]float64, len(arr))
		for i := range arr {
			result[i] = arr[i].(float64)
		}
		return result
	case bool:
		e.Properties.Type = arrTypeBool
		result := make([]bool, len(arr))
		for i := range arr {
			result[i] = arr[i].(bool)
		}
		return result
	default:
		e.Properties.Type = arrTypeUnknown
		return arr
	}
}

// UnmarshalYAML provides the same aliases, conflict handling, and value
// representation as UnmarshalJSON.
func (e *EnvSetting) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var fields map[string]interface{}
	if err := unmarshal(&fields); err != nil {
		return err
	}
	canonical, seenCanonical := fields["values"]
	legacy, seenLegacy := fields["value"]
	if seenCanonical && seenLegacy {
		return errors.New("environment setting cannot contain both values and value")
	}
	type plain EnvSetting
	var decoded plain
	if err := unmarshal(&decoded); err != nil {
		return err
	}
	*e = EnvSetting(decoded)
	if seenLegacy {
		canonical = legacy
	}
	if seenCanonical || seenLegacy {
		normalized, err := normalizeEnvironmentValue(canonical)
		if err != nil {
			return err
		}
		e.Values, e.Properties.Type = normalized, environmentValueType(normalized)
	}
	return nil
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
	RuleName          string               `json:"rule_name" yaml:"rule_name"`
	ObjectType        []string             `json:"object_type,omitempty" yaml:"object_type,omitempty"`
	Scope             string               `json:"scope" yaml:"scope"`
	PreConditions     []PreCondition       `json:"pre_conditions,omitempty" yaml:"pre_conditions,omitempty"`
	Conditions        ScrapingConditions   `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	WaitConditions    []WaitCondition      `json:"wait_conditions" yaml:"wait_conditions"`
	Elements          []Element            `json:"elements" yaml:"elements"`
	ExtractScripts    bool                 `json:"extract_scripts,omitempty" yaml:"extract_scripts,omitempty"`
	JSONFieldMappings map[string]string    `json:"json_field_mappings,omitempty" yaml:"json_field_mappings,omitempty"`
	PostProcessing    []PostProcessingStep `json:"post_processing" yaml:"post_processing"`
}

// ScrapingConditions is the set of page conditions implemented by the crawler.
// All populated conditions must match. Env remains open because environment
// matching supports scalar and structured values.
type ScrapingConditions struct {
	Element  string      `json:"element,omitempty" yaml:"element,omitempty"`
	Language string      `json:"language,omitempty" yaml:"language,omitempty"`
	Env      interface{} `json:"env,omitempty" yaml:"env,omitempty"`
}

type legacyFieldMappings map[string]string

func (m *legacyFieldMappings) UnmarshalJSON(data []byte) error {
	var direct map[string]string
	if json.Unmarshal(data, &direct) == nil {
		*m = direct
		return nil
	}
	var pairs []struct {
		Source string `json:"source_tag"`
		Dest   string `json:"dest_tag"`
	}
	if err := json.Unmarshal(data, &pairs); err != nil {
		return errors.New("json_field_rename must be a mapping or source_tag/dest_tag array")
	}
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		result[pair.Source] = pair.Dest
	}
	*m = result
	return nil
}

func (m *legacyFieldMappings) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var direct map[string]string
	if unmarshal(&direct) == nil {
		*m = direct
		return nil
	}
	var pairs []struct {
		Source string `yaml:"source_tag"`
		Dest   string `yaml:"dest_tag"`
	}
	if err := unmarshal(&pairs); err != nil {
		return errors.New("json_field_rename must be a mapping or source_tag/dest_tag array")
	}
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		result[pair.Source] = pair.Dest
	}
	*m = result
	return nil
}

// UnmarshalJSON accepts the former public spellings while retaining one
// canonical in-memory representation. Supplying both spellings is ambiguous.
func (r *ScrapingRule) UnmarshalJSON(data []byte) error {
	type plain ScrapingRule
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, a := raw["extract_scripts"]; a {
		if _, b := raw["js_files"]; b {
			return errors.New("scraping rule specifies both extract_scripts and js_files")
		}
	}
	if _, a := raw["json_field_mappings"]; a {
		if _, b := raw["json_field_rename"]; b {
			return errors.New("scraping rule specifies both json_field_mappings and json_field_rename")
		}
	}
	var aux struct {
		*plain
		JSFiles         *bool               `json:"js_files"`
		JSONFieldRename legacyFieldMappings `json:"json_field_rename"`
	}
	aux.plain = (*plain)(r)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.JSFiles != nil {
		r.ExtractScripts = *aux.JSFiles
	}
	if aux.JSONFieldRename != nil {
		r.JSONFieldMappings = aux.JSONFieldRename
	}
	return nil
}

// UnmarshalYAML provides the same alias and conflict behavior as JSON.
func (r *ScrapingRule) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain ScrapingRule
	var raw map[string]interface{}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	if _, a := raw["extract_scripts"]; a {
		if _, b := raw["js_files"]; b {
			return errors.New("scraping rule specifies both extract_scripts and js_files")
		}
	}
	if _, a := raw["json_field_mappings"]; a {
		if _, b := raw["json_field_rename"]; b {
			return errors.New("scraping rule specifies both json_field_mappings and json_field_rename")
		}
	}
	var aux struct {
		Plain           plain               `yaml:",inline"`
		JSFiles         *bool               `yaml:"js_files"`
		JSONFieldRename legacyFieldMappings `yaml:"json_field_rename"`
	}
	aux.Plain = plain(*r)
	if err := unmarshal(&aux); err != nil {
		return err
	}
	*r = ScrapingRule(aux.Plain)
	if aux.JSFiles != nil {
		r.ExtractScripts = *aux.JSFiles
	}
	if aux.JSONFieldRename != nil {
		r.JSONFieldMappings = aux.JSONFieldRename
	}
	return nil
}

// ActionRule represents an action rule
type ActionRule struct {
	RuleName        string               `json:"rule_name" yaml:"rule_name"`
	ObjectType      []string             `json:"object_type,omitempty" yaml:"object_type,omitempty"`
	Scope           string               `json:"scope" yaml:"scope"`
	ActionType      string               `json:"action_type" yaml:"action_type"`
	Selectors       []Selector           `json:"selectors" yaml:"selectors"`
	TargetSelectors []Selector           `json:"target_selectors,omitempty" yaml:"target_selectors,omitempty"`
	StoreAs         string               `json:"store_as,omitempty" yaml:"store_as,omitempty"`
	Value           string               `json:"value,omitempty" yaml:"value,omitempty"`
	URL             string               `json:"url,omitempty" yaml:"url,omitempty"`
	WaitConditions  []WaitCondition      `json:"wait_conditions" yaml:"wait_conditions"`
	Conditions      *ActionCondition     `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	PostProcessing  []PostProcessingStep `json:"post_processing" yaml:"post_processing"`
	ErrorHandling   ErrorHandling        `json:"error_handling" yaml:"error_handling"`
}

// ActionCondition is a closed, discriminated action condition.
type ActionCondition struct {
	Type       string `json:"type" yaml:"type"`
	Selector   string `json:"selector,omitempty" yaml:"selector,omitempty"`
	Language   string `json:"language,omitempty" yaml:"language,omitempty"`
	PluginCall string `json:"plugin_call,omitempty" yaml:"plugin_call,omitempty"`
}

// ParseActionCondition converts a decoded canonical or legacy condition into
// its validated typed representation.
func ParseActionCondition(values map[string]interface{}) (*ActionCondition, error) {
	if len(values) == 0 {
		return nil, nil
	}
	typeValue, canonical := values["type"]
	legacyKeys := []string{"element", "language", "plugin_call", "agent_call"}
	var legacy string
	for _, key := range legacyKeys {
		if _, ok := values[key]; ok {
			if legacy != "" {
				return nil, fmt.Errorf("action condition has multiple legacy discriminators")
			}
			legacy = key
		}
	}
	if canonical && legacy != "" {
		return nil, fmt.Errorf("action condition mixes canonical and legacy forms")
	}
	condition := &ActionCondition{}
	if canonical {
		condition.Type, _ = typeValue.(string)
		condition.Type = strings.ToLower(strings.TrimSpace(condition.Type))
		switch condition.Type {
		case "element":
			condition.Selector, _ = values["selector"].(string)
		case "language":
			condition.Language, _ = values["language"].(string)
		case "plugin_call":
			condition.PluginCall, _ = values["plugin_call"].(string)
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("canonical action condition must contain only type and selector")
		}
	} else {
		condition.Type = legacy
		switch legacy {
		case "element", "language":
			value, _ := values[legacy].(string)
			if legacy == "element" {
				condition.Selector = value
			} else {
				condition.Language = value
			}
			if len(values) != 1 {
				return nil, fmt.Errorf("legacy %s condition contains unknown fields", legacy)
			}
		case "plugin_call":
			condition.PluginCall, _ = values["selector"].(string)
			if condition.PluginCall == "" {
				condition.PluginCall, _ = values[legacy].(string)
			}
			if len(values) > 2 {
				return nil, fmt.Errorf("legacy plugin_call condition contains unknown fields")
			}
		default:
			return nil, fmt.Errorf("action condition requires a type discriminator")
		}
	}
	condition.Type = strings.ToLower(strings.TrimSpace(condition.Type))
	condition.Selector = strings.TrimSpace(condition.Selector)
	condition.Language = strings.TrimSpace(condition.Language)
	condition.PluginCall = strings.TrimSpace(condition.PluginCall)
	if condition.Type != "element" && condition.Type != "language" && condition.Type != "plugin_call" {
		return nil, fmt.Errorf("unknown action condition type %q", condition.Type)
	}
	required := map[string]string{"element": condition.Selector, "language": condition.Language, "plugin_call": condition.PluginCall}
	if required[condition.Type] == "" {
		return nil, fmt.Errorf("action condition %q is incomplete", condition.Type)
	}
	return condition, nil
}

// UnmarshalJSON accepts both the canonical discriminated object and checked-in
// legacy key-shaped objects.
func (c *ActionCondition) UnmarshalJSON(data []byte) error {
	var values map[string]interface{}
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	condition, err := ParseActionCondition(values)
	if err != nil {
		return err
	}
	if condition == nil {
		return fmt.Errorf("action condition cannot be empty")
	}
	*c = *condition
	return nil
}

// UnmarshalYAML provides the same normalization for YAML rule files.
func (c *ActionCondition) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var values map[string]interface{}
	if err := unmarshal(&values); err != nil {
		return err
	}
	condition, err := ParseActionCondition(values)
	if err != nil {
		return err
	}
	if condition == nil {
		return fmt.Errorf("action condition cannot be empty")
	}
	*c = *condition
	return nil
}

// Element represents a single element to be scraped
type Element struct {
	Key                 string     `json:"key" yaml:"key"`
	Selectors           []Selector `json:"selectors" yaml:"selectors"`
	Critical            bool       `json:"critical" yaml:"critical"`
	TransformHTMLToJSON bool       `json:"transform_html_to_json" yaml:"transform_html_to_json"`
}

// Selector represents a single selector
type Selector struct {
	SelectorType string                 `json:"selector_type" yaml:"selector_type"`
	Selector     string                 `json:"selector" yaml:"selector"`
	Details      map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
	AgentCall    *AgentCall             `json:"agent_call,omitempty" yaml:"agent_call,omitempty"`
	SelectorAttr []SelectorAttribute    `json:"selector_attributes,omitempty" yaml:"selector_attributes,omitempty"`
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

// SelectorAttribute represents a single attribute of a selector
type SelectorAttribute struct {
	Name  string      `json:"name" yaml:"name"`
	Value interface{} `json:"value" yaml:"value"`
}

// ItemToExtract represents the item to extract from the selector
type ItemToExtract struct {
	Type    string `json:"type" yaml:"type"`
	Pattern string `json:"pattern" yaml:"pattern"`
}

// WaitConditionType is the discriminator for a browser wait condition.
type WaitConditionType string

const (
	WaitConditionElementPresence WaitConditionType = "element_presence"
	WaitConditionElementVisible  WaitConditionType = "element_visible"
	WaitConditionDelay           WaitConditionType = "delay"
	WaitConditionPluginCall      WaitConditionType = "plugin_call"
)

// WaitCondition represents a single wait condition. Selector is used only by
// element conditions, Value only by delay, and Plugin only by plugin_call.
type WaitCondition struct {
	ConditionType WaitConditionType `json:"condition_type" yaml:"condition_type"`
	Selector      Selector          `json:"selector,omitempty" yaml:"selector,omitempty"`
	Value         string            `json:"value,omitempty" yaml:"value,omitempty"`
	Plugin        string            `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	AgentCall     *AgentCall        `json:"agent_call,omitempty" yaml:"agent_call,omitempty"`
}

// PostProcessingStep represents a single post-processing step
type PostProcessingStep struct {
	Type      string                 `json:"step_type" yaml:"step_type"`
	Details   map[string]interface{} `json:"details" yaml:"details"`
	AgentCall *AgentCall             `json:"agent_call,omitempty" yaml:"agent_call,omitempty"`
}

// DetectionRule represents a rule for detecting specific technologies or objects
type DetectionRule struct {
	RuleName            string                 `json:"rule_name" yaml:"rule_name"`
	ObjectType          []string               `json:"object_type,omitempty" yaml:"object_type,omitempty"`
	Scope               string                 `json:"scope" yaml:"scope"`
	ObjectName          string                 `json:"object_name" yaml:"object_name"`
	HTTPHeaderFields    []HTTPHeaderField      `json:"http_header_fields,omitempty" yaml:"http_header_fields,omitempty"`
	PageContentPatterns []PageContentSignature `json:"page_content_patterns,omitempty" yaml:"page_content_patterns,omitempty"`
	SSLSignatures       []SSLSignature         `json:"ssl_patterns,omitempty" yaml:"ssl_patterns,omitempty"`
	URLMicroSignatures  []URLMicroSignature    `json:"url_micro_signatures,omitempty" yaml:"url_micro_signatures,omitempty"`
	MetaTags            []MetaTag              `json:"meta_tags,omitempty" yaml:"meta_tags,omitempty"`
	Implies             []string               `json:"implies,omitempty" yaml:"implies,omitempty"`
	PluginCalls         []PluginCall           `json:"plugin_calls,omitempty" yaml:"plugin_calls,omitempty"`
	AgentCalls          []AgentCall            `json:"agent_calls,omitempty" yaml:"agent_calls,omitempty"`
	ExternalDetections  []ExternalDetection    `json:"external_detection,omitempty" yaml:"external_detection,omitempty"`
}

// UnmarshalJSON accepts the old certificates_patterns spelling while always
// normalizing it into SSLSignatures. Supplying both names is ambiguous.
func (r *DetectionRule) UnmarshalJSON(data []byte) error {
	type plain DetectionRule
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, canonical := fields["ssl_patterns"]; canonical {
		if _, legacy := fields["certificates_patterns"]; legacy {
			return errors.New("ssl_patterns and certificates_patterns cannot both be specified")
		}
	}
	var aux struct {
		*plain
		Legacy []SSLSignature `json:"certificates_patterns"`
	}
	aux.plain = (*plain)(r)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if _, ok := fields["certificates_patterns"]; ok {
		r.SSLSignatures = aux.Legacy
	}
	return nil
}

// UnmarshalYAML is the YAML equivalent of UnmarshalJSON.
func (r *DetectionRule) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain DetectionRule
	var fields map[string]interface{}
	if err := unmarshal(&fields); err != nil {
		return err
	}
	if _, canonical := fields["ssl_patterns"]; canonical {
		if _, legacy := fields["certificates_patterns"]; legacy {
			return errors.New("ssl_patterns and certificates_patterns cannot both be specified")
		}
	}
	var aux struct {
		plain  `yaml:",inline"`
		Legacy []SSLSignature `yaml:"certificates_patterns"`
	}
	if err := unmarshal(&aux); err != nil {
		return err
	}
	*r = DetectionRule(aux.plain)
	if _, ok := fields["certificates_patterns"]; ok {
		r.SSLSignatures = aux.Legacy
	}
	return nil
}

// PluginCall represents a call to a plugin
type PluginCall struct {
	PluginName string         `json:"plugin_name" yaml:"plugin_name"`
	PluginArgs []PluginParams `json:"plugin_args" yaml:"plugin_args"`
}

func (p *PluginCall) UnmarshalJSON(data []byte) error {
	type plain PluginCall
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, canonical := fields["plugin_args"]; canonical {
		if _, legacy := fields["plugin_parameters"]; legacy {
			return errors.New("plugin_args and plugin_parameters cannot both be specified")
		}
	}
	var aux struct {
		*plain
		Legacy []PluginParams `json:"plugin_parameters"`
	}
	aux.plain = (*plain)(p)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if _, ok := fields["plugin_parameters"]; ok {
		p.PluginArgs = aux.Legacy
	}
	return nil
}

func (p *PluginCall) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain PluginCall
	var fields map[string]interface{}
	if err := unmarshal(&fields); err != nil {
		return err
	}
	if _, canonical := fields["plugin_args"]; canonical {
		if _, legacy := fields["plugin_parameters"]; legacy {
			return errors.New("plugin_args and plugin_parameters cannot both be specified")
		}
	}
	var aux struct {
		plain  `yaml:",inline"`
		Legacy []PluginParams `yaml:"plugin_parameters"`
	}
	if err := unmarshal(&aux); err != nil {
		return err
	}
	*p = PluginCall(aux.plain)
	if _, ok := fields["plugin_parameters"]; ok {
		p.PluginArgs = aux.Legacy
	}
	return nil
}

// PluginParams represents the parameters for a plugin call
type PluginParams struct {
	ArgName    string                 `json:"parameter_name" yaml:"parameter_name"`
	ArgValue   interface{}            `json:"parameter_value" yaml:"parameter_value"`
	Properties PluginParamsProperties `json:"properties,omitempty" yaml:"properties,omitempty"`
}

// PluginParamsProperties represents the properties for the plugin parameters
type PluginParamsProperties struct {
	Type string `json:"type" yaml:"type"`
}

// UnmarshalJSON implements custom unmarshaling logic for PluginParams
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
		e.Properties.Type = varTypeStr
	case float64:
		e.ArgValue = v
		e.Properties.Type = varTypeNum
	case bool:
		e.ArgValue = v
		e.Properties.Type = varTypeBool
	case nil:
		e.ArgValue = v
		e.Properties.Type = varTypeNull
	case []interface{}:
		e.ArgValue = processPlgArgArray(v, e)
	default:
		e.ArgValue = nil
		e.Properties.Type = varTypeUnknown
	}

	return nil
}

// Helper function to handle array processing and set the type in PluginParamsProperties
func processPlgArgArray(arr []interface{}, e *PluginParams) interface{} {
	if len(arr) == 0 {
		e.Properties.Type = varTypeArr
		return arr
	}

	// Check the type of the first element to guess the array type
	switch arr[0].(type) {
	case string:
		e.Properties.Type = arrTypeStr
		var stringArray []string
		for _, elem := range arr {
			stringArray = append(stringArray, elem.(string))
		}
		return stringArray
	case float64:
		e.Properties.Type = arrTypeFloat64
		var numberArray []float64
		for _, elem := range arr {
			numberArray = append(numberArray, elem.(float64))
		}
		return numberArray
	case bool:
		e.Properties.Type = arrTypeBool
		var boolArray []bool
		for _, elem := range arr {
			boolArray = append(boolArray, elem.(bool))
		}
		return boolArray
	default:
		e.Properties.Type = arrTypeUnknown
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
	Name            string            `json:"name,omitempty" yaml:"name,omitempty"`
	Provider        string            `json:"provider" yaml:"provider"`
	DetectionParams []DetectionParams `json:"detection_params,omitempty" yaml:"detection_params,omitempty"`
}

// DetectionParams represents the parameters for an external detection service
type DetectionParams struct {
	ParamName  string      `json:"param_name" yaml:"param_name"`
	ParamValue interface{} `json:"param_value" yaml:"param_value"`
}

// HTTPHeaderField represents a pattern for matching HTTP header fields
type HTTPHeaderField struct {
	Key        string   `json:"key" yaml:"key"`
	Value      []string `json:"value,omitempty" yaml:"value,omitempty"`
	Confidence float32  `json:"confidence" yaml:"confidence"`
}

// SSLSignature represents a pattern for matching SSL Certificate fields
type SSLSignature struct {
	Key        string   `json:"key" yaml:"key"`
	Value      []string `json:"value,omitempty" yaml:"value,omitempty"`
	Confidence float32  `json:"confidence" yaml:"confidence"`
}

// URLMicroSignature represents a pattern for matching URL micro-signatures
type URLMicroSignature struct {
	Signature  string  `json:"value" yaml:"value"`
	Confidence float32 `json:"confidence" yaml:"confidence"`
}

// PageContentSignature micro-signatures are patterns that can be found in the page content
type PageContentSignature struct {
	Key        string   `json:"key" yaml:"key"`
	Attribute  string   `json:"attribute,omitempty" yaml:"attribute,omitempty"`
	Signature  []string `json:"value,omitempty" yaml:"value,omitempty"`
	Text       []string `json:"text,omitempty" yaml:"text,omitempty"`
	Confidence float32  `json:"confidence" yaml:"confidence"`
}

func (p *PageContentSignature) UnmarshalJSON(data []byte) error {
	type plain PageContentSignature
	var aux struct {
		*plain
		Text json.RawMessage `json:"text"`
	}
	aux.plain = (*plain)(p)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Text) == 0 || string(aux.Text) == "null" {
		return nil
	}
	if err := json.Unmarshal(aux.Text, &p.Text); err == nil {
		return nil
	}
	var legacy string
	if err := json.Unmarshal(aux.Text, &legacy); err != nil {
		return errors.New("page content text must be a string or an array of strings")
	}
	p.Text = []string{legacy}
	return nil
}

func (p *PageContentSignature) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Key        string      `yaml:"key"`
		Attribute  string      `yaml:"attribute"`
		Signature  []string    `yaml:"value"`
		Text       interface{} `yaml:"text"`
		Confidence float32     `yaml:"confidence"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	p.Key, p.Attribute, p.Signature, p.Confidence = raw.Key, raw.Attribute, raw.Signature, raw.Confidence
	switch value := raw.Text.(type) {
	case nil:
	case string:
		p.Text = []string{value}
	case []interface{}:
		p.Text = make([]string, len(value))
		for i, item := range value {
			s, ok := item.(string)
			if !ok {
				return errors.New("page content text entries must be strings")
			}
			p.Text[i] = s
		}
	default:
		return errors.New("page content text must be a string or an array of strings")
	}
	return nil
}

// MetaTag represents a pattern for matching HTML meta tags
type MetaTag struct {
	Name       string   `json:"key" yaml:"key"`
	Content    []string `json:"value" yaml:"value"`
	Confidence float32  `json:"confidence" yaml:"confidence"`
}

// CrawlingRule represents a crawling rule for URL fuzzing and form handling
type CrawlingRule struct {
	RuleName          string             `json:"rule_name" yaml:"rule_name"`
	ObjectType        []string           `json:"object_type,omitempty" yaml:"object_type,omitempty"`
	Scope             string             `json:"scope" yaml:"scope"`
	RequestType       string             `json:"request_type" yaml:"request_type"`
	TargetElements    []TargetElement    `json:"target_elements" yaml:"target_elements"`
	FuzzingParameters []FuzzingParameter `json:"fuzzing_parameters" yaml:"fuzzing_parameters"`
	Lifecycle         *CrawlingLifecycle `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
}

// TargetElement represents a target element specified in a crawling rule
type TargetElement struct {
	SelectorType string     `json:"selector_type" yaml:"selector_type"`
	Selector     string     `json:"selector" yaml:"selector"`
	AgentCall    *AgentCall `json:"agent_call,omitempty" yaml:"agent_call,omitempty"`
}

// FuzzingParameter represents a parameter to be fuzzed as specified in a crawling rule
type FuzzingParameter struct {
	ParameterName string   `json:"parameter_name" yaml:"parameter_name"`
	FuzzingType   string   `json:"fuzzing_type" yaml:"fuzzing_type"`
	Selector      string   `json:"selector" yaml:"selector"`
	Values        []string `json:"values,omitempty" yaml:"values,omitempty"`
	Pattern       string   `json:"pattern,omitempty" yaml:"pattern,omitempty"`
}

// EnvironmentSettings represents the environment settings for the rule group
type EnvironmentSettings struct {
	HeadlessMode         bool              `yaml:"headless_mode"`
	CustomBrowserOptions map[string]string `yaml:"custom_browser_options"`
}

// LoggingConfiguration represents the logging configuration for the rule group
type LoggingConfiguration struct {
	LogLevel   string `json:"log_level" yaml:"log_level"`
	LogMessage string `json:"log_message,omitempty" yaml:"log_message,omitempty"`
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

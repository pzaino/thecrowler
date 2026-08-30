package ruleset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

// RulesetValidationMode controls the compatibility policy used by validation.
type RulesetValidationMode uint8

const (
	// RulesetValidationStrict rejects deprecated aliases in addition to schema
	// and semantic violations.
	RulesetValidationStrict RulesetValidationMode = iota
	// RulesetValidationAllowLegacyAliases accepts a legacy alias when its
	// canonical counterpart is absent. Conflicting aliases are always invalid.
	RulesetValidationAllowLegacyAliases
)

// ValidateRulesetConfig is the single validation entry point for ruleset
// documents. It accepts JSON and YAML, validates their normalized JSON form,
// and performs constraints which Draft-07 validators do not enforce reliably.
func ValidateRulesetConfig(schema *jsonschema.Schema, data []byte, fileType string, modes ...RulesetValidationMode) error {
	if schema == nil {
		return fmt.Errorf("ruleset validation schema is nil")
	}
	mode := RulesetValidationStrict
	if len(modes) > 0 {
		mode = modes[0]
	}
	if len(modes) > 1 || mode > RulesetValidationAllowLegacyAliases {
		return fmt.Errorf("invalid ruleset validation mode")
	}

	var document interface{}
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), ".")) {
	case "", "yaml", "yml":
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("parse ruleset YAML: %w", err)
		}
		var normalizationViolations []string
		document, normalizationViolations = normalizeYAMLValue(document, "$", nil)
		if len(normalizationViolations) != 0 {
			return fmt.Errorf("normalize ruleset YAML: %s", strings.Join(normalizationViolations, "; "))
		}
	case "json":
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			return fmt.Errorf("parse ruleset JSON: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return fmt.Errorf("parse ruleset JSON: trailing data")
		}
	default:
		return fmt.Errorf("unsupported ruleset format %q", fileType)
	}
	if document == nil {
		return fmt.Errorf("parse ruleset: empty document")
	}

	jsonData, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal normalized ruleset JSON: %w", err)
	}
	issues, operationalErr := schema.ValidateBytes(context.Background(), jsonData)
	if operationalErr != nil {
		return fmt.Errorf("operate ruleset schema validator: %w", operationalErr)
	}
	if len(issues) != 0 {
		formatted := make([]string, 0, len(issues))
		for _, issue := range issues {
			path := issue.PropertyPath
			if path == "" {
				path = "$"
			}
			formatted = append(formatted, fmt.Sprintf("%s: %s", path, issue.Message))
		}
		return fmt.Errorf("ruleset schema violations: %s", strings.Join(formatted, "; "))
	}

	violations := semanticRulesetViolations(document, mode)
	if len(violations) != 0 {
		return fmt.Errorf("ruleset semantic violations: %s", strings.Join(violations, "; "))
	}
	return nil
}

// rulesetValidationJSON removes Go zero values that represent absent optional
// configuration fields. Ruleset's historical wire structs do not consistently
// use omitempty, so validating their direct JSON encoding would incorrectly
// turn absent slices, objects, and strings into explicit null/empty values.
func rulesetValidationJSON(ruleset Ruleset) ([]byte, error) {
	encoded, err := json.Marshal(ruleset)
	if err != nil {
		return nil, err
	}
	var document interface{}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	document = omitAbsentRuntimeValues(document)
	return json.Marshal(document)
}

func omitAbsentRuntimeValues(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if child == nil || child == "" {
				delete(typed, key)
				continue
			}
			typed[key] = omitAbsentRuntimeValues(child)
		}
	case []interface{}:
		for i := range typed {
			typed[i] = omitAbsentRuntimeValues(typed[i])
		}
	}
	return value
}

func normalizeYAMLValue(value interface{}, path string, violations []string) (interface{}, []string) {
	switch typed := value.(type) {
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			name, ok := key.(string)
			if !ok {
				violations = append(violations, fmt.Sprintf("%s: YAML object key %v is not a string", path, key))
				continue
			}
			result[name], violations = normalizeYAMLValue(child, path+"."+name, violations)
		}
		return result, violations
	case []interface{}:
		for i := range typed {
			typed[i], violations = normalizeYAMLValue(typed[i], fmt.Sprintf("%s[%d]", path, i), violations)
		}
	}
	return value, violations
}

var rulesetAliases = map[string]string{
	"js_files":              "extract_scripts",
	"json_field_rename":     "json_field_mappings",
	"certificates_patterns": "ssl_patterns",
	"plugin_parameters":     "plugin_args",
}

func semanticRulesetViolations(document interface{}, mode RulesetValidationMode) []string {
	var violations []string
	var visit func(interface{}, string)
	visit = func(value interface{}, path string) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for legacy, canonical := range rulesetAliases {
				_, hasLegacy := typed[legacy]
				_, hasCanonical := typed[canonical]
				if hasLegacy && hasCanonical {
					violations = append(violations, fmt.Sprintf("%s: conflicting aliases %q and %q", path, legacy, canonical))
				} else if hasLegacy && mode == RulesetValidationStrict {
					violations = append(violations, fmt.Sprintf("%s.%s: deprecated alias; use %q", path, legacy, canonical))
				}
			}
			// value is an alias only on environment-setting objects; elsewhere it
			// is a canonical and unrelated field (for example wait conditions).
			if strings.Contains(path, ".environment_settings[") {
				_, hasLegacy := typed["value"]
				_, hasCanonical := typed["values"]
				if hasLegacy && hasCanonical {
					violations = append(violations, fmt.Sprintf("%s: conflicting aliases %q and %q", path, "value", "values"))
				} else if hasLegacy && mode == RulesetValidationStrict {
					violations = append(violations, fmt.Sprintf("%s.value: deprecated alias; use %q", path, "values"))
				}
			}
			if fromText, fok := typed["valid_from"].(string); fok {
				if toText, tok := typed["valid_to"].(string); tok {
					from, fromErr := time.Parse(time.RFC3339, fromText)
					to, toErr := time.Parse(time.RFC3339, toText)
					if fromErr == nil && toErr == nil && from.After(to) {
						violations = append(violations, fmt.Sprintf("%s: valid_from must not be after valid_to", path))
					}
				}
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(typed[key], path+"."+key)
			}
		case []interface{}:
			for i, child := range typed {
				visit(child, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	visit(document, "$")
	return violations
}

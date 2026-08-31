package ruleset

import (
	"strings"
	"testing"

	"github.com/qri-io/jsonschema"
)

func validationTestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema := &jsonschema.Schema{}
	err := schema.UnmarshalJSON([]byte(`{
		"type":"object",
		"required":["format_version","ruleset_name"],
		"properties":{
			"format_version":{"type":"string","pattern":"^\\d+\\.\\d+\\.\\d+$"},
			"ruleset_name":{"type":"string"},
			"environment_settings":{"type":"array","items":{"type":"object"}}
		},
		"additionalProperties":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestValidateRulesetConfigJSONAndYAML(t *testing.T) {
	schema := validationTestSchema(t)
	valid := []struct{ format, document string }{
		{"json", `{"format_version":"1.0.0","ruleset_name":"valid"}`},
		{"yaml", "format_version: 1.0.0\nruleset_name: valid\n"},
	}
	for _, test := range valid {
		if err := ValidateRulesetConfig(schema, []byte(test.document), test.format); err != nil {
			t.Errorf("valid %s rejected: %v", test.format, err)
		}
	}
	for _, test := range []struct{ format, document string }{
		{"json", `{"format_version":"1.0","ruleset_name":"invalid"}`},
		{"yaml", "format_version: '1.0'\nruleset_name: invalid\n"},
	} {
		err := ValidateRulesetConfig(schema, []byte(test.document), test.format)
		if err == nil || !strings.Contains(err.Error(), "format_version") {
			t.Errorf("invalid %s did not report its instance path: %v", test.format, err)
		}
	}
}

func TestValidateRulesetConfigParseErrors(t *testing.T) {
	schema := validationTestSchema(t)
	for _, test := range []struct{ format, document string }{
		{"json", `{"format_version":`},
		{"yaml", "ruleset_name: [\n"},
	} {
		if err := ValidateRulesetConfig(schema, []byte(test.document), test.format); err == nil || !strings.Contains(err.Error(), "parse ruleset") {
			t.Errorf("expected useful %s parse error, got %v", test.format, err)
		}
	}
}

func TestValidateRulesetConfigAliasPolicyAndConflicts(t *testing.T) {
	schema := validationTestSchema(t)
	legacy := []byte(`{"format_version":"1.0.0","ruleset_name":"x","environment_settings":[{"value":"x"}]}`)
	if err := ValidateRulesetConfig(schema, legacy, "json"); err == nil || !strings.Contains(err.Error(), "deprecated alias") {
		t.Fatalf("strict mode accepted a legacy alias: %v", err)
	}
	if err := ValidateRulesetConfig(schema, legacy, "json", RulesetValidationAllowLegacyAliases); err != nil {
		t.Fatalf("explicit compatibility mode rejected legacy alias: %v", err)
	}
	conflict := []byte(`{"format_version":"1.0.0","ruleset_name":"x","environment_settings":[{"value":"x","values":"y"}]}`)
	if err := ValidateRulesetConfig(schema, conflict, "json", RulesetValidationAllowLegacyAliases); err == nil || !strings.Contains(err.Error(), "conflicting aliases") {
		t.Fatalf("compatibility mode accepted conflicting aliases: %v", err)
	}
}

func TestValidateRulesetConfigCrossFieldDateRange(t *testing.T) {
	schema := validationTestSchema(t)
	document := []byte(`{
		"format_version":"1.0.0",
		"ruleset_name":"x",
		"rule_groups":[{
			"valid_from":"2026-08-31T02:00:00+02:00",
			"valid_to":"2026-08-30T23:00:00Z"
		}]
	}`)
	err := ValidateRulesetConfig(schema, document, "json")
	if err == nil || !strings.Contains(err.Error(), "valid_from must not be after valid_to") {
		t.Fatalf("invalid date range was accepted: %v", err)
	}
}

// qri/jsonschema reports ordinary validation failures as a non-empty issue
// slice with a nil operational error. This guards against dropping that slice.
func TestValidateRulesetConfigQRIssuesWithNilOperationalError(t *testing.T) {
	err := ValidateRulesetConfig(validationTestSchema(t), []byte(`{"ruleset_name":"x"}`), "json")
	if err == nil || !strings.Contains(err.Error(), "schema violations") {
		t.Fatalf("qri validation issues were ignored: %v", err)
	}
}

func TestValidateRulesetConfigCompatibilityNormalizesHistoricalShapes(t *testing.T) {
	schema, _ := loadRulesetSchemaDocument(t)
	document := minimalRulesetFixture(t, "scraping_rules", map[string]interface{}{
		"rule_name": "legacy", "elements": []interface{}{map[string]interface{}{
			"key": "title", "selectors": []interface{}{map[string]interface{}{"selector_type": "css", "selector": "h1"}},
		}},
		"wait_conditions": []interface{}{map[string]interface{}{"condition_type": "element_visible", "selector": "h1"}},
	})
	if err := ValidateRulesetConfig(schema, document, "json", RulesetValidationAllowLegacyAliases); err != nil {
		t.Fatalf("compatibility mode rejected historical wait selector: %v", err)
	}
	if err := ValidateRulesetConfig(schema, document, "json"); err == nil {
		t.Fatal("strict mode accepted historical wait selector")
	}
}

func TestRulesetValidationJSONOmitsEmptyOptionalStructs(t *testing.T) {
	ruleset := Ruleset{FormatVersion: "1.0.5", Author: "test", Description: "test", Name: "test", RuleGroups: []RuleGroup{{
		GroupName: "test", IsEnabled: true, URL: "*", ScrapingRules: []ScrapingRule{{
			RuleName: "wait", Elements: []Element{{Key: "title", Selectors: []Selector{{SelectorType: "css", Selector: "h1"}}}},
			WaitConditions: []WaitCondition{{ConditionType: WaitConditionDelay, Value: "100ms"}},
		}},
	}}}
	data, err := rulesetValidationJSON(ruleset)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"selector":{}`) {
		t.Fatalf("empty optional selector was retained: %s", data)
	}
}

func TestValidateRulesetConfigCompatibilityPreservesCanonicalKeyedMetaTag(t *testing.T) {
	schema, _ := loadRulesetSchemaDocument(t)
	document := minimalRulesetFixture(t, "detection_rules", map[string]interface{}{
		"rule_name":   "canonical-meta",
		"object_name": "cms",
		"meta_tags": []interface{}{
			map[string]interface{}{
				"key":        "generator",
				"value":      []interface{}{"WordPress"},
				"confidence": 5,
			},
		},
	})

	if err := ValidateRulesetConfig(schema, document, "json"); err != nil {
		t.Fatalf("strict mode rejected canonical keyed meta tag: %v", err)
	}

	if err := ValidateRulesetConfig(
		schema,
		document,
		"json",
		RulesetValidationAllowLegacyAliases,
	); err != nil {
		t.Fatalf("compatibility mode rejected canonical keyed meta tag: %v", err)
	}
}

package ruleset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func sorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func TestCanonicalEnumParity(t *testing.T) {
	_, document := loadRulesetSchemaDocument(t)
	ruleProps := []string{"properties", "rule_groups", "items", "properties"}
	tests := []struct {
		name string
		path []string
		want []string
	}{
		{"waits", []string{"$defs", "waitCondition", "properties", "condition_type"}, []string{string(WaitConditionElementPresence), string(WaitConditionElementVisible), string(WaitConditionDelay), string(WaitConditionPluginCall)}},
		{"action post-processing", append(append([]string{}, ruleProps...), "action_rules", "items", "properties", "post_processing", "items", "properties", "step_type"), []string{"collect_cookies"}},
		{"scraping post-processing", append(append([]string{}, ruleProps...), "scraping_rules", "items", "properties", "post_processing", "items", "properties", "step_type"), []string{"replace", "remove", "transform", "validate", "clean", "set_env", "plugin_call", "agent_call", "external_api"}},
		{"public post-processing", append(append([]string{}, ruleProps...), "post_processing", "items", "properties", "step_type"), []string{"replace", "remove", "transform", "validate", "clean", "set_env", "plugin_call", "agent_call", "external_api"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := schemaEnum(t, document, test.path...)
			if !reflect.DeepEqual(sorted(got), sorted(test.want)) {
				t.Fatalf("enum = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPublicRulesValidateThroughCentralValidator(t *testing.T) {
	schema, _ := loadRulesetSchemaDocument(t)
	root := filepath.Join(rulesetRepositoryRoot(t), "rules")
	count := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		format, err := fixtureFormat(path)
		if err != nil {
			return nil
		}
		// Invalid samples must opt out visibly; there are no implicit directory exclusions.
		if strings.Contains(strings.ToLower(info.Name()), ".invalid.") {
			return nil
		}
		count++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := ValidateRulesetConfig(schema, data, format, RulesetValidationAllowLegacyAliases); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no public ruleset documents found")
	}
}

func TestCanonicalFixtureRoundTrips(t *testing.T) {
	for _, kind := range []string{"action", "scraping", "detection", "crawling"} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				data := mustFixture(t, "canonical_"+kind+"."+format)
				validateFixture(t, data, format)
				if format == "yaml" {
					data = normalizeYAMLToJSON(t, data)
				}
				var ruleset Ruleset
				if err := json.Unmarshal(data, &ruleset); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				encoded, err := rulesetValidationJSON(ruleset)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				validateFixture(t, encoded, "json")
				if len(ruleset.RuleGroups) != 1 {
					t.Fatalf("rule groups = %d", len(ruleset.RuleGroups))
				}
				group := ruleset.RuleGroups[0]
				switch kind {
				case "action":
					if len(group.ActionRules) != 1 || group.ActionRules[0].Value != "alice" || len(group.ActionRules[0].WaitConditions) != 1 {
						t.Fatalf("action fields lost: %#v", group.ActionRules)
					}
				case "scraping":
					if len(group.ScrapingRules) != 1 || group.ScrapingRules[0].JSONFieldMappings["title"] != "headline" || !group.ScrapingRules[0].ExtractScripts {
						t.Fatalf("scraping fields lost: %#v", group.ScrapingRules)
					}
				case "detection":
					if len(group.DetectionRules) != 1 || group.DetectionRules[0].ObjectName != "nginx" || len(group.DetectionRules[0].SSLSignatures) != 1 {
						t.Fatalf("detection fields lost: %#v", group.DetectionRules)
					}
				case "crawling":
					if len(group.CrawlingRules) != 1 || group.CrawlingRules[0].GetLifecycleHook("pre_crawl") == nil || len(group.CrawlingRules[0].FuzzingParameters[0].Values) != 2 {
						t.Fatalf("crawling fields lost: %#v", group.CrawlingRules)
					}
				}
			})
		}
	}
}

func TestInvalidPublicObjectFixtures(t *testing.T) {
	for _, name := range []string{"invalid_unknown_action_field.json", "invalid_unknown_scraping_condition.yaml", "invalid_malformed_crawling_target.json", "invalid_malformed_detection_signature.yaml"} {
		t.Run(name, func(t *testing.T) { requireInvalidFixture(t, name) })
	}
}

func TestErrorHandlingRejectsUnknownProperties(t *testing.T) {
	data := minimalRulesetFixture(t, "action_rules", map[string]interface{}{
		"rule_name":   "strict-error-handling",
		"action_type": "refresh",
		"error_handling": map[string]interface{}{
			"ignore":           false,
			"unexpected_field": true,
		},
	})
	schema, _ := loadRulesetSchemaDocument(t)
	if err := ValidateRulesetConfig(schema, data, "json"); err == nil {
		t.Fatal("unknown error_handling property was accepted")
	}
}

func TestCustomActionRequiresPluginCallSelector(t *testing.T) {
	schema, _ := loadRulesetSchemaDocument(t)
	tests := []struct {
		name      string
		selectors []interface{}
		wantValid bool
	}{
		{
			name: "CSS only",
			selectors: []interface{}{
				map[string]interface{}{"selector_type": "css", "selector": "button"},
			},
		},
		{
			name: "plugin call",
			selectors: []interface{}{
				map[string]interface{}{"selector_type": "css", "selector": "button"},
				map[string]interface{}{"selector_type": "plugin_call", "selector": "submit"},
			},
			wantValid: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := minimalRulesetFixture(t, "action_rules", map[string]interface{}{
				"rule_name":   "custom-plugin",
				"action_type": "custom",
				"selectors":   test.selectors,
			})
			err := ValidateRulesetConfig(schema, document, "json")
			if test.wantValid && err != nil {
				t.Fatalf("valid custom action rejected: %v", err)
			}
			if !test.wantValid && err == nil {
				t.Fatal("custom action without plugin_call selector was accepted")
			}
		})
	}
}

func TestSelectorAttributesSchemaIsStrict(t *testing.T) {
	schema, _ := loadRulesetSchemaDocument(t)
	tests := []struct {
		name       string
		attributes []interface{}
		wantValid  bool
	}{
		{
			name: "valid attribute objects",
			attributes: []interface{}{
				map[string]interface{}{"name": "visible", "value": true},
				map[string]interface{}{"name": "position", "value": 2},
				map[string]interface{}{"name": "label", "value": "next"},
			},
			wantValid: true,
		},
		{
			name:       "primitive array item",
			attributes: []interface{}{"visible"},
		},
		{
			name: "unknown attribute property",
			attributes: []interface{}{
				map[string]interface{}{"name": "visible", "value": true, "unknown": "field"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := minimalRulesetFixture(t, "scraping_rules", map[string]interface{}{
				"rule_name": "selector-attributes",
				"elements": []interface{}{
					map[string]interface{}{
						"key": "link",
						"selectors": []interface{}{
							map[string]interface{}{
								"selector_type":       "css",
								"selector":            "a.next",
								"selector_attributes": test.attributes,
							},
						},
					},
				},
			})
			err := ValidateRulesetConfig(schema, document, "json")
			if test.wantValid && err != nil {
				t.Fatalf("valid selector attributes rejected: %v", err)
			}
			if !test.wantValid && err == nil {
				t.Fatal("invalid selector attributes accepted")
			}
		})
	}
}

func TestMinimalRulesetFixtureHelper(t *testing.T) {
	data := minimalRulesetFixture(t, "action_rules", map[string]interface{}{"rule_name": "refresh", "action_type": "refresh"})
	validateFixture(t, data, "json")
}

func TestYAMLNormalizerRejectsNonStringKeys(t *testing.T) {
	var value interface{}
	if err := yaml.Unmarshal([]byte("1: value\n"), &value); err != nil {
		t.Fatal(err)
	}
	_, violations := normalizeYAMLValue(value, "$", nil)
	if len(violations) == 0 {
		t.Fatal("non-string YAML key was accepted")
	}
}

package ruleset

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestScrapingRuleAliasesAndParity(t *testing.T) {
	cases := []struct {
		name, input string
		scripts     bool
		mappings    map[string]string
	}{
		{"canonical", `{"extract_scripts":true,"json_field_mappings":{"old":"new"}}`, true, map[string]string{"old": "new"}},
		{"legacy", `{"js_files":true,"json_field_rename":{"old":"new"}}`, true, map[string]string{"old": "new"}},
		{"legacy array", `{"json_field_rename":[{"source_tag":"old","dest_tag":"new"}]}`, false, map[string]string{"old": "new"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var j, y ScrapingRule
			if err := json.Unmarshal([]byte(tc.input), &j); err != nil {
				t.Fatal(err)
			}
			// Use JSON as valid YAML to ensure both decoders see precisely the same document.
			yamlInput := tc.input
			if err := yaml.Unmarshal([]byte(yamlInput), &y); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(j, y) || j.ExtractScripts != tc.scripts || !reflect.DeepEqual(j.JSONFieldMappings, tc.mappings) {
				t.Fatalf("JSON=%#v YAML=%#v", j, y)
			}
		})
	}
}

func TestScrapingRuleAliasConflicts(t *testing.T) {
	docs := []string{`{"extract_scripts":true,"js_files":true}`, `{"json_field_mappings":{},"json_field_rename":{}}`}
	for _, doc := range docs {
		var r ScrapingRule
		if json.Unmarshal([]byte(doc), &r) == nil {
			t.Fatalf("JSON accepted conflict %s", doc)
		}
		if yaml.Unmarshal([]byte(doc), &r) == nil {
			t.Fatalf("YAML accepted conflict %s", doc)
		}
	}
}

func TestScrapingRuleCanonicalRoundTrip(t *testing.T) {
	in := ScrapingRule{RuleName: "r", ExtractScripts: true, JSONFieldMappings: map[string]string{"a": "b"}, Conditions: ScrapingConditions{Element: "main"}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "js_files") || strings.Contains(string(data), "json_field_rename") {
		t.Fatalf("legacy alias emitted: %s", data)
	}
	var out ScrapingRule
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: %#v != %#v", in, out)
	}
}

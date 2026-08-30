package ruleset

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestDetectionRuleRoundTripCanonicalFields(t *testing.T) {
	input := `{"rule_name":"detect","scope":"any","object_name":"server","http_header_fields":[{"key":"server","value":["nginx"],"confidence":4}],"page_content_patterns":[{"key":"body","text":["one","two"],"confidence":3}],"ssl_patterns":[{"key":"issuer","value":["ACME"],"confidence":2}],"url_micro_signatures":[{"value":"/admin","confidence":1}],"meta_tags":[{"name":"generator","content":"cms","confidence":5}],"implies":["http"],"plugin_calls":[{"plugin_name":"probe","plugin_args":[{"parameter_name":"confidence","parameter_value":"7"}]}],"agent_calls":[{"agent_name":"classify","timeout":1}],"external_detection":[{"name":"lookup","provider":"shodan","detection_params":[{"param_name":"token","param_value":"x"}]}]}`
	var rule DetectionRule
	if err := json.Unmarshal([]byte(input), &rule); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"object_name"`, `"http_header_fields"`, `"page_content_patterns"`, `"ssl_patterns"`, `"plugin_args"`, `"agent_calls"`, `"external_detection"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("round trip dropped %s: %s", field, encoded)
		}
	}
	if strings.Contains(string(encoded), "certificates_patterns") || strings.Contains(string(encoded), "plugin_parameters") {
		t.Fatalf("marshal emitted a legacy alias: %s", encoded)
	}

	yamlData, err := yaml.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	var again DetectionRule
	if err := yaml.Unmarshal(yamlData, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.PageContentPatterns) != 1 || len(again.PageContentPatterns[0].Text) != 2 || len(again.PluginCalls[0].PluginArgs) != 1 {
		t.Fatalf("YAML round trip lost normalized fields: %#v", again)
	}
}

func TestDetectionLegacyAliasesNormalize(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		unmarshal   func([]byte, interface{}) error
	}{
		{"JSON", `{"object_name":"x","certificates_patterns":[{"key":"issuer","confidence":1}],"plugin_calls":[{"plugin_name":"p","plugin_parameters":[]}],"page_content_patterns":[{"key":"body","text":"legacy","confidence":1}]}`, json.Unmarshal},
		{"YAML", "object_name: x\ncertificates_patterns:\n- key: issuer\n  confidence: 1\nplugin_calls:\n- plugin_name: p\n  plugin_parameters: []\npage_content_patterns:\n- key: body\n  text: legacy\n  confidence: 1\n", yaml.Unmarshal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rule DetectionRule
			if err := tc.unmarshal([]byte(tc.input), &rule); err != nil {
				t.Fatal(err)
			}
			if len(rule.SSLSignatures) != 1 || len(rule.PageContentPatterns[0].Text) != 1 || rule.PageContentPatterns[0].Text[0] != "legacy" || rule.PluginCalls[0].PluginArgs == nil {
				t.Fatalf("aliases were not normalized: %#v", rule)
			}
		})
	}
}

func TestDetectionAliasConflictsAndMalformedText(t *testing.T) {
	bad := []string{
		`{"ssl_patterns":[],"certificates_patterns":[]}`,
		`{"plugin_calls":[{"plugin_name":"p","plugin_args":[],"plugin_parameters":[]}]}`,
		`{"page_content_patterns":[{"text":["ok",2]}]}`,
	}
	for _, input := range bad {
		var rule DetectionRule
		if err := json.Unmarshal([]byte(input), &rule); err == nil {
			t.Errorf("accepted malformed detection rule: %s", input)
		}
	}
	var yamlRule DetectionRule
	if err := yaml.Unmarshal([]byte("ssl_patterns: []\ncertificates_patterns: []\n"), &yamlRule); err == nil {
		t.Error("accepted conflicting YAML TLS aliases")
	}
}

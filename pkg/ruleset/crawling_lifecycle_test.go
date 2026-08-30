package ruleset

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

var crawlingLifecycleStages = []string{
	"pre_crawl", "pre_request", "post_response", "pre_fuzz",
	"per_fuzz_candidate", "post_fuzz", "post_crawl",
}

func lifecycleRule() CrawlingRule {
	hook := func(name string) *CrawlingLifecycleHook {
		return &CrawlingLifecycleHook{AgentCall: &AgentCall{AgentName: name, Timeout: 10}}
	}
	return CrawlingRule{
		RuleName: "lifecycle", Scope: "website", RequestType: "GET",
		TargetElements:    []TargetElement{{SelectorType: "path", Selector: "{id}"}},
		FuzzingParameters: []FuzzingParameter{{ParameterName: "path", FuzzingType: "fixed_list", Selector: "{id}", Values: []string{"1"}}},
		Lifecycle: &CrawlingLifecycle{
			PreCrawl: hook("pre-crawl"), PreRequest: hook("pre-request"),
			PostResponse: hook("post-response"), PreFuzz: hook("pre-fuzz"),
			PerFuzzCandidate: hook("candidate"), PostFuzz: hook("post-fuzz"),
			PostCrawl: hook("post-crawl"),
		},
	}
}

func TestCrawlingLifecycleRoundTrip(t *testing.T) {
	want := lifecycleRule()
	for name, marshal := range map[string]struct {
		marshal   func(interface{}) ([]byte, error)
		unmarshal func([]byte, interface{}) error
	}{
		"JSON": {json.Marshal, json.Unmarshal},
		"YAML": {yaml.Marshal, yaml.Unmarshal},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := marshal.marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var got CrawlingRule
			if err := marshal.unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip mismatch:\nwant: %#v\n got: %#v", want, got)
			}
		})
	}
}

func TestGetLifecycleHookStageParity(t *testing.T) {
	r := lifecycleRule()
	for _, stage := range crawlingLifecycleStages {
		if hook := r.GetLifecycleHook(stage); hook == nil || hook.AgentCall == nil {
			t.Fatalf("implemented lifecycle stage %q is not addressable", stage)
		}
	}
	if hook := r.GetLifecycleHook("unknown_stage"); hook != nil {
		t.Fatalf("unknown stage returned hook: %#v", hook)
	}
}

func loadRulesetSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile("../../schemas/crowler-ruleset-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	schema := &jsonschema.Schema{}
	if err := json.Unmarshal(data, schema); err != nil {
		t.Fatal(err)
	}
	schema.Register("", jsonschema.GetSchemaRegistry())
	return schema
}

func crawlingRulesetJSON(t *testing.T, rule map[string]interface{}) []byte {
	t.Helper()
	doc := map[string]interface{}{
		"format_version": "1.0.5", "author": "test", "created_at": "2026-01-01T00:00:00Z",
		"description": "test", "ruleset_name": "test",
		"rule_groups": []interface{}{map[string]interface{}{
			"group_name": "test", "is_enabled": true, "url": "*", "crawling_rules": []interface{}{rule},
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validateCrawlingRule(t *testing.T, rule map[string]interface{}) int {
	t.Helper()
	errs, err := loadRulesetSchema(t).ValidateBytes(context.Background(), crawlingRulesetJSON(t, rule))
	if err != nil {
		t.Fatal(err)
	}
	return len(errs)
}

func validCrawlingRule() map[string]interface{} {
	return map[string]interface{}{
		"rule_name": "test", "scope": "website", "request_type": "GET",
		"target_elements":    []interface{}{map[string]interface{}{"selector_type": "path", "selector": "{id}"}},
		"fuzzing_parameters": []interface{}{map[string]interface{}{"parameter_name": "path", "fuzzing_type": "fixed_list", "selector": "{id}", "values": []interface{}{"1"}}},
	}
}

func TestCrawlingLifecycleSchemaStageParityAndUnknownStage(t *testing.T) {
	for _, stage := range crawlingLifecycleStages {
		t.Run(stage, func(t *testing.T) {
			rule := validCrawlingRule()
			rule["lifecycle"] = map[string]interface{}{stage: map[string]interface{}{"agent_call": map[string]interface{}{"agent_name": "agent"}}}
			if count := validateCrawlingRule(t, rule); count != 0 {
				t.Fatalf("valid stage produced %d schema errors", count)
			}
		})
	}
	rule := validCrawlingRule()
	rule["lifecycle"] = map[string]interface{}{"during_crawl": map[string]interface{}{"agent_call": map[string]interface{}{"agent_name": "agent"}}}
	if count := validateCrawlingRule(t, rule); count == 0 {
		t.Fatal("schema accepted an unknown lifecycle stage")
	}
}

func TestCrawlingFuzzingSchemaRequirements(t *testing.T) {
	tests := []struct {
		name, fuzzingType string
		field             string
		valid             bool
	}{
		{"fixed list has values", "fixed_list", "values", true},
		{"fixed list lacks values", "fixed_list", "", false},
		{"pattern has pattern", "pattern_based", "pattern", true},
		{"pattern lacks pattern", "pattern_based", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := validCrawlingRule()
			parameter := map[string]interface{}{"parameter_name": "path", "fuzzing_type": tt.fuzzingType, "selector": "{id}"}
			if tt.field == "values" {
				parameter[tt.field] = []interface{}{"1"}
			}
			if tt.field == "pattern" {
				parameter[tt.field] = "[0-9]+"
			}
			rule["fuzzing_parameters"] = []interface{}{parameter}
			valid := validateCrawlingRule(t, rule) == 0
			if valid != tt.valid {
				t.Fatalf("schema validity = %v, want %v", valid, tt.valid)
			}
		})
	}
}

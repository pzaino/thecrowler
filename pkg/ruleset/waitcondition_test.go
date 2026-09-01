package ruleset

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWaitConditionActionScrapingSerializationParity(t *testing.T) {
	fixture := []byte(`{"wait_conditions":[{"condition_type":"element_visible","selector":{"selector_type":"css","selector":"#ready"}},{"condition_type":"plugin_call","plugin":"ready"}]}`)
	var action ActionRule
	var scraping ScrapingRule
	if err := json.Unmarshal(fixture, &action); err != nil {
		t.Fatalf("unmarshal action fixture: %v", err)
	}
	if err := json.Unmarshal(fixture, &scraping); err != nil {
		t.Fatalf("unmarshal scraping fixture: %v", err)
	}
	if !reflect.DeepEqual(action.WaitConditions, scraping.WaitConditions) {
		t.Fatalf("wait conditions differ: action=%#v scraping=%#v", action.WaitConditions, scraping.WaitConditions)
	}
	want := []WaitCondition{
		{ConditionType: WaitConditionElementVisible, Selector: Selector{SelectorType: "css", Selector: "#ready"}},
		{ConditionType: WaitConditionPluginCall, Plugin: "ready"},
	}
	if !reflect.DeepEqual(action.WaitConditions, want) {
		t.Fatalf("wait conditions = %#v, want %#v", action.WaitConditions, want)
	}
}

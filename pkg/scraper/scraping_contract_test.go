package scraper

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

type countingPlugin struct{ calls int }

func (p *countingPlugin) RunPlugin(_ context.Context, req PluginRequest) (interface{}, error) {
	p.calls++
	var v map[string]interface{}
	_ = json.Unmarshal(req.Data, &v)
	return v, nil
}

func TestJSONFieldMappingBehavior(t *testing.T) {
	got, err := renameOutputFields(map[string]interface{}{"old": "value"}, map[string]string{"old": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]interface{}{"new": "value"}) {
		t.Fatalf("got %#v", got)
	}
	if _, err = renameOutputFields(map[string]interface{}{"old": 1, "new": 2}, map[string]string{"old": "new"}); err == nil {
		t.Fatal("expected collision error")
	}
}

func TestPublicPostProcessingHandlerParity(t *testing.T) {
	want := []string{"replace", "remove", "transform", "validate", "clean", "set_env", "plugin_call", "agent_call", "external_api"}
	if !reflect.DeepEqual(PublicPostProcessingHandlers, want) {
		t.Fatalf("public handlers %#v", PublicPostProcessingHandlers)
	}
	for _, handler := range PublicPostProcessingHandlers {
		if handler == "crowler_meta" {
			t.Fatal("internal handler is public")
		}
	}
	if !reflect.DeepEqual(InternalPostProcessingHandlers, []string{"crowler_meta"}) {
		t.Fatalf("internal handlers %#v", InternalPostProcessingHandlers)
	}
}

func TestExecuteRuleRunsPostProcessingOnce(t *testing.T) {
	plugin := &countingPlugin{}
	rule := &rs.ScrapingRule{RuleName: "once", PostProcessing: []rs.PostProcessingStep{{Type: "plugin_call", Details: map[string]interface{}{"plugin_name": "counter"}}}}
	if _, err := ExecuteRule(context.Background(), &Runtime{Plugins: plugin}, rule, nil); err != nil {
		t.Fatal(err)
	}
	if plugin.calls != 1 {
		t.Fatalf("plugin calls=%d, want 1", plugin.calls)
	}
}

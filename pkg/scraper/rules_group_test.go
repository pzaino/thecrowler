package scraper

import (
	"context"
	"reflect"
	"testing"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

type namedValuePlugin map[string]interface{}

func (p namedValuePlugin) RunPlugin(_ context.Context, request PluginRequest) (interface{}, error) {
	return p[request.Name], nil
}

func TestApplyRulesGroupRemoveReplacesAggregatedResult(t *testing.T) {
	group := &rs.RuleGroup{
		GroupName: "remove field",
		ScrapingRules: []rs.ScrapingRule{
			pluginValueRule("keep rule", "keep", "keep"),
			pluginValueRule("remove rule", "remove_me", "remove_me"),
		},
		PostProcessing: []rs.PostProcessingStep{{
			Type:    "remove",
			Details: map[string]interface{}{"target": `,"remove_me":"discard"`},
		}},
	}

	got, err := ApplyRulesGroup(context.Background(), &Runtime{
		Plugins: namedValuePlugin{"keep": "retained", "remove_me": "discard"},
	}, group, nil)
	if err != nil {
		t.Fatalf("ApplyRulesGroup returned error: %v", err)
	}
	if want := map[string]interface{}{"keep": "retained"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyRulesGroup result = %#v, want %#v", got, want)
	}
	if _, exists := got["remove_me"]; exists {
		t.Fatalf("removed field remains in ApplyRulesGroup result: %#v", got)
	}
}

func TestApplyRulesGroupReplaceReturnsProcessedScalar(t *testing.T) {
	group := &rs.RuleGroup{
		GroupName:     "replace status",
		ScrapingRules: []rs.ScrapingRule{pluginValueRule("status rule", "status", "status")},
		PostProcessing: []rs.PostProcessingStep{{
			Type:    "replace",
			Details: map[string]interface{}{"target": "old", "replacement": "new"},
		}},
	}

	got, err := ApplyRulesGroup(context.Background(), &Runtime{
		Plugins: namedValuePlugin{"status": "old"},
	}, group, nil)
	if err != nil {
		t.Fatalf("ApplyRulesGroup returned error: %v", err)
	}
	if status, ok := got["status"].(string); !ok || status != "new" {
		t.Fatalf("status = %#v (%T), want scalar string %q", got["status"], got["status"], "new")
	}
}

func pluginValueRule(ruleName, key, pluginName string) rs.ScrapingRule {
	return rs.ScrapingRule{
		RuleName: ruleName,
		Elements: []rs.Element{{
			Key: key,
			Selectors: []rs.Selector{{
				SelectorType: "plugin_call",
				Selector:     pluginName,
			}},
		}},
	}
}

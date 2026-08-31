package crawler

import (
	"encoding/json"
	"reflect"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
)

func TestRuntimeScopeRuleSelection(t *testing.T) {
	ruleGroup := rules.RuleGroup{
		IsEnabled: true,
		ActionRules: []rules.ActionRule{
			{RuleName: "omitted"},
			{RuleName: "any", Scope: " any "},
			{RuleName: "website", Scope: "Website"},
			{RuleName: "api", Scope: "api"},
		},
	}
	ruleset := rules.Ruleset{RuleGroups: []rules.RuleGroup{ruleGroup}}

	tests := []struct {
		name       string
		sourceType string
		wantScope  string
		wantRules  []string
	}{
		{name: "website", sourceType: "website", wantScope: "website", wantRules: []string{"omitted", "any", "website"}},
		{name: "api", sourceType: "api", wantScope: "api", wantRules: []string{"omitted", "any", "api"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(`{"crawling_config":{"source_type":"` + tt.sourceType + `"}}`)
			ctx := &ProcessContext{source: &cdb.Source{Config: &raw}}
			scope := runtimeScope(ctx)
			if scope != tt.wantScope {
				t.Fatalf("runtimeScope() = %q, want %q", scope, tt.wantScope)
			}

			selected := ruleset.GetAllEnabledActionRulesForScope("runtime-context-not-a-scope", scope)
			got := make([]string, 0, len(selected))
			for _, rule := range selected {
				got = append(got, rule.RuleName)
			}
			if !reflect.DeepEqual(got, tt.wantRules) {
				t.Fatalf("selected rules = %v, want %v", got, tt.wantRules)
			}

			groupSelected := ruleGroup.GetActionRulesForScope(scope)
			got = got[:0]
			for _, rule := range groupSelected {
				got = append(got, rule.RuleName)
			}
			if !reflect.DeepEqual(got, tt.wantRules) {
				t.Fatalf("direct group selected rules = %v, want %v", got, tt.wantRules)
			}
		})
	}
}

func TestRuntimeScopeDefaultsToWebsite(t *testing.T) {
	if got := runtimeScope(&ProcessContext{source: &cdb.Source{}}); got != "website" {
		t.Fatalf("runtimeScope() = %q, want website", got)
	}
}

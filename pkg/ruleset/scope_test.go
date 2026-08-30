package ruleset

import "testing"

func TestScopeMatches(t *testing.T) {
	tests := []struct {
		name, rule, runtime string
		want                bool
	}{
		{"same", "website", "website", true},
		{"normalized", " API ", "api", true},
		{"rule any", "ANY", "file", true},
		{"runtime any", "db", " any ", true},
		{"legacy empty rule", "", "data", true},
		{"legacy empty runtime", "website", "", true},
		{"different", "website", "api", false},
		{"unknown rule", "web", "website", false},
		{"unknown runtime", "website", "web", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScopeMatches(tt.rule, tt.runtime); got != tt.want {
				t.Fatalf("ScopeMatches(%q, %q) = %v, want %v", tt.rule, tt.runtime, got, tt.want)
			}
		})
	}
}

func TestEnabledRuleCollectionsExcludeMismatchedScopes(t *testing.T) {
	re := NewRuleEngine("", []Ruleset{{RuleGroups: []RuleGroup{{
		IsEnabled:      true,
		ActionRules:    []ActionRule{{RuleName: "action-api", Scope: "api"}, {RuleName: "action-web", Scope: "website"}},
		ScrapingRules:  []ScrapingRule{{RuleName: "scrape-api", Scope: "api"}, {RuleName: "scrape-web", Scope: "website"}},
		DetectionRules: []DetectionRule{{RuleName: "detect-api", Scope: "api"}, {RuleName: "detect-web", Scope: "website"}},
		CrawlingRules:  []CrawlingRule{{RuleName: "crawl-api", Scope: "api"}, {RuleName: "crawl-web", Scope: "website"}},
	}}}})

	if got := re.GetAllEnabledActionRules("", "api"); len(got) != 1 || got[0].RuleName != "action-api" {
		t.Fatalf("actions: %#v", got)
	}
	if got := re.GetAllEnabledScrapingRules("", "api"); len(got) != 1 || got[0].RuleName != "scrape-api" {
		t.Fatalf("scraping: %#v", got)
	}
	if got := re.GetAllEnabledDetectionRules("", "api"); len(got) != 1 || got[0].RuleName != "detect-api" {
		t.Fatalf("detection: %#v", got)
	}
	if got := re.GetAllEnabledCrawlingRules("", "api"); len(got) != 1 || got[0].RuleName != "crawl-api" {
		t.Fatalf("crawling: %#v", got)
	}
}

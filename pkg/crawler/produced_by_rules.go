package crawler

import (
	"strings"

	detect "github.com/pzaino/thecrowler/pkg/detection"
	"github.com/pzaino/thecrowler/pkg/ruleset"
)

func addDetectionProducedByRules(cm CrowlerMeta, re *ruleset.RuleEngine, ctxID string, detected map[string]detect.DetectedEntity) {
	if cm == nil || re == nil || len(detected) == 0 {
		return
	}
	for _, name := range detectionProducedByRuleNames(re.GetAllEnabledDetectionRules(ctxID), detected) {
		cm.AddProducedByRule(name)
	}
}

func detectionProducedByRuleNames(patterns []ruleset.DetectionRule, detected map[string]detect.DetectedEntity) []string {
	if len(patterns) == 0 || len(detected) == 0 {
		return nil
	}
	detectedNames := make(map[string]struct{}, len(detected))
	for key, entity := range detected {
		addDetectedName(detectedNames, key)
		addDetectedName(detectedNames, entity.EntityName)
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, rule := range patterns {
		objectName := strings.TrimSpace(rule.ObjectName)
		ruleName := strings.TrimSpace(rule.RuleName)
		if objectName == "" || ruleName == "" {
			continue
		}
		if _, ok := detectedNames[strings.ToLower(objectName)]; !ok {
			continue
		}
		if _, ok := seen[ruleName]; ok {
			continue
		}
		seen[ruleName] = struct{}{}
		out = append(out, ruleName)
	}
	return out
}

func addDetectedName(names map[string]struct{}, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	names[strings.ToLower(name)] = struct{}{}
	if strings.HasPrefix(strings.ToLower(name), "no_") {
		trimmed := strings.TrimSpace(name[3:])
		if trimmed != "" {
			names[strings.ToLower(trimmed)] = struct{}{}
		}
	}
}

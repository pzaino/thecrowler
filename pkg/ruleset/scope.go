// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package ruleset

import "strings"

// ScopeMatches reports whether a rule is applicable to a runtime scope.
//
// Scope names are compared after trimming surrounding whitespace and folding
// case. The supported names are any, website, api, file, db, and data. "any"
// is a wildcard on either side. For compatibility with rules and callers that
// predate scoped collection, an empty rule scope or an empty runtime scope is
// also treated as a wildcard. Unknown, non-empty scope names never match.
func ScopeMatches(ruleScope, runtimeScope string) bool {
	ruleScope = strings.ToLower(strings.TrimSpace(ruleScope))
	runtimeScope = strings.ToLower(strings.TrimSpace(runtimeScope))
	if ruleScope == "" || runtimeScope == "" {
		return true
	}
	if !supportedRuleScope(ruleScope) || !supportedRuleScope(runtimeScope) {
		return false
	}
	return ruleScope == "any" || runtimeScope == "any" || ruleScope == runtimeScope
}

func supportedRuleScope(scope string) bool {
	switch scope {
	case "any", "website", "api", "file", "db", "data":
		return true
	default:
		return false
	}
}

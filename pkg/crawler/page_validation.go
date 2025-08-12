// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crawler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// Typed errors so callers can react.
var (
	ErrValidationNotSatisfied = errors.New("validation not satisfied") // no explicit retry; caller decides
	ErrValidationSkip         = errors.New("validation skip")
	ErrValidationFail         = errors.New("validation fail")
	ErrValidationLogOnly      = errors.New("validation log only")
)

// ValidationAction expresses what the caller should do if the page didn't validate.
type ValidationAction string

const (
	VANone    ValidationAction = "none"     // no explicit action; caller's general retry policy applies
	VARetry   ValidationAction = "retry"    // rule asks to retry
	VASkip    ValidationAction = "skip"     // skip this page
	VAFail    ValidationAction = "fail"     // mark invalid and abort
	VALogOnly ValidationAction = "log_only" // log once, then treat as valid
)

// ValidationStatus is a pure decision object. No reloads, no logs here.
type ValidationStatus struct {
	Valid      bool             // true when the page is considered valid per rules
	Action     ValidationAction // action to take if not Valid (or special case log_only)
	RetryKey   string           // stable key for per-rule retry budget (e.g. "g0.v2" or "g1")
	MaxRetries int              // per-rule retry budget to honor when Action=retry
	Reason     string           // optional, human-readable note for debugging
}

// When a rule asks to retry, we return this typed error.
// The caller tracks how many times a given Key has been retried.
type retryDirective struct {
	Key string // unique key for this rule or specific validation
	Max int    // max retries requested by the rule
}

func (e *retryDirective) Error() string { return "validation retry requested" }

type loadValidationConfig struct {
	Groups []validationGroup `json:"groups"`
}

// One URL pattern, many validations
type validationGroup struct {
	URLPattern             string       `json:"url_pattern"`
	Scope                  string       `json:"scope"` // seed_only | source_domain | same_origin | all_crawled
	Validations            []validation `json:"validations"`
	AnyValidationSatisfies bool         `json:"any_validation_satisfies"` // default true (OR across validations)
	OnFail                 string       `json:"on_fail"`                  // retry | mark_invalid | skip | log_only
	MaxRetries             int          `json:"max_retries"`              // group-level retries when OnFail=retry
}

// One validation is a set of DOM checks
type validation struct {
	DOMChecks         []domCheck `json:"dom_checks"`
	AllChecksMustPass bool       `json:"all_checks_must_pass"` // AND across dom_checks; if false, OR across dom_checks
	// Optional per-validation stabilizer retries
	OnFail     string `json:"on_fail,omitempty"`     // retry to re-run this validation before moving to the next
	MaxRetries int    `json:"max_retries,omitempty"` // retries of this validation only
}

// Select elements, then evaluate conditions against them
type domCheck struct {
	Selector   string         `json:"selector"`             // CSS selector
	Timeout    string         `json:"timeout,omitempty"`    // seconds or an ExprTerpreter expression
	Conditions []domCondition `json:"conditions,omitempty"` // ANY-of conditions; if empty, acts like "exists >= 1"
}

// A single condition evaluated against the selected element set
type domCondition struct {
	Type      string `json:"type"`                // exists | not_exists | text | attribute | count
	Attribute string `json:"attribute,omitempty"` // for attribute
	Pattern   string `json:"pattern,omitempty"`   // regex for text/attribute
	MinCount  *int   `json:"min_count,omitempty"` // for exists/count
	MaxCount  *int   `json:"max_count,omitempty"` // for count
}

func ApplyLoadValidation(ctx *ProcessContext, wd *vdi.WebDriver, level int) (ValidationStatus, error) {
	var st ValidationStatus

	// Pull config
	ccfg, ok := ctx.srcCfg["crawling_config"].(map[string]interface{})
	if !ok {
		st.Valid = true // neutral pass
		return st, nil
	}
	rawLV, ok := ccfg["load_validation"]
	if !ok {
		st.Valid = true
		return st, nil
	}

	b, err := json.Marshal(rawLV)
	if err != nil {
		st.Valid = true
		return st, nil
	}
	var lv loadValidationConfig
	if err := json.Unmarshal(b, &lv); err != nil {
		st.Valid = true
		return st, nil
	}
	if len(lv.Groups) == 0 {
		st.Valid = true
		return st, nil
	}

	curURL, err := (*wd).CurrentURL()
	if err != nil {
		st.Valid = true
		return st, nil
	}
	seedSite := ""
	if v, ok := ccfg["site"].(string); ok {
		seedSite = v
	}

	// First-match-wins across groups
	for gIdx, g := range lv.Groups {
		if !groupInScope(g.Scope, seedSite, curURL, level) {
			continue
		}
		re, gre := regexp.Compile(g.URLPattern)
		if gre != nil || !re.MatchString(curURL) {
			continue
		}

		// Evaluate validations in order
		anyBroken := true // will flip to false if we see at least one actionable validation
		var pendingRetry *ValidationStatus

		for vIdx, v := range g.Validations {
			ok, verr := evaluateDOMChecksWithMode(ctx, wd, v.DOMChecks, v.AllChecksMustPass)
			if verr == nil {
				anyBroken = false
			}
			if verr != nil {
				// broken validation → ignore
				continue
			}
			if ok {
				st.Valid = true
				return st, nil
			}
			// Not ok and not broken. Remember per-validation retry if declared.
			if strings.EqualFold(v.OnFail, "retry") && v.MaxRetries > 0 && pendingRetry == nil {
				pendingRetry = &ValidationStatus{
					Valid:      false,
					Action:     VARetry,
					RetryKey:   fmt.Sprintf("g%d.v%d", gIdx, vIdx),
					MaxRetries: v.MaxRetries,
					Reason:     "per-validation retry",
				}
			}
		}

		// All validations broken → treat as pass
		if anyBroken {
			st.Valid = true
			st.Reason = "all validations broken; fail-open"
			return st, nil
		}

		// Prefer per-validation retry if available
		if pendingRetry != nil {
			return *pendingRetry, nil
		}

		// Apply group-level on_fail
		switch strings.ToLower(strings.TrimSpace(g.OnFail)) {
		case "retry":
			if g.MaxRetries > 0 {
				return ValidationStatus{
					Valid:      false,
					Action:     VARetry,
					RetryKey:   fmt.Sprintf("g%d", gIdx),
					MaxRetries: g.MaxRetries,
					Reason:     "group retry",
				}, nil
			}
			return ValidationStatus{Valid: false, Action: VANone, Reason: "group retry without budget"}, nil
		case "skip":
			return ValidationStatus{Valid: false, Action: VASkip}, nil
		case "log_only":
			// caller logs once, then proceeds as valid
			return ValidationStatus{Valid: true, Action: VALogOnly}, nil
		case "mark_invalid":
			return ValidationStatus{Valid: false, Action: VAFail}, nil
		default:
			// No explicit action → let caller's general retry policy decide
			return ValidationStatus{Valid: false, Action: VANone}, nil
		}
	}

	// No group matched → neutral pass
	st.Valid = true
	return st, nil
}

func evalValidationGroup(ctx *ProcessContext, wd *vdi.WebDriver, g validationGroup) (bool, error) {
	// Default OR across validations
	for idx, v := range g.Validations {
		ok, err := evalOneValidation(ctx, wd, v)
		if ok {
			return true, nil
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "validation #%d failed: %v", idx, err)
	}
	return false, fmt.Errorf("all validations failed")
}

func evalOneValidation(ctx *ProcessContext, wd *vdi.WebDriver, v validation) (bool, error) {
	attempts := 1
	if strings.EqualFold(v.OnFail, "retry") && v.MaxRetries > 0 {
		attempts = v.MaxRetries + 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		ok, err := evaluateDOMChecksWithMode(ctx, wd, v.DOMChecks, v.AllChecksMustPass)
		if ok {
			return true, nil
		}
		last = err
		// brief settle time between retries of the same validation
		if i < attempts-1 {
			_, _ = vdiSleep(ctx, 0.8)
		}
	}
	return false, last
}

func evaluateDOMChecksWithMode(ctx *ProcessContext, wd *vdi.WebDriver, checks []domCheck, allMust bool) (bool, error) {
	if len(checks) == 0 {
		return true, nil
	}
	var firstErr error
	for i, dc := range checks {
		// Optional per-check wait
		if strings.TrimSpace(dc.Timeout) != "" {
			sec := exi.GetFloat(dc.Timeout)
			if sec > 0 {
				_, _ = vdiSleep(ctx, sec)
			}
		}
		// Find elements (treat errors as zero elements)
		elems, _ := (*wd).FindElements(vdi.ByCSSSelector, dc.Selector)

		ok, err := evalDomCheck(ctx, wd, dc, elems)
		if allMust {
			if !ok {
				if firstErr == nil {
					firstErr = fmt.Errorf("check %d failed: %w", i, err)
				}
				return false, firstErr
			}
		} else {
			if ok {
				return true, nil
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("check %d failed: %w", i, err)
			}
		}
	}
	if allMust {
		return true, nil
	}
	return false, firstErr
}

func evalDomCheck(_ *ProcessContext, _ *vdi.WebDriver, dc domCheck, elems []vdi.WebElement) (bool, error) {
	// Default behavior if no conditions were specified: "exists >= 1"
	if len(dc.Conditions) == 0 {
		if len(elems) >= 1 {
			return true, nil
		}
		return false, fmt.Errorf("exists: found 0 elements for selector %q", dc.Selector)
	}

	// ANY-of conditions passes the check
	var firstErr error
	for _, cond := range dc.Conditions {
		ok, err := evalConditionOnElements(cond, elems)
		if ok {
			return true, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return false, firstErr
}

func evalConditionOnElements(cond domCondition, elems []vdi.WebElement) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(cond.Type)) {
	case "exists":
		min := 1
		if cond.MinCount != nil {
			min = *cond.MinCount
		}
		if len(elems) >= min {
			return true, nil
		}
		return false, fmt.Errorf("exists: %d < min %d", len(elems), min)

	case "not_exists":
		if len(elems) == 0 {
			return true, nil
		}
		return false, fmt.Errorf("not_exists: found %d", len(elems))

	case "count":
		min := 0
		if cond.MinCount != nil {
			min = *cond.MinCount
		}
		max := -1
		if cond.MaxCount != nil {
			max = *cond.MaxCount
		}
		n := len(elems)
		if n < min {
			return false, fmt.Errorf("count: %d < min %d", n, min)
		}
		if max >= 0 && n > max {
			return false, fmt.Errorf("count: %d > max %d", n, max)
		}
		return true, nil

	case "text":
		if cond.Pattern == "" {
			return false, fmt.Errorf("text: missing pattern")
		}
		re, err := regexp.Compile(cond.Pattern)
		if err != nil {
			return false, fmt.Errorf("text: bad regex: %v", err)
		}
		for _, e := range elems {
			t, _ := e.Text()
			if re.MatchString(strings.TrimSpace(t)) {
				return true, nil
			}
		}
		return false, fmt.Errorf("text: no element matched regex")

	case "attribute":
		if cond.Attribute == "" || cond.Pattern == "" {
			return false, fmt.Errorf("attribute: missing attribute or pattern")
		}
		re, err := regexp.Compile(cond.Pattern)
		if err != nil {
			return false, fmt.Errorf("attribute: bad regex: %v", err)
		}
		for _, e := range elems {
			val, err := e.GetAttribute(cond.Attribute)
			if err == nil && re.MatchString(strings.TrimSpace(val)) {
				return true, nil
			}
		}
		return false, fmt.Errorf("attribute: no attr matched regex")

	default:
		return false, fmt.Errorf("unknown condition type: %s", cond.Type)
	}
}

func groupInScope(scope, seed, current string, level int) bool {
	s := strings.ToLower(strings.TrimSpace(scope))
	if s == "" {
		s = "source_domain"
	}
	switch s {
	case "seed_only":
		return level <= 0
	case "all_crawled":
		return true
	case "same_origin":
		seedU, _ := url.Parse(seed)
		curU, _ := url.Parse(current)
		return sameOrigin(seedU, curU)
	case "source_domain":
		seedU, _ := url.Parse(seed)
		curU, _ := url.Parse(current)
		return sameRegistrableDomain(seedU.Hostname(), curU.Hostname())
	default:
		return true
	}
}

func sameOrigin(seedU, curU *url.URL) bool {
	if seedU == nil || curU == nil {
		return false
	}
	ap := seedU.Port()
	if ap == "" {
		ap = defaultPort(seedU.Scheme)
	}
	bp := curU.Port()
	if bp == "" {
		bp = defaultPort(curU.Scheme)
	}
	return strings.EqualFold(seedU.Scheme, curU.Scheme) &&
		strings.EqualFold(seedU.Hostname(), curU.Hostname()) &&
		ap == bp
}

func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// simple suffix-based domain match
func sameRegistrableDomain(seed, cur string) bool {
	seed = strings.ToLower(strings.TrimSpace(seed))
	cur = strings.ToLower(strings.TrimSpace(cur))
	if seed == "" || cur == "" {
		return false
	}
	return seed == cur || strings.HasSuffix(seed, "."+cur) || strings.HasSuffix(cur, "."+seed)
}

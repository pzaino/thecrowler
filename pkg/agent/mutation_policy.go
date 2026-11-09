package agent

import (
	"fmt"
	"strings"
	"time"
)

// MutationPolicy is the trusted operator opt-in for model-driven state
// changes. It is parsed only from frozen manifest or deployment configuration
// and never from model output, tool results or event data.
type MutationPolicy struct {
	Enabled           bool
	AllowedOperations []string
	Issuer            string
	ProposalTTL       time.Duration
	MaxRequests       int
	BudgetWindow      time.Duration
}

// OperationsSet returns the explicit operation allowlist as a set.
func (p *MutationPolicy) OperationsSet() map[string]bool {
	if p == nil {
		return nil
	}
	out := make(map[string]bool, len(p.AllowedOperations))
	for _, name := range p.AllowedOperations {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// EnabledOperations reports whether an operation is explicitly permitted.
func (p *MutationPolicy) EnabledOperations(name string) bool {
	if p == nil || !p.Enabled {
		return false
	}
	return p.OperationsSet()[name]
}

var mutationPolicyKeys = map[string]bool{
	"enabled": true, "allowed_operations": true, "issuer": true,
	"proposal_ttl": true, "max_requests": true, "budget_window": true,
}

func parseMutationPolicyRaw(raw any) (*MutationPolicy, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := normalizeStringMap(raw)
	if !ok {
		return nil, fmt.Errorf("mutation_policy must be an object")
	}
	for key := range values {
		if !mutationPolicyKeys[key] {
			return nil, fmt.Errorf("unknown mutation_policy field %q", key)
		}
	}
	enabledRaw, ok := values["enabled"]
	if !ok {
		return nil, fmt.Errorf("mutation_policy.enabled is required")
	}
	enabled, ok := enabledRaw.(bool)
	if !ok {
		return nil, fmt.Errorf("mutation_policy.enabled must be a boolean")
	}
	policy := &MutationPolicy{Enabled: enabled}
	if !enabled {
		return policy, nil
	}
	operations, err := parseStringList(values["allowed_operations"], "mutation_policy.allowed_operations", 1, 16, maxToolNameLen, true)
	if err != nil {
		return nil, err
	}
	if !containsString(operations, crawlRequestToolName) {
		return nil, ErrCrawlMutationOperationRequired
	}
	issuer, err := optionalString(values, "issuer", maxCrawlRequestIDLen)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, fmt.Errorf("mutation_policy.issuer is required")
	}
	maxRequests, err := requiredInt(values, "max_requests")
	if err != nil {
		return nil, err
	}
	if maxRequests <= 0 || maxRequests > maxCrawlPolicyRequests {
		return nil, ErrCrawlMutationQuotaRequired
	}
	budgetWindow, err := parseMutationWindow(values["budget_window"], "budget_window")
	if err != nil {
		return nil, err
	}
	ttl := 30 * time.Minute
	if rawTTL, present := values["proposal_ttl"]; present && rawTTL != nil {
		ttl, err = parseMutationWindow(rawTTL, "proposal_ttl")
		if err != nil {
			return nil, err
		}
	}
	if ttl >= maxCrawlProposalLifetime {
		return nil, fmt.Errorf("mutation_policy.proposal_ttl must be less than %s", maxCrawlProposalLifetime)
	}
	policy.AllowedOperations = operations
	policy.Issuer = strings.TrimSpace(issuer)
	policy.ProposalTTL = ttl
	policy.MaxRequests = maxRequests
	policy.BudgetWindow = budgetWindow
	return policy, nil
}

func parseMutationWindow(raw any, field string) (time.Duration, error) {
	if raw == nil {
		return 0, fmt.Errorf("mutation_policy.%s is required", field)
	}
	var duration time.Duration
	switch value := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("mutation_policy.%s must be a duration such as 30m", field)
		}
		duration = parsed
	case int:
		duration = time.Duration(value) * time.Second
	case int64:
		duration = time.Duration(value) * time.Second
	case float64:
		if value != float64(int64(value)) {
			return 0, fmt.Errorf("mutation_policy.%s must be a whole number of seconds or a duration string", field)
		}
		duration = time.Duration(int64(value)) * time.Second
	default:
		return 0, fmt.Errorf("mutation_policy.%s must be a duration string or seconds", field)
	}
	if duration <= 0 || duration > maxCrawlPolicyBudget {
		return 0, fmt.Errorf("mutation_policy.%s must be within the supported range", field)
	}
	if field == "budget_window" && duration > maxCrawlPolicyBudget {
		return 0, fmt.Errorf("mutation_policy.%s must be within the supported range", field)
	}
	return duration, nil
}

// normalizeMutationPolicy reads the optional trusted policy from step params
// first, then ai config. A missing policy is disabled and fails closed.
func normalizeMutationPolicy(params, config map[string]any) (*MutationPolicy, error) {
	var raw any
	if params != nil {
		if value, ok := params["mutation_policy"]; ok && value != nil {
			raw = value
		}
	}
	if raw == nil {
		if aiSection := mapStringAny(config["ai"]); len(aiSection) > 0 {
			if value, ok := aiSection["mutation_policy"]; ok && value != nil {
				raw = value
			}
		}
	}
	if raw == nil {
		if value, ok := config["mutation_policy"]; ok && value != nil {
			raw = value
		}
	}
	if raw == nil {
		return &MutationPolicy{Enabled: false}, nil
	}
	if err := rejectInputMarkers(raw); err != nil {
		return nil, fmt.Errorf("mutation_policy must be statically declared")
	}
	return parseMutationPolicyRaw(raw)
}

func requiredInt(values map[string]any, key string) (int, error) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, fmt.Errorf("mutation_policy.%s is required", key)
	}
	switch value := raw.(type) {
	case int:
		return value, nil
	case int64:
		return int(value), nil
	case float64:
		if value != float64(int64(value)) {
			return 0, fmt.Errorf("mutation_policy.%s must be an integer", key)
		}
		return int(value), nil
	default:
		return 0, fmt.Errorf("mutation_policy.%s must be an integer", key)
	}
}

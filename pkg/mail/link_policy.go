package mail

import (
	"net"
	"net/url"
	"strings"
)

// LinkDecision describes what the mail pipeline may do with a discovered link.
// Recording a link only retains its metadata; only LinkDecisionEnqueue permits
// handing the URL to a remote-fetch queue.
type LinkDecision string

const (
	LinkDecisionIgnore     LinkDecision = "ignore"
	LinkDecisionRecordOnly LinkDecision = "record-only"
	LinkDecisionEnqueue    LinkDecision = "enqueue"
)

// LinkPolicyEvaluator applies one LinkPolicy to the links in a single message.
// Evaluators are intentionally message-scoped because they enforce the
// per-message link limit.
type LinkPolicyEvaluator struct {
	policy LinkPolicy
	seen   int
}

// NewLinkPolicyEvaluator returns a message-scoped evaluator. Policy slices are
// copied so a caller cannot change a decision policy while it is being used.
func NewLinkPolicyEvaluator(policy LinkPolicy) *LinkPolicyEvaluator {
	policy.AllowedSchemes = append([]string(nil), policy.AllowedSchemes...)
	policy.Allowlist = append([]string(nil), policy.Allowlist...)
	policy.Denylist = append([]string(nil), policy.Denylist...)
	return &LinkPolicyEvaluator{policy: policy}
}

// Evaluate returns the policy decision for link and consumes one slot from the
// message's link budget. Denylists and hard safety rules take precedence over
// allowlists and remote-follow opt-in.
func (evaluator *LinkPolicyEvaluator) Evaluate(link Link) LinkDecision {
	if evaluator == nil {
		return LinkDecisionRecordOnly
	}

	evaluator.seen++
	if evaluator.policy.MaxLinksPerMessage <= 0 || evaluator.seen > evaluator.policy.MaxLinksPerMessage {
		return LinkDecisionIgnore
	}
	if !evaluator.policy.Extract {
		return LinkDecisionIgnore
	}

	// Reclassify from the URL instead of trusting caller-supplied metadata;
	// otherwise a forged "normal" classification could enqueue an action URL.
	classification := ClassifyLink(link.URL)
	parsed, safeRemote := parseSafeRemoteLink(link.URL)
	if !safeRemote {
		return LinkDecisionIgnore
	}

	if classification == LinkUnknown || classification == LinkMailto || classification == LinkCID {
		return LinkDecisionIgnore
	}
	if evaluator.policy.SuppressUnsubscribe && classification == LinkUnsubscribe {
		return LinkDecisionIgnore
	}

	host := normalizePolicyHost(parsed.Hostname())
	if matchesHostList(host, evaluator.policy.Denylist) {
		return LinkDecisionIgnore
	}

	// Links that mutate account or subscription state are retained for audit,
	// but are never automatically fetched.
	if classification == LinkAuthAction || classification == LinkUnsubscribe {
		return LinkDecisionRecordOnly
	}
	if !evaluator.policy.FollowRemote {
		return LinkDecisionRecordOnly
	}
	if !matchesScheme(parsed.Scheme, evaluator.policy.AllowedSchemes) {
		return LinkDecisionRecordOnly
	}
	if len(evaluator.policy.Allowlist) > 0 && !matchesHostList(host, evaluator.policy.Allowlist) {
		return LinkDecisionRecordOnly
	}

	return LinkDecisionEnqueue
}

// EvaluateURL classifies and evaluates a raw URL.
func (evaluator *LinkPolicyEvaluator) EvaluateURL(rawURL string) LinkDecision {
	return evaluator.Evaluate(Link{URL: rawURL, Classification: ClassifyLink(rawURL)})
}

// Seen returns the number of links presented to this message-scoped evaluator.
func (evaluator *LinkPolicyEvaluator) Seen() int {
	if evaluator == nil {
		return 0
	}
	return evaluator.seen
}

func parseSafeRemoteLink(rawURL string) (*url.URL, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || containsUnsafeURLCharacter(rawURL) {
		return nil, false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, false
	}
	if parsed.Hostname() == "" {
		return nil, false
	}
	return parsed, true
}

func matchesScheme(scheme string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), scheme) {
			return true
		}
	}
	return false
}

func matchesHostList(host string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = normalizePolicyHost(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "*.") {
			root := strings.TrimPrefix(pattern, "*.")
			if host != root && strings.HasSuffix(host, "."+root) {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

func normalizePolicyHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return host
}

package mail

import "testing"

func TestLinkPolicyDecisionValuesAreStable(t *testing.T) {
	t.Parallel()

	decisions := []LinkDecision{LinkDecisionIgnore, LinkDecisionRecordOnly, LinkDecisionEnqueue}
	wanted := []string{"ignore", "record-only", "enqueue"}
	for index, decision := range decisions {
		if string(decision) != wanted[index] {
			t.Errorf("decision %d = %q, want %q", index, decision, wanted[index])
		}
	}
}

func TestLinkPolicyUsesSafeRecordOnlyDefault(t *testing.T) {
	t.Parallel()

	policy := DefaultSourceConfig().Extraction.Links
	evaluator := NewLinkPolicyEvaluator(policy)
	if got := evaluator.EvaluateURL("https://example.test/article"); got != LinkDecisionRecordOnly {
		t.Fatalf("default decision = %q, want %q", got, LinkDecisionRecordOnly)
	}
}

func TestLinkPolicyRequiresExplicitRemoteOptIn(t *testing.T) {
	t.Parallel()

	policy := DefaultSourceConfig().Extraction.Links
	policy.Allowlist = []string{"example.test"}

	withoutOptIn := NewLinkPolicyEvaluator(policy)
	if got := withoutOptIn.EvaluateURL("https://example.test/article"); got != LinkDecisionRecordOnly {
		t.Errorf("decision without opt-in = %q, want %q", got, LinkDecisionRecordOnly)
	}

	policy.FollowRemote = true
	withOptIn := NewLinkPolicyEvaluator(policy)
	if got := withOptIn.EvaluateURL("https://example.test/article"); got != LinkDecisionEnqueue {
		t.Errorf("decision with opt-in = %q, want %q", got, LinkDecisionEnqueue)
	}
}

func TestLinkPolicyPrecedence(t *testing.T) {
	t.Parallel()

	policy := LinkPolicy{
		Extract:             true,
		FollowRemote:        true,
		AllowedSchemes:      []string{"https"},
		Allowlist:           []string{"*.example.test", "blocked.test"},
		Denylist:            []string{"blocked.test", "private.example.test"},
		MaxLinksPerMessage:  20,
		SuppressUnsubscribe: true,
	}
	tests := []struct {
		name string
		url  string
		want LinkDecision
	}{
		{name: "allowlisted subdomain", url: "https://news.example.test/article", want: LinkDecisionEnqueue},
		{name: "wildcard does not include apex", url: "https://example.test/article", want: LinkDecisionRecordOnly},
		{name: "allowlist miss is retained", url: "https://other.test/article", want: LinkDecisionRecordOnly},
		{name: "denylist beats explicit allowlist", url: "https://blocked.test/article", want: LinkDecisionIgnore},
		{name: "denylist beats wildcard allowlist", url: "https://private.example.test/article", want: LinkDecisionIgnore},
		{name: "unsubscribe suppression beats allowlist", url: "https://news.example.test/unsubscribe?token=secret", want: LinkDecisionIgnore},
		{name: "disallowed safe scheme is retained", url: "http://news.example.test/article", want: LinkDecisionRecordOnly},
	}

	evaluator := NewLinkPolicyEvaluator(policy)
	for _, test := range tests {
		if got := evaluator.EvaluateURL(test.url); got != test.want {
			t.Errorf("%s: EvaluateURL(%q) = %q, want %q", test.name, test.url, got, test.want)
		}
	}
}

func TestLinkPolicyMaximumLinksPerMessage(t *testing.T) {
	t.Parallel()

	evaluator := NewLinkPolicyEvaluator(LinkPolicy{
		Extract:            true,
		FollowRemote:       true,
		AllowedSchemes:     []string{"https"},
		MaxLinksPerMessage: 2,
	})

	if got := evaluator.EvaluateURL("https://one.example.test"); got != LinkDecisionEnqueue {
		t.Errorf("first decision = %q, want %q", got, LinkDecisionEnqueue)
	}
	if got := evaluator.EvaluateURL("https://two.example.test"); got != LinkDecisionEnqueue {
		t.Errorf("second decision = %q, want %q", got, LinkDecisionEnqueue)
	}
	if got := evaluator.EvaluateURL("https://three.example.test"); got != LinkDecisionIgnore {
		t.Errorf("over-limit decision = %q, want %q", got, LinkDecisionIgnore)
	}
	if got := evaluator.Seen(); got != 3 {
		t.Errorf("Seen() = %d, want 3", got)
	}
}

func TestLinkPolicyIgnoresUnsafeSchemes(t *testing.T) {
	t.Parallel()

	policy := LinkPolicy{
		Extract:            true,
		FollowRemote:       true,
		AllowedSchemes:     []string{"https", "javascript", "data", "file"},
		MaxLinksPerMessage: 10,
	}
	for _, rawURL := range []string{
		"javascript:alert(document.cookie)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"https://user:password@example.test/private",
	} {
		if got := NewLinkPolicyEvaluator(policy).EvaluateURL(rawURL); got != LinkDecisionIgnore {
			t.Errorf("EvaluateURL(%q) = %q, want %q", rawURL, got, LinkDecisionIgnore)
		}
	}
}

func TestLinkPolicyNeverEnqueuesAuthenticationActions(t *testing.T) {
	t.Parallel()

	policy := LinkPolicy{
		Extract:            true,
		FollowRemote:       true,
		AllowedSchemes:     []string{"https"},
		Allowlist:          []string{"accounts.example.test"},
		MaxLinksPerMessage: 10,
	}
	for _, rawURL := range []string{
		"https://accounts.example.test/reset-password?token=secret",
		"https://accounts.example.test/verify?code=secret",
		"https://accounts.example.test/magic-link/secret",
	} {
		// Supply a forged classification to verify that the evaluator derives
		// action semantics itself rather than trusting upstream metadata.
		link := Link{URL: rawURL, Classification: LinkNormal}
		if got := NewLinkPolicyEvaluator(policy).Evaluate(link); got != LinkDecisionRecordOnly {
			t.Errorf("Evaluate(%q) = %q, want %q", rawURL, got, LinkDecisionRecordOnly)
		}
	}
}

func TestLinkPolicyUnsubscribeCanBeRecordedButNotEnqueued(t *testing.T) {
	t.Parallel()

	policy := LinkPolicy{
		Extract:            true,
		FollowRemote:       true,
		AllowedSchemes:     []string{"https"},
		MaxLinksPerMessage: 10,
	}
	if got := NewLinkPolicyEvaluator(policy).EvaluateURL("https://example.test/unsubscribe?id=42"); got != LinkDecisionRecordOnly {
		t.Fatalf("unsuppressed unsubscribe decision = %q, want %q", got, LinkDecisionRecordOnly)
	}

	policy.SuppressUnsubscribe = true
	if got := NewLinkPolicyEvaluator(policy).EvaluateURL("https://example.test/unsubscribe?id=42"); got != LinkDecisionIgnore {
		t.Fatalf("suppressed unsubscribe decision = %q, want %q", got, LinkDecisionIgnore)
	}
}

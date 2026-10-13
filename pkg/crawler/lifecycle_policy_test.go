package crawler

import (
	"testing"
)

// Characterization matrix for the historical policy coupling.
// These tests pin existing behavior; they do not endorse it.
// Changing any row requires explicit owner approval.
func TestLifecyclePolicyMatrix(t *testing.T) {
	policies := []string{"always", "never", "on_start", "", "none", "Always", " ALWAYS "}
	for _, p := range policies {
		wantReset := p == "always"
		if got := resetSessionForUserAgent(p); got != wantReset {
			t.Errorf("resetSessionForUserAgent(%q) = %t, want %t", p, got, wantReset)
		}
		if got := refreshUserAgentOnSetup(p); got != wantReset {
			t.Errorf("refreshUserAgentOnSetup(%q) = %t, want %t", p, got, wantReset)
		}
	}
}

func TestLifecyclePoliciesAreIndependentFlags(t *testing.T) {
	// The two decisions read different settings: a test-only matrix proving
	// they can diverge (e.g. UA rotation without cookie reset and vice versa).
	if !resetSessionForUserAgent("always") || refreshUserAgentOnSetup("never") {
		t.Error("expected reset=true/setup=false for (always, never)")
	}
	if resetSessionForUserAgent("never") || !refreshUserAgentOnSetup("always") {
		t.Error("expected reset=false/setup=true for (never, always)")
	}
}

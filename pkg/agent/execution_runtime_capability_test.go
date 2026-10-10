package agent

import "testing"

func TestRequiredCapabilityForAIInteraction(t *testing.T) {
	got, known := requiredCapabilityForAction("AIInteraction")
	if !known || got != "ai_reasoning" {
		t.Fatalf("expected (ai_reasoning, true), got (%q, %v)", got, known)
	}
}

func TestCapabilityAllowedAIAliases(t *testing.T) {
	identity := AgentIdentity{Capabilities: []string{"ai_interaction"}}
	if !capabilityAllowed(identity, "AIInteraction") {
		t.Fatalf("expected legacy alias ai_interaction to remain supported")
	}

	identity.Capabilities = []string{"ai_reasoning"}
	if !capabilityAllowed(identity, "AIInteraction") {
		t.Fatalf("expected ai_reasoning to be supported")
	}
}

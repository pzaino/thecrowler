package agent

import "testing"

func TestRequiredCapabilityForAIInteraction(t *testing.T) {
	if got := requiredCapabilityForAction("AIInteraction"); got != "ai_reasoning" {
		t.Fatalf("expected ai_reasoning, got %s", got)
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

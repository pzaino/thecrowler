package ruleset

import "testing"

func TestValidateAgentCall(t *testing.T) {
	valid := &AgentCall{AgentName: "a", Timeout: 1, OnError: "fail", MergeStrategy: "merge"}
	if err := ValidateAgentCall(valid); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	cases := []AgentCall{
		{Timeout: 1},
		{AgentName: "a", Timeout: 0},
		{AgentName: "a", Timeout: 1, OnError: "bad"},
		{AgentName: "a", Timeout: 1, MergeStrategy: "bad"},
	}
	for _, c := range cases {
		if ValidateAgentCall(&c) == nil {
			t.Fatalf("expected error for %#v", c)
		}
	}
}

func TestNormalizeAgentCallDefaults(t *testing.T) {
	ac := &AgentCall{AgentName: "a", Timeout: 1}
	NormalizeAgentCallDefaults(ac)
	if ac.OnError != "fail" || ac.MergeStrategy != "replace" {
		t.Fatalf("bad defaults: %#v", ac)
	}
}

func TestHasGetAgentCall(t *testing.T) {
	ac := &AgentCall{AgentName: "a", Timeout: 1}
	s := Selector{AgentCall: ac}
	if !s.HasAgentCall() || s.GetAgentCall() == nil {
		t.Fatal("selector")
	}
	w := WaitCondition{AgentCall: ac}
	if !w.HasAgentCall() || w.GetAgentCall() == nil {
		t.Fatal("wait")
	}
	p := PostProcessingStep{AgentCall: ac}
	if !p.HasAgentCall() || p.GetAgentCall() == nil {
		t.Fatal("post")
	}
	tg := TargetElement{AgentCall: ac}
	if !tg.HasAgentCall() || tg.GetAgentCall() == nil {
		t.Fatal("target")
	}
}

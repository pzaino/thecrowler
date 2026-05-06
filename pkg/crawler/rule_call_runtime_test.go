package crawler

import "testing"

func TestRuleCallRuntimeStateDepthAndBudget(t *testing.T) {
	s := newRuleCallRuntimeState()
	s.maxDepth = 1
	s.maxPerSource = 1
	if err := s.beforeCall("1", "loop-a"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := s.beforeCall("1", "loop-b"); err == nil {
		t.Fatal("expected depth error")
	}
	s.afterCall("loop-a")
	if err := s.beforeCall("1", "loop-c"); err == nil {
		t.Fatal("expected per-source budget error")
	}
}

func TestRuleCallRuntimeStateLoopDetection(t *testing.T) {
	s := newRuleCallRuntimeState()
	if err := s.beforeCall("1", "loop-a"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := s.beforeCall("1", "loop-a"); err == nil {
		t.Fatal("expected loop detection error")
	}
}


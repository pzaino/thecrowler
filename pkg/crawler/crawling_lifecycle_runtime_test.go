package crawler

import "testing"

func TestLifecycleRuntimeStateGuardrails(t *testing.T) {
	s := newLifecycleRuntimeState(1)
	if err := s.beforeCall("src1|page", "", "r1"); err != nil { t.Fatal(err) }
	s.afterCall("r1")
	if err := s.beforeCall("src1|page", "", "r2"); err == nil { t.Fatal("expected exhaustion") }
}

func TestLifecycleRuntimeStateDedupAndRecursion(t *testing.T) {
	s := newLifecycleRuntimeState(3)
	if err := s.beforeCall("src1|fuzz", "d1", "r1"); err != nil { t.Fatal(err) }
	if err := s.beforeCall("src1|fuzz", "d2", "r1"); err == nil { t.Fatal("expected recursion") }
	s.afterCall("r1")
	if err := s.beforeCall("src1|fuzz", "d1", "r2"); err != nil { t.Fatalf("dedup should no-op, got %v", err) }
}

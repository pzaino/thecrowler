package agent

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// backdateBudget moves a budget's start into the past to deterministically
// simulate an exhausted deadline without sleeping.
func backdateBudget(t *testing.T, bm *constraintBudgetManager, ago time.Duration) {
	t.Helper()
	if bm == nil {
		t.Fatalf("nil budget")
	}
	bm.startedAt = time.Now().UTC().Add(-ago)
	if bm.hasTimeBudget {
		if bm.cancel != nil {
			bm.cancel()
		}
		ctx, cancel := context.WithDeadline(context.Background(), bm.startedAt.Add(bm.timeBudget))
		bm.ctx, bm.cancel = ctx, cancel
	}
}

// TestDeadlineExceededBeforeStep denies a step whose budget already lapsed.
func TestDeadlineExceededBeforeStep(t *testing.T) {
	bm, err := newConstraintBudgetManager(AgentIdentity{
		Constraints: &AgentConstraints{TimeBudget: "1h"},
	})
	if err != nil {
		t.Fatalf("budget setup: %v", err)
	}
	defer bm.release()
	backdateBudget(t, bm, 2*time.Hour)
	if err := bm.preStepCheck("AIInteraction"); err == nil ||
		!strings.Contains(err.Error(), "time_budget exceeded") {
		t.Fatalf("expected time_budget denial, got %v", err)
	}
}

// TestDeadlineExceededBetweenSteps proves the second step re-checks a
// practically exhausted deadline. The 1ns budget lapses before any second
// attempt can start.
func TestDeadlineExceededBetweenSteps(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "AIInteraction"})
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "dl-agent", Name: "DL Agent", TrustLevel: "trusted",
			Capabilities: []string{"ai_reasoning"},
			Constraints:  &AgentConstraints{TimeBudget: "1ns"},
		},
		Jobs: []Job{{Name: "DL Agent", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AIInteraction", "params": map[string]interface{}{}},
				{"action": "AIInteraction", "params": map[string]interface{}{}},
			}}},
	}
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "time_budget exceeded") {
		t.Fatalf("expected deadline denial between steps, got %v", err)
	}
}

// touchDetectingHandler fails the test if the driver is ever reached.
type touchDetectingHandler struct {
	fakeDBHandler
	touched bool
}

func (h *touchDetectingHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	h.touched = true
	return nil, stringErr("must not reach driver")
}

var _ cdb.Handler = &touchDetectingHandler{}

// TestCreateEventRespectsExpiredDeadline is fully deterministic: with an
// already-expired propagated deadline the action fails fast and never
// touches the driver.
func TestCreateEventRespectsExpiredDeadline(t *testing.T) {
	touching := &touchDetectingHandler{}
	params := map[string]interface{}{
		StrRequest: map[string]interface{}{},
		StrConfig: map[string]interface{}{
			"db_handler": touching,
			cfgKeyAgentRuntime: map[string]interface{}{
				groupDeadlineKey: time.Now().UTC().Add(-time.Minute).UnixNano(),
			},
		},
		"event_type": "test.event",
	}

	a := &CreateEventAction{}
	_, err := a.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "time_budget exceeded") {
		t.Fatalf("expected fast deadline denial, got %v", err)
	}
	if touching.touched {
		t.Fatalf("expired deadline must not reach the driver")
	}
}

// TestEventTimeoutClamp pins the min(own bound, remaining budget) rule.
func TestEventTimeoutClamp(t *testing.T) {
	far := map[string]interface{}{
		StrConfig: map[string]interface{}{
			cfgKeyAgentRuntime: map[string]interface{}{
				groupDeadlineKey: time.Now().UTC().Add(time.Hour).UnixNano(),
			},
		},
	}
	timeout, ok := eventActionTimeout(far)
	if !ok || timeout != createEventTimeout {
		t.Fatalf("far deadline must keep own bound, got %v, %v", timeout, ok)
	}
	near := map[string]interface{}{
		StrConfig: map[string]interface{}{
			cfgKeyAgentRuntime: map[string]interface{}{
				groupDeadlineKey: time.Now().UTC().Add(time.Second).UnixNano(),
			},
		},
	}
	timeout, ok = eventActionTimeout(near)
	if !ok || timeout <= 0 || timeout > createEventTimeout {
		t.Fatalf("near deadline must clamp inside (0, own bound], got %v, %v", timeout, ok)
	}
	absent := map[string]interface{}{}
	timeout, ok = eventActionTimeout(absent)
	if !ok || timeout != createEventTimeout {
		t.Fatalf("absent deadline must keep own bound, got %v, %v", timeout, ok)
	}
}

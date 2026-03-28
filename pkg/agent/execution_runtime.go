package agent

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	cfgKeyAgentRuntime = "agent_runtime"
	cfgKeyAgent        = "agent"
)

// AgentExecutionContext captures per-run identity metadata and correlation fields.
type AgentExecutionContext struct {
	RunID            string        `json:"run_id"`
	TraceID          string        `json:"trace_id"`
	Source           string        `json:"source,omitempty"`
	Owner            string        `json:"owner,omitempty"`
	IdentitySnapshot AgentIdentity `json:"identity_snapshot"`
	StartedAt        time.Time     `json:"started_at"`
}

type constraintBudgetManager struct {
	startedAt      time.Time
	timeBudget     time.Duration
	hasTimeBudget  bool
	maxSteps       int
	eventRateLimit float64
	executedSteps  int
	eventsCreated  int
}

func newConstraintBudgetManager(identity AgentIdentity) (*constraintBudgetManager, error) {
	bm := &constraintBudgetManager{startedAt: time.Now().UTC()}
	if identity.Constraints == nil {
		return bm, nil
	}

	bm.maxSteps = identity.Constraints.MaxSteps
	bm.eventRateLimit = identity.Constraints.EventRateLimit
	if tb := strings.TrimSpace(identity.Constraints.TimeBudget); tb != "" {
		d, err := time.ParseDuration(tb)
		if err != nil {
			return nil, fmt.Errorf("invalid time_budget %q: %w", tb, err)
		}
		bm.timeBudget = d
		bm.hasTimeBudget = true
	}
	return bm, nil
}

func (bm *constraintBudgetManager) preStepCheck(actionName string) error {
	if bm == nil {
		return nil
	}
	if bm.maxSteps > 0 && bm.executedSteps >= bm.maxSteps {
		return fmt.Errorf("constraint gate denied action %s: max_steps exceeded (%d)", actionName, bm.maxSteps)
	}
	if bm.hasTimeBudget && time.Since(bm.startedAt) > bm.timeBudget {
		return fmt.Errorf("constraint gate denied action %s: time_budget exceeded (%s)", actionName, bm.timeBudget)
	}
	if strings.EqualFold(actionName, "CreateEvent") && bm.eventRateLimit > 0 {
		maxEvents := int(math.Ceil(bm.eventRateLimit))
		if maxEvents <= 0 {
			maxEvents = 1
		}
		if bm.eventsCreated >= maxEvents {
			return fmt.Errorf("constraint gate denied action %s: event_rate_limit exceeded (%.2f)", actionName, bm.eventRateLimit)
		}
	}
	return nil
}

func (bm *constraintBudgetManager) markActionExecuted(actionName string) {
	if bm == nil {
		return
	}
	bm.executedSteps++
	if strings.EqualFold(actionName, "CreateEvent") {
		bm.eventsCreated++
	}
}

func runtimeFlagsFromConfig(iCfg map[string]any) cfg.AgentRuntimeConfig {
	flags := cfg.AgentRuntimeConfig{}
	if iCfg == nil {
		return flags
	}

	decode := func(raw any) {
		switch v := raw.(type) {
		case cfg.AgentRuntimeConfig:
			flags = v
		case *cfg.AgentRuntimeConfig:
			if v != nil {
				flags = *v
			}
		case map[string]any:
			if b, ok := v["identity_enforcement"].(bool); ok {
				flags.IdentityEnforcement = b
			}
			if b, ok := v["contract_enforcement"].(bool); ok {
				flags.ContractEnforcement = b
			}
			if b, ok := v["memory_runtime"].(bool); ok {
				flags.MemoryRuntime = b
			}
		}
	}

	if raw, ok := iCfg[cfgKeyAgentRuntime]; ok {
		decode(raw)
	}
	if raw, ok := iCfg[cfgKeyAgent]; ok {
		decode(raw)
	}
	return flags
}

func newAgentExecutionContext(identity AgentIdentity, source string) AgentExecutionContext {
	return AgentExecutionContext{
		RunID:            uuid.NewString(),
		TraceID:          uuid.NewString(),
		Source:           source,
		Owner:            identity.Owner,
		IdentitySnapshot: identity,
		StartedAt:        time.Now().UTC(),
	}
}

func requiredCapabilityForAction(actionName string) string {
	switch strings.TrimSpace(actionName) {
	case "RunCommand":
		return "run_command"
	case "DBQuery":
		return "db_query"
	case "AIInteraction":
		return "ai_interaction"
	case "PluginExecution":
		return "plugin_execution"
	case "CreateEvent":
		return "create_event"
	case "Decision":
		return "decision"
	default:
		return ""
	}
}

func capabilityAllowed(identity AgentIdentity, actionName string) bool {
	required := requiredCapabilityForAction(actionName)
	if required == "" {
		return true
	}
	if len(identity.Capabilities) == 0 {
		return false
	}
	for _, capability := range identity.Capabilities {
		normalized := strings.ToLower(strings.TrimSpace(capability))
		if normalized == "all" || normalized == required {
			return true
		}
	}
	return false
}

func trustLevelRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "privileged", "high", "internal":
		return 3
	case "trusted", "medium":
		return 2
	default:
		return 1
	}
}

func minTrustRankForAction(actionName string) int {
	switch strings.TrimSpace(actionName) {
	case "RunCommand", "DBQuery", "PluginExecution":
		return 2
	default:
		return 1
	}
}

func trustAllowed(identity AgentIdentity, actionName string) bool {
	return trustLevelRank(identity.TrustLevel) >= minTrustRankForAction(actionName)
}

func applyExecutionContext(params map[string]interface{}, ctx AgentExecutionContext) {
	if params == nil {
		return
	}
	configMap, _ := params[StrConfig].(map[string]interface{})
	if configMap == nil {
		configMap = map[string]interface{}{}
		params[StrConfig] = configMap
	}
	runtimeMap, _ := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	if runtimeMap == nil {
		runtimeMap = map[string]interface{}{}
		switch existing := configMap[cfgKeyAgentRuntime].(type) {
		case cfg.AgentRuntimeConfig:
			runtimeMap["identity_enforcement"] = existing.IdentityEnforcement
			runtimeMap["contract_enforcement"] = existing.ContractEnforcement
			runtimeMap["memory_runtime"] = existing.MemoryRuntime
		case *cfg.AgentRuntimeConfig:
			if existing != nil {
				runtimeMap["identity_enforcement"] = existing.IdentityEnforcement
				runtimeMap["contract_enforcement"] = existing.ContractEnforcement
				runtimeMap["memory_runtime"] = existing.MemoryRuntime
			}
		}
	}
	runtimeMap["run_id"] = ctx.RunID
	runtimeMap["trace_id"] = ctx.TraceID
	runtimeMap["source"] = ctx.Source
	runtimeMap["owner"] = ctx.Owner
	runtimeMap["identity_snapshot"] = ctx.IdentitySnapshot
	configMap[cfgKeyAgentRuntime] = runtimeMap
}

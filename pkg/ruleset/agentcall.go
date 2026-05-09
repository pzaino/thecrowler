package ruleset

import (
	"fmt"
	"strings"
)

type AgentCall struct {
	AgentName     string                 `json:"agent_name" yaml:"agent_name"`
	Params        map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`
	Timeout       int                    `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	OnError       string                 `json:"on_error,omitempty" yaml:"on_error,omitempty"`
	StoreAs       string                 `json:"store_as,omitempty" yaml:"store_as,omitempty"`
	MergeStrategy string                 `json:"merge_strategy,omitempty" yaml:"merge_strategy,omitempty"`
}

func (s *Selector) HasAgentCall() bool { return s != nil && s.AgentCall != nil }
func (s *Selector) GetAgentCall() *AgentCall {
	if s == nil {
		return nil
	}
	return s.AgentCall
}
func (w *WaitCondition) HasAgentCall() bool { return w != nil && w.AgentCall != nil }
func (w *WaitCondition) GetAgentCall() *AgentCall {
	if w == nil {
		return nil
	}
	return w.AgentCall
}
func (p *PostProcessingStep) HasAgentCall() bool { return p != nil && p.AgentCall != nil }
func (p *PostProcessingStep) GetAgentCall() *AgentCall {
	if p == nil {
		return nil
	}
	return p.AgentCall
}
func (t *TargetElement) HasAgentCall() bool { return t != nil && t.AgentCall != nil }
func (t *TargetElement) GetAgentCall() *AgentCall {
	if t == nil {
		return nil
	}
	return t.AgentCall
}

func ValidateAgentCall(ac *AgentCall) error {
	if ac == nil {
		return nil
	}
	if strings.TrimSpace(ac.AgentName) == "" {
		return fmt.Errorf("agent_name is required")
	}
	if ac.Timeout < 0 || ac.Timeout == 0 {
		return fmt.Errorf("timeout must be >= 1")
	}
	if ac.OnError != "" {
		switch ac.OnError {
		case "fail", "continue", "ignore":
		default:
			return fmt.Errorf("invalid on_error: %s", ac.OnError)
		}
	}
	if ac.MergeStrategy != "" {
		switch ac.MergeStrategy {
		case "replace", "merge", "append", "ignore":
		default:
			return fmt.Errorf("invalid merge_strategy: %s", ac.MergeStrategy)
		}
	}
	return nil
}

func NormalizeAgentCallDefaults(ac *AgentCall, familyDefaults ...AgentCall) {
	if ac == nil {
		return
	}
	if len(familyDefaults) > 0 {
		d := familyDefaults[0]
		if ac.OnError == "" {
			ac.OnError = d.OnError
		}
		if ac.MergeStrategy == "" {
			ac.MergeStrategy = d.MergeStrategy
		}
	}
	if ac.OnError == "" {
		ac.OnError = "fail"
	}
	if ac.MergeStrategy == "" {
		ac.MergeStrategy = "replace"
	}
}

package crawler

import (
	"fmt"
	"strings"
	"time"

	agent "github.com/pzaino/thecrowler/pkg/agent"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type RuleCallKind string

const (
	RuleCallKindPlugin RuleCallKind = "plugin_call"
	RuleCallKindAgent  RuleCallKind = "agent_call"
)

type RuleCallRequest struct {
	Kind       RuleCallKind
	PluginName string
	AgentName  string
	Params     map[string]interface{}
	TimeoutSec int
	OnError    string
	StoreAs    string
	Caller     string
}

type RuleCallResult struct {
	Kind     RuleCallKind           `json:"kind"`
	Name     string                 `json:"name"`
	Success  bool                   `json:"success"`
	TimedOut bool                   `json:"timed_out"`
	Value    interface{}            `json:"value,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

func executeRuleCall(ctx *ProcessContext, wd *vdi.WebDriver, req RuleCallRequest) RuleCallResult {
	result := RuleCallResult{Kind: req.Kind, Success: false, Metadata: map[string]interface{}{"caller": req.Caller}}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 30
	}
	if req.OnError == "" {
		req.OnError = "fail"
	}

	for k, v := range req.Params {
		if s, ok := v.(string); ok {
			req.Params[k] = interpolateRuleCallParam(ctx, wd, s)
		}
	}

	start := time.Now()
	done := make(chan struct{})
	var val interface{}
	var err error
	go func() {
		defer close(done)
		switch req.Kind {
		case RuleCallKindPlugin:
			result.Name = req.PluginName
			val, err = executePluginCall(ctx, wd, req.PluginName, req.Params)
		case RuleCallKindAgent:
			result.Name = req.AgentName
			val, err = executeAgentCall(req.AgentName, req.Params)
		default:
			err = fmt.Errorf("unsupported rule call kind: %s", req.Kind)
		}
	}()

	select {
	case <-done:
		result.Metadata["duration_ms"] = time.Since(start).Milliseconds()
	case <-time.After(time.Duration(req.TimeoutSec) * time.Second):
		result.TimedOut = true
		result.Error = "timeout"
		result.Metadata["duration_ms"] = time.Since(start).Milliseconds()
		if req.OnError == "continue" || req.OnError == "ignore" {
			return result
		}
		return result
	}

	if err != nil {
		result.Error = err.Error()
		cmn.DebugMsg(cmn.DbgLvlError, "rule call failed kind=%s name=%s caller=%s err=%v", req.Kind, result.Name, req.Caller, err)
		if req.OnError == "continue" || req.OnError == "ignore" {
			return result
		}
		return result
	}
	result.Value = val
	result.Success = true
	cmn.DebugMsg(cmn.DbgLvlDebug3, "rule call ok kind=%s name=%s caller=%s", req.Kind, result.Name, req.Caller)
	return result
}

func interpolateRuleCallParam(ctx *ProcessContext, wd *vdi.WebDriver, s string) interface{} {
	t := strings.TrimSpace(strings.ToLower(s))
	switch t {
	case "%current_url%":
		u, _ := (*wd).CurrentURL()
		return u
	case "%source_url%":
		if ctx != nil && ctx.source != nil {
			return ctx.source.URL
		}
	}
	return s
}

func executePluginCall(ctx *ProcessContext, wd *vdi.WebDriver, pluginName string, _ map[string]interface{}) (interface{}, error) {
	plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginName)
	}
	return (*wd).ExecuteScript(plugin.String(), nil)
}

func executeAgentCall(agentName string, params map[string]interface{}) (interface{}, error) {
	lookup := strings.TrimSpace(agentName)
	if lookup == "" || strings.ContainsAny(lookup, `/\\`) {
		return nil, fmt.Errorf("invalid agent_name")
	}
	if agent.AgentsEngine == nil {
		return nil, fmt.Errorf("agent engine not initialized")
	}
	if _, ok := agent.AgentsEngine.GetAgentByName(lookup); !ok {
		return nil, fmt.Errorf("agent not found: %s", lookup)
	}
	return map[string]interface{}{"agent_name": lookup, "status": "ok"}, agent.AgentsEngine.ExecuteAgent(lookup, params)
}

func normalizeFromAgentCall(ac *rules.AgentCall, caller string) RuleCallRequest {
	rules.NormalizeAgentCallDefaults(ac)
	return RuleCallRequest{Kind: RuleCallKindAgent, AgentName: ac.AgentName, Params: ac.Params, TimeoutSec: ac.Timeout, OnError: ac.OnError, StoreAs: ac.StoreAs, Caller: caller}
}

package crawler

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	agent "github.com/pzaino/thecrowler/pkg/agent"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// RuleCallKind represents the type of rule call, either a plugin call or an agent call.
type RuleCallKind string

const (
	// RuleCallKindPlugin indicates a call to a JavaScript plugin.
	RuleCallKindPlugin RuleCallKind = "plugin_call"
	// RuleCallKindAgent indicates a call to an agent.
	RuleCallKindAgent RuleCallKind = "agent_call"
)

// RuleCallRequest represents a request to execute a rule call, including its kind, parameters, and metadata.
type RuleCallRequest struct {
	Kind       RuleCallKind
	PluginName string
	AgentName  string
	Params     map[string]interface{}
	TimeoutSec int
	OnError    string
	StoreAs    string
	Caller     string
	RuleFamily string
	RuleName   string
	RuleGroup  string
}

// RuleCallResult represents the result of a rule call, including its success status, any returned value, and metadata.
type RuleCallResult struct {
	Kind     RuleCallKind           `json:"kind"`
	Name     string                 `json:"name"`
	Success  bool                   `json:"success"`
	TimedOut bool                   `json:"timed_out"`
	Value    interface{}            `json:"value,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type ruleCallRuntimeState struct {
	mu            sync.Mutex
	activeDepth   int
	maxDepth      int
	callsBySource map[string]int
	maxPerSource  int
	activeLoops   map[string]struct{}
}

func newRuleCallRuntimeState() *ruleCallRuntimeState {
	return &ruleCallRuntimeState{maxDepth: 8, maxPerSource: 50, callsBySource: map[string]int{}, activeLoops: map[string]struct{}{}}
}

func (s *ruleCallRuntimeState) beforeCall(sourceID, loopKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.activeLoops[loopKey]; ok {
		return fmt.Errorf("rule call loop detected")
	}
	if s.activeDepth >= s.maxDepth {
		return fmt.Errorf("rule call nested depth exceeded")
	}
	s.activeDepth++
	s.callsBySource[sourceID]++
	if s.callsBySource[sourceID] > s.maxPerSource {
		s.activeDepth--
		return fmt.Errorf("rule call per-source budget exhausted")
	}
	s.activeLoops[loopKey] = struct{}{}
	return nil
}

func (s *ruleCallRuntimeState) afterCall(loopKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeDepth > 0 {
		s.activeDepth--
	}
	delete(s.activeLoops, loopKey)
}

func executeRuleCall(ctx *ProcessContext, wd *vdi.WebDriver, req RuleCallRequest) RuleCallResult {
	const (
		continueOnError = "continue"
		ignoreOnError   = "ignore"
	)
	result := RuleCallResult{Kind: req.Kind, Success: false, Metadata: map[string]interface{}{"caller": req.Caller}}
	if ctx != nil && ctx.ruleCallState == nil {
		ctx.ruleCallState = newRuleCallRuntimeState()
	}
	sourceID := "0"
	sourceURL := ""
	if ctx != nil && ctx.source != nil {
		sourceID = strconv.FormatUint(ctx.source.ID, 10)
		sourceURL = strings.TrimSpace(ctx.source.URL)
	}
	loopKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", req.Kind, req.Caller, req.RuleFamily, req.RuleGroup, req.RuleName, sourceID, sourceURL)
	if ctx != nil && ctx.ruleCallState != nil {
		if err := ctx.ruleCallState.beforeCall(sourceID, loopKey); err != nil {
			result.Error = err.Error()
			result.Metadata["status"] = "denied"
			return result
		}
		defer ctx.ruleCallState.afterCall(loopKey)
	}
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
		if req.OnError == continueOnError || req.OnError == ignoreOnError {
			return result
		}
		return result
	}

	if err != nil {
		result.Error = err.Error()
		cmn.DebugMsg(cmn.DbgLvlError, "rule call failed kind=%s name=%s caller=%s err=%v", req.Kind, result.Name, req.Caller, err)
		if req.OnError == continueOnError || req.OnError == ignoreOnError {
			return result
		}
		return result
	}
	result.Value = val
	result.Success = true
	result.Metadata["status"] = "success"
	result.Metadata["rule"] = map[string]string{"family": req.RuleFamily, "group": req.RuleGroup, "name": req.RuleName}
	result.Metadata["agent_name"] = req.AgentName
	result.Metadata["source_id"] = sourceID
	result.Metadata["source_url"] = sourceURL
	if result.Kind == RuleCallKindAgent {
		result.Value = map[string]interface{}{"agent_name": req.AgentName, "status": "ok"}
	}
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

func executePluginCall(ctx *ProcessContext, wd *vdi.WebDriver, pluginName string, params map[string]interface{}) (interface{}, error) {
	plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginName)
	}
	return plugin.Execute(wd, ctx.db, ctx.config.Plugins.PluginsTimeout, params)
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

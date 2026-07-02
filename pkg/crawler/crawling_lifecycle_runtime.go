package crawler

import (
	"fmt"
	"strings"
	"sync"

	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type lifecycleRuntimeState struct {
	mu                 sync.Mutex
	callsByScope       map[string]int
	seenDedup          map[string]struct{}
	activeRecursionKey map[string]struct{}
	maxPerScope        int
}

func newLifecycleRuntimeState(maxPerScope int) *lifecycleRuntimeState {
	if maxPerScope <= 0 {
		maxPerScope = 20
	}
	return &lifecycleRuntimeState{callsByScope: map[string]int{}, seenDedup: map[string]struct{}{}, activeRecursionKey: map[string]struct{}{}, maxPerScope: maxPerScope}
}

func (s *lifecycleRuntimeState) beforeCall(scope, dedupKey, recursionKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.activeRecursionKey[recursionKey]; ok {
		return fmt.Errorf("lifecycle recursion detected")
	}
	if dedupKey != "" {
		if _, ok := s.seenDedup[dedupKey]; ok {
			return nil
		}
		s.seenDedup[dedupKey] = struct{}{}
	}
	s.callsByScope[scope]++
	if s.callsByScope[scope] > s.maxPerScope {
		return fmt.Errorf("lifecycle call guardrail exhausted")
	}
	s.activeRecursionKey[recursionKey] = struct{}{}
	return nil
}
func (s *lifecycleRuntimeState) afterCall(recursionKey string) {
	s.mu.Lock()
	delete(s.activeRecursionKey, recursionKey)
	s.mu.Unlock()
}

func executeCrawlingLifecycleHook(ctx *ProcessContext, wd *vdi.WebDriver, state *lifecycleRuntimeState, rule rules.CrawlingRule, stage string, in map[string]interface{}) RuleCallResult {
	hook := rule.GetLifecycleHook(stage)
	if hook == nil || hook.AgentCall == nil {
		return RuleCallResult{Success: true, Metadata: map[string]interface{}{"stage": stage, "skipped": true}}
	}
	req := normalizeFromAgentCall(hook.AgentCall, "crawling.lifecycle."+stage)
	if req.Params == nil {
		req.Params = map[string]interface{}{}
	}
	ctxMap := map[string]interface{}{"stage": stage, "rule": rule.RuleName}
	for k, v := range in {
		ctxMap[k] = v
	}
	req.Params["crawl_context"] = ctxMap
	sourceID := ""
	sourceURL := ""
	if ctx != nil && ctx.source != nil {
		sourceID = fmt.Sprintf("%d", ctx.source.ID)
		sourceURL = strings.TrimSpace(ctx.source.URL)
	}
	recKey := fmt.Sprintf("%s|%s|%s|%s|%s", rule.RuleName, stage, req.AgentName, sourceID, sourceURL)
	scope := fmt.Sprintf("%s|%s", sourceID, stage)
	if err := state.beforeCall(scope, strings.TrimSpace(req.StoreAs), recKey); err != nil {
		return RuleCallResult{Success: false, Error: err.Error(), Metadata: map[string]interface{}{"stage": stage}}
	}
	defer state.afterCall(recKey)
	return executeRuleCall(ctx, wd, req)
}

// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"context"
	"fmt"
	"strings"
	"time"

	browseractions "github.com/pzaino/thecrowler/pkg/browser/actions"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	defaultActionRulesConfig = "{\"config\":\"default\"}"
	defaultRbeeActionURL     = "http://localhost:3000/v1/rb"
)

func processActionRules(wd *vdi.WebDriver, ctx *ProcessContext, url string) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ProcActionRules] Starting to search and process CROWler Action rules...")
	// Run Action Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Executing CROWler configured Action rules...")
		// Execute the rules
		if strings.TrimSpace(string((*ctx.source.Config))) == defaultActionRulesConfig {
			runDefaultActionRules(wd, ctx)
		} else {
			configStr := string((*ctx.source.Config))
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Configuration: %v", configStr)
		}
	}
	// Check for rules based on the URL
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcActionRules] Executing CROWler URL based Action rules (if any)...")
	// If the URL matches a rule, execute it
	processURLRules(wd, ctx, url)

}

func processURLRules(wd *vdi.WebDriver, ctx *ProcessContext, url string) {
	// Find all the rulesets that match the URL
	rsl, err := ctx.re.GetAllRulesetByURL(url)
	if err == nil && len(rsl) != 0 {
		for _, rs := range rsl {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcURLRules] Executing ruleset: %s", rs.Name)
			// Execute all the rules in the ruleset
			executeActionRules(ctx, rs.GetAllEnabledActionRules(ctx.GetContextID(), true), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}

	// Find all the rulesgroup that match the URL
	rgl, err := ctx.re.GetAllRulesGroupByURL(url)
	if err == nil && len(rgl) != 0 {
		for _, rg := range rgl {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcURLRules] Executing rule group: %s", rg.GroupName)
			// Set the environment variables for the rule group
			rg.SetEnv(ctx.GetContextID())
			// Execute all the rules in the rule group
			executeActionRules(ctx, rg.GetActionRules(), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}

}

func executeActionRules(ctx *ProcessContext, actionRules []rules.ActionRule, wd *vdi.WebDriver) {
	runtime := newActionRuntime(ctx, wd)
	for i := range actionRules {
		if err := browseractions.ExecuteRule(context.Background(), runtime, &actionRules[i]); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
		}
		ctx.Status.TotalActions.Add(1)
	}
}

func executeRule(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) {
	if err := browseractions.ExecuteRule(context.Background(), newActionRuntime(ctx, wd), r); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
	}
}

// executeActionRule executes a single ActionRule.
func executeActionRule(ctx *ProcessContext, r *rules.ActionRule, wd *vdi.WebDriver) error {
	return browseractions.ExecuteRule(context.Background(), newActionRuntime(ctx, wd), r)
}

func WaitForCondition(ctx *ProcessContext, wd *vdi.WebDriver, r rules.WaitCondition) error {
	return browseractions.WaitForCondition(context.Background(), newActionRuntime(ctx, wd), r)
}

func checkActionConditions(ctx *ProcessContext, conditions map[string]interface{}, wd *vdi.WebDriver) bool {
	matches, err := browseractions.ConditionsMatch(context.Background(), newActionRuntime(ctx, wd), conditions)
	return err == nil && matches
}

func findElementBySelectorType(ctx *ProcessContext, wd *vdi.WebDriver, selectors []rules.Selector) (vdi.WebElement, rules.Selector, error) {
	lookup := &crawlerActionLookup{ctx: ctx, wd: wd}
	var lastErr error
	for _, selector := range selectors {
		element, err := lookup.FindElement(context.Background(), selector)
		if err == nil && element != nil {
			return element, selector, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no selectors configured")
	}
	return nil, rules.Selector{}, lastErr
}

func newActionRuntime(ctx *ProcessContext, wd *vdi.WebDriver) *browseractions.Runtime {
	if wd == nil && ctx != nil {
		wd = ctx.GetWebDriver()
	}
	var driver vdi.WebDriver
	if wd != nil {
		driver = *wd
	}
	return &browseractions.Runtime{
		ContextID:  actionContextID(ctx),
		WebDriver:  driver,
		Rules:      &crawlerActionLookup{ctx: ctx, wd: wd},
		Cookies:    &crawlerCookieSink{ctx: ctx},
		Screenshot: crawlerScreenshotHook(wd),
		CheckStatus: func(actionCtx context.Context) error {
			if err := actionCtx.Err(); err != nil {
				return err
			}
			if ctx != nil && ctx.Status != nil && ctx.Status.PipelineRunning.Load() > 1 {
				return browseractions.ErrRuntimeStopped
			}
			return nil
		},
		Options: browseractions.Options{HBS: browseractions.HBSOptions{
			Enabled:          true,
			SeleniumFallback: true,
			Rbee:             browseractions.RbeeEndpoints{Action: defaultRbeeActionURL},
		}},
	}
}

func actionContextID(ctx *ProcessContext) string {
	if ctx == nil || ctx.source == nil {
		return ""
	}
	return ctx.GetContextID()
}

type crawlerActionLookup struct {
	ctx *ProcessContext
	wd  *vdi.WebDriver
}

func (l *crawlerActionLookup) FindElement(_ context.Context, selector rules.Selector) (vdi.WebElement, error) {
	return FindElementByType(l.ctx, l.wd, selector)
}

func (l *crawlerActionLookup) PluginScript(_ context.Context, name string) (string, bool, error) {
	if l == nil || l.ctx == nil || l.ctx.re == nil {
		return "", false, nil
	}
	plugin, exists := l.ctx.re.JSPlugins.GetPlugin(name)
	if !exists {
		return "", false, nil
	}
	return plugin.String(), true, nil
}

func (l *crawlerActionLookup) CallPlugin(_ context.Context, name string, value string) error {
	if l == nil || l.ctx == nil {
		return fmt.Errorf("crawler action runtime is unavailable")
	}
	res := executeRuleCall(l.ctx, l.wd, RuleCallRequest{
		Kind:       RuleCallKindPlugin,
		PluginName: name,
		Params:     map[string]interface{}{"value": value},
		TimeoutSec: 30,
		OnError:    "fail",
		Caller:     "action.execute_javascript",
	})
	if !res.Success {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}

type crawlerCookieSink struct {
	ctx *ProcessContext
}

func (s *crawlerCookieSink) CollectCookies(_ context.Context, cookies map[string]interface{}) error {
	if s == nil || s.ctx == nil || len(cookies) == 0 {
		return nil
	}
	if s.ctx.CollectedCookies == nil {
		s.ctx.CollectedCookies = make(map[string]interface{})
	}
	for name, value := range cookies {
		s.ctx.CollectedCookies[name] = value
	}
	return nil
}

func crawlerScreenshotHook(wd *vdi.WebDriver) browseractions.ScreenshotHook {
	return func(_ context.Context, filename string, maxHeight int) error {
		_, err := TakeScreenshot(wd, filename, maxHeight)
		return err
	}
}
func DefaultActionConfig(url string) cfg.SourceConfig {
	return cfg.SourceConfig{
		FormatVersion: "1.0",
		Author:        "The CROWler team",
		CreatedAt:     time.Now(),
		Description:   "Default configuration",
		SourceName:    "Example Source",
		CrawlingConfig: cfg.CrawlingConfig{
			Site: url,
		},
		ExecutionPlan: []cfg.ExecutionPlanItem{
			{
				Label: "Default Execution Plan",
				Conditions: cfg.Condition{
					URLPatterns: []string{url},
				},
				RuleGroups: []string{"CookieAcceptanceRulesExtended"},
			},
		},
	}
}

func runDefaultActionRules(wd *vdi.WebDriver, ctx *ProcessContext) {
	// Execute the default scraping rules
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing default action rules...")

	// Get the default scraping rules
	url, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting the current URL: %v", err)
		url = ""
	}
	rs := DefaultActionConfig(url)
	// Check if the conditions are met
	if len(rs.ExecutionPlan) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No execution plan found for the current URL")
		return
	}
	// Execute all the rules in the ruleset
	for _, r := range rs.ExecutionPlan {
		// Check the conditions
		if !checkActionPreConditions(r.Conditions, url) {
			continue
		}
		if !checkActionConditions(ctx, r.AdditionalConditions, wd) {
			continue
		}
		if len(r.Rulesets) > 0 {
			executePlannedRulesets(wd, ctx, r)
		}
		if len(r.RuleGroups) > 0 {
			executePlannedRuleGroups(wd, ctx, r)
		}
		if len(r.Rules) > 0 {
			executePlannedRules(wd, ctx, r)
		}
	}
}

// checkActionPreConditions checks if the pre conditions are met
// for example if the page URL is listed in the list of URLs
// for which this rule is valid.

// executeActionRulesByURL executes the action rules associated with the URL.
func executeActionRulesByURL(wd *vdi.WebDriver, ctx *ProcessContext, url string) {
	processURLRules(wd, ctx, url)
}

// checkActionPreConditions checks if the pre conditions are met
// for example if the page URL is listed in the list of URLs
// for which this rule is valid.
func checkActionPreConditions(conditions cfg.Condition, url string) bool {
	canProceed := true
	// Check the URL patterns
	if len(conditions.URLPatterns) > 0 {
		for _, pattern := range conditions.URLPatterns {
			if strings.Contains(url, pattern) {
				canProceed = true
			} else {
				canProceed = false
			}
		}
	}
	return canProceed
}

// executePlannedRules executes the rules in the execution plan
func executePlannedRules(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rules in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rules...")
	// Get the rule
	for _, ruleName := range planned.Rules {
		if ruleName == "" {
			continue
		}
		executeActionRuleByName(ruleName, wd, ctx)
	}
}

func executeActionRuleByName(ruleName string, wd *vdi.WebDriver, ctx *ProcessContext) {
	rule, err := ctx.re.GetActionRuleByName(ruleName)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting action rule: %v", err)
		return
	}

	// Execute the rule
	if err = executeActionRule(ctx, rule, wd); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing action rule: %v", err)
	}
}

// executePlannedRuleGroups executes the rule groups in the execution plan
func executePlannedRuleGroups(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rule groups in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rule groups...")
	// Get the rule group
	for _, ruleGroupName := range planned.RuleGroups {
		if strings.TrimSpace(ruleGroupName) == "" {
			continue
		}
		rg, err := ctx.re.GetRuleGroupByName(ruleGroupName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting rule group '%s': %v", ruleGroupName, err)
		} else {
			// Execute the rule group
			executeActionRules(ctx, rg.GetActionRules(), wd)
			ctx.Status.TotalActions.Add(int32(len(rg.GetActionRules())))
		}
	}
}

// executePlannedRulesets executes the rulesets in the execution plan
func executePlannedRulesets(wd *vdi.WebDriver, ctx *ProcessContext, planned cfg.ExecutionPlanItem) {
	// Execute the rulesets in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rulesets...")
	// Get the ruleset
	for _, rulesetName := range planned.Rulesets {
		if rulesetName == "" {
			continue
		}
		rs, err := ctx.re.GetRulesetByName(rulesetName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting ruleset: %v", err)
		} else {
			// Execute the ruleset
			executeActionRules(ctx, rs.GetAllEnabledActionRules(ctx.GetContextID(), true), wd)
			// Clean up non-persistent rules
			cmn.KVStore.DeleteByCID(ctx.GetContextID())
		}
	}
}

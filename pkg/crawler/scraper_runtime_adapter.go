package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	browseractions "github.com/pzaino/thecrowler/pkg/browser/actions"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	scraper "github.com/pzaino/thecrowler/pkg/scraper"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

type scraperRuntimeAdapter struct {
	ctx      *ProcessContext
	wd       *vdi.WebDriver
	pageInfo *PageInfo
}

func newScraperRuntimeAdapter(ctx *ProcessContext, wd *vdi.WebDriver) *scraper.Runtime {
	return newScraperRuntimeAdapterWithPageInfo(ctx, wd, nil)
}

func newScraperRuntimeAdapterWithPageInfo(ctx *ProcessContext, wd *vdi.WebDriver, pageInfo *PageInfo) *scraper.Runtime {
	adapter := &scraperRuntimeAdapter{ctx: ctx, wd: wd, pageInfo: pageInfo}
	timeout := 15 * time.Second
	if ctx != nil && ctx.config.Plugins.PluginsTimeout > 0 {
		timeout = time.Duration(ctx.config.Plugins.PluginsTimeout) * time.Second
	}
	return &scraper.Runtime{
		ContextIDs:    adapter,
		RuleCalls:     adapter,
		Plugins:       adapter,
		Agents:        adapter,
		HTTP:          scraper.HTTPClientTransformer{Client: &http.Client{Timeout: timeout}},
		Environment:   adapter,
		EnvSetter:     adapter,
		EnvCleaner:    adapter,
		Configuration: adapter,
		CrowlerMeta:   adapter,
		Failures:      adapter,
		BeforeRule: func(context.Context, *rs.ScrapingRule) error {
			if ctx != nil {
				_ = vdi.Refresh(ctx)
			}
			return nil
		},
		BeforeApply: func(context.Context, *rs.ScrapingRule) error {
			if ctx != nil {
				_ = vdi.Refresh(ctx)
			}
			return nil
		},
		WaitCondition: func(execCtx context.Context, condition rs.WaitCondition) error {
			return browseractions.WaitForCondition(execCtx, newActionRuntime(ctx, wd), condition)
		},
		MatchConditions: func(_ context.Context, conditions map[string]interface{}) (bool, error) {
			return checkScrapingConditions(ctx, conditions, wd), nil
		},
		AugmentResult: func(_ context.Context, rule *rs.ScrapingRule, result map[string]interface{}) map[string]interface{} {
			if rule == nil || !rule.JsFiles {
				return result
			}
			scripts, err := scraper.ExtractJavaScriptFiles(scraper.JavaScriptRequest{Driver: wd})
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error extracting scripts: %v", err)
				return result
			}
			result["js_files"] = scripts.Scripts
			return result
		},
		MatchValue: func(item interface{}, selector rs.Selector) (bool, error) {
			return matchValue(ctx, item, selector), nil
		},
	}
}

func (a *scraperRuntimeAdapter) ContextID() string {
	if a == nil || a.ctx == nil || a.ctx.source == nil {
		return ""
	}
	return a.ctx.GetContextID()
}

func (a *scraperRuntimeAdapter) CallRule(ctx context.Context, req scraper.RuleCallRequest) (scraper.RuleCallResult, error) {
	if err := ctx.Err(); err != nil {
		return scraper.RuleCallResult{}, err
	}
	if a == nil || a.ctx == nil {
		return scraper.RuleCallResult{}, fmt.Errorf("crawler runtime is unavailable")
	}
	wd := a.wd
	if wd == nil {
		wd = a.ctx.GetWebDriver()
	}
	kind := RuleCallKind(req.Kind)
	crawlerReq := RuleCallRequest{Kind: kind, Params: a.paramsWithPageInfo(req.Parameters, nil), TimeoutSec: int(req.Timeout.Seconds()), OnError: "fail", Caller: req.Caller, RuleName: req.RuleName}
	if crawlerReq.TimeoutSec <= 0 {
		crawlerReq.TimeoutSec = 30
	}
	switch kind {
	case RuleCallKindPlugin:
		crawlerReq.PluginName = req.Name
	case RuleCallKindAgent:
		crawlerReq.AgentName = req.Name
	default:
		crawlerReq.Kind = RuleCallKindPlugin
		crawlerReq.PluginName = req.Name
	}
	done := make(chan RuleCallResult, 1)
	go func() { done <- executeRuleCall(a.ctx, wd, crawlerReq) }()
	var res RuleCallResult
	select {
	case <-ctx.Done():
		return scraper.RuleCallResult{}, ctx.Err()
	case res = <-done:
	}
	if !res.Success {
		if res.Error == "" {
			res.Error = "runtime rule call failed"
		}
		return scraper.RuleCallResult{Value: res.Value}, fmt.Errorf("%s", res.Error)
	}
	return scraper.RuleCallResult{Value: res.Value}, nil
}

func (a *scraperRuntimeAdapter) RunPlugin(ctx context.Context, req scraper.PluginRequest) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.Data) == 0 {
		result, err := a.CallRule(ctx, scraper.RuleCallRequest{Kind: string(RuleCallKindPlugin), Name: req.Name, Parameters: req.Parameters, Caller: req.Caller, RuleName: req.RuleName, Step: req.Step})
		return result.Value, err
	}
	step := &rs.PostProcessingStep{Type: strPluginCall, Details: map[string]interface{}{"plugin_name": req.Name, "parameters": a.paramsWithPageInfo(req.Parameters, req.Data)}}
	data := append([]byte(nil), req.Data...)
	done := make(chan error, 1)
	go func() { done <- processCustomJS(a.ctx, step, &data) }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return data, nil
	}
}

func (a *scraperRuntimeAdapter) RunAgent(ctx context.Context, req scraper.AgentRequest) (interface{}, error) {
	result, err := a.CallRule(ctx, scraper.RuleCallRequest{Kind: string(RuleCallKindAgent), Name: req.Name, Parameters: a.paramsWithPageInfo(req.Parameters, req.Data), Caller: req.Caller, RuleName: req.RuleName, Step: req.Step})
	return result.Value, err
}

func (a *scraperRuntimeAdapter) paramsWithPageInfo(parameters map[string]interface{}, data []byte) map[string]interface{} {
	merged := make(map[string]interface{}, len(parameters)+3)
	for k, v := range parameters {
		merged[k] = v
	}
	if a == nil || a.pageInfo == nil {
		return merged
	}
	jsonData := map[string]interface{}{}
	if existing, ok := merged["json_data"].(map[string]interface{}); ok {
		for k, v := range existing {
			jsonData[k] = v
		}
	}
	if len(data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(data, &dataMap); err == nil {
			for k, v := range dataMap {
				jsonData[k] = v
			}
		}
	}
	mergePageInfoIntoJSONData(jsonData, a.pageInfo)
	merged["json_data"] = jsonData
	merged["page_info"] = a.pageInfo
	return merged
}

func mergePageInfoIntoJSONData(jsonData map[string]interface{}, pageInfo *PageInfo) {
	if jsonData == nil || pageInfo == nil {
		return
	}
	if len(pageInfo.ScrapedData) > 0 {
		if _, exists := jsonData["scraped_data"]; !exists {
			jsonData["scraped_data"] = pageInfo.ScrapedData
		}
		var xhr []interface{}
		for _, item := range pageInfo.ScrapedData {
			if value, ok := item["xhr"]; ok {
				xhr = append(xhr, value)
			}
		}
		if len(xhr) > 0 {
			if _, exists := jsonData["xhr"]; !exists {
				jsonData["xhr"] = xhr
			}
			if _, exists := jsonData["XHR"]; !exists {
				jsonData["XHR"] = xhr
			}
		}
	}
}

func (a *scraperRuntimeAdapter) LookupEnvironment(_ context.Context, key string) (interface{}, bool, error) {
	if a == nil || a.ctx == nil || cmn.KVStore == nil {
		return nil, false, nil
	}
	value, _, err := cmn.KVStore.Get(key, a.ContextID())
	if err != nil {
		if cmn.KVSErrorIsKeyNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return value, true, nil
}

func (a *scraperRuntimeAdapter) SetEnvironment(_ context.Context, key string, value interface{}, properties scraper.EnvironmentProperties) error {
	if cmn.KVStore == nil {
		return nil
	}
	return cmn.KVStore.Set(key, value, cmn.NewKVStoreProperty(properties.Persistent, properties.Static, properties.SessionValid, properties.Shared, properties.Source, a.ContextID(), properties.Type))
}

func (a *scraperRuntimeAdapter) ClearEnvironment(_ context.Context) error {
	if cmn.KVStore == nil || a == nil || a.ContextID() == "" {
		return nil
	}
	cmn.KVStore.DeleteByCID(a.ContextID())
	return nil
}

func (a *scraperRuntimeAdapter) LookupConfiguration(_ context.Context, key string) (interface{}, bool) {
	if a == nil || a.ctx == nil {
		return nil, false
	}
	switch strings.TrimSpace(key) {
	case "plugins.timeout":
		return a.ctx.config.Plugins.PluginsTimeout, true
	case "source.url":
		if a.ctx.source != nil {
			return a.ctx.source.URL, true
		}
	case "source.meta_data":
		if a.ctx.source != nil && a.ctx.source.Config != nil {
			var configMap map[string]interface{}
			if err := json.Unmarshal(*a.ctx.source.Config, &configMap); err == nil {
				value, ok := configMap["meta_data"]
				return value, ok
			}
		}
	}
	if value := os.Getenv(key); value != "" {
		return value, true
	}
	return nil, false
}

func (a *scraperRuntimeAdapter) ReportFailure(_ context.Context, failure scraper.Failure) {
	cmn.DebugMsg(cmn.DbgLvlError, "scraper step failed rule=%s step=%d kind=%s name=%s err=%v", failure.RuleName, failure.Step, failure.Kind, failure.Name, failure.Err)
}

func (a *scraperRuntimeAdapter) currentCrowlerMeta() CrowlerMeta {
	if a == nil || a.ctx == nil {
		return NewCrowlerMeta(nil, nil)
	}
	if a.ctx.crowlerMeta != nil {
		return a.ctx.crowlerMeta
	}
	if a.ctx.ni != nil && a.ctx.ni.CrowlerMeta != nil {
		a.ctx.crowlerMeta = CrowlerMeta(a.ctx.ni.CrowlerMeta)
		a.ctx.crowlerMeta.EnsureSourceUID(a.ctx.source)
		return a.ctx.crowlerMeta
	}
	if a.ctx.hi != nil && a.ctx.hi.CrowlerMeta != nil {
		a.ctx.crowlerMeta = CrowlerMeta(a.ctx.hi.CrowlerMeta)
		a.ctx.crowlerMeta.EnsureSourceUID(a.ctx.source)
		return a.ctx.crowlerMeta
	}
	a.ctx.crowlerMeta = NewCrowlerMetaFromSource(a.ctx.source, a.ctx.srcCfg)
	a.ctx.crowlerMeta.EnsureSourceUID(a.ctx.source)
	return a.ctx.crowlerMeta
}

func (a *scraperRuntimeAdapter) SetCrowlerMetaSection(_ context.Context, section string, value map[string]interface{}) error {
	return a.currentCrowlerMeta().SetSection(section, value)
}

func (a *scraperRuntimeAdapter) SetCrowlerMetaTag(_ context.Context, section, key string, value interface{}) error {
	return a.currentCrowlerMeta().SetTag(section, key, value)
}

func (a *scraperRuntimeAdapter) DeleteCrowlerMetaSection(_ context.Context, section string) error {
	return a.currentCrowlerMeta().DeleteSection(section)
}

func (a *scraperRuntimeAdapter) DeleteCrowlerMetaTag(_ context.Context, section, key string) error {
	return a.currentCrowlerMeta().DeleteTag(section, key)
}

func (a *scraperRuntimeAdapter) AddCrowlerMetaObjectType(_ context.Context, labels ...string) error {
	a.currentCrowlerMeta().AddObjectType(labels...)
	return nil
}

// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	browser "github.com/pzaino/thecrowler/pkg/browser"
	browseractions "github.com/pzaino/thecrowler/pkg/browser/actions"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"github.com/pzaino/thecrowler/pkg/infoseed/searchproviders"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/pzaino/thecrowler/pkg/scraper"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// BrowserDependencies contains the optional browser capabilities shared by all
// WebDriver information-seed providers. HTTP providers do not use these fields.
type BrowserDependencies struct {
	Sessions searchproviders.BrowserSessionProvider
	Actions  searchproviders.BrowserActionExecutor
	Scraper  searchproviders.BrowserResultScraper
	Rules    searchproviders.BrowserRuleResolver
}

// RunnerOptions contains explicitly injected optional runner dependencies.
type RunnerOptions struct {
	Browser BrowserDependencies
}

// NewBrowserDependencies adapts the application-owned VDI pool, validated
// configuration, and a read-only snapshot of the rules engine. The returned
// dependencies lease pool entries but never stop or otherwise own the pool.
func NewBrowserDependencies(pool *vdi.Pool, config *cfg.Config, engine *rules.RuleEngine) BrowserDependencies {
	if pool == nil || config == nil || engine == nil || pool.Size() == 0 {
		return BrowserDependencies{}
	}
	resolver := newBrowserRuleSnapshot(engine)
	return BrowserDependencies{
		Sessions: &applicationBrowserSessions{pool: pool, config: config},
		Actions:  applicationBrowserActions{},
		Scraper:  applicationBrowserScraper{},
		Rules:    resolver,
	}
}

type applicationBrowserSessions struct {
	pool   *vdi.Pool
	config *cfg.Config
}

func (p *applicationBrowserSessions) Acquire(ctx context.Context, request searchproviders.BrowserSessionRequest) (searchproviders.BrowserSession, searchproviders.BrowserSessionLease, error) {
	if p == nil || p.pool == nil || p.config == nil {
		return nil, nil, errors.New("information seed webdriver session dependencies are unavailable")
	}
	lease, instance, err := p.pool.AcquireContext(ctx, strings.Join(request.VDIAllowList, ","))
	if err != nil {
		return nil, nil, fmt.Errorf("acquire application VDI lease: %w", err)
	}
	connectContext := &informationSeedVDIContext{config: p.config, instance: instance}
	wd, err := vdi.ConnectVDI(connectContext, instance, 0)
	if err != nil {
		_ = lease.Release()
		return nil, nil, fmt.Errorf("connect application VDI lease: %w", err)
	}
	if err := browser.Setup(ctx, wd, browser.SetupOptions{InitialURL: "about:blank", SetGPUPatch: p.config.Crawler.SetVDIGPUPatch, ReinforceBrowserSettings: true}); err != nil {
		_ = browser.Cleanup(wd, browser.CleanupOptions{Close: true})
		_ = lease.Release()
		return nil, nil, err
	}
	return &applicationBrowserSession{wd: wd}, applicationBrowserLease{lease: lease}, nil
}

type applicationBrowserSession struct{ wd vdi.WebDriver }

func (s *applicationBrowserSession) Navigate(ctx context.Context, target string) error {
	return browser.Navigate(ctx, s.wd, target, 0)
}

func (s *applicationBrowserSession) CurrentURL(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.wd.CurrentURL()
}

func (s *applicationBrowserSession) Close(context.Context) error {
	return browser.Cleanup(s.wd, browser.CleanupOptions{Close: true})
}

func (s *applicationBrowserSession) webDriver() vdi.WebDriver { return s.wd }

type applicationBrowserLease struct{ lease *vdi.Lease }

func (l applicationBrowserLease) Release(context.Context) error {
	if l.lease == nil {
		return nil
	}
	return l.lease.Release()
}

type informationSeedVDIContext struct {
	config      *cfg.Config
	instance    vdi.SeleniumInstance
	wd          vdi.WebDriver
	closed      bool
	returned    bool
	operationMu sync.Mutex
}

func (c *informationSeedVDIContext) GetWebDriver() *vdi.WebDriver          { return &c.wd }
func (c *informationSeedVDIContext) GetConfig() *cfg.Config                { return c.config }
func (c *informationSeedVDIContext) GetSource() *cdb.Source                { return nil }
func (c *informationSeedVDIContext) GetVDIClosedFlag() *bool               { return &c.closed }
func (c *informationSeedVDIContext) SetVDIClosedFlag(value bool)           { c.closed = value }
func (c *informationSeedVDIContext) GetVDIOperationMutex() *sync.Mutex     { return &c.operationMu }
func (c *informationSeedVDIContext) GetVDIReturnedFlag() *bool             { return &c.returned }
func (c *informationSeedVDIContext) SetVDIReturnedFlag(value bool)         { c.returned = value }
func (c *informationSeedVDIContext) GetVDIInstance() *vdi.SeleniumInstance { return &c.instance }

type webdriverSession interface{ webDriver() vdi.WebDriver }

type applicationBrowserActions struct{}

func (applicationBrowserActions) Execute(ctx context.Context, session searchproviders.BrowserSession, request searchproviders.BrowserActionRequest) error {
	wrapped, ok := session.(webdriverSession)
	if !ok || wrapped.webDriver() == nil {
		return errors.New("information seed webdriver action session is unavailable")
	}
	actionRules := make([]rules.ActionRule, 0, len(request.Rules))
	for _, resolved := range request.Rules {
		rule, ok := resolved.Value.(rules.ActionRule)
		if !ok {
			return fmt.Errorf("information seed action rule %q has an invalid resolved value", resolved.Name)
		}
		actionRules = append(actionRules, rule)
	}
	wd := wrapped.webDriver()
	runtime := &browseractions.Runtime{
		WebDriver: wd,
		Rules:     informationSeedActionLookup{driver: &wd},
		Options: browseractions.Options{HBS: browseractions.HBSOptions{
			Enabled: strings.EqualFold(request.Mode, searchproviders.BrowserActionModeHBS),
		}},
	}
	return browseractions.ExecuteRules(ctx, runtime, actionRules)
}

type informationSeedActionLookup struct{ driver *vdi.WebDriver }

func (l informationSeedActionLookup) FindElement(ctx context.Context, selector rules.Selector) (vdi.WebElement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := (scraper.Extractor{Driver: l.driver}).FindElement(scraper.LookupRequest{Selector: selector})
	if err != nil {
		return nil, err
	}
	return result.Elements[0], nil
}

func (informationSeedActionLookup) PluginScript(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (informationSeedActionLookup) CallPlugin(context.Context, string, string) error {
	return errors.New("information seed browser action plugins are unavailable")
}

type applicationBrowserScraper struct{}

func (applicationBrowserScraper) Scrape(ctx context.Context, session searchproviders.BrowserSession, resolved []searchproviders.BrowserScrapingRule) ([]map[string]interface{}, error) {
	wrapped, ok := session.(webdriverSession)
	if !ok || wrapped.webDriver() == nil {
		return nil, errors.New("information seed webdriver scraping session is unavailable")
	}
	wd := wrapped.webDriver()
	records := make([]map[string]interface{}, 0, len(resolved))
	for _, item := range resolved {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := item.Value.(rules.ScrapingRule)
		if !ok {
			return nil, fmt.Errorf("information seed scraping rule %q has an invalid resolved value", item.Name)
		}
		record, err := scraper.ApplyRule(ctx, scraper.NoopRuntime(), &rule, &wd)
		if err != nil {
			return nil, fmt.Errorf("apply scraping rule %q: %w", item.Name, err)
		}
		records = append(records, expandBrowserRecords(record)...)
	}
	return records, nil
}

func expandBrowserRecords(record map[string]interface{}) []map[string]interface{} {
	urls, ok := record["url"].([]interface{})
	if !ok {
		return []map[string]interface{}{record}
	}
	records := make([]map[string]interface{}, 0, len(urls))
	for i, rawURL := range urls {
		item := make(map[string]interface{}, len(record))
		for key, value := range record {
			if values, isSlice := value.([]interface{}); isSlice && i < len(values) {
				item[key] = values[i]
			} else {
				item[key] = value
			}
		}
		item["url"] = rawURL
		records = append(records, item)
	}
	return records
}

type browserRuleSnapshot struct {
	actions  map[string]rules.ActionRule
	scraping map[string]rules.ScrapingRule
}

func newBrowserRuleSnapshot(engine *rules.RuleEngine) *browserRuleSnapshot {
	snapshot := &browserRuleSnapshot{actions: map[string]rules.ActionRule{}, scraping: map[string]rules.ScrapingRule{}}
	for _, rule := range engine.GetAllActionRules() {
		snapshot.actions[normalizeRuleReference(rule.RuleName)] = rule
	}
	for _, rule := range engine.GetAllScrapingRules() {
		snapshot.scraping[normalizeRuleReference(rule.RuleName)] = rule
	}
	return snapshot
}

func (r *browserRuleSnapshot) ResolveActionRules(ctx context.Context, references []string) ([]searchproviders.BrowserActionRule, error) {
	resolved := make([]searchproviders.BrowserActionRule, 0, len(references))
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := r.actions[normalizeRuleReference(reference)]
		if !ok {
			return nil, fmt.Errorf("information seed action rule %q was not found", reference)
		}
		resolved = append(resolved, searchproviders.BrowserActionRule{Name: rule.RuleName, Value: rule})
	}
	return resolved, nil
}

func (r *browserRuleSnapshot) ResolveScrapingRules(ctx context.Context, references []string) ([]searchproviders.BrowserScrapingRule, error) {
	resolved := make([]searchproviders.BrowserScrapingRule, 0, len(references))
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := r.scraping[normalizeRuleReference(reference)]
		if !ok {
			return nil, fmt.Errorf("information seed scraping rule %q was not found", reference)
		}
		resolved = append(resolved, searchproviders.BrowserScrapingRule{Name: rule.RuleName, Value: rule})
	}
	return resolved, nil
}

func normalizeRuleReference(reference string) string {
	return strings.ToLower(strings.TrimSpace(reference))
}

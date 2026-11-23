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

package crawler

// Source-boundary sticky-session orchestration. Exactly one rotation is
// attempted for a new browser-backed source crawl that opts in, before any
// WebDriver session exists. Page workers, Selenium refreshes and retries never
// call this path.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

const stickySessionRotationTimeout = 20 * time.Second

// stickySessionRotator is the narrow client boundary used by the crawler.
type stickySessionRotator interface {
	Rotate(context.Context, proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error)
}

// SetEffectiveProxy records the one proxy decision for this source crawl.
func (ctx *ProcessContext) SetEffectiveProxy(proxy, source string) {
	if ctx == nil {
		return
	}
	ctx.effectiveProxy = proxy
	ctx.effectiveProxySet = true
	ctx.effectiveProxySource = source
}

// GetEffectiveProxy implements vdi.EffectiveProxyProvider.
func (ctx *ProcessContext) GetEffectiveProxy() (string, bool) {
	if ctx == nil {
		return "", false
	}
	return ctx.effectiveProxy, ctx.effectiveProxySet
}

// GetEffectiveProxySource returns the diagnostic provenance of the stored
// proxy decision.
func (ctx *ProcessContext) GetEffectiveProxySource() string {
	if ctx == nil {
		return ""
	}
	return ctx.effectiveProxySource
}

// validateStickySourceProtocol keeps the 2.3.1 boundary explicit: only the
// browser-backed web path may use sticky sessions.
func validateStickySourceProtocol(sticky bool, protocol SourceProtocol) error {
	if sticky && protocol != SourceProtocolWeb {
		return fmt.Errorf("crawling_config.sticky_session is only supported for browser-backed web sources")
	}
	return nil
}

// validateStickySourceBoundary rechecks the protocol and the stored source
// site, so a legacy document cannot opt into sticky mode on a non-browser site.
func validateStickySourceBoundary(sticky bool, protocol SourceProtocol, crawling cfg.CrawlingConfig) error {
	if err := validateStickySourceProtocol(sticky, protocol); err != nil {
		return err
	}
	if !sticky {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(crawling.SourceType), cfg.SourceTypeEmail) {
		return fmt.Errorf("crawling_config.sticky_session is not supported for email sources")
	}
	if err := cfg.ValidateStickySessionSite(crawling.Site); err != nil {
		return fmt.Errorf("crawling_config.sticky_session: %w", err)
	}
	return nil
}

// resolveBrowserProxy computes and stores the effective proxy for the leased
// VDI. The raw source document is required only for the legacy top-level
// proxy_url layer; canonical and custom values come from the decoded document.
func (ctx *ProcessContext) resolveBrowserProxy(sel vdi.SeleniumInstance, sourceCfg cfg.SourceConfig, raw json.RawMessage) (cfg.ProxyResolution, error) {
	if ctx == nil {
		return cfg.ProxyResolution{}, fmt.Errorf("resolving browser proxy: process context is nil")
	}
	sticky := sourceCfg.CrawlingConfig.StickySession != nil && *sourceCfg.CrawlingConfig.StickySession

	var legacyProxy string
	var legacyPresent bool
	var customProxy string
	var customPresent bool
	profile := sel.Config.StickySession

	if sticky {
		var strictErr error
		if _, apiErr := cfg.NormalizeStickyAPIURL(profile.APIURL); apiErr != nil {
			return cfg.ProxyResolution{}, fmt.Errorf("vdi.sticky_session.api_url: %w", apiErr)
		}
		if strings.ContainsAny(profile.Provider, " \t\r\n") {
			return cfg.ProxyResolution{}, fmt.Errorf("vdi.sticky_session.provider must not contain whitespace")
		}
		legacyProxy, legacyPresent, strictErr = cfg.LegacyTopLevelProxyStrict(raw)
		if strictErr != nil {
			return cfg.ProxyResolution{}, fmt.Errorf("sticky_session: %w", strictErr)
		}
		customProxy, customPresent, strictErr = cfg.SourceCustomVDIProxyStrict(sourceCfg, ctx.config.Selenium, sel.Config.Name)
		if strictErr != nil {
			return cfg.ProxyResolution{}, fmt.Errorf("sticky_session: %w", strictErr)
		}
		if !cfg.VDIByNameUnique(ctx.config.Selenium, sel.Config.Name) {
			return cfg.ProxyResolution{}, fmt.Errorf(
				"sticky_session requires a unique VDI association for %q", sel.Config.Name)
		}
	} else {
		legacyProxy, legacyPresent = cfg.LegacyTopLevelProxy(raw)
		customProxy, customPresent = cfg.SourceCustomVDIProxy(sourceCfg, ctx.config.Selenium, sel.Config.Name)
	}
	resolution, err := cfg.ResolveEffectiveProxy(cfg.EffectiveProxyInput{
		SourceProxy:             sourceCfg.CrawlingConfig.Proxy,
		LegacyProxy:             legacyProxy,
		LegacyProxyPresent:      legacyPresent,
		CustomProxy:             customProxy,
		CustomProxyPresent:      customPresent,
		VDIProxy:                sel.Config.ProxyURL,
		Sticky:                  sticky,
		StickyProxy:             profile.ProxyURL,
		StickyProfileConfigured: profile.Configured(),
	})
	if err != nil {
		return cfg.ProxyResolution{}, err
	}
	if err := cfg.ValidateBrowserProxyCompatibility(sel.Config.Type, resolution.URL); err != nil {
		return cfg.ProxyResolution{}, err
	}
	ctx.SetEffectiveProxy(resolution.URL, string(resolution.Source))
	return resolution, nil
}

// startBrowserCrawl is the single source-start boundary. It resolves the
// proxy, performs at most one sticky-session rotation, and only then invokes
// connect (the WebDriver session creation). A failed proxy decision or failed
// rotation returns before connect is called.
func (ctx *ProcessContext) startBrowserCrawl(
	sel vdi.SeleniumInstance,
	sourceCfg cfg.SourceConfig,
	raw json.RawMessage,
	connect func() error,
) error {
	if err := ctx.checkCrawlFence(); err != nil {
		return err
	}
	sticky := sourceCfg.CrawlingConfig.StickySession != nil && *sourceCfg.CrawlingConfig.StickySession

	if _, err := ctx.resolveBrowserProxy(sel, sourceCfg, raw); err != nil {
		return err
	}
	if !sticky {
		if connect == nil {
			return fmt.Errorf("browser connection callback is nil")
		}
		return connect()
	}

	// Authoritative pre-rotation check: the local crawl context is not proof
	// that this attempt still owns the source. A superseded claim must not
	// rotate a shared upstream identity.
	if err := ctx.validateFencedClaim(); err != nil {
		return err
	}
	if err := ctx.checkCrawlFence(); err != nil {
		return err
	}

	profile := sel.Config.StickySession
	getenv := ctx.stickyGetenv
	if getenv == nil {
		getenv = os.Getenv
	}
	token := strings.TrimSpace(getenv(profile.TokenEnv))
	if token == "" {
		return fmt.Errorf(
			"sticky_session: engine token environment variable %q is not set", profile.TokenEnv)
	}

	generateKey := ctx.stickyKeyGenerate
	if generateKey == nil {
		generateKey = proxycontrol.NewIdempotencyKey
	}
	idempotencyKey, err := generateKey()
	if err != nil {
		return fmt.Errorf("sticky_session: generating idempotency key: %w", err)
	}

	rotator := ctx.stickyRotator
	if rotator == nil {
		rotator = proxycontrol.NewRotator()
	}
	rotationCtx, cancel := context.WithTimeout(ctx.crawlContext(), stickySessionRotationTimeout)
	defer cancel()

	response, err := rotator.Rotate(rotationCtx, proxycontrol.RotateRequest{
		APIURL:         profile.APIURL,
		Token:          token,
		IdempotencyKey: idempotencyKey,
		Provider:       profile.Provider,
	})
	if err != nil {
		sourceID := uint64(0)
		if ctx.source != nil {
			sourceID = ctx.source.ID
		}
		cmn.DebugMsg(cmn.DbgLvlError,
			"sticky session rotation failed for VDI '%s' (source %d): %v",
			sel.Config.Name, sourceID, err)
		return fmt.Errorf("sticky session rotation for VDI %q: %w", sel.Config.Name, err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo,
		"sticky session rotated for VDI '%s' (provider '%s', generation %d, replayed=%t)",
		sel.Config.Name, response.Provider, response.Generation, response.Replayed)

	// The attempt may have been cancelled or superseded while the API call
	// was in flight. Never start a browser after losing the fence.
	if err := ctx.checkCrawlFence(); err != nil {
		return err
	}
	if err := ctx.validateFencedClaim(); err != nil {
		return err
	}
	if err := ctx.checkCrawlFence(); err != nil {
		return err
	}
	if connect == nil {
		return fmt.Errorf("browser connection callback is nil")
	}
	return connect()
}

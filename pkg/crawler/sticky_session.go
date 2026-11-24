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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

const stickySessionRotationBudget = 120 * time.Second

// stickySessionRotator is the narrow engine-direct client boundary.
type stickySessionRotator interface {
	Rotate(context.Context, proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error)
}

// stickyCDPSessionRotator is the CDP-first transport using the leased VDI.
type stickyCDPSessionRotator interface {
	Rotate(context.Context, string, proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error)
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

// validateStickySourceProtocol keeps the browser-session boundary explicit:
// only the browser-backed web path may use sticky sessions.
func validateStickySourceProtocol(sticky bool, protocol SourceProtocol) error {
	if sticky && protocol != SourceProtocolWeb {
		return fmt.Errorf("sticky_session is only supported for browser-backed web sources")
	}
	return nil
}

// validateStickySourceBoundary rechecks the protocol and the effective
// retrieval URL, so a legacy or minimal source document cannot select sticky
// mode for a non-browser path. An absent crawling_config.site falls back to
// the actual Source URL.
func validateStickySourceBoundary(sticky bool, protocol SourceProtocol, crawling cfg.CrawlingConfig, sourceURL string) error {
	if err := validateStickySourceProtocol(sticky, protocol); err != nil {
		return err
	}
	if !sticky {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(crawling.SourceType), cfg.SourceTypeEmail) {
		return fmt.Errorf("sticky_session is not supported for email sources")
	}
	if err := cfg.ValidateStickySelectionSite(crawling, sourceURL); err != nil {
		return fmt.Errorf("sticky_session: %w", err)
	}
	return nil
}

// resolveNonWebSourceSession applies the ordinary proxy decision for a
// non-web source that can create a browser for follow-on work (email web
// links). Network-only sources never launch a browser, so their historical
// path is unchanged and source VDI proxy/identity fields cannot affect network
// collection. Only an identity mismatch is fatal; other proxy-resolution
// failures keep the historical VDI-default fallback.
func (ctx *ProcessContext) resolveNonWebSourceSession(
	protocol SourceProtocol,
	sel vdi.SeleniumInstance,
	sourceCfg cfg.SourceConfig,
	raw json.RawMessage,
) error {
	if protocol != SourceProtocolEmail {
		return nil
	}
	selection := cfg.EffectiveVDIInput{
		Leased:      sel.Config,
		LeasedIndex: ctx.SelID,
		Merged:      ctx.config.Selenium,
		Source:      sourceCfg,
		Raw:         raw,
	}
	resolution, err := cfg.ResolveSourceProxy(selection)
	if err == nil {
		ctx.SetEffectiveProxy(resolution.URL, string(resolution.Source))
		return nil
	}
	if errors.Is(err, cfg.ErrVDIIdentityMismatch) {
		return err
	}
	cmn.DebugMsg(cmn.DbgLvlWarn,
		"resolving non-sticky source proxy for non-web source: %v", err)
	nonWebProxy := strings.TrimSpace(sel.Config.ProxyURL)
	nonWebSource := cfg.ProxySourceVDI
	if nonWebProxy == "" {
		nonWebSource = cfg.ProxySourceDirect
	}
	ctx.SetEffectiveProxy(nonWebProxy, string(nonWebSource))
	return nil
}

// startBrowserCrawl is the single source-start boundary. It resolves one
// effective VDI/profile/proxy decision, performs at most one sticky-session
// rotation, and only then invokes connect (the WebDriver session creation).
// The same effective VDI is stored in SelInstance so refresh and reconnect
// paths use exactly the settings that were validated here.
func (ctx *ProcessContext) startBrowserCrawl(
	sel vdi.SeleniumInstance,
	sourceCfg cfg.SourceConfig,
	raw json.RawMessage,
	connect func() error,
) error {
	if err := ctx.checkCrawlFence(); err != nil {
		return err
	}
	selection := ctx.effectiveVDI
	if selection == nil {
		resolved, err := cfg.ResolveEffectiveVDI(cfg.EffectiveVDIInput{
			Leased:      sel.Config,
			LeasedIndex: ctx.SelID,
			Merged:      ctx.config.Selenium,
			Source:      sourceCfg,
			Raw:         raw,
		})
		if err != nil {
			return err
		}
		selection = &resolved
		ctx.effectiveVDI = selection
	}
	if selection.LeasedIndex != ctx.SelID {
		return fmt.Errorf(
			"effective VDI index %d does not match the acquired VDI index %d (leased VDI %q)",
			selection.LeasedIndex, ctx.SelID, sel.Config.Name)
	}
	if err := cfg.ValidateLeasedVDIIdentity(sel.Config, selection.Management); err != nil {
		return fmt.Errorf("effective VDI %d (%q) does not match the acquired VDI instance: %w",
			ctx.SelID, sel.Config.Name, err)
	}
	if err := cfg.ValidateLeasedVDIIdentity(selection.Management, selection.Browser); err != nil {
		return fmt.Errorf("effective browser VDI %d (%q) does not match its management identity: %w",
			ctx.SelID, sel.Config.Name, err)
	}
	effective := vdi.SeleniumInstance{Service: sel.Service, Config: selection.Browser}
	management := vdi.SeleniumInstance{Service: sel.Service, Config: selection.Management}
	ctx.SelInstance = effective
	ctx.SetEffectiveProxy(selection.Proxy.URL, string(selection.Proxy.Source))

	if !selection.Sticky {
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

	profile := selection.StickyProfile
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

	rotationCtx, cancel := context.WithTimeout(ctx.crawlContext(), stickySessionRotationBudget)
	defer cancel()

	response, transport, err := ctx.rotateStickySession(rotationCtx, management, profile, token, idempotencyKey)
	if err != nil {
		sourceID := uint64(0)
		if ctx.source != nil {
			sourceID = ctx.source.ID
		}
		cmn.DebugMsg(cmn.DbgLvlError,
			"sticky session rotation failed for VDI '%s' (source %d): %v",
			management.Config.Name, sourceID, err)
		return fmt.Errorf("sticky session rotation for VDI %q: %w", management.Config.Name, err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo,
		"sticky session rotated for VDI '%s' via %s (provider '%s', generation %d, replayed=%t, origin=%s)",
		management.Config.Name, transport, response.Provider, response.Generation, response.Replayed, selection.StickyOrigin)

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

// rotateStickySession applies the CDP-first / engine-direct fallback policy.
// One key and one token are used for every attempt.
func (ctx *ProcessContext) rotateStickySession(
	rotationCtx context.Context,
	sel vdi.SeleniumInstance,
	profile cfg.StickySessionConfig,
	token, idempotencyKey string,
) (proxycontrol.RotateResponse, string, error) {
	directRequest := proxycontrol.RotateRequest{
		APIURL:         profile.APIURL,
		Token:          token,
		IdempotencyKey: idempotencyKey,
		Provider:       profile.Provider,
	}

	direct := ctx.stickyRotator
	// An explicitly injected engine-direct rotator (tests and embedders) keeps
	// the historical direct-only behaviour unless a CDP transport is also
	// supplied. Production leaves both nil and gets CDP-first automatically.
	useCDP := vdi.SupportsCDPStickyRotation(sel) && (direct == nil || ctx.stickyCDPRotator != nil)
	var cdpErr error
	if useCDP {
		cdp := ctx.stickyCDPRotator
		if cdp == nil {
			cdp = vdi.NewCDPStickyRotator(sel)
		}
		cdpURL := strings.TrimSpace(profile.VDIAPIURL)
		if cdpURL == "" {
			cdpURL = profile.APIURL
		}
		cdpRequest := directRequest
		cdpRequest.APIURL = cdpURL
		response, err := cdp.Rotate(rotationCtx, cdpURL, cdpRequest)
		if err == nil {
			if !validStickyRotationResponse(response, profile.Provider) {
				return proxycontrol.RotateResponse{}, "cdp",
					&proxycontrol.Error{Kind: proxycontrol.FailureInvalidResponse}
			}
			return response, "cdp", nil
		}
		if errors.Is(err, vdi.ErrCDPStickyUnsafeCleanup) {
			ctx.quarantineSession("sticky rotation temporary browser cleanup failed")
			return proxycontrol.RotateResponse{}, "cdp", err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			ctx.crawlAborted() || proxycontrol.IsFailureKind(err, proxycontrol.FailureCancelled) {
			return proxycontrol.RotateResponse{}, "cdp", err
		}
		if proxycontrol.IsDefinitive(err) {
			return proxycontrol.RotateResponse{}, "cdp", err
		}
		if errors.Is(err, vdi.ErrCDPStickyUnavailable) || errors.Is(err, vdi.ErrCDPStickyUncertain) ||
			proxycontrol.IsFailureKind(err, proxycontrol.FailureTransport) ||
			proxycontrol.IsFailureKind(err, proxycontrol.FailureTimeout) {
			cdpErr = err
			cmn.DebugMsg(cmn.DbgLvlWarn,
				"CDP sticky rotation unavailable for VDI '%s'; trying engine-direct fallback: %v",
				sel.Config.Name, err)
		} else {
			return proxycontrol.RotateResponse{}, "cdp", err
		}
	}

	if direct == nil {
		direct = proxycontrol.NewRotator()
	}
	response, err := direct.Rotate(rotationCtx, directRequest)
	if err != nil {
		if cdpErr != nil {
			return proxycontrol.RotateResponse{}, "direct", fmt.Errorf(
				"sticky session rotation failed (CDP: %v; engine-direct: %w)", cdpErr, err)
		}
		return proxycontrol.RotateResponse{}, "direct", err
	}
	if !validStickyRotationResponse(response, profile.Provider) {
		return proxycontrol.RotateResponse{}, "direct",
			&proxycontrol.Error{Kind: proxycontrol.FailureInvalidResponse}
	}
	return response, "direct", nil
}

func validStickyRotationResponse(response proxycontrol.RotateResponse, provider string) bool {
	return response.Status == "rotated" && response.Generation >= 1 && response.Provider == provider
}

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

package config

// This file owns the single effective per-crawl VDI selection contract. It
// combines the leased VDI's global defaults, the source's positional
// custom.vdi overrides, and the source's explicit sticky tri-state into one
// immutable decision. The crawler, the rotation transports and the browser
// launcher all consume this decision instead of independently re-resolving the
// proxy or sticky mode.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StickySelectionOrigin identifies why the effective selection is sticky.
// It is diagnostic only and never grants authority.
type StickySelectionOrigin string

const (
	// StickySelectionNone means the crawl uses ordinary proxy/direct egress.
	StickySelectionNone StickySelectionOrigin = "none"
	// StickySelectionGlobal means the VDI's ordinary proxy selects its default
	// sticky profile.
	StickySelectionGlobal StickySelectionOrigin = "global-default"
	// StickySelectionExplicit means the source explicitly requested sticky
	// rotation through crawling_config.sticky_session.
	StickySelectionExplicit StickySelectionOrigin = "source-explicit"
	// StickySelectionProfile means the source supplied an approved custom
	// sticky profile.
	StickySelectionProfile StickySelectionOrigin = "source-profile"
	// StickySelectionListener means the effective proxy matched a configured
	// sticky listener, so browsing it without rotation would be unsafe.
	StickySelectionListener StickySelectionOrigin = "sticky-listener"
)

// EffectiveVDISelection is the immutable per-crawl VDI decision.
type EffectiveVDISelection struct {
	// Browser is the effective configuration for the real source browser. It
	// may include source-level type/host/port/ordinary-proxy overrides and the
	// selected sticky profile.
	Browser Selenium
	// Management is the leased VDI identity used for the disposable CDP
	// management browser. It deliberately preserves the leased host/port/name
	// and browser type so a source cannot retarget the privileged management
	// browser at another Selenium server.
	Management Selenium
	// Proxy is the single downstream proxy decision for both rotation
	// transports and the final source browser.
	Proxy ProxyResolution
	// Sticky reports whether one rotation must succeed before the source
	// browser starts.
	Sticky bool
	// StickyProfile is the selected complete profile when Sticky is true.
	StickyProfile StickySessionConfig
	// StickyOrigin explains the decision for diagnostics.
	StickyOrigin StickySelectionOrigin
}

// EffectiveVDIInput is the complete, provenance-aware selection input set.
type EffectiveVDIInput struct {
	// Leased is the original pool entry. It supplies the stable VDI identity,
	// the global proxy/sticky defaults and the operator-approved profile list.
	Leased Selenium
	// LeasedIndex is the pool slot/configuration index when known.
	LeasedIndex int
	// Merged is the per-source merged VDI list (already isolated from the
	// process-global configuration).
	Merged []Selenium
	// Source is the strictly decoded source document subset.
	Source SourceConfig
	// Raw is the original source document, used only for legacy proxy_url
	// presence/type handling.
	Raw json.RawMessage
}

// SourceCustomVDIEntry is the strictly decoded source override for one VDI
// position. Only the fields relevant to the browser-session boundary are
// exposed; unrelated custom extensions remain untouched.
type SourceCustomVDIEntry struct {
	ProxyURL       string
	ProxyPresent   bool
	StickySession  StickySessionConfig
	StickyPresent  bool
	SourcePosition int
}

// NormalizeStickySessionProfile validates and canonicalizes one complete
// profile. An empty profile is accepted only when allowEmpty is true.
func NormalizeStickySessionProfile(profile StickySessionConfig, allowEmpty bool) (StickySessionConfig, error) {
	profile.ProxyURL = strings.TrimSpace(profile.ProxyURL)
	profile.APIURL = strings.TrimSpace(profile.APIURL)
	profile.VDIAPIURL = strings.TrimSpace(profile.VDIAPIURL)
	profile.TokenEnv = strings.TrimSpace(profile.TokenEnv)
	profile.Provider = strings.TrimSpace(profile.Provider)

	if profile.IsEmpty() {
		if allowEmpty {
			return StickySessionConfig{}, nil
		}
		return StickySessionConfig{}, fmt.Errorf(
			"sticky_session requires proxy_url, api_url, token_env and provider together")
	}
	if !profile.Configured() {
		return StickySessionConfig{}, fmt.Errorf(
			"sticky_session requires proxy_url, api_url, token_env and provider together")
	}
	proxyURL, err := NormalizeStickySessionURL(profile.ProxyURL)
	if err != nil {
		return StickySessionConfig{}, fmt.Errorf("sticky_session.proxy_url: %w", err)
	}
	apiURL, err := NormalizeStickyAPIURL(profile.APIURL)
	if err != nil {
		return StickySessionConfig{}, fmt.Errorf("sticky_session.api_url: %w", err)
	}
	vdiAPIURL := ""
	if profile.VDIAPIURL != "" {
		vdiAPIURL, err = NormalizeStickyAPIURL(profile.VDIAPIURL)
		if err != nil {
			return StickySessionConfig{}, fmt.Errorf("sticky_session.vdi_api_url: %w", err)
		}
	}
	if !validEnvironmentVariableName(profile.TokenEnv) {
		return StickySessionConfig{}, fmt.Errorf(
			"sticky_session.token_env must be a valid environment variable name")
	}
	if strings.ContainsAny(profile.Provider, " \t\r\n") {
		return StickySessionConfig{}, fmt.Errorf(
			"sticky_session.provider must not contain whitespace")
	}
	profile.ProxyURL = proxyURL
	profile.APIURL = apiURL
	profile.VDIAPIURL = vdiAPIURL
	return profile, nil
}

// Equal reports whether two profiles are the same normalized profile.
func (s StickySessionConfig) Equal(other StickySessionConfig) bool {
	return s.ProxyURL == other.ProxyURL &&
		s.APIURL == other.APIURL &&
		s.VDIAPIURL == other.VDIAPIURL &&
		s.TokenEnv == other.TokenEnv &&
		s.Provider == other.Provider
}

// ApprovedStickyProfiles returns the normalized default and allowlisted
// profiles for this VDI. It rejects duplicate profiles and duplicate listener
// bindings, which would make profile selection ambiguous.
func ApprovedStickyProfiles(sel Selenium) ([]StickySessionConfig, error) {
	profiles := make([]StickySessionConfig, 0, 1+len(sel.ApprovedStickyProfiles))
	if !sel.StickySession.IsEmpty() {
		profile, err := NormalizeStickySessionProfile(sel.StickySession, false)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	for i, raw := range sel.ApprovedStickyProfiles {
		profile, err := NormalizeStickySessionProfile(raw, false)
		if err != nil {
			return nil, fmt.Errorf("approved_sticky_profiles[%d]: %w", i, err)
		}
		for _, existing := range profiles {
			if existing.Equal(profile) {
				return nil, fmt.Errorf(
					"approved_sticky_profiles[%d] duplicates another approved profile", i)
			}
			if existing.ProxyURL == profile.ProxyURL {
				return nil, fmt.Errorf(
					"approved_sticky_profiles[%d] reuses the sticky listener of another profile", i)
			}
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

// ParseSourceCustomVDIEntry strictly decodes the source override for one VDI
// position. A missing custom.vdi list or a position beyond its length means no
// override. A present but malformed entry, proxy_url, or sticky_session is an
// error so it cannot silently inherit an unrelated profile.
func ParseSourceCustomVDIEntry(source SourceConfig, index int) (SourceCustomVDIEntry, error) {
	entry := SourceCustomVDIEntry{SourcePosition: index}
	if source.Custom == nil {
		return entry, nil
	}
	rawEntries, ok := source.Custom["vdi"]
	if !ok || rawEntries == nil {
		return entry, nil
	}
	entries, ok := rawEntries.([]interface{})
	if !ok {
		return entry, fmt.Errorf("custom.vdi must be a list")
	}
	if index < 0 || index >= len(entries) {
		return entry, nil
	}
	rawEntry, ok := entries[index].(map[string]interface{})
	if !ok {
		return entry, fmt.Errorf("custom.vdi[%d] must be an object", index)
	}
	if rawProxy, present := rawEntry["proxy_url"]; present && rawProxy != nil {
		value, ok := rawProxy.(string)
		if !ok {
			return entry, fmt.Errorf("custom.vdi[%d].proxy_url must be a string", index)
		}
		entry.ProxyURL = value
		entry.ProxyPresent = true
	}
	if rawSticky, present := rawEntry["sticky_session"]; present {
		profile, err := decodeStickySessionObject(rawSticky)
		if err != nil {
			return entry, fmt.Errorf("custom.vdi[%d].sticky_session: %w", index, err)
		}
		entry.StickySession = profile
		entry.StickyPresent = true
	}
	return entry, nil
}

// ValidateSourceCustomVDI validates every present custom.vdi sticky profile.
// It is used by the strict source-session decoder and by source ingestion so a
// malformed profile cannot be hidden behind an unrelated legacy merge failure.
func ValidateSourceCustomVDI(custom map[string]interface{}) error {
	if custom == nil {
		return nil
	}
	rawEntries, ok := custom["vdi"]
	if !ok || rawEntries == nil {
		return nil
	}
	entries, ok := rawEntries.([]interface{})
	if !ok {
		return fmt.Errorf("custom.vdi must be a list")
	}
	for i, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]interface{})
		if !ok {
			return fmt.Errorf("custom.vdi[%d] must be an object", i)
		}
		if rawProxy, present := entry["proxy_url"]; present && rawProxy != nil {
			if _, ok := rawProxy.(string); !ok {
				return fmt.Errorf("custom.vdi[%d].proxy_url must be a string", i)
			}
		}
		if rawSticky, present := entry["sticky_session"]; present {
			if _, err := decodeStickySessionObject(rawSticky); err != nil {
				return fmt.Errorf("custom.vdi[%d].sticky_session: %w", i, err)
			}
		}
	}
	return nil
}

// SourceCustomVDIHasSticky reports whether any custom.vdi entry explicitly
// supplies a sticky profile. The custom tree must already have passed strict
// validation; a malformed tree is still reported as an error.
func SourceCustomVDIHasSticky(custom map[string]interface{}) (bool, error) {
	if err := ValidateSourceCustomVDI(custom); err != nil {
		return false, err
	}
	rawEntries, ok := custom["vdi"]
	if !ok || rawEntries == nil {
		return false, nil
	}
	entries := rawEntries.([]interface{})
	for _, rawEntry := range entries {
		entry := rawEntry.(map[string]interface{})
		if _, present := entry["sticky_session"]; present {
			return true, nil
		}
	}
	return false, nil
}

func decodeStickySessionObject(raw interface{}) (StickySessionConfig, error) {
	value, ok := raw.(map[string]interface{})
	if !ok {
		return StickySessionConfig{}, fmt.Errorf("must be an object")
	}
	allowed := map[string]bool{
		"proxy_url":   true,
		"api_url":     true,
		"vdi_api_url": true,
		"token_env":   true,
		"provider":    true,
	}
	profile := StickySessionConfig{}
	for key, rawValue := range value {
		if !allowed[key] {
			return StickySessionConfig{}, fmt.Errorf("unknown field %q", key)
		}
		if rawValue == nil {
			return StickySessionConfig{}, fmt.Errorf("%s must be a string", key)
		}
		text, ok := rawValue.(string)
		if !ok {
			return StickySessionConfig{}, fmt.Errorf("%s must be a string", key)
		}
		switch key {
		case "proxy_url":
			profile.ProxyURL = text
		case "api_url":
			profile.APIURL = text
		case "vdi_api_url":
			profile.VDIAPIURL = text
		case "token_env":
			profile.TokenEnv = text
		case "provider":
			profile.Provider = text
		}
	}
	normalized, err := NormalizeStickySessionProfile(profile, false)
	if err != nil {
		return StickySessionConfig{}, err
	}
	return normalized, nil
}

// ValidateSourceStickyAuthorization enforces the operator allowlist for
// source-provided sticky control parameters. It is called at API ingestion and
// again by the runtime selection so stored legacy rows cannot bypass it.
func (c Config) ValidateSourceStickyAuthorization(source SourceConfig) error {
	if err := ValidateSourceCustomVDI(source.Custom); err != nil {
		return err
	}
	rawEntries, ok := source.Custom["vdi"]
	if !ok || rawEntries == nil {
		return nil
	}
	entries := rawEntries.([]interface{})
	for i, rawEntry := range entries {
		entry := rawEntry.(map[string]interface{})
		if _, present := entry["sticky_session"]; !present {
			continue
		}
		if i >= len(c.Selenium) {
			return fmt.Errorf(
				"custom.vdi[%d].sticky_session does not map to a configured VDI", i)
		}
		profile, err := ParseSourceCustomVDIEntry(source, i)
		if err != nil {
			return err
		}
		approved, err := ApprovedStickyProfiles(c.Selenium[i])
		if err != nil {
			return err
		}
		if _, ok := matchApprovedProfile(profile.StickySession, approved); !ok {
			return fmt.Errorf(
				"custom.vdi[%d].sticky_session is not approved by the operator for VDI %q",
				i, c.Selenium[i].Name)
		}
	}
	return nil
}

// ResolveSourceProxy resolves the ordinary (non-sticky) proxy for one source
// and VDI. It is used by non-web protocols, which do not enter the sticky
// source-start boundary but may still use a browser for optional email-link
// browsing. The ordinary precedence and sentinel behavior are preserved.
func ResolveSourceProxy(in EffectiveVDIInput) (ProxyResolution, error) {
	index, err := effectiveVDIIndex(in.Leased, in.Merged, in.LeasedIndex)
	if err != nil {
		return ProxyResolution{}, err
	}
	custom, err := ParseSourceCustomVDIEntry(in.Source, index)
	if err != nil {
		return ProxyResolution{}, err
	}
	return resolveNonStickyForSelection(in, custom)
}

// ResolveEffectiveVDI applies the documented per-crawl selection order.
func ResolveEffectiveVDI(in EffectiveVDIInput) (EffectiveVDISelection, error) {
	index, err := effectiveVDIIndex(in.Leased, in.Merged, in.LeasedIndex)
	if err != nil {
		return EffectiveVDISelection{}, err
	}
	custom, err := ParseSourceCustomVDIEntry(in.Source, index)
	if err != nil {
		return EffectiveVDISelection{}, err
	}
	approved, err := ApprovedStickyProfiles(in.Leased)
	if err != nil {
		return EffectiveVDISelection{}, err
	}

	selection := EffectiveVDISelection{StickyOrigin: StickySelectionNone}
	selection.Browser = in.Merged[index]
	selection.Management = in.Leased

	explicit := in.Source.CrawlingConfig.StickySession
	stickyRequested := (explicit != nil && *explicit) || custom.StickyPresent

	if explicit != nil && !*explicit {
		if custom.StickyPresent {
			return EffectiveVDISelection{}, fmt.Errorf(
				"crawling_config.sticky_session:false conflicts with custom.vdi[%d].sticky_session",
				index)
		}
		preliminary, err := resolveNonStickyForSelection(in, custom)
		if err != nil {
			return EffectiveVDISelection{}, err
		}
		if match, ok, err := matchKnownStickyListener(preliminary.URL, approved); err != nil {
			return EffectiveVDISelection{}, err
		} else if ok {
			return EffectiveVDISelection{}, fmt.Errorf(
				"crawling_config.sticky_session:false would browse the configured sticky listener for VDI %q without rotation (provider %q)",
				in.Leased.Name, match.Provider)
		}
		return finishSelection(selection, preliminary, StickySessionConfig{}, StickySelectionNone)
	}

	if stickyRequested {
		profile, origin, err := selectStickyProfile(in, custom, approved)
		if err != nil {
			return EffectiveVDISelection{}, err
		}
		resolution, err := resolveStickyForSelection(in, custom, profile)
		if err != nil {
			return EffectiveVDISelection{}, err
		}
		if !VDIByNameUnique(in.Merged, in.Leased.Name) {
			return EffectiveVDISelection{}, fmt.Errorf(
				"sticky_session requires a unique VDI association for %q", in.Leased.Name)
		}
		selection.StickyProfile = profile
		selection.StickyOrigin = origin
		return finishSelection(selection, resolution, profile, origin)
	}

	preliminary, err := resolveNonStickyForSelection(in, custom)
	if err != nil {
		return EffectiveVDISelection{}, err
	}
	if match, ok, err := matchKnownStickyListener(preliminary.URL, approved); err != nil {
		return EffectiveVDISelection{}, err
	} else if ok {
		resolution, err := resolveStickyForSelection(in, custom, match)
		if err != nil {
			return EffectiveVDISelection{}, err
		}
		if !VDIByNameUnique(in.Merged, in.Leased.Name) {
			return EffectiveVDISelection{}, fmt.Errorf(
				"sticky_session requires a unique VDI association for %q", in.Leased.Name)
		}
		origin := StickySelectionListener
		global, globalErr := NormalizeStickySessionProfile(in.Leased.StickySession, true)
		if globalErr == nil && !global.IsEmpty() && global.Equal(match) && !hasExplicitSourceProxy(in, custom) {
			origin = StickySelectionGlobal
		}
		return finishSelection(selection, resolution, match, origin)
	}
	return finishSelection(selection, preliminary, StickySessionConfig{}, StickySelectionNone)
}

func hasExplicitSourceProxy(in EffectiveVDIInput, custom SourceCustomVDIEntry) bool {
	if strings.TrimSpace(in.Source.CrawlingConfig.Proxy) != "" {
		return true
	}
	if custom.ProxyPresent && strings.TrimSpace(custom.ProxyURL) != "" {
		return true
	}
	legacy, present := LegacyTopLevelProxy(in.Raw)
	return present && strings.TrimSpace(legacy) != ""
}

func finishSelection(selection EffectiveVDISelection, resolution ProxyResolution, profile StickySessionConfig, origin StickySelectionOrigin) (EffectiveVDISelection, error) {
	if err := ValidateBrowserProxyCompatibility(selection.Browser.Type, resolution.URL); err != nil {
		return EffectiveVDISelection{}, err
	}
	selection.Proxy = resolution
	selection.Sticky = origin != StickySelectionNone
	selection.StickyOrigin = origin
	selection.StickyProfile = profile
	if selection.Sticky {
		selection.Browser.StickySession = profile
		selection.Management.StickySession = profile
	} else {
		selection.Browser.StickySession = StickySessionConfig{}
		selection.Management.StickySession = StickySessionConfig{}
	}
	selection.Browser.ProxyURL = resolution.URL
	return selection, nil
}

func effectiveVDIIndex(leased Selenium, merged []Selenium, leasedIndex int) (int, error) {
	if len(merged) == 0 {
		return 0, fmt.Errorf("effective VDI selection requires at least one configured VDI")
	}
	if leasedIndex >= 0 && leasedIndex < len(merged) &&
		strings.EqualFold(strings.TrimSpace(leased.Name), strings.TrimSpace(merged[leasedIndex].Name)) {
		return leasedIndex, nil
	}
	matches := make([]int, 0, 1)
	for i := range merged {
		if strings.EqualFold(strings.TrimSpace(merged[i].Name), strings.TrimSpace(leased.Name)) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf("leased VDI %q is not present in the effective configuration", leased.Name)
	}
	return 0, fmt.Errorf("leased VDI %q is ambiguous in the effective configuration", leased.Name)
}

func resolveNonStickyForSelection(in EffectiveVDIInput, custom SourceCustomVDIEntry) (ProxyResolution, error) {
	legacy, legacyPresent := LegacyTopLevelProxy(in.Raw)
	return ResolveEffectiveProxy(EffectiveProxyInput{
		SourceProxy:             in.Source.CrawlingConfig.Proxy,
		LegacyProxy:             legacy,
		LegacyProxyPresent:      legacyPresent,
		CustomProxy:             custom.ProxyURL,
		CustomProxyPresent:      custom.ProxyPresent,
		VDIProxy:                in.Leased.ProxyURL,
		Sticky:                  false,
		StickyProfileConfigured: false,
	})
}

func resolveStickyForSelection(in EffectiveVDIInput, custom SourceCustomVDIEntry, profile StickySessionConfig) (ProxyResolution, error) {
	legacy, legacyPresent, err := LegacyTopLevelProxyStrict(in.Raw)
	if err != nil {
		return ProxyResolution{}, fmt.Errorf("sticky_session: %w", err)
	}
	return ResolveEffectiveProxy(EffectiveProxyInput{
		SourceProxy:             in.Source.CrawlingConfig.Proxy,
		LegacyProxy:             legacy,
		LegacyProxyPresent:      legacyPresent,
		CustomProxy:             custom.ProxyURL,
		CustomProxyPresent:      custom.ProxyPresent,
		VDIProxy:                in.Leased.ProxyURL,
		Sticky:                  true,
		StickyProxy:             profile.ProxyURL,
		StickyProfileConfigured: true,
	})
}

func selectStickyProfile(in EffectiveVDIInput, custom SourceCustomVDIEntry, approved []StickySessionConfig) (StickySessionConfig, StickySelectionOrigin, error) {
	if custom.StickyPresent {
		profile, ok := matchApprovedProfile(custom.StickySession, approved)
		if !ok {
			return StickySessionConfig{}, StickySelectionNone, fmt.Errorf(
				"custom.vdi[%d].sticky_session is not approved by the operator for VDI %q",
				custom.SourcePosition, in.Leased.Name)
		}
		return profile, StickySelectionProfile, nil
	}

	if in.Source.CrawlingConfig.StickySession != nil && *in.Source.CrawlingConfig.StickySession {
		if explicitProfile, ok, err := selectExplicitProxyProfile(in, custom, approved); err != nil {
			return StickySessionConfig{}, StickySelectionNone, err
		} else if ok {
			return explicitProfile, StickySelectionExplicit, nil
		}
		if len(approved) > 0 {
			if in.Leased.StickySession.IsEmpty() {
				return StickySessionConfig{}, StickySelectionNone, fmt.Errorf(
					"sticky_session is enabled but the selected VDI has no default sticky_session profile; add an approved_sticky_profiles entry or select one with custom.vdi[].sticky_session")
			}
			return approved[0], StickySelectionExplicit, nil
		}
		return StickySessionConfig{}, StickySelectionNone, fmt.Errorf(
			"sticky_session is enabled but the selected VDI has no complete sticky_session profile")
	}
	return StickySessionConfig{}, StickySelectionNone, fmt.Errorf(
		"sticky_session requires a complete effective profile")
}

func selectExplicitProxyProfile(in EffectiveVDIInput, custom SourceCustomVDIEntry, approved []StickySessionConfig) (StickySessionConfig, bool, error) {
	values := make([]string, 0, 3)
	if value := strings.TrimSpace(in.Source.CrawlingConfig.Proxy); value != "" {
		values = append(values, value)
	}
	legacy, legacyPresent, err := LegacyTopLevelProxyStrict(in.Raw)
	if err != nil {
		return StickySessionConfig{}, false, fmt.Errorf("sticky_session: %w", err)
	}
	if legacyPresent && strings.TrimSpace(legacy) != "" {
		values = append(values, strings.TrimSpace(legacy))
	}
	if custom.ProxyPresent && strings.TrimSpace(custom.ProxyURL) != "" {
		values = append(values, strings.TrimSpace(custom.ProxyURL))
	}
	matched := make([]StickySessionConfig, 0, 1)
	for _, value := range values {
		if value == "no_proxy" {
			continue
		}
		normalized, err := NormalizeSourceProxyURL(value)
		if err != nil {
			return StickySessionConfig{}, false, fmt.Errorf("sticky_session explicit proxy: %w", err)
		}
		for _, profile := range approved {
			if profile.ProxyURL == normalized && !containsProfile(matched, profile) {
				matched = append(matched, profile)
			}
		}
	}
	switch len(matched) {
	case 0:
		return StickySessionConfig{}, false, nil
	case 1:
		return matched[0], true, nil
	default:
		return StickySessionConfig{}, false, fmt.Errorf(
			"sticky_session explicit proxy matches multiple approved profiles for VDI %q", in.Leased.Name)
	}
}

func matchApprovedProfile(candidate StickySessionConfig, approved []StickySessionConfig) (StickySessionConfig, bool) {
	for _, profile := range approved {
		if profile.Equal(candidate) {
			return profile, true
		}
	}
	return StickySessionConfig{}, false
}

func containsProfile(profiles []StickySessionConfig, candidate StickySessionConfig) bool {
	for _, profile := range profiles {
		if profile.Equal(candidate) {
			return true
		}
	}
	return false
}

func matchKnownStickyListener(raw string, approved []StickySessionConfig) (StickySessionConfig, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "no_proxy" {
		return StickySessionConfig{}, false, nil
	}
	normalized, err := NormalizeStickySessionURL(trimmed)
	if err != nil {
		return StickySessionConfig{}, false, nil
	}
	var matched StickySessionConfig
	found := false
	for _, profile := range approved {
		if profile.ProxyURL != normalized {
			continue
		}
		if found && !matched.Equal(profile) {
			return StickySessionConfig{}, false, fmt.Errorf(
				"effective proxy matches multiple approved sticky profiles")
		}
		matched = profile
		found = true
	}
	return matched, found, nil
}

// ValidateStickySelectionSite validates the retrieval URL used by a sticky
// browser crawl. An absent source-document site falls back to the actual
// Source URL, because a source may have no configuration document at all.
func ValidateStickySelectionSite(crawling CrawlingConfig, sourceURL string) error {
	site := strings.TrimSpace(crawling.Site)
	if site == "" {
		site = strings.TrimSpace(sourceURL)
	}
	if err := ValidateStickySessionSite(site); err != nil {
		return err
	}
	return nil
}

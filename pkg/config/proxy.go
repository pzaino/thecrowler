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

// This file owns the single proxy-resolution contract shared by the
// source-start sticky-session hook and the browser launcher. Nothing here
// mutates configuration: every function is a pure transformation of its
// inputs so two concurrent sources cannot share a proxy decision.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ProxySource identifies the configuration layer that supplied the effective
// proxy. It is diagnostic only and never grants authority.
type ProxySource string

const (
	// ProxySourceDirect means no downstream proxy is configured.
	ProxySourceDirect ProxySource = "direct"
	// ProxySourceCanonical is the schema-supported source field
	// crawling_config.proxy.
	ProxySourceCanonical ProxySource = "crawling_config.proxy"
	// ProxySourceLegacy is the historical top-level source proxy_url. It is
	// read for already-stored documents but is not a supported schema field.
	ProxySourceLegacy ProxySource = "source.proxy_url"
	// ProxySourceCustomVDI is an explicitly source-supplied
	// custom.vdi[].proxy_url for the selected VDI.
	ProxySourceCustomVDI ProxySource = "custom.vdi.proxy_url"
	// ProxySourceVDI is the global vdi[].proxy_url default.
	ProxySourceVDI ProxySource = "vdi.proxy_url"
	// ProxySourceSticky is the trusted vdi[].sticky_session.proxy_url
	// listener selected for a sticky source.
	ProxySourceSticky ProxySource = "vdi.sticky_session.proxy_url"
)

// ProxyResolution is the exact downstream proxy the browser must use.
type ProxyResolution struct {
	URL    string
	Source ProxySource
}

// EffectiveProxyInput is the complete, provenance-aware proxy input set.
//
// LegacyProxyPresent and CustomProxyPresent record key presence in the source
// document, not merely a non-empty value: an explicitly blank value inherits
// the next layer exactly as it did before crawling_config.proxy existed.
type EffectiveProxyInput struct {
	SourceProxy        string
	LegacyProxy        string
	LegacyProxyPresent bool
	CustomProxy        string
	CustomProxyPresent bool
	VDIProxy           string

	Sticky                  bool
	StickyProxy             string
	StickyProfileConfigured bool
}

// ResolveEffectiveProxy applies the documented precedence:
//
//	non-sticky: crawling_config.proxy > legacy source proxy_url >
//	            explicit custom.vdi proxy_url > vdi.proxy_url > direct
//	sticky:     the trusted vdi.sticky_session.proxy_url listener, with any
//	            explicit source override required to match it exactly
//
// The legacy and custom layers preserve their historical sentinel semantics
// (the exact lowercase value "no_proxy" selects a direct connection). A
// malformed canonical proxy is an error rather than a silent direct fallback.
func ResolveEffectiveProxy(in EffectiveProxyInput) (ProxyResolution, error) {
	if in.Sticky {
		return resolveStickyProxy(in)
	}
	return resolveNonStickyProxy(in)
}

func resolveNonStickyProxy(in EffectiveProxyInput) (ProxyResolution, error) {
	if canonical := strings.TrimSpace(in.SourceProxy); canonical != "" {
		normalized, err := NormalizeSourceProxyURL(canonical)
		if err != nil {
			return ProxyResolution{}, fmt.Errorf("crawling_config.proxy: %w", err)
		}
		return ProxyResolution{URL: normalized, Source: ProxySourceCanonical}, nil
	}

	effective := in.VDIProxy
	source := ProxySourceVDI

	if custom := strings.TrimSpace(in.CustomProxy); in.CustomProxyPresent && custom != "" {
		if custom == "no_proxy" {
			effective = ""
		} else {
			effective = custom
		}
		source = ProxySourceCustomVDI
	}

	if in.LegacyProxyPresent {
		if legacy := strings.TrimSpace(in.LegacyProxy); legacy != "" {
			if legacy == "no_proxy" {
				effective = ""
			} else {
				effective = legacy
			}
			source = ProxySourceLegacy
		}
	}

	if strings.TrimSpace(effective) == "" {
		return ProxyResolution{URL: "", Source: ProxySourceDirect}, nil
	}
	return ProxyResolution{URL: effective, Source: source}, nil
}

func resolveStickyProxy(in EffectiveProxyInput) (ProxyResolution, error) {
	if !in.StickyProfileConfigured {
		return ProxyResolution{}, fmt.Errorf(
			"sticky_session is enabled but the selected VDI has no complete sticky_session profile")
	}
	stickyURL, err := NormalizeStickySessionURL(in.StickyProxy)
	if err != nil {
		return ProxyResolution{}, fmt.Errorf("vdi.sticky_session.proxy_url: %w", err)
	}

	for _, override := range []struct {
		name    string
		value   string
		present bool
	}{
		{"crawling_config.proxy", in.SourceProxy, strings.TrimSpace(in.SourceProxy) != ""},
		{"source.proxy_url", in.LegacyProxy, in.LegacyProxyPresent && strings.TrimSpace(in.LegacyProxy) != ""},
		{"custom.vdi.proxy_url", in.CustomProxy, in.CustomProxyPresent && strings.TrimSpace(in.CustomProxy) != ""},
	} {
		if !override.present {
			continue
		}
		value := strings.TrimSpace(override.value)
		if value == "no_proxy" {
			return ProxyResolution{}, fmt.Errorf(
				"%s cannot disable the proxy for a sticky_session source", override.name)
		}
		normalized, err := NormalizeSourceProxyURL(value)
		if err != nil {
			return ProxyResolution{}, fmt.Errorf("%s: %w", override.name, err)
		}
		if normalized != stickyURL {
			return ProxyResolution{}, fmt.Errorf(
				"%s does not match the selected VDI sticky listener", override.name)
		}
	}
	return ProxyResolution{URL: stickyURL, Source: ProxySourceSticky}, nil
}

// NormalizeSourceProxyURL validates a browser downstream proxy listener and
// returns a canonical scheme://host:port representation. It intentionally
// rejects URL credentials, resource paths, queries and fragments: the value
// selects a listener, not an HTTP resource.
func NormalizeSourceProxyURL(raw string) (string, error) {
	return normalizeProxyListener(raw, map[string]struct{}{
		"http":   {},
		"https":  {},
		"socks5": {},
	})
}

// NormalizeStickySessionURL validates the trusted microproxy sticky listener.
// The microproxy data plane supports HTTP/HTTPS downstream listeners.
func NormalizeStickySessionURL(raw string) (string, error) {
	return normalizeProxyListener(raw, map[string]struct{}{
		"http":  {},
		"https": {},
	})
}

// NormalizeStickyAPIURL validates the trusted microproxy data-plane API origin.
func NormalizeStickyAPIURL(raw string) (string, error) {
	return normalizeProxyListener(raw, map[string]struct{}{
		"http":  {},
		"https": {},
	})
}

// ValidateSourceProtocolOptions validates the browser-protocol fields that are
// shared by source ingest and runtime. It deliberately does not require the
// complete SourceConfig document, so API update paths can reuse it without
// changing their partial-update semantics.
func ValidateSourceProtocolOptions(source SourceConfig) error {
	if proxy := strings.TrimSpace(source.CrawlingConfig.Proxy); proxy != "" {
		if _, err := NormalizeSourceProxyURL(proxy); err != nil {
			return fmt.Errorf("invalid crawling_config.proxy: %w", err)
		}
	}
	hasCustomSticky, err := SourceCustomVDIHasSticky(source.Custom)
	if err != nil {
		return err
	}
	sticky := source.CrawlingConfig.StickySession
	if sticky != nil && !*sticky && hasCustomSticky {
		return fmt.Errorf("crawling_config.sticky_session:false conflicts with a custom.vdi[].sticky_session profile")
	}
	stickyRequested := hasCustomSticky || (sticky != nil && *sticky)
	if !stickyRequested {
		return nil
	}
	if source.Email != nil {
		return fmt.Errorf("sticky_session is only supported for browser-backed sources")
	}
	if strings.EqualFold(strings.TrimSpace(source.CrawlingConfig.SourceType), SourceTypeEmail) {
		return fmt.Errorf("sticky_session is not supported for email sources")
	}
	if strings.TrimSpace(source.CrawlingConfig.Site) != "" {
		if err := ValidateStickySessionSite(source.CrawlingConfig.Site); err != nil {
			return fmt.Errorf("sticky_session: %w", err)
		}
	}
	return nil
}

// ValidateBrowserProxyCompatibility rejects proxy combinations that the
// selected browser cannot launch. Firefox has no HTTPS downstream listener
// support; failing before a sticky rotation avoids consuming a new upstream
// identity that could never be browsed.
func ValidateBrowserProxyCompatibility(browser, proxy string) error {
	if !strings.EqualFold(strings.TrimSpace(browser), "firefox") {
		return nil
	}
	trimmed := strings.TrimSpace(proxy)
	if trimmed == "" || trimmed == "no_proxy" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil // the browser-specific fallback reports malformed listeners
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("firefox does not support an HTTPS downstream proxy listener")
	}
	return nil
}

// ValidateStickySessionSite rejects stored source documents whose retrieval URL
// cannot be served by the browser-backed sticky path. It is used both at ingest
// and again before the crawl boundary so a legacy stored value cannot bypass it.
func ValidateStickySessionSite(site string) error {
	parsed, err := url.Parse(strings.TrimSpace(site))
	if err != nil {
		return fmt.Errorf("sticky_session requires a valid crawling_config.site URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ftp", "ftps":
		if parsed.Hostname() == "" {
			return fmt.Errorf("sticky_session requires a valid crawling_config.site URL")
		}
		return nil
	default:
		return fmt.Errorf("sticky_session is only supported for browser-backed sources")
	}
}

func normalizeProxyListener(raw string, schemes map[string]struct{}) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("URL is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if _, ok := schemes[scheme]; !ok {
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL must not contain credentials")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("URL requires a host")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL must not contain a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || strings.ContainsAny(trimmed, "?#") {
		return "", fmt.Errorf("URL must not contain a query or fragment")
	}
	portText := parsed.Port()
	if portText == "" {
		return "", fmt.Errorf("URL requires an explicit port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("URL has an invalid port")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.ContainsAny(host, " \t\r\n") {
		return "", fmt.Errorf("URL has an invalid host")
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// LegacyTopLevelProxy reads the historical source-document proxy_url key.
// A missing key, JSON null or non-string value is treated as absent, matching
// the pre-existing ConnectVDI behavior. The values themselves are not
// validated here: the legacy layer preserved malformed values for diagnosis.
func LegacyTopLevelProxy(raw json.RawMessage) (string, bool) {
	value, present, err := LegacyTopLevelProxyStrict(raw)
	if err != nil {
		return "", false
	}
	return value, present
}

// LegacyTopLevelProxyStrict is the fail-closed form used for sticky sources:
// a present non-string proxy_url is a malformed override, not an absent key.
func LegacyTopLevelProxyStrict(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	var document struct {
		ProxyURL json.RawMessage `json:"proxy_url"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", false, err
	}
	if len(document.ProxyURL) == 0 || strings.TrimSpace(string(document.ProxyURL)) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(document.ProxyURL, &value); err != nil {
		return "", true, fmt.Errorf("source.proxy_url must be a string")
	}
	return value, true, nil
}

// SourceCustomVDIProxy returns the explicitly source-supplied custom.vdi
// proxy_url for the named VDI. custom.vdi is merged positionally against the
// configured VDI list, so the same positional rule is used here. A malformed
// entry or an absent key reports "no override", matching combineVDICfg.
func SourceCustomVDIProxy(source SourceConfig, vdiOrder []Selenium, vdiName string) (string, bool) {
	value, present, err := SourceCustomVDIProxyStrict(source, vdiOrder, vdiName)
	if err != nil {
		return "", false
	}
	return value, present
}

// SourceCustomVDIProxyStrict is the fail-closed form used for sticky sources:
// a present but malformed proxy_url is an error rather than an absent override.
func SourceCustomVDIProxyStrict(source SourceConfig, vdiOrder []Selenium, vdiName string) (string, bool, error) {
	vdiName = strings.TrimSpace(vdiName)
	if vdiName == "" || source.Custom == nil {
		return "", false, nil
	}
	entries, ok := source.Custom["vdi"].([]interface{})
	if !ok {
		return "", false, nil
	}
	index := -1
	for i := range vdiOrder {
		if strings.EqualFold(strings.TrimSpace(vdiOrder[i].Name), vdiName) {
			index = i
			break
		}
	}
	if index < 0 || index >= len(entries) {
		return "", false, nil
	}
	entry, ok := entries[index].(map[string]interface{})
	if !ok {
		return "", false, fmt.Errorf("custom.vdi[%d] must be an object", index)
	}
	raw, ok := entry["proxy_url"]
	if !ok {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("custom.vdi[%d].proxy_url must be a string", index)
	}
	return value, true, nil
}

// VDIByNameUnique reports whether the configured VDI list names the selected
// VDI exactly once. Sticky sources fail closed on an ambiguous association.
func VDIByNameUnique(vdiOrder []Selenium, vdiName string) bool {
	vdiName = strings.TrimSpace(vdiName)
	if vdiName == "" {
		return false
	}
	matches := 0
	for i := range vdiOrder {
		if strings.EqualFold(strings.TrimSpace(vdiOrder[i].Name), vdiName) {
			matches++
		}
	}
	return matches == 1
}

// DecodeSourceConfig strictly decodes one stored source document. The
// CrawlingConfig decoder enforces the strict boolean contract for incognito,
// follow_redirects and sticky_session.
func DecodeSourceConfig(raw json.RawMessage) (SourceConfig, error) {
	var source SourceConfig
	if err := json.Unmarshal(raw, &source); err != nil {
		return SourceConfig{}, err
	}
	return source, nil
}

// SourceSessionConfig is the subset of a source document that controls the
// browser-session boundary. It is decoded strictly and independently of
// unrelated document fields so a malformed unrelated value cannot hide a
// sticky-session opt-in.
type SourceSessionConfig struct {
	CrawlingConfig CrawlingConfig
	Custom         map[string]interface{}
}

// DecodeSourceSessionConfig strictly decodes the crawling_config and custom
// subtrees used by proxy and sticky-session resolution. A malformed
// crawling_config is always an error because it may contain the opt-in. A
// malformed custom subtree or custom.vdi sticky profile is also an error: it
// could otherwise silently drop a source-selected profile or inherit an
// unrelated global one.
func DecodeSourceSessionConfig(raw json.RawMessage) (SourceSessionConfig, error) {
	var envelope struct {
		CrawlingConfig json.RawMessage `json:"crawling_config"`
		Custom         json.RawMessage `json:"custom"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SourceSessionConfig{}, err
	}
	var result SourceSessionConfig
	if len(envelope.CrawlingConfig) != 0 && strings.TrimSpace(string(envelope.CrawlingConfig)) != "null" {
		if err := json.Unmarshal(envelope.CrawlingConfig, &result.CrawlingConfig); err != nil {
			return SourceSessionConfig{}, fmt.Errorf("crawling_config: %w", err)
		}
	}
	if len(envelope.Custom) != 0 && strings.TrimSpace(string(envelope.Custom)) != "null" {
		if err := json.Unmarshal(envelope.Custom, &result.Custom); err != nil {
			return SourceSessionConfig{}, fmt.Errorf("custom: %w", err)
		}
		if result.Custom == nil {
			return SourceSessionConfig{}, fmt.Errorf("custom must be an object")
		}
		if err := ValidateSourceCustomVDI(result.Custom); err != nil {
			return SourceSessionConfig{}, err
		}
	}
	return result, nil
}

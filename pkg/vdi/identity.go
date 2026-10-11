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

package vdi

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// OSIdentity carries the per-API representations of one advertised browser
// OS for a single selection event.
//
// Preservation note (PR2, VDI-005): the host OS (e.g. Linux in the VDI
// container) and the advertised browser OS (crawler.browser_platform) are
// different concepts. A user-selected identity is preserved across every
// surface CROWler configures; surfaces it cannot configure keep native
// values and are reported as limitations, never silently normalized.
type OSIdentity struct {
	// Key is the resolved OS key: windows, darwin, linux, android, ios.
	Key string
	// NavigatorPlatform is the navigator.platform (and CDP platform
	// override) representation, e.g. "Win32".
	NavigatorPlatform string
	// UserAgentDataPlatform is the navigator.userAgentData.platform (and
	// Client Hints metadata platform) representation, e.g. "Windows".
	UserAgentDataPlatform string
	// Mobile reflects the configured device class (crawler.platform).
	Mobile bool
}

// MapAdvertisedOSIdentity maps a resolved OS key to the representation each
// browser interface requires. The literal configured enum is never reused
// blindly: "darwin" is not a valid navigator.platform ("MacIntel" is), and
// the device class ("desktop"/"mobile") is never a platform at all.
// Unknown keys fall back to the Linux identity, matching the global default.
func MapAdvertisedOSIdentity(osKey string, mobile bool) OSIdentity {
	switch strings.ToLower(strings.TrimSpace(osKey)) {
	case "windows":
		return OSIdentity{Key: "windows", NavigatorPlatform: "Win32", UserAgentDataPlatform: "Windows", Mobile: mobile}
	case "darwin", "macos", "mac":
		return OSIdentity{Key: "darwin", NavigatorPlatform: "MacIntel", UserAgentDataPlatform: "macOS", Mobile: mobile}
	case "android":
		return OSIdentity{Key: "android", NavigatorPlatform: "Linux armv8l", UserAgentDataPlatform: "Android", Mobile: mobile}
	case "ios":
		return OSIdentity{Key: "ios", NavigatorPlatform: "iPhone", UserAgentDataPlatform: "iOS", Mobile: mobile}
	default:
		return OSIdentity{Key: "linux", NavigatorPlatform: "Linux x86_64", UserAgentDataPlatform: "Linux", Mobile: mobile}
	}
}

var chromeVersionPattern = regexp.MustCompile(`Chrome/(\d+)`)

// ChromeMajorVersion parses the major version from a Chrome/Chromium
// User-Agent string (e.g. "131" from "Chrome/131.0.0.0"). ok is false when
// the UA carries no Chrome token (notably Firefox/Safari identities), in
// which case callers must leave Client Hints brand surfaces alone instead of
// advertising a fabricated version.
func ChromeMajorVersion(userAgent string) (major string, ok bool) {
	m := chromeVersionPattern.FindStringSubmatch(userAgent)
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// ClientHintBrand is one navigator.userAgentData.brands / Sec-CH-UA entry.
type ClientHintBrand struct {
	Brand   string
	Version string
}

// notABrandVersion is the GREASE brand version pinned by the long-standing
// script body; it is preserved, not derived.
const notABrandVersion = "8"

// ClientHintBrandsFor builds the brand list appropriate to the selected
// browser product (review finding 1). Chromium-family products keep the
// two-brand Chromium shape, with the product brand appended for Edge and
// Opera using each product's own major version; non-Chromium products (and
// legacy EdgeHTML, which predates Client Hints) yield nil so brand surfaces
// stay native instead of fabricated. Camouflage is not restricted to the
// installed browser product: any configured identity keeps its own brands.
func ClientHintBrandsFor(product, chromeMajor, productMajor string) []ClientHintBrand {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "edge":
		if chromeMajor == "" || productMajor == "" {
			return nil
		}
		return []ClientHintBrand{
			{Brand: "Chromium", Version: chromeMajor},
			{Brand: "Not-A.Brand", Version: notABrandVersion},
			{Brand: "Microsoft Edge", Version: productMajor},
		}
	case "opera":
		if chromeMajor == "" || productMajor == "" {
			return nil
		}
		return []ClientHintBrand{
			{Brand: "Chromium", Version: chromeMajor},
			{Brand: "Not-A.Brand", Version: notABrandVersion},
			{Brand: "Opera", Version: productMajor},
		}
	case "chrome", "chromium", "herring", "atcontent":
		if chromeMajor == "" {
			return nil
		}
		return []ClientHintBrand{
			{Brand: "Chromium", Version: chromeMajor},
			{Brand: "Not-A.Brand", Version: notABrandVersion},
		}
	default:
		return nil
	}
}

// renderBrandsLiteral renders brands as a JavaScript array literal with safe
// string encoding.
func renderBrandsLiteral(brands []ClientHintBrand) string {
	parts := make([]string, 0, len(brands))
	for _, b := range brands {
		parts = append(parts, fmt.Sprintf("{ brand: %s, version: %s }",
			cmn.JSStringLiteral(b.Brand), cmn.JSStringLiteral(b.Version)))
	}
	return strings.Join(parts, ",\n            ")
}

// antiDetectionScript builds the new-document camouflage script from explicit
// identity inputs. All free-text values (UA, platforms, brand names) are
// embedded via JSON string literals so quotes, backslashes, or non-ASCII
// text cannot break the script. The WebGL, Audio, plugin, and marker
// behavior is byte-for-byte the long-standing script body; only identity
// derivation, safe encoding, and removal of the "loaded" debug trace
// changed.
//
// Lifecycle note (VDI-006/VDI-014): this script captures one selection
// event. Rotation replaces the registration via ReplaceIdentityScript; a
// superseded registration is removed first so no stale UA-A script survives
// alongside UA-B overrides.
func antiDetectionScript(userAgent string, ident OSIdentity, brands []ClientHintBrand) string {
	userAgentDataBlock := `// No Chromium brand surface for the selected identity:
// Client Hints brand values are left at native values.
`
	if len(brands) > 0 {
		userAgentDataBlock = fmt.Sprintf(`if (navigator.userAgentData) {
    Object.defineProperty(navigator.userAgentData, 'platform', { value: %s });
    Object.defineProperty(navigator.userAgentData, 'mobile', { value: %t });
    Object.defineProperty(navigator.userAgentData, 'brands', {
        value: [
            %s
        ]
    });
}
`, cmn.JSStringLiteral(ident.UserAgentDataPlatform), ident.Mobile, renderBrandsLiteral(brands))
	}

	return fmt.Sprintf(`Object.defineProperty(navigator, 'webdriver', {
    get: () => undefined
});

Object.defineProperty(navigator, 'userAgent', {
    get: () => %s
});

Object.defineProperty(navigator, 'platform', {
    get: () => %s
});

%s
Object.defineProperty(navigator, 'plugins', {
    get: () => [
        { name: "Chrome PDF Viewer" },
        { name: "Chromium PDF Plugin" }
    ]
});

Object.defineProperty(navigator, 'mimeTypes', {
    get: () => [
        { type: "application/pdf", suffixes: "pdf", description: "PDF" }
    ]
});

delete window.cdc_adoQpoasnfa76pfcZLmcfl_Array;
delete window.cdc_adoQpoasnfa76pfcZLmcfl_Promise;
delete window.cdc_adoQpoasnfa76pfcZLmcfl_Symbol;

(function() {
    const getParameter = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function(param) {
        if (param === 37445) return "Intel Inc.";
        if (param === 37446) return "Mesa Intel(R) UHD Graphics";
        return getParameter.call(this, param);
    };
})();

(function() {
    const original = AudioBuffer.prototype.getChannelData;
    AudioBuffer.prototype.getChannelData = function() {
        const data = original.apply(this, arguments);
        for (let i = 0; i < data.length; i += 100) {
            data[i] = data[i] + (Math.random() * 0.0000001);
        }
        return data;
    };
})();
`, cmn.JSStringLiteral(userAgent), cmn.JSStringLiteral(ident.NavigatorPlatform), userAgentDataBlock)
}

// userAgentOverrideParams builds the Network.setUserAgentOverride parameters
// for the selected identity. userAgentMetadata carries the product's Client
// Hints brands, platform, and device-class mobile flag when the selection
// yields brands; otherwise only userAgent and platform are sent and brand
// surfaces stay native. First-document HTTP observation versus page-side
// JavaScript comparison requires a live observer and is integration-pending
// (VDI-021 / review finding 4).
func userAgentOverrideParams(userAgent, cdpPlatform string, ident OSIdentity, brands []ClientHintBrand) map[string]interface{} {
	params := map[string]interface{}{
		"userAgent": userAgent,
	}
	if cdpPlatform != "" {
		params["platform"] = cdpPlatform
	}
	if len(brands) > 0 {
		entries := make([]map[string]interface{}, 0, len(brands))
		for _, b := range brands {
			entries = append(entries, map[string]interface{}{
				"brand":   b.Brand,
				"version": b.Version,
			})
		}
		params["userAgentMetadata"] = map[string]interface{}{
			"brands":   entries,
			"platform": ident.UserAgentDataPlatform,
			"mobile":   ident.Mobile,
		}
	}
	return params
}

// SetUserAgentOverrideWithMetadata sets the user agent, platform, and derived
// Client Hints metadata via CDP for one selection event. All surfaces derive
// from the same identity so UA, Sec-CH-UA*, navigator.platform, and
// navigator.userAgentData agree per the selected (possibly synthetic)
// profile. Custom identities are preserved: values are derived from the
// selected UA, never forced to host-native ones.
func SetUserAgentOverrideWithMetadata(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, brands []ClientHintBrand) error {
	_, err := ExecuteCDPCommand(wd, delayMs, "Network.setUserAgentOverride",
		userAgentOverrideParams(userAgent, ident.NavigatorPlatform, ident, brands))
	return err
}

// identityScripts tracks the new-document identity script registered per
// WebDriver session so rotation can replace (not duplicate) it.
var identityScripts = struct {
	sync.Mutex
	bySession map[string]string
}{bySession: make(map[string]string)}

// parseScriptIdentifier extracts the CDP script identifier from an
// addScriptToEvaluateOnNewDocument result, returning "" when absent.
func parseScriptIdentifier(result interface{}) string {
	payload, ok := result.(map[string]interface{})
	if !ok {
		return ""
	}
	id, _ := payload["identifier"].(string)
	return id
}

// ForgetIdentityScript drops the tracked registration for a session.
// Best-effort bookkeeping only; it never fails and never touches the
// browser. Exported for session-teardown paths outside this package (e.g.
// browser.Cleanup with Close, which bypasses QuitSelenium).
func ForgetIdentityScript(sessionID string) {
	forgetIdentityScript(sessionID)
}

// forgetIdentityScript drops the tracked registration for a session.
// Best-effort bookkeeping only; it never fails and never touches the browser.
func forgetIdentityScript(sessionID string) {
	if sessionID == "" {
		return
	}
	identityScripts.Lock()
	defer identityScripts.Unlock()
	delete(identityScripts.bySession, sessionID)
}

// ErrIdentityScriptUntracked reports a registration the driver acknowledged
// without a CDP identifier: the script is active for future documents but
// cannot be selectively removed, so a later rotation cannot replace it.
var ErrIdentityScriptUntracked = errors.New("identity script registered without a CDP identifier")

// ReplaceIdentityScript registers source as the session's new-document
// identity script, removing the previously registered one first (review
// finding 2). Without replacement, a request-level UA rotation would leave
// the connection-time script (UA A) active alongside UA-B CDP overrides for
// every subsequent navigation.
//
// Failure semantics (follow-up finding 1) are strict by design:
//   - If removing the previous script fails, its identifier is RETAINED
//     (never forgotten) and the new script is NOT registered: stacking UA B
//     over an irremovable UA A is useless, because the non-configurable
//     property definitions make the first script win every redefinition.
//     The error propagates so the caller handles it explicitly; a dead
//     session surfaces as "invalid session id" to the existing recovery
//     paths, and the next rotation retries the removal.
//   - If registration succeeds but reports no identifier, the script is
//     active but untracked: ErrIdentityScriptUntracked propagates so the
//     caller knows replacement is impossible without a session reset.
//
// Session-reset and rotation policies are unchanged.
func ReplaceIdentityScript(wd WebDriver, delayMs int, source string) error {
	if wd == nil {
		return fmt.Errorf("webdriver is nil")
	}
	sessionID := wd.SessionID()

	identityScripts.Lock()
	previous := identityScripts.bySession[sessionID]
	identityScripts.Unlock()

	if previous != "" {
		if err := RemoveScriptToEvaluateOnNewDocument(wd, delayMs, previous); err != nil {
			return fmt.Errorf("removing superseded identity script: %w", err)
		}
		forgetIdentityScript(sessionID)
	}

	identifier, err := AddScriptWithIdentifier(wd, delayMs, source)
	if err != nil {
		return err
	}
	if sessionID == "" || identifier == "" {
		return fmt.Errorf("registering identity script: %w", ErrIdentityScriptUntracked)
	}
	identityScripts.Lock()
	identityScripts.bySession[sessionID] = identifier
	identityScripts.Unlock()
	return nil
}

// RefreshIdentityScript builds the identity script for the current selection
// and replaces the session registration with it. changeUserAgent calls it
// after selecting UA B so later navigations in the same session evaluate the
// current identity, not the connection-time one.
func RefreshIdentityScript(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, brands []ClientHintBrand) error {
	return ReplaceIdentityScript(wd, delayMs, antiDetectionScript(userAgent, ident, brands))
}

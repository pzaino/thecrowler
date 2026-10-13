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
// Preservation note: the host OS (e.g. Linux in the VDI
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

// ClientHintsDetails carries the high-entropy Client Hints values derived
// for one selection event.
//
// Provenance rule: every field is either parsed from the selected UA string,
// frozen by platform convention, or taken from the documented per-OS table
// below — never from the host. Fields Chromium requires in
// Network.setUserAgentOverride.userAgentMetadata but CROWler cannot derive
// (notably a per-release OS build number) use conventional values disclosed
// in the implementation report; they are data, tunable without structural
// change, and must not be mistaken for measured host values.
type ClientHintsDetails struct {
	// FullVersion is the complete browser version parsed from the selected
	// UA (product-aware: Edg/ for Edge, OPR/ for Opera, else Chrome/).
	FullVersion string
	// PlatformVersion is the advertised OS version: Windows is frozen to
	// "10.0.0" by UA-reduction for every release; Android majors parse from
	// the UA ("Android 10" -> "10.0.0"); Linux is "" (verified: native
	// Chromium-on-Linux reports empty); darwin/ios use the conventional
	// table values below.
	PlatformVersion string
	// Architecture is the advertised CPU architecture ("x86"/"arm").
	Architecture string
	// Model is the device model ("" on desktop, matching real Chrome;
	// parsed from mobile UAs, "" when absent).
	Model string
	// Bitness is "64", except 32-bit ARM Android identities ("32",
	// matching the advertised "Linux armv8l" platform string).
	Bitness string
	// FullVersionList mirrors Brands with full dotted versions for the
	// Sec-CH-UA-Full-Version-List surface: Chromium/product entries carry
	// their full versions, Not-A.Brand mirrors the pinned short "8" as
	// "8.0.0.0". Nil when no honest list is buildable (non-Chromium
	// identities, or unresolvable versions) — callers then omit the field
	// instead of leaking host values.
	FullVersionList []ClientHintBrand
}

var (
	chromeFullPattern     = regexp.MustCompile(`Chrome/(\d+\.\d+\.\d+\.\d+)`)
	edgeFullPattern       = regexp.MustCompile(`Edg/(\d+\.\d+\.\d+\.\d+)`)
	operaFullPattern      = regexp.MustCompile(`OPR/(\d+\.\d+\.\d+\.\d+)`)
	androidVersionPattern = regexp.MustCompile(`Android (\d+)`)
)

// FullVersionFor extracts the product's full dotted version from UA tokens:
// the Edg/ value for Edge, the OPR/ value for Opera, else the Chrome/ value.
// Tokens in the shipped database are sometimes truncated ("Edg/131.0.0.");
// when no quad is present but the major is known, major+".0.0.0" is used so
// the value stays shape-valid and major-consistent. It returns "" only when
// no usable token exists (callers then omit fullVersion).
func FullVersionFor(userAgent, product string) string {
	chromeMajor, _ := ChromeMajorVersion(userAgent)
	productMajor := cmn.ProductMajorVersion(userAgent, product)
	quadFor := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(userAgent); len(m) == 2 {
			return m[1]
		}
		return ""
	}
	completed := func(quad, major string) string {
		if quad != "" {
			return quad
		}
		if major != "" {
			return major + ".0.0.0"
		}
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "edge":
		return completed(quadFor(edgeFullPattern), productMajor)
	case "opera":
		return completed(quadFor(operaFullPattern), productMajor)
	default:
		return completed(quadFor(chromeFullPattern), chromeMajor)
	}
}

// FullVersionListFor builds the full-version brand list mirroring brands:
// Chromium/product entries carry full dotted versions, Not-A.Brand mirrors
// the pinned short version as "8.0.0.0" (self-consistent pair; the 8-vs-99
// staleness question is a separate tunable, not changed here). A product
// brand without a resolvable product build falls back to the engine build
// rather than mixing in host values (finding 2). It returns nil unless a
// complete list is buildable, so callers omit the surface instead of
// emitting a partial one.
func FullVersionListFor(brands []ClientHintBrand, chromeFull, product, productFull string) []ClientHintBrand {
	if len(brands) == 0 || chromeFull == "" {
		return nil
	}
	if productFull == "" {
		productFull = chromeFull
	}
	out := make([]ClientHintBrand, 0, len(brands))
	for _, b := range brands {
		switch b.Brand {
		case "Chromium":
			out = append(out, ClientHintBrand{Brand: b.Brand, Version: chromeFull})
		case "Microsoft Edge", "Opera":
			if productFull == "" {
				return nil
			}
			out = append(out, ClientHintBrand{Brand: b.Brand, Version: productFull})
		case "Not-A.Brand":
			out = append(out, ClientHintBrand{Brand: b.Brand, Version: notABrandVersion + ".0.0.0"})
		default:
			return nil
		}
	}
	return out
}

// deviceModelForUA derives the advertised device model: "" on desktop (real
// Chrome sends an empty model there); on mobile the last "; "-separated
// token of the first UA parenthetical (e.g. "SM-G960F"), "iPhone"/"iPad" for
// iOS, or "" when nothing parseable is present.
func deviceModelForUA(userAgent string, mobile bool) string {
	if !mobile {
		return ""
	}
	if strings.Contains(userAgent, "iPhone") {
		return "iPhone"
	}
	if strings.Contains(userAgent, "iPad") {
		return "iPad"
	}
	open := strings.Index(userAgent, "(")
	close := strings.Index(userAgent, ")")
	if open < 0 || close <= open {
		return ""
	}
	parts := strings.Split(userAgent[open+1:close], ";")
	candidate := strings.TrimSpace(parts[len(parts)-1])
	// Reject empty, OS/family tokens, and implausible fragments: the shipped
	// database contains a truncated "(Linux; Android 10; K)" model token,
	// and no legitimate device model is a single character.
	if candidate == "" || len(candidate) < 2 || strings.HasPrefix(candidate, "Android") || strings.EqualFold(candidate, "Mobile") || strings.EqualFold(candidate, "Linux") {
		return ""
	}
	return candidate
}

// NewClientHintsDetails derives the high-entropy values for one selection
// event from the selected UA, product, and advertised OS identity.
func NewClientHintsDetails(userAgent, product string, ident OSIdentity) ClientHintsDetails {
	chromeMajor, _ := ChromeMajorVersion(userAgent)
	brands := ClientHintBrandsFor(product, chromeMajor, cmn.ProductMajorVersion(userAgent, product))
	chromeFull := FullVersionFor(userAgent, "chrome")
	productFull := FullVersionFor(userAgent, product)
	details := ClientHintsDetails{
		FullVersion:     productFull,
		Bitness:         "64",
		Model:           deviceModelForUA(userAgent, ident.Mobile),
		FullVersionList: FullVersionListFor(brands, chromeFull, product, productFull),
	}
	if details.FullVersion == "" {
		details.FullVersion = chromeFull
	}
	switch ident.Key {
	case "windows":
		// Frozen by UA-reduction: every Windows release reports 10.0.0.
		details.PlatformVersion = "10.0.0"
		details.Architecture = "x86"
	case "darwin":
		// Conventional value (approval-tunable): the UA-frozen 10_15_7
		// would contradict Chrome>=120 requirements, so a plausible
		// release is advertised instead. See the implementation report.
		details.PlatformVersion = "14.0.0"
		details.Architecture = "arm"
	case "android":
		if m := androidVersionPattern.FindStringSubmatch(userAgent); len(m) == 2 {
			details.PlatformVersion = m[1] + ".0.0"
		} else {
			details.PlatformVersion = "13.0.0"
		}
		details.Architecture = "arm"
		details.Bitness = "32"
	case "ios":
		details.PlatformVersion = "17.0.0"
		details.Architecture = "arm"
	default: // linux and unknown keys (linux is the global default)
		// Verified against Chromium source
		// (components/embedder_support/user_agent_utils.cc GetPlatformVersion):
		// native Linux reports an EMPTY platform version, so the advertised
		// Linux identity does the same. A hardcoded kernel number would
		// mismatch real Chromium-on-Linux behavior.
		details.PlatformVersion = ""
		details.Architecture = "x86"
	}
	return details
}

// highEntropyHintNames is the valid high-entropy hint vocabulary. Unknown
// tokens (including low-entropy names such as platform/mobile/brands, which
// are direct properties, not hint results) must never appear in results.
var highEntropyHintNames = []string{
	"architecture", "bitness", "formFactors", "fullVersionList",
	"model", "platformVersion", "uaFullVersion", "wow64",
}

// highEntropySpoofBlock returns the getHighEntropyValues override IIFE that
// keeps page-side high-entropy reads consistent with the CDP metadata and
// the advertised UA.
//
// Contract (follow-up review): the override always delegates to the native
// implementation first, preserving result shape, unknown-token handling,
// omitted-argument defaulting, and Permissions Policy behavior (a disallowed
// hint rejects via the native call instead of being bypassed). A spoofed
// value replaces a field ONLY when the native result actually exposed it:
// fields the native response withheld (e.g. under Permissions Policy) stay
// withheld rather than being synthesized. Low-entropy names are never
// synthesized into results. When userAgentData (or the API) is absent —
// notably native Firefox identities — the block no-ops.
func highEntropySpoofBlock(details ClientHintsDetails) string {
	allowlist := "[" + strings.Join(quotedAll(highEntropyHintNames), ",") + "]"
	listFragment := ""
	if len(details.FullVersionList) > 0 {
		listFragment = `,
            "fullVersionList": [
                ` + renderBrandsLiteral(details.FullVersionList) + `
            ]`
	}
	return fmt.Sprintf(`(function() {
    try {
        if (!navigator.userAgentData || !navigator.userAgentData.getHighEntropyValues) return;
        const nativeHints = navigator.userAgentData.getHighEntropyValues.bind(navigator.userAgentData);
        const highEntropy = %s;
        const spoofed = {
            "platformVersion": %s,
            "architecture": %s,
            "model": %s,
            "bitness": %s,
            "uaFullVersion": %s%s
        };
        navigator.userAgentData.getHighEntropyValues = function(hints) {
            return nativeHints(hints).then(native => {
                const result = (native && typeof native === 'object') ? Object.assign({}, native) : {};
                const wanted = Array.isArray(hints) ? hints : highEntropy;
                wanted.forEach(h => {
                    if (highEntropy.indexOf(h) !== -1 && Object.prototype.hasOwnProperty.call(spoofed, h) && Object.prototype.hasOwnProperty.call(result, h)) {
                        result[h] = spoofed[h];
                    }
                });
                return result;
            });
        };
    } catch (e) {
        console.error('Error installing high-entropy hints override:', e);
    }
})();
`, allowlist, cmn.JSStringLiteral(details.PlatformVersion),
		cmn.JSStringLiteral(details.Architecture), cmn.JSStringLiteral(details.Model),
		cmn.JSStringLiteral(details.Bitness), cmn.JSStringLiteral(details.FullVersion), listFragment)
}

func quotedAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, cmn.JSStringLiteral(n))
	}
	return out
}

// antiDetectionScript builds the new-document camouflage script from explicit
// identity inputs. All free-text values (UA, platforms, brand names) are
// embedded via JSON string literals so quotes, backslashes, or non-ASCII
// text cannot break the script. The WebGL, Audio, plugin, and marker
// behavior is byte-for-byte the long-standing script body; only identity
// derivation, safe encoding, and removal of the "loaded" debug trace
// changed.
//
// Lifecycle note: this script captures one selection
// event. Rotation replaces the registration via ReplaceIdentityScript; a
// superseded registration is removed first so no stale UA-A script survives
// alongside UA-B overrides.
func antiDetectionScript(userAgent string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails) string {
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

%s`, cmn.JSStringLiteral(userAgent), cmn.JSStringLiteral(ident.NavigatorPlatform), userAgentDataBlock,
		highEntropySpoofBlock(details))
}

// userAgentOverrideParams builds the Network.setUserAgentOverride parameters
// for the selected identity. userAgentMetadata carries the product's Client
// Hints brands, platform, device-class mobile flag, and the required
// high-entropy fields (platformVersion, architecture, model; fullVersion
// when derivable) when the selection yields brands; otherwise only userAgent
// and platform are sent and brand surfaces stay native.
//
// Schema basis: per the DevTools protocol,
// userAgentMetadata.platform/platformVersion/architecture/model/mobile are
// REQUIRED — omitting them risks the whole command being rejected, which
// would silently drop even the userAgent override. Values come from
// NewClientHintsDetails (UA-derived, frozen, or conventional table values),
// never from the host. First-document HTTP observation versus page-side
// JavaScript comparison is covered by the Client Hints integration test.
func userAgentOverrideParams(userAgent, cdpPlatform string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails) map[string]interface{} {
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
		metadata := map[string]interface{}{
			"brands":          entries,
			"platform":        ident.UserAgentDataPlatform,
			"platformVersion": details.PlatformVersion,
			"architecture":    details.Architecture,
			"model":           details.Model,
			"mobile":          ident.Mobile,
			"bitness":         details.Bitness,
		}
		if details.FullVersion != "" {
			metadata["fullVersion"] = details.FullVersion
		}
		// The full-version brand list travels only when completely
		// resolvable; otherwise the key is omitted rather than leaking
		// host build numbers (finding 2).
		if len(details.FullVersionList) > 0 {
			listEntries := make([]map[string]interface{}, 0, len(details.FullVersionList))
			for _, b := range details.FullVersionList {
				listEntries = append(listEntries, map[string]interface{}{
					"brand":   b.Brand,
					"version": b.Version,
				})
			}
			metadata["fullVersionList"] = listEntries
		}
		params["userAgentMetadata"] = metadata
	}
	return params
}

// SetUserAgentOverrideWithMetadata sets the user agent, platform, and derived
// Client Hints metadata via CDP for one selection event. All surfaces derive
// from the same identity so UA, Sec-CH-UA*, navigator.platform, and
// navigator.userAgentData agree per the selected (possibly synthetic)
// profile. Custom identities are preserved: values are derived from the
// selected UA, never forced to host-native ones.
func SetUserAgentOverrideWithMetadata(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails) error {
	_, err := ExecuteCDPCommand(wd, delayMs, "Network.setUserAgentOverride",
		userAgentOverrideParams(userAgent, ident.NavigatorPlatform, ident, brands, details))
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
func RefreshIdentityScript(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails) error {
	return ReplaceIdentityScript(wd, delayMs, antiDetectionScript(userAgent, ident, brands, details))
}

// abortFailedSession best-effort tears down a newly created session that
// ConnectVDI refuses to hand out (follow-up finding 3): it quits the driver
// (a dead session simply reports an error, which is ignored) and drops any
// tracked identity-script registration, so neither a server-side session
// nor registry state leaks. The session ID is captured BEFORE teardown:
// drivers may clear it during Close/Quit, which would otherwise orphan the
// registry entry. It never returns an error; call sites invoke it
// immediately before returning their own classified failure.
func abortFailedSession(wd WebDriver) {
	if wd == nil {
		return
	}
	sessionID := wd.SessionID()
	_ = wd.Close()
	_ = wd.Quit()
	forgetIdentityScript(sessionID)
}

// establishInitialIdentity applies the selected identity to a fresh session
// before any navigation: CDP network domain first, then the UA + Client
// Hints metadata override (order matters and is pinned by tests). Script
// registration stays in InjectAntiDetectionPatches (Replace registry); the
// caller classifies failures (fatal on CDP-capable browsers, best-effort on
// Firefox, which has no CDP bridge).
func establishInitialIdentity(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, brands []ClientHintBrand, details ClientHintsDetails) error {
	if err := EnableNetwork(wd, delayMs, nil); err != nil {
		return fmt.Errorf("enabling CDP network domain: %w", err)
	}
	if err := SetUserAgentOverrideWithMetadata(wd, delayMs, userAgent, ident, brands, details); err != nil {
		return fmt.Errorf("overriding user agent: %w", err)
	}
	return nil
}

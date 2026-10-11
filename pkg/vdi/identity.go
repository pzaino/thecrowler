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
	"fmt"
	"regexp"
	"strings"

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

// clientHintBrands renders the navigator.userAgentData.brands entries for a
// Chrome major version. The two-brand Chromium shape is preserved
// deliberately (open-source Chromium sends Chromium + Not-A.Brand, unlike
// branded Google Chrome which adds a third Google Chrome entry); only the
// previously hardcoded "132" becomes derived from the selected UA.
func clientHintBrands(major string) string {
	return fmt.Sprintf("{ brand: %s, version: %s },\n            { brand: %s, version: %s }",
		cmn.JSStringLiteral("Chromium"), cmn.JSStringLiteral(major),
		cmn.JSStringLiteral("Not-A.Brand"), cmn.JSStringLiteral("8"))
}

// antiDetectionScript builds the new-document camouflage script from explicit
// identity inputs. All free-text values (UA, platforms) are embedded via
// JSON string literals so quotes, backslashes, or non-ASCII text cannot
// break the script. The WebGL, Audio, plugin, and marker behavior is
// byte-for-byte the long-standing script body; only identity derivation,
// safe encoding, and removal of the "loaded" debug trace changed.
//
// Lifecycle note (VDI-006 task 9 / VDI-014): this script captures the UA and
// OS selected for one event. A later request-level UA rotation under
// change_useragent registers CDP/window-level overrides but does not replace
// this registration; versioned replacement is owned by VDI-014.
func antiDetectionScript(userAgent string, ident OSIdentity, chromeMajor string) string {
	var userAgentDataBlock string
	if chromeMajor != "" {
		userAgentDataBlock = fmt.Sprintf(`if (navigator.userAgentData) {
    Object.defineProperty(navigator.userAgentData, 'platform', { value: %s });
    Object.defineProperty(navigator.userAgentData, 'mobile', { value: %t });
    Object.defineProperty(navigator.userAgentData, 'brands', {
        value: [
            %s
        ]
    });
}
`, cmn.JSStringLiteral(ident.UserAgentDataPlatform), ident.Mobile, clientHintBrands(chromeMajor))
	} else {
		userAgentDataBlock = `// No Chrome token in the selected identity: Client Hints brand
// surfaces are left at native values.
`
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
// for the selected identity. userAgentMetadata carries the derived Client
// Hints brands, platform, and device-class mobile flag when the selected UA
// is a Chrome identity; for other families only userAgent and platform are
// sent and brand surfaces stay native. First-document HTTP observation
// versus page-side JavaScript comparison requires a live observer and is
// integration-pending (VDI-021).
func userAgentOverrideParams(userAgent, cdpPlatform string, ident OSIdentity, chromeMajor string) map[string]interface{} {
	params := map[string]interface{}{
		"userAgent": userAgent,
	}
	if cdpPlatform != "" {
		params["platform"] = cdpPlatform
	}
	if chromeMajor != "" {
		params["userAgentMetadata"] = map[string]interface{}{
			"brands": []map[string]interface{}{
				{"brand": "Chromium", "version": chromeMajor},
				{"brand": "Not-A.Brand", "version": "8"},
			},
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
func SetUserAgentOverrideWithMetadata(wd WebDriver, delayMs int, userAgent string, ident OSIdentity, chromeMajor string) error {
	_, err := ExecuteCDPCommand(wd, delayMs, "Network.setUserAgentOverride",
		userAgentOverrideParams(userAgent, ident.NavigatorPlatform, ident, chromeMajor))
	return err
}

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
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// Shared browser-camouflage patch builders.
//
// Scope contract (read before changing behavior, not just text):
//   - antiDetectionScript (identity.go, CDP new-document registration) is the
//     ONLY patch that survives navigation. It carries identity surfaces.
//   - GPUPatch / ReinforceBrowserSettings / setNavigatorProperties /
//     addLoadListener run one-shot via ExecuteScript on the current (usually
//     blank) document and do NOT survive navigation. They keep their scope;
//     this file only repairs their API contracts and unifies shared mock
//     shapes/strings with the persistent script.
// Capabilities are preserved everywhere; defective implementations are
// repaired surgically. Nothing here changes launch arguments, rotation
// policy, configuration keys, or defaults.

const (
	// gpuVendorSpoof and gpuRendererSpoof are the single advertised WebGL
	// identity used by every WebGL patch (persistent and one-shot), so
	// surfaces never contradict each other across documents. Values are
	// conventional data, tunable without structural change.
	gpuVendorSpoof   = "Intel Inc."
	gpuRendererSpoof = "Mesa Intel(R) UHD Graphics"

	// canvasSpoofDataURL is a decodable 1x1 transparent PNG. The previous
	// constant ("fakemockdata") was not a valid PNG and broke every page
	// reading canvas output, while remaining trivially detectable. A
	// constant output is still recognizable across canvases, but pages keep
	// working; per-call variation is a separate, owner-gated decision.
	canvasSpoofDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)

// pluginArrayLiteral returns the canonical PluginArray-shaped JavaScript
// expression shared by the persistent script and all one-shot patches: a
// real Array (length, indexing, iteration) with item()/namedItem()/refresh()
// expandos and two PDF plugins carrying name/filename/description. Numeric
// placeholders ([1,2,3,4,5]) and bare {name}-only objects failed basic API
// checks (plugins[0].name, item(), length mismatches across patches).
// instanceof PluginArray / toStringTag fidelity is a known limitation
// (see the implementation report).
func pluginArrayLiteral() string {
	return `(() => {
    const makePlugin = (name, filename, description) => {
        const plugin = { description: description, filename: filename, name: name, length: 1 };
        plugin.item = function(index) { return (index === 0) ? { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" } : null; };
        plugin.namedItem = function(name) { return (name === "application/pdf") ? { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" } : null; };
        return plugin;
    };
    const plugins = [
        makePlugin("Chrome PDF Viewer", "internal-pdf-viewer", "Portable Document Format"),
        makePlugin("Chromium PDF Plugin", "internal-pdf-viewer", "Portable Document Format")
    ];
    plugins.item = function(index) { return (index >= 0 && index < this.length) ? this[index] : null; };
    plugins.namedItem = function(name) {
        for (let i = 0; i < this.length; i++) { if (this[i].name === name) return this[i]; }
        return null;
    };
    plugins.refresh = function() {};
    return plugins;
})()`
}

// mimeArrayLiteral returns the canonical MimeTypeArray-shaped JavaScript
// expression: a real Array with item()/namedItem() expandos.
func mimeArrayLiteral() string {
	return `(() => {
    const mimes = [
        { type: "application/pdf", suffixes: "pdf", description: "Portable Document Format" }
    ];
    mimes.item = function(index) { return (index >= 0 && index < this.length) ? this[index] : null; };
    mimes.namedItem = function(name) {
        for (let i = 0; i < this.length; i++) { if (this[i].type === name) return this[i]; }
        return null;
    };
    return mimes;
})()`
}

// webglSpoofScript returns the shared WebGL identity patch applied to both
// WebGLRenderingContext and WebGL2RenderingContext (the latter was
// previously unpatched, leaking a contradictory native identity). Native
// calls keep their receiver (unbound calls throw "Illegal invocation").
// The advertised strings are the single gpuVendorSpoof/gpuRendererSpoof
// pair by construction, so patches cannot disagree with each other.
// Callers embed the returned source into their own script scope.
func webglSpoofScript() string {
	return `(function() {
    const contexts = [WebGLRenderingContext];
    if (typeof WebGL2RenderingContext !== "undefined") contexts.push(WebGL2RenderingContext);
    contexts.forEach(function(Ctx) {
        try {
            const getParameter = Ctx.prototype.getParameter;
            Ctx.prototype.getParameter = function(parameter) {
                if (parameter === 37445) return "` + gpuVendorSpoof + `";
                if (parameter === 37446) return "` + gpuRendererSpoof + `";
                return getParameter.call(this, parameter);
            };
        } catch (e) {
            console.error('Error applying WebGL identity patch:', e);
        }
    });
})();`
}

// gpuPatchScript returns the optional GPU patch body (gated by
// crawler.set_vdi_gpu_patch at the call site). It wraps context creation so
// WebGL contexts report the shared spoofed identity, and guards against null
// contexts (previously an unguarded getExtension call threw). Mechanism and
// toggle semantics preserved; only the null guard and the unified strings
// changed.
func gpuPatchScript() string {
	return `
	HTMLCanvasElement.prototype.getContext = (function(orig) {
	return function(type, attribs) {
		const ctx = orig.call(this, type, attribs);
		if (!ctx) return ctx;
		if (type === 'webgl' || type === 'webgl2') {
		const ext = ctx.getExtension('WEBGL_debug_renderer_info');
		if (ext) {
			const getParamOrig = ctx.getParameter.bind(ctx);
			ctx.getParameter = function(param) {
			if (param === ext.UNMASKED_RENDERER_WEBGL) return '` + gpuRendererSpoof + `';
			if (param === ext.UNMASKED_VENDOR_WEBGL) return '` + gpuVendorSpoof + `';
			return getParamOrig(param);
			};
		}
		}
		return ctx;
	};
	})(HTMLCanvasElement.prototype.getContext);
	`
}

// loadListenerScript returns the load-listener registration body.
// Lifecycle note: it executes one-shot on the current document and
// does not survive navigation; the persistent new-document script already
// covers these properties on every document. Kept for compatibility; a
// removal proposal is recorded in the implementation report for approval.
func loadListenerScript() string {
	return `
        window.addEventListener('load', () => {
            try {
                Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});
                Object.defineProperty(window, 'RTCDataChannel', {value: undefined});
                Object.defineProperty(navigator, 'mediaDevices', {value: undefined});
                Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
                Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
                Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4});
                Object.defineProperty(navigator, 'plugins', {get: () => ` + pluginArrayLiteral() + `});
            } catch (err) {
                console.error('Error applying browser settings on page load:', err);
            }
        });
    `
}

// reinforceScript returns the one-shot reinforcement body applied to the
// current (blank) document during browser setup. Repairs vs the previous
// version, all behavior-preserving except where noted:
//   - stage 1 defines webdriver configurable and guards each delete, so the
//     proto-cleanup actually runs (a TypeError previously skipped it) and
//     stage 7 can redefine the property (previously threw);
//   - canvas returns a decodable PNG instead of an invalid constant;
//   - WebGL uses the shared snippet (bound native calls, both GL prototypes);
//   - permissions delegates with its receiver bound (previously threw
//     "Illegal invocation" for every non-notifications query);
//   - the attachShadow override (forcing null, breaking all web-component
//     pages with no documented intent) is REMOVED — flagged for approval;
//   - stages renumbered after the removal.
func reinforceScript() string {
	return fmt.Sprintf(`
        try {
			Object.defineProperty(navigator, 'webdriver', {get: () => undefined, configurable: true});
			delete window._Selenium_IDE_Recorder;
			delete navigator.webdriver;
		} catch (err) {
            console.error('Error reinforcing browser settings stage 1:', err);
        }

		try {
			if (navigator.webdriver !== undefined && navigator.webdriver !== null) {
				delete navigator.webdriver.chrome;
			}
			delete navigator.__proto__.webdriver;
		} catch (err) {
            console.error('Error reinforcing browser settings stage 2:', err);
        }

		try {
            Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});
            Object.defineProperty(window, 'RTCDataChannel', {value: undefined});
            Object.defineProperty(navigator, 'mediaDevices', {value: undefined});
            Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
            Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4});
            Object.defineProperty(navigator, 'plugins', {get: () => %s});
			Object.defineProperty(navigator, 'getUserMedia', {value: undefined});
			Object.defineProperty(window, 'webkitRTCPeerConnection', {value: undefined});
		} catch (err) {
            console.error('Error reinforcing browser settings stage 3:', err);
        }

		try {
			HTMLCanvasElement.prototype.toDataURL = function() { return '%s'; };
		} catch (err) {
            console.error('Error reinforcing browser settings stage 4:', err);
        }

		try {
			%s
		} catch (err) {
            console.error('Error reinforcing browser settings stage 5:', err);
        }

		try {
			Object.defineProperty(window, 'devicePixelRatio', {
				get: function() { return 1; }
			});
		} catch (err) {
            console.error('Error reinforcing browser settings stage 6:', err);
        }

		try {
			const originalQuery = navigator.permissions.query.bind(navigator.permissions);
			navigator.permissions.query = (parameters) => (
				parameters.name === 'notifications'
					? Promise.resolve({ state: 'denied' })
					: originalQuery(parameters)
			);
		} catch (err) {
            console.error('Error reinforcing browser settings stage 7:', err);
        }

		try {
			Object.defineProperty(navigator, 'webdriver', {
				get: () => undefined,
				configurable: true,
			});

			Object.defineProperty(document, 'selenium-evaluate', { value: undefined });
			Error.stackTraceLimit = 0; // Limit error stack traces
        } catch (err) {
            console.error('Error reinforcing browser settings stage 8:', err);
        }

		try {
			Object.defineProperty(navigator, 'doNotTrack', {get: () => '1'}); // User enables Do Not Track
		} catch (err) {
            console.error('Error reinforcing browser settings stage 9:', err);
        }

		try {
			// Clean IndexedDB
            indexedDB.deleteDatabase('webdriver');
		} catch (err) {
            console.error('Error reinforcing browser settings stage 10:', err);
        }
    `, pluginArrayLiteral(), canvasSpoofDataURL, webglSpoofScript())
}

// navigatorPropertyScripts builds the one-shot navigator-property scripts
// applied post-connect to the current document (scope: current document
// only; persistence belongs to the new-document registration). The language
// table, geometry, and privacy restrictions are unchanged; only the plugins
// mock uses the canonical shape and the webdriver definition is
// configurable for safe re-application.
func navigatorPropertyScripts(lang, userAgent string, ident OSIdentity) []string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	selectedLanguage := ""

	switch lang {
	case "en-gb":
		selectedLanguage = "['en-GB', 'en']"
	case "fr-fr":
		selectedLanguage = "['fr-FR', 'fr']"
	case "de-de":
		selectedLanguage = "['de-DE', 'de']"
	case "es-es":
		selectedLanguage = "['es-ES', 'es']"
	case "it-it":
		selectedLanguage = "['it-IT', 'it']"
	case "pt-pt":
		selectedLanguage = "['pt-PT', 'pt']"
	case "pt-br":
		selectedLanguage = "['pt-BR', 'pt']"
	case "ja-jp":
		selectedLanguage = "['ja-JP', 'ja']"
	case "ko-kr":
		selectedLanguage = "['ko-KR', 'ko']"
	case "zh-cn":
		selectedLanguage = "['zh-CN', 'zh']"
	case "zh-tw":
		selectedLanguage = "['zh-TW', 'zh']"
	default:
		selectedLanguage = "['en-US', 'en']"
	}

	// Set the navigator properties
	return []string{
		"Object.defineProperty(navigator, 'webdriver', {get: () => undefined, configurable: true})",
		"window.navigator.chrome = {runtime: {}}",
		"Object.defineProperty(navigator, 'languages', {get: () => " + selectedLanguage + "})",
		"Object.defineProperty(navigator, 'plugins', {get: () => " + pluginArrayLiteral() + "})",
		"Object.defineProperty(navigator, 'deviceMemory', {get: () => 8})",        // Example device memory spoof
		"Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4})", // Example cores
		// Disable geolocation API
		"Object.defineProperty(navigator, 'geolocation', {get: () => null})",

		// Mock `navigator.doNotTrack`
		"Object.defineProperty(navigator, 'doNotTrack', {get: () => '1'})", // User enables Do Not Track

		// Mock `navigator.vendor` and `navigator.platform` from the selected
		// advertised OS; the previous hardcoded 'Linux' ignored the
		// user's browser_platform choice.
		"Object.defineProperty(navigator, 'vendor', {get: () => 'Google Inc.'})",
		"Object.defineProperty(navigator, 'platform', {get: () => " + cmn.JSStringLiteral(ident.NavigatorPlatform) + "})",

		// Spoof `navigator.deviceMemory`
		//"Object.defineProperty(navigator, 'deviceMemory', {get: () => 4})", // 4 GB memory

		// Spoof `navigator.hardwareConcurrency`
		//"Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4})", // 4 CPU cores

		//"Object.defineProperty(navigator, 'getBattery', {get: () => undefined})", // Disable battery API

		// Override screen width and height
		"Object.defineProperty(screen, 'width', {get: () => 1920})",
		"Object.defineProperty(screen, 'height', {get: () => 1080})",

		// Override window inner and outer dimensions
		"Object.defineProperty(window, 'innerWidth', {get: () => 1920})",
		"Object.defineProperty(window, 'innerHeight', {get: () => 1080})",
		"Object.defineProperty(window, 'outerWidth', {get: () => 1920})",
		"Object.defineProperty(window, 'outerHeight', {get: () => 1080})",

		// Mock `navigator.appVersion` and `navigator.userAgent` with safe
		// string encoding (raw interpolation broke on quotes,
		// backslashes, or non-ASCII text in the UA).
		"Object.defineProperty(navigator, 'appVersion', {get: () => " + cmn.JSStringLiteral(userAgent) + "})",
		"Object.defineProperty(navigator, 'userAgent', {get: () => " + cmn.JSStringLiteral(userAgent) + "})",

		"Object.defineProperty(navigator, 'getBattery', {get: () => undefined})", // Disable battery API

		// Mock `navigator.connection`
		"Object.defineProperty(navigator, 'connection', {get: () => ({type: 'wifi', downlink: 10.0})})", // Mimic WiFi connection

		// Disable WebRTC APIs to prevent IP leakage
		"Object.defineProperty(window, 'RTCPeerConnection', {value: undefined});",
		"Object.defineProperty(window, 'RTCDataChannel', {value: undefined});",
	}
}

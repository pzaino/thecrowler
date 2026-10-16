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
	"strconv"
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
)

// registryCore returns the JavaScript that builds the browser plugin/MIME
// object graph ONCE and returns it as a {plugins, mimes} registry object.
//
// No discoverable marker. The PREVIOUS version cached the graph on a
// `globalThis.__crowlerBrowserRegistry__` key: although non-enumerable, that
// name reflects through JS reflection (getOwnPropertyNames / getOwnPropertyKeys
// / symbol scans are not the only reflection a careful fingerprinter runs, and a
// "crowler"-named key is an identifiable artifact), and it also risked a stale
// cross-realm reference. This builder instead returns a fresh registry per
// invocation and lets the CALLER memoize it in a closure (registryGettersScript
// for the persistent identity script; the standalone getters for the one-shot
// scripts), so no global, prototype, or symbol property is introduced.
//
// The object graph is faithful to the native Plugin/PluginArray/MimeType/
// MimeTypeArray shape:
//   - two PDF plugins (Chrome PDF Viewer, Chromium PDF Plugin), each an
//     indexable Plugin whose .mimeTypes is a MimeTypeArray and whose
//     item()/namedItem() resolve into that MimeTypeArray;
//   - each MimeType carries type/suffixes/description/enabledPlugin, and the
//     flat MimeTypeArray (navigator.mimeTypes) reuses the SAME MimeType
//     instances, so plugins[i].mimeTypes[j] === navigator.mimeTypes[k] and
//     .enabledPlugin === its owning plugin.
//
// Known fidelity limitation (unchanged and now explicitly documented, never
// claimed away): the arrays/objects stand in for the native PluginArray /
// MimeTypeArray / Plugin / MimeType HOST objects, so `instanceof` against the
// real host classes and the host `Symbol.toStringTag` ("PluginArray" /
// "MimeTypeArray") are not reproducible from plain JavaScript objects, and the
// property descriptors on the synthetic arrays are not bit-for-bit the host
// shapes. What IS reproduced and cross-consistent — which is the part that
// drives a fingerprint — is the shape, the index/namedItem/item behavior,
// iteration, the length/first/last values, and the mutual cross-linking
// (plugins[i].mimeTypes[j] === navigator.mimeTypes[k], enabledPlugin round-trip,
// and stable identity across repeated reads).
func registryCore() string {
	return `(function () {
        const makeMime = function (type, suffixes, description) {
            return { type: type, suffixes: suffixes, description: description, enabledPlugin: null };
        };
        const makeMimeArray = function (list) {
            const arr = list.slice(0);
            arr.item = function (index) { return (index >= 0 && index < this.length) ? this[index] : null; };
            arr.namedItem = function (name) {
                for (let i = 0; i < this.length; i++) { if (this[i].type === name) return this[i]; }
                return null;
            };
            arr.refresh = function () {};
            return arr;
        };
        const makePlugin = function (name, filename, description, mimes) {
            const plugin = {
                name: name,
                filename: filename,
                description: description,
                length: mimes.length,
                mimeTypes: mimes,
                item: function (index) { return mimes.item(index); },
                namedItem: function (name) { return mimes.namedItem(name); },
                refresh: function () {},
            };
            for (let i = 0; i < mimes.length; i++) { plugin[i] = mimes[i]; }
            return plugin;
        };
        // Two PDF plugins, each owning its own application/pdf MimeType; the flat
        // navigator.mimeTypes reuses the same MimeType instances so
        // plugins[i].mimeTypes[j] === navigator.mimeTypes[k] and enabledPlugin
        // round-trips, mirroring the native object graph.
        const pdf0 = makeMime("application/pdf", "pdf", "Portable Document Format");
        const pdf1 = makeMime("application/pdf", "pdf", "Portable Document Format");
        const p0Mimes = makeMimeArray([pdf0]);
        const p1Mimes = makeMimeArray([pdf1]);
        const p0 = makePlugin("Chrome PDF Viewer", "internal-pdf-viewer", "Portable Document Format", p0Mimes);
        const p1 = makePlugin("Chromium PDF Plugin", "internal-pdf-viewer", "Portable Document Format", p1Mimes);
        pdf0.enabledPlugin = p0;
        pdf1.enabledPlugin = p1;
        const plugins = (function () {
            const arr = [p0, p1];
            arr.item = function (index) { return (index >= 0 && index < this.length) ? this[index] : null; };
            arr.namedItem = function (name) {
                for (let i = 0; i < this.length; i++) { if (this[i].name === name) return this[i]; }
                return null;
            };
            arr.refresh = function () {};
            return arr;
        })();
        const mimes = makeMimeArray([pdf0, pdf1]);
        return { plugins: plugins, mimes: mimes };
    })()`
}

// registryGettersScript installs navigator.plugins and navigator.mimeTypes as
// TWO getters that share ONE closure-memoized registry, so (a) repeated reads
// return identical objects (stable identity), (b) the MimeType instances are
// shared between plugins[i].mimeTypes and the flat navigator.mimeTypes (so
// plugins[0].mimeTypes[0] === navigator.mimeTypes[0] and enabledPlugin
// round-trips), and (c) no global/prototype/symbol property is introduced. The
// registry is built lazily on first access and the SAME closure variable holds
// it thereafter; a re-injection after rotation re-runs this block but, because
// the getters are redefined rather than stacked, identity remains stable without
// a marker.
func registryGettersScript() string {
	return `(function () {
        var reg;
        var R = function () {
            if (reg === undefined) reg = ` + registryCore() + `;
            return reg;
        };
        try {
            Object.defineProperty(navigator, "plugins", {
                configurable: true,
                get: function () { return R().plugins; },
            });
        } catch (e) {}
        try {
            Object.defineProperty(navigator, "mimeTypes", {
                configurable: true,
                get: function () { return R().mimes; },
            });
        } catch (e) {}
    })();`
}

// pluginArrayLiteral returns a memoizing GETTER FUNCTION (not a value) for
// navigator.plugins, used by the one-shot scripts (loadListener, reinforce,
// navigatorPropertyScripts) that install only the plugins surface. It builds the
// registry in its own closure on first access and caches it, so repeated reads
// are stable without any global/prototype marker. Install it as the `get` of an
// accessor descriptor: `get: <this>`, not `get: () => <this>`.
func pluginArrayLiteral() string {
	return "(function () {\n" +
		"var cached;\n" +
		"return function () {\n" +
		"if (cached === undefined) cached = (" + registryCore() + ").plugins;\n" +
		"return cached;\n" +
		"};\n})()"
}

// mimeArrayLiteral returns a memoizing GETTER FUNCTION (not a value) for
// navigator.mimeTypes, used by the one-shot scripts that install the mimeTypes
// surface. It builds the registry in its own closure on first access and caches
// it, so repeated reads are stable without any global/prototype marker. Install
// it as the `get` of an accessor descriptor.
func mimeArrayLiteral() string {
	return "(function () {\n" +
		"var cached;\n" +
		"return function () {\n" +
		"if (cached === undefined) cached = (" + registryCore() + ").mimes;\n" +
		"return cached;\n" +
		"};\n})()"
}

// webglSpoofScript returns the shared WebGL identity patch applied to both
// WebGLRenderingContext and WebGL2RenderingContext (the latter was previously
// unpatched, leaking a contradictory native identity).
//
// Robustness and correctness:
//   - Each API is probed with typeof, so a missing WebGL/WebGL2 binding
//     neither throws nor aborts the surrounding script: the new-document
//     script embeds this BEFORE the Client-Hints override, and an unguarded
//     throw here would silently drop the remaining identity initialization.
//   - Installation is idempotent per realm via a closure-held WeakSet of
//     patched prototypes (a NON-discoverable guard, not a prototype/global
//     marker). A re-run after an identity/UA rotation leaves an already-patched
//     prototype untouched, so getParameter is never stacked on itself.
//   - Native-first receiver/extension semantics: the captured native getParameter
//     is ALWAYS called first via getParameter.call(this, parameter). This (a)
//     validates the receiver — a fabricated/unbound `this` throws "Illegal
//     invocation" in the native call and therefore can never obtain a spoofed
//     value; (b) preserves the native error/availability state; and (c) returns
//     the native result for every parameter not spoofed.
//   - Extension-aware spoofing: the unmasked vendor/renderer (native enums
//     37445/37446) are spoofed ONLY when the browser's own query returns a
//     non-empty string — i.e. WEBGL_debug_renderer_info is available and
//     activated. When it is absent/disabled the native result is not a string,
//     so no value is fabricated (the spoof is not claimed where it cannot be
//     observed). This keeps the prototype patch consistent with the optional
//     per-context gpuPatchScript.
//
// The advertised strings are the single gpuVendorSpoof/gpuRendererSpoof pair
// by construction, so this per-prototype patch cannot disagree with the
// per-context gpuPatchScript, which uses the same constants. Callers embed the
// returned source into their own script scope.
func webglSpoofScript() string {
	return `(function() {
    "use strict";
    // Idempotency is a closure-held WeakSet of patched prototypes — NOT a
    // discoverable prototype/global marker. A re-injected new-document script
    // (identity/UA rotation) re-runs this block, but a prototype already in the
    // set is left untouched, so getParameter is never stacked on itself.
    const patched = new WeakSet();
    function patch(Ctx) {
        // typeof probe: a missing WebGL binding must never throw or abort the
        // surrounding identity script (this runs before the Client-Hints
        // override, so a throw here would drop the remaining initialization).
        if (typeof Ctx === "undefined" || Ctx === null || !Ctx || !Ctx.prototype) {
            return;
        }
        if (typeof Ctx.prototype.getParameter !== "function") {
            return;
        }
        const proto = Ctx.prototype;
        if (patched.has(proto)) {
            return;
        }
        // Native-first: capture the native getParameter and ALWAYS call it first,
        // via getParameter.call(this, parameter). This preserves (a) the receiver
        // contract — a fabricated/unbound 'this' throws "Illegal invocation"
        // here and so never reaches a spoofed return; (b) the native
        // error/availability state; and (c) the native result for every
        // parameter we do not spoof.
        const getParameter = proto.getParameter;
        proto.getParameter = function(parameter) {
            // Native answer first; it validates the receiver and, for 37445/37446,
            // is only a real string when WEBGL_debug_renderer_info is available and
            // activated.
            const result = getParameter.call(this, parameter);
            // Spoof the unmasked vendor/renderer (UNMASKED_VENDOR_WEBGL /
            // UNMASKED_RENDERER_WEBGL) only when the browser's native query for
            // that parameter is itself valid (a non-empty string). When the
            // extension is absent/disabled the native result is not a string, so
            // we DO NOT fabricate a spoofed value and return the native one.
            if ((parameter === 37445 || parameter === 37446) &&
                typeof result === "string" && result !== "") {
                return parameter === 37445 ? "` + gpuVendorSpoof + `" : "` + gpuRendererSpoof + `";
            }
            return result;
        };
        patched.add(proto);
    }
    try {
        patch(typeof WebGLRenderingContext !== "undefined" ? WebGLRenderingContext : undefined);
        patch(typeof WebGL2RenderingContext !== "undefined" ? WebGL2RenderingContext : undefined);
    } catch (e) {
        console.error('Error applying WebGL identity patch:', e);
    }
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
                Object.defineProperty(navigator, 'plugins', {get: ` + pluginArrayLiteral() + `});
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
            Object.defineProperty(navigator, 'plugins', {get: %s});
			Object.defineProperty(navigator, 'getUserMedia', {value: undefined});
			Object.defineProperty(window, 'webkitRTCPeerConnection', {value: undefined});
		} catch (err) {
            console.error('Error reinforcing browser settings stage 3:', err);
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
    `, pluginArrayLiteral(), webglSpoofScript())
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
		"Object.defineProperty(navigator, 'plugins', {get: " + pluginArrayLiteral() + "})",
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

func canvasExportScript(seed uint32) string {
	script := `(function(seed) {
    "use strict";

    if (typeof HTMLCanvasElement === "undefined") return;

    const proto = HTMLCanvasElement.prototype;
    const nativeURL = proto.toDataURL;
    const nativeBlob = proto.toBlob;

    function prepare(source) {
        const w = source.width;
        const h = source.height;

        // Preserve native behavior for empty or exceptionally large canvases.
        if (!w || !h || w * h > 4000000) return source;

        const copy = document.createElement("canvas");
        copy.width = w;
        copy.height = h;

        const ctx = copy.getContext("2d");
        if (!ctx) return source;

        // Copy actual rendered pixels, not a fabricated image.
        ctx.drawImage(source, 0, 0);

        const image = ctx.getImageData(0, 0, w, h);
        const pixels = image.data;

        // Stable for the same seed, dimensions and pixel content.
        let state = (seed ^ Math.imul(w, 2654435761) ^ h) >>> 0;
        if (state === 0) state = 1;

        const count = Math.min(16, w * h);

        for (let i = 0; i < count; i++) {
            state ^= state << 13;
            state ^= state >>> 17;
            state ^= state << 5;
            state >>>= 0;

            const index = (state % (w * h)) * 4;

            // Preserve transparent pixels.
            if (pixels[index + 3] >= 128) {
                pixels[index] ^= 1;
            }
        }

        ctx.putImageData(image, 0, 0);
        return copy;
    }

    proto.toDataURL = function(type, quality) {
        if (!(this instanceof HTMLCanvasElement)) {
            return nativeURL.apply(this, arguments);
        }

        let source;
        try {
            source = prepare(this);
        } catch (err) {
            if (err && err.name === "SecurityError") throw err;
            return nativeURL.apply(this, arguments);
        }
        return nativeURL.apply(source, arguments);
    };

    proto.toBlob = function(callback, type, quality) {
        if (!(this instanceof HTMLCanvasElement) ||
            typeof callback !== "function") {
            return nativeBlob.apply(this, arguments);
        }

        let source;
        try {
            source = prepare(this);
        } catch (err) {
            if (err && err.name === "SecurityError") throw err;
            return nativeBlob.apply(this, arguments);
        }
        return nativeBlob.apply(source, arguments);
    };
})(__SEED__);`

	return strings.ReplaceAll(
		script,
		"__SEED__",
		strconv.FormatUint(uint64(seed), 10),
	)
}

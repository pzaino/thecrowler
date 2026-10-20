// Camouflage patch execution harness.
//
// Usage: node camouflage_harness.js <phase> <script.js> [extra...]
// Prints one JSON object to stdout and always exits 0 (failures are data).
//
// Stubs model just enough browser behavior to execute the VDI patch scripts
// with REALISTIC failure semantics: native methods that require their
// receiver throw "Illegal invocation" when unbound; redefining a
// non-configurable property throws; everything else is permissive. console.error
// output is captured so stage-level error swallowing becomes observable.

const fs = require('fs');
const vm = require('vm');

const phase = process.argv[2];
const scriptFile = process.argv[3];
const scriptText = fs.readFileSync(scriptFile, 'utf8');

function makeWorld() {
  const errors = [];

  const navigatorProto = {};
  const navigator = Object.create(navigatorProto);
  Object.assign(navigator, {
    webdriver: true,
    userAgent: 'native-ua',
    appVersion: 'native-appversion',
    platform: 'NativeOS',
    vendor: 'Native Vendor',
    languages: ['en-US'],
    deviceMemory: 4,
    hardwareConcurrency: 8,
    plugins: [],
    mimeTypes: [],
    mediaDevices: { getUserMedia: function () {} },
    getUserMedia: function () {},
    geolocation: {},
    doNotTrack: null,
    connection: { type: 'ethernet' },
    onLine: true,
  });
  navigator.userAgentData = {
    platform: 'Linux',
    mobile: false,
    brands: [{ brand: 'Native', version: '1' }],
    getHighEntropyValues: function (hints) {
      const table = {
        platformVersion: '9.9.9-host', architecture: 'host-arch',
        model: 'host-model', bitness: 'host-bitness',
        uaFullVersion: '9.9.9-host', fullVersionList: 'host-list',
      };
      const wanted = Array.isArray(hints) ? hints : Object.keys(table);
      if (wanted.indexOf('forbidden-hint') !== -1) {
        return Promise.reject(new Error('NotAllowedError'));
      }
      const out = {};
      // HARNESS_WITHHOLD (comma-separated) simulates Permissions Policy
      // withholding: listed keys are omitted from the native result.
      const withheld = (process.env.HARNESS_WITHHOLD || '').split(',').map(s => s.trim()).filter(Boolean);
      wanted.forEach(h => { if (withheld.indexOf(h) === -1) out[h] = (h in table) ? table[h] : ('native:' + h); });
      return Promise.resolve(out);
    },
  };

  class Permissions {
    query(parameters) {
      if (!(this instanceof Permissions)) {
        throw new TypeError("Illegal invocation: permissions.query requires a Permissions receiver");
      }
      return Promise.resolve({ state: 'prompt' });
    }
  }
  navigator.permissions = new Permissions();

  function makeGLContext() {
    // Like a real canvas context, instances inherit the (possibly patched)
    // WebGL prototype: pre-patch getParameter resolves natively.
    const ctx = {
      __isGLContext: true,
      params: { 1: 'passthrough-value', 37445: 'Real Vendor', 37446: 'Real Renderer', 999: 'passthrough-value' },
      getExtension: function (name) {
        if (name === 'WEBGL_debug_renderer_info') {
          return { UNMASKED_RENDERER_WEBGL: 37446, UNMASKED_VENDOR_WEBGL: 37445 };
        }
        return null;
      },
    };
    Object.setPrototypeOf(ctx, WebGLRenderingContext.prototype);
    return ctx;
  }
  function nativeGetParameter(param) {
    if (!this || !this.__isGLContext) {
      throw new TypeError('Illegal invocation: getParameter requires a WebGL context receiver');
    }
    return this.params[param];
  }
  function WebGLRenderingContext() {}
  WebGLRenderingContext.prototype.getParameter = nativeGetParameter;
  function WebGL2RenderingContext() {}
  WebGL2RenderingContext.prototype.getParameter = nativeGetParameter;

  // --- Pixel-faithful Canvas mock (for the deterministic export patch) ---
  // The native export path is modeled precisely because the camouflage's
  // correctness is about preserving it: the patch COPIES the live source into
  // a fresh canvas, perturbs that copy's pixels, and re-encodes — so the source
  // must stay unchanged, repeats must be deterministic per (seed, content), and
  // a SecurityError from the source's 2D context must propagate (tainted canvas)
  // rather than being swallowed. Native encoders are content-reflective (the
  // output bytes depend on the pixel store) yet prefix the real container magic,
  // so a decodable-format check (PNG magic) still holds for an untouched export.
  const PNG_MAGIC = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
  function contentBytes(tag, w, h, data) {
    // Stable FNV-1a digest over (type, dims, pixels) -> 4 digest bytes appended
    // after the 8-byte container magic, plus dims. Identical inputs yield
    // identical bytes; a single perturbed pixel flips the digest.
    let x = 0x811c9dc5 >>> 0;
    const tagStr = String(tag || 'image/png');
    const mix = function (v) { x ^= (v & 0xff); x = Math.imul(x, 0x0103cfc); };
    for (let i = 0; i < tagStr.length; i++) mix(tagStr.charCodeAt(i));
    mix(w); mix(h);
    for (let i = 0; i < data.length; i++) mix(data[i]);
    x >>>= 0;
    return PNG_MAGIC.concat([
      x & 0xff, (x >>> 8) & 0xff, (x >>> 16) & 0xff, (x >>> 24) & 0xff,
      (w & 0xff), (h & 0xff), (tagStr.length & 0xff), 0,
    ]);
  }
  function newPixelStore(w, h, fill) {
    const data = new Uint8Array((w || 1) * (h || 1) * 4);
    const f = (fill === undefined || fill === null) ? 0 : (fill & 0xff);
    for (let i = 0; i < data.length; i++) data[i] = f;
    return data;
  }
  function HTMLCanvasElement() {
    this.width = 1; this.height = 1;
    this._data = newPixelStore(1, 1, 0);
    this._exhausted = false;  // a 2D-unavailable canvas (getContext -> null)
    this._tainted = false;    // a tainted canvas (getContext -> SecurityError)
    this._errType = 'none';   // a canvas whose 2D context throws a non-security error
  }
  HTMLCanvasElement.prototype.getContext = function (type) {
    if (this._tainted) {
      const e = new Error('Tainted canvas');
      e.name = 'SecurityError';
      throw e;
    }
    if (this._errType === 'plain') {
      throw new Error('boom (non-security)');
    }
    if (this._exhausted) return null;
    if (type === 'webgl' || type === 'webgl2' || type === 'experimental-webgl') return makeGLContext();
    // 2D pixel-store operations the export patch relies on. drawImage copies
    // the source store into a FRESH destination store, so the source is never
    // aliased by the working copy; getImageData returns a live view of it.
    const self2d = this;
    return {
      fillRect: function () {},
      drawImage: function (src, dx, dy) {
        // A source-canvas error surfaces here like the native contract: a plain
        // error (non-security) must fall back to native, while a SecurityError
        // (tainted cross-origin source) must propagate to the caller.
        if (src && src._errType === 'plain') {
          throw new Error('drawImage boom (non-security)');
        }
        if (src && src._tainted) {
          const e = new Error('Cannot extract image data from a tainted canvas');
          e.name = 'SecurityError';
          throw e;
        }
        // Copy the source pixel store into this (destination) store. A fresh
        // copy per call, so the source is never aliased by the destination.
        const n = Math.min(self2d._data.length, src._data ? src._data.length : 0);
        for (let i = 0; i < n; i++) self2d._data[i] = src._data[i];
      },
      getImageData: function (x, y, w, h) {
        return { data: self2d._data, width: w, height: h };
      },
      putImageData: function (image) {
        const src = image.data;
        const n = Math.min(src.length, self2d._data.length);
        for (let i = 0; i < n; i++) self2d._data[i] = src[i];
      },
      canvas: this,
    };
  };
  HTMLCanvasElement.prototype.toDataURL = function (type) {
    const tag = type || 'image/png';
    return 'data:' + tag + ';base64,' +
      Buffer.from(contentBytes(tag, this.width, this.height, this._data)).toString('base64');
  };
  HTMLCanvasElement.prototype.toBlob = function (callback, type, quality) {
    const tag = type || 'image/png';
    const bytes = contentBytes(tag, this.width, this.height, this._data);
    const arr = new Uint8Array(bytes.length);
    for (let i = 0; i < bytes.length; i++) arr[i] = bytes[i];
    const blob = {
      type: tag,
      size: arr.length,
      _bytes: arr,
      arrayBuffer: function () { return arr.buffer.slice(arr.byteOffset, arr.byteOffset + arr.byteLength); },
      text: async function () { return String.fromCharCode.apply(null, arr); },
    };
    if (typeof callback === 'function') callback(blob);
  };

  // Faithful AudioBuffer mock for the deterministic AudioBuffer camouflage.
  // Web Audio semantics are modeled precisely because the camouflage's
  // correctness is exactly about preserving them:
  //   - getChannelData returns a LIVE Float32Array view of the channel store
  //     (the SAME reference on every call, reflecting the native contract), so
  //     a patch that mutates "the returned buffer" corrupts real audio;
  //   - out-of-range / non-integer channels throw like the native method, so
  //     the patch's error propagation can be verified;
  //   - copyFromChannel/copyToChannel operate on the live store, so the
  //     "no mutation / consistency" contract can be observed.
  // The per-channel store lives in a side table keyed by the buffer (a
  // WeakMap), so a returned AudioBuffer carries NO own data property — matching
  // the native AudioBuffer, whose channels are exposed only through getters (not
  // own properties). The "exactly-once, marker-free" contract therefore has no
  // observable place to hide a per-instance marker. The fill uses a fixed
  // non-round value so a sub-1e-7 deterministic perturbation is observable in
  // float32 while legitimate audio stays audibly unchanged.
  const abstore = new WeakMap(); // buffer -> [Float32Array channel views]
  const RENDER_BASE = 0.123456789;
  // nextDecodeRejection flips decodeAudioData into a failing mode so the probe
  // can exercise the Promise-rejection and callback-error paths without needing
  // a malformed audio payload (a real decoder rejects an undecodable buffer).
  let nextDecodeRejection = false;

  function makeRenderedBuffer() {
    // A freshly produced AudioBuffer, filled (as a native render would) with a
    // fixed base so the render-boundary perturbation is observable in float32.
    const buf = new AudioBuffer(1, 1000, 44100);
    const d = abstore.get(buf)[0];
    for (let i = 0; i < d.length; i++) d[i] = RENDER_BASE;
    return buf;
  }

  function AudioBuffer(channelCount, length, sampleRate) {
    if (!(this instanceof AudioBuffer)) {
      throw new TypeError("Cannot call a class as a function");
    }
    let nch = 1, len = 0;
    if (typeof channelCount === "object" && channelCount !== null) {
      nch = channelCount.numberOfChannels || 1;
      len = channelCount.length || 0;
    } else {
      nch = (typeof channelCount === "number" && channelCount) ? channelCount : 1;
      len = (typeof length === "number") ? length : 0;
    }
    const store = [];
    for (let c = 0; c < nch; c++) store.push(new Float32Array(len));
    // The live store is held outside the instance, so a returned buffer has no
    // own data property (and thus no room for a per-instance patch marker).
    abstore.set(this, store);
    // Standard AudioBuffer accessors (native getters); in the mock they are own
    // data properties, so a returned buffer has exactly these four own
    // properties and no patch-introduced marker.
    Object.defineProperty(this, "numberOfChannels", { value: nch, enumerable: true, configurable: true });
    Object.defineProperty(this, "sampleRate", { value: (typeof sampleRate === "number" && sampleRate) ? sampleRate : 44100, enumerable: true, configurable: true });
    Object.defineProperty(this, "length", { value: len, enumerable: true, configurable: true });
    Object.defineProperty(this, "duration", { value: ((typeof sampleRate === "number" && sampleRate) ? len / sampleRate : len / 44100), enumerable: true, configurable: true });
  }
  AudioBuffer.prototype.getChannelData = function (channel) {
    if (!Number.isInteger(channel) || channel < 0 || channel >= this.numberOfChannels) {
      throw new RangeError(
        'Failed to execute "getChannelData" on "AudioBuffer": the provided channel is out of range'
      );
    }
    // Live view: the SAME backing reference on every call.
    return abstore.get(this)[channel];
  };
  // copyFromChannel(destination, channelNumber, startInFrames): native direction
  // copies the channel data OUT into the external destination.
  AudioBuffer.prototype.copyFromChannel = function (destination, channel, start) {
    if (!Number.isInteger(channel) || channel < 0 || channel >= this.numberOfChannels) {
      throw new RangeError(
        'Failed to execute "copyFromChannel" on "AudioBuffer": the provided channel is out of range'
      );
    }
    const src = abstore.get(this)[channel];
    const s = (typeof start === "number") ? start : 0;
    const n = Math.min(destination.length, src.length - s);
    for (let i = 0; i < n; i++) destination[i] = src[s + i];
    return this;
  };
  // copyToChannel(source, channelNumber, startInFrames): native direction copies
  // the external source IN into the channel.
  AudioBuffer.prototype.copyToChannel = function (source, channel, start) {
    if (!Number.isInteger(channel) || channel < 0 || channel >= this.numberOfChannels) {
      throw new RangeError(
        'Failed to execute "copyToChannel" on "AudioBuffer": the provided channel is out of range'
      );
    }
    const dst = abstore.get(this)[channel];
    const s = (typeof start === "number") ? start : 0;
    const n = Math.min(source.length, dst.length - s);
    for (let i = 0; i < n; i++) dst[s + i] = source[i];
    return this;
  };

  // BaseAudioContext: the shared base of AudioContext and OfflineAudioContext,
  // on which decodeAudioData natively lives. Both concrete decoders inherit this
  // single implementation, so a prototype-level patch on BaseAudioContext reaches
  // every concrete decoder — the real browser layout the patch targets. The mock
  // models all three completion boundaries: Promise form, legacy callback form,
  // and a rejection mode (Promise rejects / the error callback is invoked).
  function BaseAudioContext() {}
  BaseAudioContext.prototype.decodeAudioData = function (data) {
    const self = this;
    const failing = nextDecodeRejection;
    nextDecodeRejection = false;
    if (arguments.length >= 2 && typeof arguments[1] === "function") {
      // Legacy callback form: the engine invokes the callbacks asynchronously.
      const success = arguments[1];
      const errback = (arguments.length >= 3 && typeof arguments[2] === "function") ? arguments[2] : null;
      Promise.resolve().then(function () {
        if (failing) {
          if (errback) errback.call(self, new Error("The audio data provided cannot be processed."));
          return;
        }
        success.call(self, makeRenderedBuffer());
      });
      return void 0;
    }
    // Promise form (or a synchronous throw on a malformed argument).
    if (failing) return Promise.reject(new Error("The audio data provided cannot be processed."));
    return Promise.resolve(makeRenderedBuffer());
  };

  // AudioContext and OfflineAudioContext inherit decodeAudioData from the shared
  // base; OfflineAudioContext additionally provides startRendering (a Promise
  // boundary). Neither defines an OWN decodeAudioData, so a single patch on the
  // base reaches both.
  function AudioContext() {}
  Object.setPrototypeOf(AudioContext.prototype, BaseAudioContext.prototype);
  function OfflineAudioContext() {}
  Object.setPrototypeOf(OfflineAudioContext.prototype, BaseAudioContext.prototype);
  OfflineAudioContext.prototype.createBuffer = function (nch, len) {
    return new AudioBuffer(nch, len);
  };
  OfflineAudioContext.prototype.startRendering = function () {
    // OfflineAudioContext.decodeAudioData is inherited from BaseAudioContext;
    // startRendering is offline-only and always resolves a Promise<AudioBuffer>.
    if (nextDecodeRejection) {
      nextDecodeRejection = false;
      return Promise.reject(new Error("The audio data provided cannot be processed."));
    }
    return Promise.resolve(makeRenderedBuffer());
  };

  function Element() {}
  Element.prototype.attachShadow = function () { return { mode: 'open', host: this }; };

  function RTCPeerConnection() {}
  function RTCDataChannel() {}

  const window = {
    navigator: navigator,
    RTCPeerConnection: RTCPeerConnection,
    RTCDataChannel: RTCDataChannel,
    webkitRTCPeerConnection: RTCPeerConnection,
    devicePixelRatio: 1,
    innerWidth: 800, innerHeight: 600, outerWidth: 800, outerHeight: 600,
    _Selenium_IDE_Recorder: {},
    cdc_adoQpoasnfa76pfcZLmcfl_Array: 1,
    cdc_adoQpoasnfa76pfcZLmcfl_Promise: 1,
    cdc_adoQpoasnfa76pfcZLmcfl_Symbol: 1,
    addEventListener: function (type, listener) {
      (this.__listeners[type] = this.__listeners[type] || []).push(listener);
    },
    __listeners: {},
    __fire: function (type) {
      (this.__listeners[type] || []).forEach(fn => fn());
    },
  };
  // document is controllable so the export patch's working-copy allocation can
  // be exercised through both its normal branch (a fresh 2D-capable canvas) and
  // its null-context fallback (a 2D-unavailable canvas).
  let nextCopyExhausted = false;
  const document = {
    createElement: function (tag) {
      if (tag !== 'canvas') return { tagName: tag };
      const c = new HTMLCanvasElement();
      // A realistic, non-trivial size so the export actually copies pixels.
      c.width = 8; c.height = 8;
      c._data = newPixelStore(8, 8, 0);
      if (nextCopyExhausted) { c._exhausted = true; nextCopyExhausted = false; }
      return c;
    },
  };
  const screen = { width: 800, height: 600 };
  const indexedDB = { deleteDatabase: function () { return undefined; } };
  const consoleStub = { error: function (msg) { errors.push(String(msg)); }, log: function () {} };

  // Canvas-probe helpers exposed on the world (not on the patch's sandbox) so a
  // test can build specific canvases and compare export outputs without the
  // patch being able to see them.
  const makeCanvas = function (w, h, fill) {
    const c = new HTMLCanvasElement();
    c.width = w; c.height = h; c._data = newPixelStore(w, h, fill);
    return c;
  };
  const buffersEqual = function (a, b) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
    return true;
  };

  const sandbox = {
    navigator, window, document, screen, indexedDB,
    HTMLCanvasElement, WebGLRenderingContext, WebGL2RenderingContext,
    AudioBuffer, BaseAudioContext, AudioContext, OfflineAudioContext, Element, RTCPeerConnection, RTCDataChannel,
    // Standard intrinsics the patch scripts reference: Float32Array/Uint8Array
    // are the view types the camouflage copies into; Buffer backs the native
    // base64 export; Number/RangeError back the out-of-range channel contract.
    Float32Array, Uint8Array, Buffer, Number, RangeError,
    Error, Object, Array, Promise, JSON, Math,
    console: consoleStub,
    // Probe-only helpers.
    __setDecodeRejection: function (v) { nextDecodeRejection = !!v; },
    __makeCanvas: makeCanvas,
    __urlBytes: function (dataURL) {
      const i = dataURL.indexOf('base64,');
      return Buffer.from(dataURL.slice(i + 7), 'base64');
    },
    __blobBytes: function (blob) { return (blob && blob._bytes) ? blob._bytes : (blob && blob.arrayBuffer ? new Uint8Array(blob.arrayBuffer()) : new Uint8Array(0)); },
    __buffersEqual: buffersEqual,
    __clone: function (a) { const o = new Uint8Array(a.length); for (let i = 0; i < a.length; i++) o[i] = a[i]; return o; },
    __setNextCopyExhausted: function (v) { nextCopyExhausted = !!v; },
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  return { sandbox, errors, navigator, window, document };
}

function snapshot(world) {
  const { navigator, window } = world;
  const out = {};
  try { out.webdriver = navigator.webdriver; } catch (e) { out.webdriver = 'threw:' + e.message; }
  try { out.userAgent = navigator.userAgent; } catch (e) { out.userAgent = 'threw'; }
  try { out.platform = navigator.platform; } catch (e) { out.platform = 'threw'; }
  try { out.deviceMemory = navigator.deviceMemory; } catch (e) { out.deviceMemory = 'threw'; }
  try { out.hardwareConcurrency = navigator.hardwareConcurrency; } catch (e) { out.hardwareConcurrency = 'threw'; }
  try {
    const p = navigator.plugins;
    out.pluginsLength = p.length;
    out.plugin0 = (p.length > 0 && typeof p[0] === 'object' && p[0] !== null) ? p[0].name : typeof p[0];
    out.pluginItem = typeof p.item;
    out.pluginNamedItem = typeof p.namedItem;
    out.pluginItem0 = (typeof p.item === 'function') ? String(p.item(0) && p.item(0).name) : 'no-item-fn';
    out.pluginMimeType = (p.length > 0 && p[0] && typeof p[0].item === 'function') ? String(p[0].item(0) && p[0].item(0).type) : 'no-plugin-item-fn';
  } catch (e) { out.pluginsError = String(e && e.message || e); }
  try {
    const m = navigator.mimeTypes;
    out.mimeLength = m.length;
    out.mime0type = (m.length > 0) ? m[0].type : null;
  } catch (e) { out.mimeError = String(e && e.message || e); }
  // Stability + cross-relationships: repeated getter reads must return the
  // identical objects (the native navigator object graph is stable and its
  // MimeType instances are shared between each plugin's .mimeTypes and the
  // global navigator.mimeTypes). Guarded so phases that never install the
  // registry (e.g. a bare WebGL or audio phase) degrade to null/false instead
  // of throwing.
  try {
    const p = navigator.plugins;
    const m = navigator.mimeTypes;
    out.pluginsStable = (navigator.plugins === p);
    out.mimesStable = (navigator.mimeTypes === m);
    out.pluginIndexConsistent = (p && p[0] && typeof p[0].item === 'function') ? (p[0][0] === p[0].item(0)) : null;
    out.pluginNamedItemConsistent = (p && p[0] && p[1] && typeof p[1].name === 'string') ? p.namedItem(p[1].name) === p[1] : null;
    out.pluginMimeShared = (p && p[0] && p[0].mimeTypes && p[0].mimeTypes.length > 0 && m && m.length > 0)
      ? (p[0].mimeTypes[0] === m[0]) : null;
    out.mimeEnabledPlugin = (m && m[0] && m[0].enabledPlugin && p && p[0])
      ? (m[0].enabledPlugin === p[0]) : null;
  } catch (e) { out.registryErr = String(e && e.message || e); }
  try { out.devicePixelRatio = window.devicePixelRatio; } catch (e) { out.devicePixelRatio = 'threw'; }
  try { out.toDataURL = new world.sandbox.HTMLCanvasElement().toDataURL(); } catch (e) { out.toDataURL = 'threw:' + e.message; }
  try {
    // Real GL context: extension available, native returns strings, patch
    // returns spoofed vendor/renderer.
    const gl = { __isGLContext: true, params: { 37445: 'RealVendor', 37446: 'RealRenderer', 1: 'other' } };
    const gp = world.sandbox.WebGLRenderingContext.prototype.getParameter;
    out.webglVendor = gp.call(gl, 37445);
    out.webglRenderer = gp.call(gl, 37446);
    out.webglPassthrough = gp.call(gl, 1);
    const gl2proto = world.sandbox.WebGL2RenderingContext.prototype.getParameter;
    out.webgl2Patched = (gl2proto !== world.sandbox.__nativeGL2GetParameter);
    // Extension-unavailable: native returns a non-string for 37445, so the
    // patch must NOT fabricate a spoofed value.
    const glNoExt = { __isGLContext: true, params: {} };
    out.webglExtNoVendor = gp.call(glNoExt, 37445);
    out.webglExtNoRenderer = gp.call(glNoExt, 37446);
    out.webglExtNoFabricated = (out.webglExtNoVendor !== 'Intel Inc.' && out.webglExtNoRenderer !== 'Mesa Intel(R) UHD Graphics');
    // Fabricated receiver: a non-GL `this` must not yield a spoofed result.
    // Native getParameter.call(nonGL, 37445) throws "Illegal invocation".
    out.webglFakerReceiver = 'not-thrown';
    try {
      const fake = { not_a_gl: true };
      gp.call(fake, 37445);
      out.webglFakerReceiver = 'no-throw';
    } catch (e) {
      out.webglFakerReceiver = 'threw:' + (e.message || 'throw');
    }
  } catch (e) { out.webglError = String(e && e.message || e); }
  // P1 enumeration: no discoverable CROWler marker on the patched prototypes
  // or on globalThis.
  try {
    const protoNames = Object.getOwnPropertyNames(world.sandbox.WebGLRenderingContext.prototype);
    out.webglProtoNames = protoNames.filter(n => /crowler|__/.test(n)).join(',');
    const protoSyms = Object.getOwnPropertySymbols(world.sandbox.WebGLRenderingContext.prototype);
    out.webglProtoSyms = protoSyms.length;
    const globalNames = Object.getOwnPropertyNames(world.sandbox);
    out.crowlerGlobal = globalNames.filter(n => /crowler/i.test(n)).join(',');
    const abProto = world.sandbox.AudioBuffer && world.sandbox.AudioBuffer.prototype;
    if (abProto) {
      out.crowlerAudioProto = Object.getOwnPropertyNames(abProto).filter(n => /crowler/i.test(n)).join(',');
    }
  } catch (e) { out.enumError = String(e && e.message || e); }
  try {
    // Per-context path (GPUPatch wraps getContext, not the prototype).
    const canvas = new world.sandbox.HTMLCanvasElement();
    const ctx = canvas.getContext('webgl');
    const ext = ctx.getExtension('WEBGL_debug_renderer_info');
    out.gpuCtxVendor = ctx.getParameter(ext.UNMASKED_VENDOR_WEBGL);
    out.gpuCtxRenderer = ctx.getParameter(ext.UNMASKED_RENDERER_WEBGL);
    out.gpuCtxPassthrough = ctx.getParameter(1);
    const spent = new world.sandbox.HTMLCanvasElement();
    spent._exhausted = true;
    out.gpuNullPassthrough = (spent.getContext('webgl') === null);
  } catch (e) { out.gpuCtxError = String(e && e.message || e); }
  try {
    const el = new world.sandbox.Element();
    const shadow = el.attachShadow({ mode: 'open' });
    out.attachShadowNull = (shadow === null);
    out.attachShadowType = typeof shadow;
  } catch (e) { out.attachShadowError = String(e && e.message || e); }
  try {
    out.permsNotifications = 'pending-promise';
  } catch (e) { out.permsError = String(e); }
  try { out.stackTraceLimit = Error.stackTraceLimit; } catch (e) { out.stackTraceLimit = 'threw'; }
  try { out.cdcDeleted = (window.cdc_adoQpoasnfa76pfcZLmcfl_Array === undefined); } catch (e) { out.cdcDeleted = 'threw'; }
  try {
    out.uadPlatform = (navigator.userAgentData && navigator.userAgentData.platform) || null;
    out.uadMobile = (navigator.userAgentData && navigator.userAgentData.mobile) || null;
    out.uadBrands = (navigator.userAgentData && navigator.userAgentData.brands) || null;
  } catch (e) { out.uadError = String(e && e.message || e); }
  return out;
}

// arraysEqual compares two array-likes element-wise (used to assert the
// AudioBuffer patch's per-read determinism).
function arraysEqual(a, b) {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

// audioProbe exercises the deterministic AudioBuffer camouflage against a
// FAITHFUL AudioBuffer mock and render/decode-boundary mocks. It tests BOTH the
// preserved native getChannelData contract (live view, write-through, copy
// consistency, range error, no-accumulation) AND the render/decode-boundary
// perturbation in every supported completion form — the modern Promise form, the
// legacy success-error callback form, and OfflineAudioContext decoding (inherited
// from BaseAudioContext) — while getChannelData itself stays native. Each
// produced buffer is perturbed exactly once and carries no discoverable own
// property (verified against a fresh, unperturbed buffer's own-property set).
//
// Deliberately conservative: each assertion degrades to a string on failure,
// so the JSON always carries the probe's findings.
async function audioProbe(world) {
  const out = {};
  try {
    const S = world.sandbox;
    const AudioBuffer = S.AudioBuffer;
    const AudioContext = S.AudioContext;
    const OfflineAudioContext = S.OfflineAudioContext;
    const F32 = S.Float32Array;
    const REFBASE = 0.123456789; // the fixed render fill; a perturbed sample differs
    const buf = new AudioBuffer(1, 300, 44100);

    // --- Native getChannelData contract (sync, on a fresh buffer) ---

    // Return type: getChannelData returns a Float32Array view.
    const a = buf.getChannelData(0);
    out.audioType = Object.prototype.toString.call(a).toLowerCase();
    out.audioLen = a.length;

    // Write-through: writing to the returned view mutates the live store.
    out.audioWriteThrough = (function () {
      // 0.5 is exactly representable in float32; the live view must reflect the write.
      const val = 0.5;
      buf.getChannelData(0)[5] = val;
      return buf.getChannelData(0)[5] === val;
    })();

    // No-accumulation: repeated reads without writes are identical.
    out.audioDeterministic = (function () {
      const x = buf.getChannelData(0);
      const y = buf.getChannelData(0);
      return arraysEqual(x, y);
    })();

    // copyFromChannel reads the live store into a destination (native order).
    out.audioCopyFromConsistent = (function () {
      const dst = new F32(300);
      buf.copyFromChannel(dst, 0);
      // dst[5] should equal what write-through wrote (0.5 is float32-exact).
      return dst[5] === 0.5;
    })();

    // copyToChannel writes an external source INTO the channel (native order).
    out.audioCopyToChannel = (function () {
      const src = new F32(300);
      for (let i = 0; i < 300; i++) src[i] = 1; // 1.0 is float32-exact
      buf.copyToChannel(src, 0);
      return buf.getChannelData(0)[5] === 1.0;
    })();

    // Raw-intact: reads do not corrupt the live store. Snapshot, read, compare.
    out.audioRawIntact = (function () {
      const snap = new F32(300);
      buf.copyFromChannel(snap, 0);
      // Read three times (a read-only patch leaves the store unchanged; a
      // mutating legacy patch accumulates noise).
      buf.getChannelData(0);
      buf.getChannelData(0);
      buf.getChannelData(0);
      const resnap = new F32(300);
      buf.copyFromChannel(resnap, 0);
      return arraysEqual(snap, resnap);
    })();

    // Native out-of-range channel throws RangeError through the native method.
    out.audioRangeError = 'no-throw';
    try {
      buf.getChannelData(5);
      out.audioRangeError = 'no-throw';
    } catch (e) {
      out.audioRangeError = 'threw';
    }
    out.audioRangeCopyFrom = 'no-throw';
    try {
      buf.copyFromChannel(new F32(1), 5);
      out.audioRangeCopyFrom = 'no-throw';
    } catch (e) {
      out.audioRangeCopyFrom = 'threw';
    }

    // Multi-channel: a 2-channel buffer's channels are independent.
    out.audioMultiChannel = (function () {
      const m = new AudioBuffer(2, 300);
      m.getChannelData(0)[0] = 1.0;
      m.getChannelData(1)[0] = 2.0;
      return m.getChannelData(0)[0] === 1.0 && m.getChannelData(1)[0] === 2.0;
    })();

    // Multi-buffer: two independent buffers do not share state.
    out.audioMultiBuffer = (function () {
      const b1 = new AudioBuffer(1, 300);
      const b2 = new AudioBuffer(1, 300);
      b1.getChannelData(0)[0] = 0.99;
      return b2.getChannelData(0)[0] === 0;
    })();

    const canDecode = !!(AudioContext && typeof AudioContext.prototype.decodeAudioData === 'function');

    // --- Render-boundary perturbation via startRendering (offline, Promise) ---
    // A freshly rendered buffer's stored samples carry the deterministic seed
    // perturbation while getChannelData reads stay native, and re-reads of the
    // same buffer do not drift (the perturbation is applied once at the boundary,
    // not per read — no accumulation).
    out.audioRenderedSample100 = null;
    out.audioRenderedPerturbed = false;
    out.audioRenderedDeterministic = false;
    try {
      if (OfflineAudioContext && typeof OfflineAudioContext.prototype.startRendering === 'function') {
        const oac = new (OfflineAudioContext)();
        const rendered = await oac.startRendering();
        out.audioRenderedSample100 = rendered.getChannelData(0)[100];
        out.audioRenderedPerturbed = (Math.abs(out.audioRenderedSample100 - REFBASE) > 1e-9);
        out.audioRenderedDeterministic = (rendered.getChannelData(0)[100] === out.audioRenderedSample100);
      }
    } catch (e) {
      out.audioRenderError = String((e && e.message) || e);
    }

    // --- Exactly-once, marker-free perturbation ---
    // A produced buffer is perturbed exactly once and the patch leaves NO own
    // property on it: its own-property set is identical to a fresh, unperturbed
    // buffer's. A per-instance marker (the defect being removed) would add a name
    // here, so the sets would differ.
    out.audioNoInstanceMarker = false;
    out.audioInstancePropSet = null;
    try {
      const clean = new AudioBuffer(1, 128, 44100);
      const cleanNames = Object.getOwnPropertyNames(clean).slice().sort();
      const rendered = await (new (OfflineAudioContext)()).startRendering();
      const renderedNames = Object.getOwnPropertyNames(rendered).slice().sort();
      out.audioInstancePropSet = JSON.stringify(renderedNames);
      out.audioNoInstanceMarker = arraysEqual(cleanNames, renderedNames);
    } catch (e) {
      out.audioInstanceMarkerError = String((e && e.message) || e);
    }

    // --- Promise-form decoding (modern API) perturbs the resolved buffer ---
    out.audioPromiseHasThen = false;
    out.audioPromiseSample100 = null;
    out.audioPromisePerturbed = false;
    out.audioPromiseDeterministic = false;
    try {
      if (canDecode) {
        const ac = new (AudioContext)();
        const p = ac.decodeAudioData(new S.Uint8Array([1, 2, 3]));
        out.audioPromiseHasThen = !!(p && typeof p.then === 'function');
        const rendered = await p;
        out.audioPromiseSample100 = rendered.getChannelData(0)[100];
        out.audioPromisePerturbed = (Math.abs(out.audioPromiseSample100 - REFBASE) > 1e-9);
        out.audioPromiseDeterministic = (rendered.getChannelData(0)[100] === out.audioPromiseSample100);
      }
    } catch (e) {
      out.audioPromiseError = String((e && e.message) || e);
    }

    // --- Legacy success/error callback-form decoding perturbs the delivered
    // buffer; the application's success callback must observe the perturbed value.
    out.audioCallbackSample100 = null;
    out.audioCallbackPerturbed = false;
    try {
      if (canDecode) {
        const ac = new (AudioContext)();
        let sample100 = null;
        await new Promise(function (resolve, reject) {
          ac.decodeAudioData(new S.Uint8Array([1, 2, 3]),
            function (decoded) { sample100 = decoded.getChannelData(0)[100]; resolve(); },
            function (err) { reject(err); });
        });
        out.audioCallbackSample100 = sample100;
        out.audioCallbackPerturbed = (Math.abs(sample100 - REFBASE) > 1e-9);
      }
    } catch (e) {
      out.audioCallbackError = String((e && e.message) || e);
    }

    // --- OfflineAudioContext.decodeAudioData (inherited from BaseAudioContext)
    // is perturbed through the single base-level patch.
    out.audioOfflineSample100 = null;
    out.audioOfflinePerturbed = false;
    try {
      if (OfflineAudioContext && typeof OfflineAudioContext.prototype.decodeAudioData === 'function') {
        const oac = new (OfflineAudioContext)();
        const rendered = await oac.decodeAudioData(new S.Uint8Array([1, 2, 3]));
        out.audioOfflineSample100 = rendered.getChannelData(0)[100];
        out.audioOfflinePerturbed = (Math.abs(out.audioOfflineSample100 - REFBASE) > 1e-9);
      }
    } catch (e) {
      out.audioOfflineError = String((e && e.message) || e);
    }

    // --- Promise-form rejection propagates: a failing decode rejects the returned
    // Promise (it does NOT resolve to a perturbed/phantom buffer).
    out.audioRejectHasThen = false;
    out.audioRejectThrew = 'not-run';
    out.audioRejectReason = null;
    try {
      if (canDecode) {
        const ac = new (AudioContext)();
        S.__setDecodeRejection(true);
        const p = ac.decodeAudioData(new S.Uint8Array([1, 2, 3]));
        out.audioRejectHasThen = !!(p && typeof p.then === 'function');
        let resolved = false;
        await p.then(function () { resolved = true; }, function (reason) {
          out.audioRejectReason = (reason && (reason.name || reason.message)) || null;
        });
        out.audioRejectThrew = resolved ? 'resolved-unexpectedly' : 'rejected';
      }
    } catch (e) {
      out.audioRejectThrew = 'threw-synchronous:' + String((e && e.message) || e);
    }

    // --- Legacy callback-form error handling: a failing decode must invoke the
    // (optional) error callback rather than the success callback.
    out.audioCallbackErr = 'not-run';
    out.audioCallbackErrInvoked = false;
    try {
      if (canDecode) {
        const ac = new (AudioContext)();
        S.__setDecodeRejection(true);
        const result = await new Promise(function (resolve, reject) {
          ac.decodeAudioData(new S.Uint8Array([1, 2, 3]),
            function (decoded) { reject(new Error('success-called-on-failure')); },
            function (err) { resolve('errback:' + ((err && err.message) || String(err))); });
        });
        out.audioCallbackErr = result;
        out.audioCallbackErrInvoked = (typeof result === 'string' && result.indexOf('errback:') === 0);
      }
    } catch (e) {
      out.audioCallbackErr = 'failure:' + String((e && e.message) || e);
      out.audioCallbackErrInvoked = false;
    }
  } catch (e) {
    out.audioProbeError = String((e && e.message) || e);
  }
  return out;
}

// canvasProbe exercises the deterministic export camouflage against the
// pixel-faithful Canvas mock: content-reflective encoding with container-valid
// output, MIME-type pass-through, toBlob/toDataURL byte consistency, per-realm
// determinism, source immutability, tainted-canvas SecurityError propagation
// (with a non-security error falling back to native), and a 2D-null working
// copy falling back to the native export. Each check degrades to a string on
// failure so the JSON always carries the probe's findings.
function canvasProbe(world) {
  const out = {};
  const S = world.sandbox;
  const eq = S.__buffersEqual;
  try {
    // Content-reflective, container-valid exports for the three common types.
    const c = S.__makeCanvas(8, 8, 255);
    out.canvasPNG = c.toDataURL();
    out.canvasJPEG = c.toDataURL('image/jpeg');
    out.canvasWebP = c.toDataURL('image/webp');

    // toBlob must agree with toDataURL byte-for-byte for identical content.
    let blob = null;
    c.toBlob(function (b) { blob = b; }, 'image/png');
    out.canvasBlobEqURL = blob
      ? eq(S.__urlBytes(c.toDataURL('image/png')), S.__blobBytes(blob))
      : null;

    // Deterministic: two exports of the same content in one realm agree.
    out.canvasDeterministic = (c.toDataURL() === c.toDataURL());

    // The original canvas is untouched: the export perturbs a COPY, never the
    // source store.
    const before = S.__clone(c._data);
    c.toDataURL('image/png');
    c.toBlob(function () {}, 'image/jpeg');
    out.canvasOriginalUnchanged = eq(before, c._data);

    // Tainted-canvas security: a SecurityError from the source must propagate
    // through BOTH export APIs, not be swallowed as a fallback.
    const tainted = S.__makeCanvas(8, 8, 255);
    tainted._tainted = true;
    out.canvasSecurityURL = 'no-throw';
    try {
      tainted.toDataURL();
    } catch (e) {
      out.canvasSecurityURL = (e && e.name === 'SecurityError') ? 'threw-security' : ('threw:' + (e && e.name));
    }
    out.canvasSecurityBlob = 'no-throw';
    try {
      tainted.toBlob(function () {}, 'image/png');
    } catch (e) {
      out.canvasSecurityBlob = (e && e.name === 'SecurityError') ? 'threw-security' : ('threw:' + (e && e.name));
    }

    // A non-security source error must fall back to the NATIVE export of the
    // source (not propagate the throw, not swallow the export).
    const errc = S.__makeCanvas(8, 8, 255);
    errc._errType = 'plain';
    out.canvasNonSecurityFallback = 'threw';
    try {
      const v = errc.toDataURL();
      out.canvasNonSecurityFallback = (typeof v === 'string' && v.indexOf('data:') === 0) ? 'native' : ('bad:' + String(v));
    } catch (e) {
      out.canvasNonSecurityFallback = 'threw:' + (e && e.name);
    }

    // 2D-null working copy: a 2D-unavailable copy degrades to the native
    // export of the source without throwing.
    S.__setNextCopyExhausted(true);
    out.canvasNull2DFallback = 'threw';
    try {
      const v = c.toDataURL();
      out.canvasNull2DFallback = (typeof v === 'string') ? 'native' : 'bad';
    } catch (e) {
      out.canvasNull2DFallback = 'threw:' + (e && e.name);
    }
  } catch (e) {
    out.canvasProbeError = String((e && e.message) || e);
  }
  return out;
}

async function main() {
  const world = makeWorld();
  // stash native WebGL2 getter for patch detection
  world.sandbox.__nativeGL2GetParameter = world.sandbox.WebGL2RenderingContext.prototype.getParameter;
  const result = { phase, threw: null, errors: [], values: null };
  try {
    if (phase === 'navigator-lines') {
      // The script file holds a JSON array of one statement per element,
      // mirroring production's per-line ExecuteScript isolation (elements
      // may span multiple lines, so plain newline-splitting would corrupt
      // them).
      const lines = JSON.parse(scriptText);
      const lineErrors = [];
      for (const line of lines) {
        try {
          vm.runInContext(line, world.sandbox, { filename: 'navigator-line.js' });
        } catch (e) {
          lineErrors.push(String((e && e.message) || e));
        }
      }
      result.lineErrors = lineErrors;
    } else if (phase === 'loadlistener') {
      vm.runInContext(scriptText, world.sandbox, { filename: 'loadlistener.js' });
      world.window.__fire('load');
    } else if (phase === 'reinforce-twice') {
      vm.runInContext(scriptText, world.sandbox, { filename: 'reinforce.js' });
      vm.runInContext(scriptText, world.sandbox, { filename: 'reinforce.js' });
    } else if (phase === 'audio-twice') {
      // Install the render/decode patch twice in the SAME realm to observe that a
      // same-realm re-evaluation leaves NO discoverable prototype or global marker
      // and does not corrupt the live store. This is a test artifact: a
      // cross-evaluation re-injection is NOT a supported production scenario (the
      // new-document identity script runs once per realm). The per-closure
      // WeakSets guard within an evaluation; across two evaluations the patch
      // stays observationally stable and marker-free.
      vm.runInContext(scriptText, world.sandbox, { filename: 'audio-1st.js' });
      vm.runInContext(scriptText, world.sandbox, { filename: 'audio-2nd.js' });
      result.values = result.values || {};
      // A same-realm reinstall leaves no discoverable prototype marker; the
      // crowler global is reported by the snapshot (crowlerGlobal string), which
      // runs after this branch and is the source of truth for the test.
      const abP = world.sandbox.AudioBuffer && world.sandbox.AudioBuffer.prototype;
      result.values.audioNoMarker = !abP ||
        !Object.prototype.hasOwnProperty.call(abP, '__crowlerAudioPatched__');
      const acP = world.sandbox.AudioContext && world.sandbox.AudioContext.prototype;
      result.values.audioNoMarker2 = !acP ||
        !Object.prototype.hasOwnProperty.call(acP, '__crowlerAudioPatched__');
    } else if (phase === 'audio-no-base') {
      // Exercise the fallback path: on an engine that does NOT expose
      // BaseAudioContext, the patch must wrap the concrete AudioContext /
      // OfflineAudioContext prototypes instead of the shared base.
      delete world.sandbox.BaseAudioContext;
      vm.runInContext(scriptText, world.sandbox, { filename: 'audio-no-base.js' });
    } else if (phase === 'webgl-absent') {
      // Missing WebGL/WebGL2 bindings must not throw or abort the surrounding
      // identity script (which runs this before the Client-Hints override).
      delete world.sandbox.WebGLRenderingContext;
      delete world.sandbox.WebGL2RenderingContext;
      vm.runInContext(scriptText, world.sandbox, { filename: 'webgl-absent.js' });
    } else {
      vm.runInContext(scriptText, world.sandbox, { filename: phase + '.js' });
    }
  } catch (e) {
    result.threw = String((e && e.message) || e);
  }
  // WebGL observable idempotency: where the phase exercises a WebGL patch,
  // install it a SECOND time in the same realm and confirm the OBSERVED identity
  // did not drift. A cross-evaluation re-injection builds a fresh closure whose
  // WeakSet cannot see the first wrapper, so the prototype reference may re-wrap;
  // the meaningful guarantee is observable idempotency (the spoofed value and the
  // native-first receiver validation are unchanged), not pointer stability.
  // (The full antiDetectionScript is intentionally excluded: re-running it throws
  // at its non-configurable navigator redefines, so a "second run" would abort
  // before reaching the WebGL block and give a false signal — the WebGL-only
  // phases below test the guard in isolation.)
  if (phase === 'webgl-double' || phase === 'reinforce-twice') {
    const gl1 = world.sandbox.WebGLRenderingContext.prototype.getParameter;
    const gl2 = world.sandbox.WebGL2RenderingContext.prototype.getParameter;
    try {
      vm.runInContext(scriptText, world.sandbox, { filename: 'webgl-2nd.js' });
    } catch (e) {
      // The reinstall itself throwing is captured as a world error elsewhere;
      // record it here so the idempotency finding is complete.
    }
    result.values = result.values || {};
    result.values.webglStable = (
      world.sandbox.WebGLRenderingContext.prototype.getParameter === gl1 &&
      world.sandbox.WebGL2RenderingContext.prototype.getParameter === gl2
    );
    result.values.webglVendorAfter = world.sandbox.WebGLRenderingContext.prototype.getParameter.call(
      { __isGLContext: true, params: { 37445: 'V', 37446: 'R' } }, 37445
    );
  }
  result.errors = world.errors;
  result.values = Object.assign(result.values || {}, snapshot(world));
  // AudioBuffer probe for the deterministic camouflage phases.
  if (phase === 'audio' || phase === 'audio-twice' || phase === 'audio-no-base' || phase === 'inject') {
    result.values = Object.assign(result.values || {}, await audioProbe(world));
  }
  // Canvas export probe for the deterministic export phase.
  if (phase === 'canvas') {
    result.values = Object.assign(result.values || {}, canvasProbe(world));
  }
  // resolve the permissions probe (async by nature)
  try {
    const q = world.navigator.permissions.query({ name: 'notifications' });
    result.values.permsNotifications = await q.then(v => v.state, e => 'rejected:' + e.message);
    const q2 = world.navigator.permissions.query({ name: 'camera' });
    result.values.permsCamera = await q2.then(v => v.state, e => 'rejected:' + e.message);
  } catch (e) {
    result.values.permsError = 'threw-sync:' + e.message;
  }
  // high-entropy probe (async)
  try {
    if (world.navigator.userAgentData && world.navigator.userAgentData.getHighEntropyValues) {
      const he = await world.navigator.userAgentData.getHighEntropyValues(['platformVersion', 'architecture', 'model', 'bitness', 'uaFullVersion', 'fullVersionList', 'platform', 'unknown-hint-xyz']);
      result.values.highEntropy = he;
      const heDefault = await world.navigator.userAgentData.getHighEntropyValues();
      result.values.highEntropyDefaultKeys = (heDefault && typeof heDefault === 'object') ? Object.keys(heDefault).sort() : 'non-object';
    } else {
      result.values.highEntropy = 'no-uad-api';
    }
  } catch (e) {
    result.values.highEntropy = 'rejected:' + e.message;
  }
  console.log(JSON.stringify(result));
}

main().catch(e => {
  console.log(JSON.stringify({ phase, threw: 'harness:' + e.message, errors: [], values: null }));
});

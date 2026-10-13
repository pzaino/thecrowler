// Camouflage patch execution harness (VDI-008/VDI-011/VDI-012/VDI-014, PR5).
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
      wanted.forEach(h => { out[h] = (h in table) ? table[h] : ('native:' + h); });
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

  const VALID_PNG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
  function HTMLCanvasElement() { this._exhausted = false; }
  HTMLCanvasElement.prototype.getContext = function (type) {
    if (this._exhausted) return null;
    if (type === 'webgl' || type === 'webgl2' || type === 'experimental-webgl') return makeGLContext();
    return { kind: '2d-context' };
  };
  HTMLCanvasElement.prototype.toDataURL = function () { return VALID_PNG; };

  function AudioBuffer() {}
  AudioBuffer.prototype.getChannelData = function () { return [1, 2, 3, 4, 5]; };

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
  const document = {};
  const screen = { width: 800, height: 600 };
  const indexedDB = { deleteDatabase: function () { return undefined; } };
  const consoleStub = { error: function (msg) { errors.push(String(msg)); }, log: function () {} };

  const sandbox = {
    navigator, window, document, screen, indexedDB,
    HTMLCanvasElement, WebGLRenderingContext, WebGL2RenderingContext,
    AudioBuffer, Element, RTCPeerConnection, RTCDataChannel,
    Error, Object, Array, Promise, JSON, Math,
    console: consoleStub,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  return { sandbox, errors, navigator, window, document, VALID_PNG };
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
  try { out.devicePixelRatio = window.devicePixelRatio; } catch (e) { out.devicePixelRatio = 'threw'; }
  try { out.toDataURL = new world.sandbox.HTMLCanvasElement().toDataURL(); } catch (e) { out.toDataURL = 'threw:' + e.message; }
  try {
    const gl = { __isGLContext: true, params: { 37445: 'V', 37446: 'R', 1: 'other' } };
    const gp = world.sandbox.WebGLRenderingContext.prototype.getParameter;
    out.webglVendor = gp.call(gl, 37445);
    out.webglRenderer = gp.call(gl, 37446);
    out.webglPassthrough = gp.call(gl, 1);
    const gl2proto = world.sandbox.WebGL2RenderingContext.prototype.getParameter;
    out.webgl2Patched = (gl2proto !== world.sandbox.__nativeGL2GetParameter);
  } catch (e) { out.webglError = String(e && e.message || e); }
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
    } else {
      vm.runInContext(scriptText, world.sandbox, { filename: phase + '.js' });
    }
  } catch (e) {
    result.threw = String((e && e.message) || e);
  }
  result.errors = world.errors;
  result.values = snapshot(world);
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

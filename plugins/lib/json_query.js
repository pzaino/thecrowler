// name: json_query
// description: Safe JSONPath-style querying and projection for CROWler engine plugins
// type: lib_plugin
// version: 1.0.0

/*
 * json_query is an ES5-compatible, dependency-free JSON query library for
 * CROWler engine, API, event, and library plugins.
 *
 * Supported document roots: object, array, string, number, boolean, and null.
 * Supported selectors:
 *   $                         root
 *   .name                     child property
 *   ['name'] / ["name"]       quoted child property
 *   * / [*]                   wildcard
 *   [0] / [-1]                array index (negative indexes count from the end)
 *   [start:end:step]          array slice
 *   [0,2,'name']              union
 *   ..name / ..*              recursive descent
 *   [?(@.field == value)]     filters
 *
 * Paths may omit the leading '$', so CROWler attribute-indexing paths such as
 * details.contacts[*].email work unchanged.
 */
var json_query = (function () {
    "use strict";

    var VERSION = "1.0.0";
    var hasOwn = Object.prototype.hasOwnProperty;
    var objectToString = Object.prototype.toString;
    var pathCache = {};
    var pathCacheOrder = [];
    var pathCacheSize = 256;

    function makeError(code, message, path, position) {
        var err = new Error(message);
        err.name = "JSONQueryError";
        err.code = code;
        if (typeof path === "string") err.path = path;
        if (typeof position === "number") err.position = position;
        return err;
    }

    function isArray(value) {
        return objectToString.call(value) === "[object Array]";
    }

    function isObject(value) {
        return value !== null && typeof value === "object" && !isArray(value);
    }

    function isContainer(value) {
        return value !== null && typeof value === "object";
    }

    function isFiniteNumber(value) {
        return typeof value === "number" && isFinite(value) && !isNaN(value);
    }

    function isInteger(value) {
        return isFiniteNumber(value) && Math.floor(value) === value;
    }

    function own(obj, key) {
        return obj !== null && obj !== undefined && hasOwn.call(obj, key);
    }

    function trim(value) {
        return String(value).replace(/^\s+|\s+$/g, "");
    }

    function startsWith(value, prefix) {
        return String(value).slice(0, String(prefix).length) === String(prefix);
    }

    function endsWith(value, suffix) {
        value = String(value);
        suffix = String(suffix);
        return value.slice(value.length - suffix.length) === suffix;
    }

    function repeat(value, count) {
        var out = "";
        while (count > 0) {
            if (count & 1) out += value;
            count >>= 1;
            if (count) value += value;
        }
        return out;
    }

    function indexOfIdentity(items, value) {
        var i;
        for (i = 0; i < items.length; i += 1) {
            if (items[i] === value) return i;
        }
        return -1;
    }

    function normalizeOptions(options) {
        options = options || {};
        var out = {
            maxDepth: own(options, "maxDepth") ? Number(options.maxDepth) : 256,
            maxNodes: own(options, "maxNodes") ? Number(options.maxNodes) : 1000000,
            maxResults: own(options, "maxResults") ? Number(options.maxResults) : 100000,
            maxPathLength: own(options, "maxPathLength") ? Number(options.maxPathLength) : 16384,
            maxTokens: own(options, "maxTokens") ? Number(options.maxTokens) : 1024,
            deterministic: options.deterministic !== false,
            detectCycles: options.detectCycles !== false,
            validateDocument: options.validateDocument === true,
            cache: options.cache !== false
        };
        var names = ["maxDepth", "maxNodes", "maxResults", "maxPathLength", "maxTokens"];
        var i;
        for (i = 0; i < names.length; i += 1) {
            if (!isInteger(out[names[i]]) || out[names[i]] < 1) {
                throw makeError("E_OPTIONS", names[i] + " must be a positive integer");
            }
        }
        return out;
    }

    function parseJSON(text) {
        if (typeof text !== "string") {
            throw makeError("E_JSON_TYPE", "parse() requires a JSON string");
        }
        try {
            return JSON.parse(text);
        } catch (err) {
            throw makeError("E_JSON_PARSE", "Invalid JSON: " + String(err && err.message ? err.message : err));
        }
    }

    function valueType(value) {
        if (value === null) return "null";
        if (isArray(value)) return "array";
        if (typeof value === "number") return "number";
        if (typeof value === "string") return "string";
        if (typeof value === "boolean") return "boolean";
        if (isObject(value)) return "object";
        return typeof value;
    }

    function validateJSONValue(value, options) {
        var opts = normalizeOptions(options);
        var ancestors = [];
        var visited = 0;

        function walk(node, depth) {
            var keys, i, key;
            visited += 1;
            if (visited > opts.maxNodes) {
                throw makeError("E_NODE_LIMIT", "JSON document exceeds maxNodes");
            }
            if (depth > opts.maxDepth) {
                throw makeError("E_DEPTH_LIMIT", "JSON document exceeds maxDepth");
            }
            if (node === null || typeof node === "string" || typeof node === "boolean") return true;
            if (typeof node === "number") {
                if (!isFiniteNumber(node)) {
                    throw makeError("E_JSON_NUMBER", "JSON numbers must be finite");
                }
                return true;
            }
            if (!isContainer(node)) {
                throw makeError("E_JSON_VALUE", "Unsupported JSON value type: " + typeof node);
            }
            if (opts.detectCycles && indexOfIdentity(ancestors, node) !== -1) {
                throw makeError("E_CYCLE", "Cyclic objects are not valid JSON documents");
            }
            ancestors.push(node);
            if (isArray(node)) {
                for (i = 0; i < node.length; i += 1) {
                    if (!own(node, i)) {
                        throw makeError("E_SPARSE_ARRAY", "Sparse arrays are not valid JSON documents");
                    }
                    walk(node[i], depth + 1);
                }
            } else {
                keys = Object.keys(node);
                for (i = 0; i < keys.length; i += 1) {
                    key = keys[i];
                    walk(node[key], depth + 1);
                }
            }
            ancestors.pop();
            return true;
        }

        return walk(value, 0);
    }

    function decodeQuoted(raw, path, offset) {
        var quote = raw.charAt(0);
        var i = 1;
        var out = "";
        var ch, esc, hex;
        if ((quote !== "'" && quote !== '"') || raw.charAt(raw.length - 1) !== quote) {
            throw makeError("E_PATH_QUOTE", "Invalid quoted property selector", path, offset);
        }
        while (i < raw.length - 1) {
            ch = raw.charAt(i);
            if (ch !== "\\") {
                out += ch;
                i += 1;
                continue;
            }
            i += 1;
            if (i >= raw.length - 1) {
                throw makeError("E_PATH_ESCAPE", "Unterminated escape sequence", path, offset + i);
            }
            esc = raw.charAt(i);
            if (esc === "u") {
                hex = raw.slice(i + 1, i + 5);
                if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
                    throw makeError("E_PATH_ESCAPE", "Invalid Unicode escape", path, offset + i);
                }
                out += String.fromCharCode(parseInt(hex, 16));
                i += 5;
                continue;
            }
            if (esc === "b") out += "\b";
            else if (esc === "f") out += "\f";
            else if (esc === "n") out += "\n";
            else if (esc === "r") out += "\r";
            else if (esc === "t") out += "\t";
            else if (esc === "v") out += "\v";
            else if (esc === "0") out += "\0";
            else if (esc === "\\" || esc === "/" || esc === "'" || esc === '"') out += esc;
            else out += esc;
            i += 1;
        }
        return out;
    }

    function splitTopLevel(text, delimiter, path, offset) {
        var parts = [];
        var start = 0;
        var quote = "";
        var escaped = false;
        var paren = 0;
        var bracket = 0;
        var regex = false;
        var i, ch, prev;
        for (i = 0; i < text.length; i += 1) {
            ch = text.charAt(i);
            if (quote) {
                if (escaped) escaped = false;
                else if (ch === "\\") escaped = true;
                else if (ch === quote) quote = "";
                continue;
            }
            if (regex) {
                if (escaped) escaped = false;
                else if (ch === "\\") escaped = true;
                else if (ch === "/") regex = false;
                continue;
            }
            if (ch === "'" || ch === '"') {
                quote = ch;
                continue;
            }
            prev = i > 0 ? text.charAt(i - 1) : "";
            if (ch === "/" && (i === 0 || /[=(,!&|?:]/.test(prev))) {
                regex = true;
                continue;
            }
            if (ch === "(") paren += 1;
            else if (ch === ")") paren -= 1;
            else if (ch === "[") bracket += 1;
            else if (ch === "]") bracket -= 1;
            else if (ch === delimiter && paren === 0 && bracket === 0) {
                parts.push(text.slice(start, i));
                start = i + 1;
            }
            if (paren < 0 || bracket < 0) {
                throw makeError("E_PATH_SYNTAX", "Unbalanced selector", path, offset + i);
            }
        }
        if (quote || regex || paren !== 0 || bracket !== 0) {
            throw makeError("E_PATH_SYNTAX", "Unterminated selector", path, offset + text.length);
        }
        parts.push(text.slice(start));
        return parts;
    }

    function readBracket(path, start) {
        var quote = "";
        var escaped = false;
        var paren = 0;
        var nested = 0;
        var i, ch;
        for (i = start + 1; i < path.length; i += 1) {
            ch = path.charAt(i);
            if (quote) {
                if (escaped) escaped = false;
                else if (ch === "\\") escaped = true;
                else if (ch === quote) quote = "";
                continue;
            }
            if (ch === "'" || ch === '"') {
                quote = ch;
                continue;
            }
            if (ch === "(") paren += 1;
            else if (ch === ")") paren -= 1;
            else if (ch === "[") nested += 1;
            else if (ch === "]") {
                if (nested > 0) nested -= 1;
                else if (paren === 0) {
                    return { content: path.slice(start + 1, i), end: i };
                }
            }
            if (paren < 0) {
                throw makeError("E_PATH_SYNTAX", "Unbalanced filter expression", path, i);
            }
        }
        throw makeError("E_PATH_SYNTAX", "Unterminated bracket selector", path, start);
    }

    function parseSlice(raw, path, position) {
        var pieces = raw.split(":");
        var values = [];
        var i, text, n;
        if (pieces.length < 2 || pieces.length > 3) {
            throw makeError("E_PATH_SLICE", "Invalid array slice", path, position);
        }
        for (i = 0; i < 3; i += 1) {
            text = i < pieces.length ? trim(pieces[i]) : "";
            if (text === "") values.push(null);
            else {
                if (!/^-?\d+$/.test(text)) {
                    throw makeError("E_PATH_SLICE", "Slice bounds must be integers", path, position);
                }
                n = Number(text);
                if (!isInteger(n)) {
                    throw makeError("E_PATH_SLICE", "Slice bound is outside the supported integer range", path, position);
                }
                values.push(n);
            }
        }
        if (values[2] === 0) {
            throw makeError("E_PATH_SLICE", "Slice step cannot be zero", path, position);
        }
        return { type: "slice", start: values[0], end: values[1], step: values[2] };
    }

    function parseSelector(raw, path, position) {
        raw = trim(raw);
        if (raw === "") throw makeError("E_PATH_SELECTOR", "Empty selector", path, position);
        if (raw === "*") return { type: "wildcard" };
        if (raw.charAt(0) === "'" || raw.charAt(0) === '"') {
            return { type: "child", key: decodeQuoted(raw, path, position) };
        }
        if (/^-?\d+$/.test(raw)) {
            var index = Number(raw);
            if (!isInteger(index)) {
                throw makeError("E_PATH_INDEX", "Array index is outside the supported integer range", path, position);
            }
            return { type: "index", index: index };
        }
        if (raw.indexOf(":") !== -1) return parseSlice(raw, path, position);
        if (/^[A-Za-z_$][A-Za-z0-9_$-]*$/.test(raw)) return { type: "child", key: raw };
        throw makeError("E_PATH_SELECTOR", "Unsupported selector: " + raw, path, position);
    }

    function parseBracketSelector(content, path, position) {
        var text = trim(content);
        if (startsWith(text, "?(") && endsWith(text, ")")) {
            return { type: "filter", expression: parseFilter(text.slice(2, -1), path, position + 2) };
        }
        var parts = splitTopLevel(text, ",", path, position);
        var selectors = [];
        var i;
        for (i = 0; i < parts.length; i += 1) {
            selectors.push(parseSelector(parts[i], path, position));
        }
        return selectors.length === 1 ? selectors[0] : { type: "union", selectors: selectors };
    }

    function parsePath(path, options) {
        if (path && typeof path === "object" && path.__json_query_compiled === true) return path;
        if (typeof path !== "string") throw makeError("E_PATH_TYPE", "Path must be a string");
        var opts = normalizeOptions(options);
        if (path.length > opts.maxPathLength) throw makeError("E_PATH_LIMIT", "Path exceeds maxPathLength", path);
        var cacheKey = "#" + path;
        if (opts.cache && own(pathCache, cacheKey)) return pathCache[cacheKey];

        var tokens = [];
        var i = 0;
        var start, name, bracket, selector, recursive;
        if (path.charAt(0) === "$") i = 1;
        if (i < path.length && path.charAt(i) !== "." && path.charAt(i) !== "[") {
            start = i;
            while (i < path.length && path.charAt(i) !== "." && path.charAt(i) !== "[") i += 1;
            name = path.slice(start, i);
            if (name === "*") tokens.push({ type: "wildcard" });
            else if (name) tokens.push({ type: "child", key: name });
        }

        while (i < path.length) {
            if (tokens.length >= opts.maxTokens) throw makeError("E_TOKEN_LIMIT", "Path exceeds maxTokens", path, i);
            recursive = false;
            if (path.charAt(i) === ".") {
                if (path.charAt(i + 1) === ".") {
                    recursive = true;
                    i += 2;
                } else {
                    i += 1;
                }
                if (i >= path.length) throw makeError("E_PATH_SYNTAX", "Path cannot end with '.'", path, i - 1);
                if (path.charAt(i) === "[") {
                    bracket = readBracket(path, i);
                    selector = parseBracketSelector(bracket.content, path, i + 1);
                    selector.recursive = recursive;
                    tokens.push(selector);
                    i = bracket.end + 1;
                    continue;
                }
                if (path.charAt(i) === "*") {
                    tokens.push({ type: "wildcard", recursive: recursive });
                    i += 1;
                    continue;
                }
                start = i;
                name = "";
                while (i < path.length && path.charAt(i) !== "." && path.charAt(i) !== "[") {
                    if (path.charAt(i) === "\\") {
                        i += 1;
                        if (i >= path.length) throw makeError("E_PATH_ESCAPE", "Unterminated property escape", path, i - 1);
                    }
                    name += path.charAt(i);
                    i += 1;
                }
                if (name === "") throw makeError("E_PATH_SYNTAX", "Empty property selector", path, start);
                tokens.push({ type: "child", key: name, recursive: recursive });
                continue;
            }
            if (path.charAt(i) === "[") {
                bracket = readBracket(path, i);
                selector = parseBracketSelector(bracket.content, path, i + 1);
                tokens.push(selector);
                i = bracket.end + 1;
                continue;
            }
            throw makeError("E_PATH_SYNTAX", "Unexpected character '" + path.charAt(i) + "'", path, i);
        }

        var compiled = { __json_query_compiled: true, source: path, tokens: tokens };
        if (opts.cache) {
            pathCache[cacheKey] = compiled;
            pathCacheOrder.push(cacheKey);
            while (pathCacheOrder.length > pathCacheSize) {
                delete pathCache[pathCacheOrder.shift()];
            }
        }
        return compiled;
    }

    function canonicalKey(key) {
        return "['" + String(key).replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "']";
    }

    function childPath(base, key, arrayIndex) {
        return arrayIndex ? base + "[" + key + "]" : base + canonicalKey(key);
    }

    function sortedKeys(value, deterministic) {
        var keys = Object.keys(value);
        if (deterministic) keys.sort();
        return keys;
    }

    function addResult(out, node, state) {
        state.results += 1;
        if (state.results > state.options.maxResults) {
            throw makeError("E_RESULT_LIMIT", "Query exceeds maxResults", state.path);
        }
        out.push(node);
    }

    function touch(state, depth) {
        state.nodes += 1;
        if (state.nodes > state.options.maxNodes) {
            throw makeError("E_NODE_LIMIT", "Query exceeds maxNodes", state.path);
        }
        if (depth > state.options.maxDepth) {
            throw makeError("E_DEPTH_LIMIT", "Query exceeds maxDepth", state.path);
        }
    }

    function normalizeIndex(index, length) {
        return index < 0 ? length + index : index;
    }

    function sliceIndexes(length, start, end, step) {
        var result = [];
        var i;
        step = step === null ? 1 : step;
        if (step > 0) {
            start = start === null ? 0 : normalizeIndex(start, length);
            end = end === null ? length : normalizeIndex(end, length);
            if (start < 0) start = 0;
            if (start > length) start = length;
            if (end < 0) end = 0;
            if (end > length) end = length;
            for (i = start; i < end; i += step) result.push(i);
        } else {
            start = start === null ? length - 1 : normalizeIndex(start, length);
            end = end === null ? -1 : normalizeIndex(end, length);
            if (start >= length) start = length - 1;
            if (start < -1) start = -1;
            if (end >= length) end = length - 1;
            if (end < -1) end = -1;
            for (i = start; i > end; i += step) result.push(i);
        }
        return result;
    }

    function selectDirect(node, selector, state, depth) {
        var out = [];
        var value = node.value;
        var keys, i, key, idx, indexes, sub;
        touch(state, depth);
        if (selector.type === "child") {
            if (isObject(value) && own(value, selector.key)) {
                addResult(out, { value: value[selector.key], path: childPath(node.path, selector.key, false), parent: value, key: selector.key }, state);
            }
        } else if (selector.type === "index") {
            if (isArray(value)) {
                idx = normalizeIndex(selector.index, value.length);
                if (idx >= 0 && idx < value.length && own(value, idx)) {
                    addResult(out, { value: value[idx], path: childPath(node.path, idx, true), parent: value, key: idx }, state);
                }
            }
        } else if (selector.type === "wildcard") {
            if (isArray(value)) {
                for (i = 0; i < value.length; i += 1) {
                    if (own(value, i)) addResult(out, { value: value[i], path: childPath(node.path, i, true), parent: value, key: i }, state);
                }
            } else if (isObject(value)) {
                keys = sortedKeys(value, state.options.deterministic);
                for (i = 0; i < keys.length; i += 1) {
                    key = keys[i];
                    addResult(out, { value: value[key], path: childPath(node.path, key, false), parent: value, key: key }, state);
                }
            }
        } else if (selector.type === "slice") {
            if (isArray(value)) {
                indexes = sliceIndexes(value.length, selector.start, selector.end, selector.step);
                for (i = 0; i < indexes.length; i += 1) {
                    idx = indexes[i];
                    if (own(value, idx)) addResult(out, { value: value[idx], path: childPath(node.path, idx, true), parent: value, key: idx }, state);
                }
            }
        } else if (selector.type === "union") {
            for (i = 0; i < selector.selectors.length; i += 1) {
                sub = selectDirect(node, selector.selectors[i], state, depth);
                Array.prototype.push.apply(out, sub);
            }
        } else if (selector.type === "filter") {
            if (isArray(value)) {
                for (i = 0; i < value.length; i += 1) {
                    if (own(value, i) && evaluateFilter(selector.expression, value[i], state.root, state)) {
                        addResult(out, { value: value[i], path: childPath(node.path, i, true), parent: value, key: i }, state);
                    }
                }
            } else if (isObject(value)) {
                keys = sortedKeys(value, state.options.deterministic);
                for (i = 0; i < keys.length; i += 1) {
                    key = keys[i];
                    if (evaluateFilter(selector.expression, value[key], state.root, state)) {
                        addResult(out, { value: value[key], path: childPath(node.path, key, false), parent: value, key: key }, state);
                    }
                }
            }
        }
        return out;
    }

    function selectRecursive(node, selector, state, depth, ancestors) {
        var out = [];
        var direct = selectDirect(node, selector, state, depth);
        var value = node.value;
        var keys, i, key, child, nested;
        Array.prototype.push.apply(out, direct);
        if (!isContainer(value)) return out;
        if (state.options.detectCycles && indexOfIdentity(ancestors, value) !== -1) {
            throw makeError("E_CYCLE", "Cyclic object encountered during recursive descent", state.path);
        }
        ancestors.push(value);
        if (isArray(value)) {
            for (i = 0; i < value.length; i += 1) {
                if (!own(value, i)) continue;
                child = { value: value[i], path: childPath(node.path, i, true), parent: value, key: i };
                nested = selectRecursive(child, selector, state, depth + 1, ancestors);
                Array.prototype.push.apply(out, nested);
            }
        } else {
            keys = sortedKeys(value, state.options.deterministic);
            for (i = 0; i < keys.length; i += 1) {
                key = keys[i];
                child = { value: value[key], path: childPath(node.path, key, false), parent: value, key: key };
                nested = selectRecursive(child, selector, state, depth + 1, ancestors);
                Array.prototype.push.apply(out, nested);
            }
        }
        ancestors.pop();
        return out;
    }

    function execute(document, compiled, options, sharedState) {
        var opts = options && options.maxDepth ? options : normalizeOptions(options);
        if (opts.validateDocument) validateJSONValue(document, opts);
        compiled = parsePath(compiled, opts);
        var state = sharedState || { options: opts, nodes: 0, results: 0, root: document, path: compiled.source };
        var current = [{ value: document, path: "$", parent: null, key: null }];
        var next, i, j, selected, token;
        for (i = 0; i < compiled.tokens.length; i += 1) {
            token = compiled.tokens[i];
            next = [];
            for (j = 0; j < current.length; j += 1) {
                if (token.recursive) selected = selectRecursive(current[j], token, state, 0, []);
                else selected = selectDirect(current[j], token, state, 0);
                Array.prototype.push.apply(next, selected);
            }
            current = next;
            if (current.length === 0) break;
        }
        return current;
    }

    function valuesOf(nodes) {
        var out = [];
        var i;
        for (i = 0; i < nodes.length; i += 1) out.push(nodes[i].value);
        return out;
    }

    function all(document, path, options) {
        return valuesOf(execute(document, path, options));
    }

    function nodes(document, path, options) {
        var raw = execute(document, path, options);
        var out = [];
        var i;
        for (i = 0; i < raw.length; i += 1) out.push({ path: raw[i].path, value: raw[i].value });
        return out;
    }

    function first(document, path, defaultValue, options) {
        var result = execute(document, path, options);
        return result.length ? result[0].value : defaultValue;
    }

    function exists(document, path, options) {
        return execute(document, path, options).length > 0;
    }

    function count(document, path, options) {
        return execute(document, path, options).length;
    }

    function paths(document, path, options) {
        var result = execute(document, path, options);
        var out = [];
        var i;
        for (i = 0; i < result.length; i += 1) out.push(result[i].path);
        return out;
    }

    function safeOutputKey(key) {
        return key !== "__proto__" && key !== "prototype" && key !== "constructor";
    }

    function project(document, basePath, mapping, options) {
        if (!isObject(mapping)) throw makeError("E_PROJECT_MAPPING", "project() requires an object mapping");
        var opts = normalizeOptions(options);
        var state = { options: opts, nodes: 0, results: 0, root: document, path: String(basePath) };
        var bases = execute(document, basePath, opts, state);
        var out = [];
        var mapKeys = Object.keys(mapping);
        if (mapKeys.length > opts.maxTokens) {
            throw makeError("E_PROJECT_LIMIT", "Projection exceeds maxTokens fields");
        }
        var i, j, spec, relativePath, mode, values, row, key, defaultValue, required;
        for (i = 0; i < bases.length; i += 1) {
            row = {};
            for (j = 0; j < mapKeys.length; j += 1) {
                key = mapKeys[j];
                if (!safeOutputKey(key)) throw makeError("E_PROJECT_KEY", "Unsafe projection key: " + key);
                spec = mapping[key];
                mode = "first";
                defaultValue = null;
                required = false;
                if (typeof spec === "string") relativePath = spec;
                else if (isObject(spec)) {
                    relativePath = spec.path;
                    mode = spec.mode || "first";
                    if (own(spec, "default")) defaultValue = spec["default"];
                    required = spec.required === true;
                } else {
                    throw makeError("E_PROJECT_SPEC", "Projection field '" + key + "' must be a path string or specification object");
                }
                if (typeof relativePath !== "string") throw makeError("E_PROJECT_PATH", "Projection field '" + key + "' has no valid path");
                if (relativePath === "@") relativePath = "$";
                else if (startsWith(relativePath, "@.")) relativePath = relativePath.slice(1);
                state.path = relativePath;
                if (startsWith(relativePath, "$")) values = valuesOf(execute(document, relativePath, opts, state));
                else values = valuesOf(execute(bases[i].value, relativePath, opts, state));
                if (required && values.length === 0) throw makeError("E_PROJECT_REQUIRED", "Required projection field not found: " + key);
                if (mode === "all") row[key] = values;
                else if (mode === "count") row[key] = values.length;
                else if (mode === "exists") row[key] = values.length > 0;
                else if (mode === "first") row[key] = values.length ? values[0] : defaultValue;
                else throw makeError("E_PROJECT_MODE", "Unsupported projection mode: " + mode);
            }
            out.push(row);
        }
        return out;
    }

    function queryJSON(text, path, options) {
        return all(parseJSON(text), path, options);
    }

    function clearCache() {
        pathCache = {};
        pathCacheOrder = [];
    }

    function setCacheSize(size) {
        size = Number(size);
        if (!isInteger(size) || size < 0) throw makeError("E_CACHE_SIZE", "Cache size must be a non-negative integer");
        pathCacheSize = size;
        while (pathCacheOrder.length > pathCacheSize) delete pathCache[pathCacheOrder.shift()];
        if (pathCacheSize === 0) clearCache();
    }

    function cacheStats() {
        return { size: pathCacheOrder.length, capacity: pathCacheSize };
    }

    /* Filter expression parser and evaluator. */
    function tokenizeFilter(source, path, offset) {
        var tokens = [];
        var i = 0;
        var ch, start, quote, escaped, raw, flags, pattern;
        function push(type, value, pos) { tokens.push({ type: type, value: value, position: pos }); }
        while (i < source.length) {
            ch = source.charAt(i);
            if (/\s/.test(ch)) { i += 1; continue; }
            if (ch === "'" || ch === '"') {
                quote = ch; start = i; i += 1; escaped = false;
                while (i < source.length) {
                    ch = source.charAt(i);
                    if (escaped) escaped = false;
                    else if (ch === "\\") escaped = true;
                    else if (ch === quote) break;
                    i += 1;
                }
                if (i >= source.length) throw makeError("E_FILTER_STRING", "Unterminated string literal", path, offset + start);
                raw = source.slice(start, i + 1);
                push("literal", decodeQuoted(raw, path, offset + start), start);
                i += 1; continue;
            }
            if (ch === "/") {
                start = i; i += 1; escaped = false; pattern = "";
                while (i < source.length) {
                    ch = source.charAt(i);
                    if (escaped) { pattern += "\\" + ch; escaped = false; i += 1; continue; }
                    if (ch === "\\") { escaped = true; i += 1; continue; }
                    if (ch === "/") break;
                    pattern += ch; i += 1;
                }
                if (i >= source.length) throw makeError("E_FILTER_REGEX", "Unterminated regular expression", path, offset + start);
                i += 1; flags = "";
                while (i < source.length && /[gim]/.test(source.charAt(i))) { flags += source.charAt(i); i += 1; }
                flags = flags.replace(/g/g, "");
                try { push("regex", new RegExp(pattern, flags), start); }
                catch (err) { throw makeError("E_FILTER_REGEX", "Invalid regular expression: " + err.message, path, offset + start); }
                continue;
            }
            if (ch === "@" || ch === "$") {
                start = i; i += 1;
                while (i < source.length) {
                    ch = source.charAt(i);
                    if (/\s/.test(ch) || /[=!<>~&|(),]/.test(ch)) break;
                    if (ch === "[") {
                        var b = readBracket(source, i);
                        i = b.end + 1;
                    } else i += 1;
                }
                push("path", source.slice(start, i), start);
                continue;
            }
            if (/[0-9-]/.test(ch) && /-?\d/.test(source.slice(i, i + 2))) {
                start = i;
                var numberMatch = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(source.slice(i));
                if (!numberMatch) throw makeError("E_FILTER_NUMBER", "Invalid number literal", path, offset + i);
                i += numberMatch[0].length;
                push("literal", Number(numberMatch[0]), start);
                continue;
            }
            var op3 = source.slice(i, i + 3);
            var op2 = source.slice(i, i + 2);
            if (op3 === "===" || op3 === "!==") { push("operator", op3, i); i += 3; continue; }
            if (op2 === "&&" || op2 === "||" || op2 === "==" || op2 === "!=" || op2 === "<=" || op2 === ">=" || op2 === "=~" || op2 === "!~") {
                push("operator", op2, i); i += 2; continue;
            }
            if (ch === "!" || ch === "<" || ch === ">" || ch === "(" || ch === ")" || ch === ",") {
                push(ch === "(" || ch === ")" || ch === "," ? ch : "operator", ch, i); i += 1; continue;
            }
            if (/[A-Za-z_]/.test(ch)) {
                start = i; i += 1;
                while (i < source.length && /[A-Za-z0-9_]/.test(source.charAt(i))) i += 1;
                raw = source.slice(start, i);
                if (raw === "true") push("literal", true, start);
                else if (raw === "false") push("literal", false, start);
                else if (raw === "null") push("literal", null, start);
                else push("identifier", raw, start);
                continue;
            }
            throw makeError("E_FILTER_TOKEN", "Unexpected filter character '" + ch + "'", path, offset + i);
        }
        push("eof", null, source.length);
        return tokens;
    }

    function parseFilter(source, path, offset) {
        var tokens = tokenizeFilter(source, path, offset);
        var index = 0;
        function peek(type, value) {
            var t = tokens[index];
            return t.type === type && (arguments.length < 2 || t.value === value);
        }
        function take(type, value) {
            var t = tokens[index];
            var matches = arguments.length < 2 ? peek(type) : peek(type, value);
            if (!matches) throw makeError("E_FILTER_SYNTAX", "Unexpected token in filter", path, offset + t.position);
            index += 1;
            return t;
        }
        function primary() {
            var t, args;
            if (peek("literal")) { t = take("literal"); return { kind: "literal", value: t.value }; }
            if (peek("regex")) { t = take("regex"); return { kind: "literal", value: t.value }; }
            if (peek("path")) { t = take("path"); return { kind: "path", value: t.value }; }
            if (peek("identifier")) {
                t = take("identifier");
                if (!peek("(")) throw makeError("E_FILTER_SYNTAX", "Unknown identifier: " + t.value, path, offset + t.position);
                take("("); args = [];
                if (!peek(")")) {
                    do { args.push(orExpr()); if (!peek(",")) break; take(","); } while (true);
                }
                take(")");
                return { kind: "call", name: t.value, args: args };
            }
            if (peek("(")) { take("("); var expr = orExpr(); take(")"); return expr; }
            t = tokens[index];
            throw makeError("E_FILTER_SYNTAX", "Expected filter operand", path, offset + t.position);
        }
        function unary() {
            if (peek("operator", "!")) { take("operator", "!"); return { kind: "unary", op: "!", value: unary() }; }
            return primary();
        }
        function comparison() {
            var left = unary();
            if (peek("operator") && /^(===|!==|==|!=|<=|>=|<|>|=~|!~)$/.test(tokens[index].value)) {
                var op = take("operator").value;
                return { kind: "binary", op: op, left: left, right: unary() };
            }
            return left;
        }
        function andExpr() {
            var left = comparison();
            while (peek("operator", "&&")) { take("operator", "&&"); left = { kind: "binary", op: "&&", left: left, right: comparison() }; }
            return left;
        }
        function orExpr() {
            var left = andExpr();
            while (peek("operator", "||")) { take("operator", "||"); left = { kind: "binary", op: "||", left: left, right: andExpr() }; }
            return left;
        }
        var result = orExpr();
        take("eof");
        return result;
    }

    function filterPathValues(path, current, root, state) {
        var base, relative;
        if (path.charAt(0) === "@") {
            base = current;
            relative = path.slice(1);
            if (relative.charAt(0) === ".") relative = relative.slice(1);
        } else {
            base = root;
            relative = path;
        }
        if (relative === "" || relative === "$") return [base];
        if (path.charAt(0) === "@" && relative.charAt(0) !== "[") relative = relative;
        var nestedOptions = {
            maxDepth: state.options.maxDepth,
            maxNodes: state.options.maxNodes,
            maxResults: state.options.maxResults,
            maxPathLength: state.options.maxPathLength,
            maxTokens: state.options.maxTokens,
            deterministic: state.options.deterministic,
            detectCycles: state.options.detectCycles,
            validateDocument: false,
            cache: state.options.cache
        };
        return valuesOf(execute(base, relative, nestedOptions, state));
    }

    function asList(value) {
        return value && value.__filter_values === true ? value.values : [value];
    }

    function deepEqual(a, b, stackA, stackB) {
        var typeA = valueType(a), typeB = valueType(b);
        var keysA, keysB, i;
        if (typeA !== typeB) return false;
        if (a === b) return true;
        if (typeA === "number") return isNaN(a) && isNaN(b);
        if (typeA !== "array" && typeA !== "object") return false;
        stackA = stackA || []; stackB = stackB || [];
        i = indexOfIdentity(stackA, a);
        if (i !== -1) return stackB[i] === b;
        stackA.push(a); stackB.push(b);
        if (typeA === "array") {
            if (a.length !== b.length) return false;
            for (i = 0; i < a.length; i += 1) if (!deepEqual(a[i], b[i], stackA, stackB)) return false;
        } else {
            keysA = Object.keys(a).sort(); keysB = Object.keys(b).sort();
            if (keysA.length !== keysB.length) return false;
            for (i = 0; i < keysA.length; i += 1) {
                if (keysA[i] !== keysB[i] || !deepEqual(a[keysA[i]], b[keysB[i]], stackA, stackB)) return false;
            }
        }
        stackA.pop(); stackB.pop();
        return true;
    }

    function scalarCompare(left, right, op) {
        if (op === "==" || op === "===") return deepEqual(left, right);
        if (op === "!=" || op === "!==") return !deepEqual(left, right);
        if (op === "=~" || op === "!~") {
            var regex = right instanceof RegExp ? right : new RegExp(String(right));
            regex.lastIndex = 0;
            var matched = regex.test(String(left));
            return op === "=~" ? matched : !matched;
        }
        if ((typeof left !== "number" && typeof left !== "string") || (typeof right !== "number" && typeof right !== "string")) return false;
        if (op === "<") return left < right;
        if (op === "<=") return left <= right;
        if (op === ">") return left > right;
        if (op === ">=") return left >= right;
        return false;
    }

    function truthy(value) {
        if (value && value.__filter_values === true) {
            if (value.values.length === 0) return false;
            var i;
            for (i = 0; i < value.values.length; i += 1) if (truthy(value.values[i])) return true;
            return false;
        }
        return !!value;
    }

    function evaluateCall(name, args) {
        var list, value, needle, i;
        if (name === "exists") {
            list = asList(args[0]); return list.length > 0;
        }
        if (name === "length") {
            list = asList(args[0]);
            if (args[0] && args[0].__filter_values === true && list.length !== 1) return list.length;
            value = list.length ? list[0] : null;
            if (typeof value === "string" || isArray(value)) return value.length;
            if (isObject(value)) return Object.keys(value).length;
            return 0;
        }
        if (name === "type") {
            list = asList(args[0]); return list.length ? valueType(list[0]) : "undefined";
        }
        if (name === "contains") {
            list = asList(args[0]); needle = asList(args[1])[0];
            for (i = 0; i < list.length; i += 1) {
                value = list[i];
                if (typeof value === "string" && value.indexOf(String(needle)) !== -1) return true;
                if (isArray(value)) {
                    var j; for (j = 0; j < value.length; j += 1) if (deepEqual(value[j], needle)) return true;
                }
                if (isObject(value) && own(value, String(needle))) return true;
            }
            return false;
        }
        if (name === "startsWith" || name === "endsWith") {
            list = asList(args[0]); needle = asList(args[1])[0];
            for (i = 0; i < list.length; i += 1) {
                if (typeof list[i] === "string" && (name === "startsWith" ? startsWith(list[i], needle) : endsWith(list[i], needle))) return true;
            }
            return false;
        }
        if (name === "match") {
            list = asList(args[0]); needle = asList(args[1])[0];
            var re = needle instanceof RegExp ? needle : new RegExp(String(needle));
            for (i = 0; i < list.length; i += 1) { re.lastIndex = 0; if (re.test(String(list[i]))) return true; }
            return false;
        }
        throw makeError("E_FILTER_FUNCTION", "Unsupported filter function: " + name);
    }

    function evaluateFilterAst(ast, current, root, state) {
        var left, right, leftList, rightList, i, j, args;
        if (ast.kind === "literal") return ast.value;
        if (ast.kind === "path") return { __filter_values: true, values: filterPathValues(ast.value, current, root, state) };
        if (ast.kind === "unary") return !truthy(evaluateFilterAst(ast.value, current, root, state));
        if (ast.kind === "call") {
            args = [];
            for (i = 0; i < ast.args.length; i += 1) args.push(evaluateFilterAst(ast.args[i], current, root, state));
            return evaluateCall(ast.name, args);
        }
        if (ast.kind === "binary") {
            if (ast.op === "&&") {
                left = evaluateFilterAst(ast.left, current, root, state);
                return truthy(left) && truthy(evaluateFilterAst(ast.right, current, root, state));
            }
            if (ast.op === "||") {
                left = evaluateFilterAst(ast.left, current, root, state);
                return truthy(left) || truthy(evaluateFilterAst(ast.right, current, root, state));
            }
            left = evaluateFilterAst(ast.left, current, root, state);
            right = evaluateFilterAst(ast.right, current, root, state);
            leftList = asList(left); rightList = asList(right);
            for (i = 0; i < leftList.length; i += 1) {
                for (j = 0; j < rightList.length; j += 1) {
                    if (scalarCompare(leftList[i], rightList[j], ast.op)) return true;
                }
            }
            return false;
        }
        return false;
    }

    function evaluateFilter(ast, current, root, state) {
        return truthy(evaluateFilterAst(ast, current, root, state));
    }

    return {
        version: VERSION,
        parse: parseJSON,
        validate: validateJSONValue,
        compile: parsePath,
        all: all,
        query: all,
        queryJSON: queryJSON,
        first: first,
        exists: exists,
        count: count,
        nodes: nodes,
        paths: paths,
        project: project,
        typeOf: valueType,
        clearCache: clearCache,
        setCacheSize: setCacheSize,
        cacheStats: cacheStats
    };
})();

// Export library for include("json_query") and plugin unit tests.
var result = json_query;

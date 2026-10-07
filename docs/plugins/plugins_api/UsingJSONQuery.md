# Using the `json_query` library plugin

`json_query` is a dependency-free, ES5-compatible library plugin for selecting and projecting data from JSON documents inside CROWler engine, API, event, and library plugins.

It accepts every valid JSON document root:

- object
- array
- string
- number
- boolean
- `null`

The query functions operate on native JavaScript values. Use `parse()` or `queryJSON()` when the input is JSON text.

## Loading the library

```javascript
var jq = include("json_query");
if (!jq || jq.error) {
    throw new Error("Unable to include json_query");
}
```

A scraper post-processing engine plugin can query the collected document through `params.json_data`:

```javascript
var jq = include("json_query");

var emails = jq.all(
    params.json_data,
    "data.users[*].profile.email"
);

result = {
    emails: emails
};
```

The leading `$` is optional. These paths are equivalent:

```text
$.data.users[*].profile.email
data.users[*].profile.email
```

This keeps paths compatible with the format used by CROWler attribute indexing.

## Selector grammar

| Selector | Meaning |
| --- | --- |
| `$` | Document root |
| `.name` | Child property |
| `['name']`, `["name"]` | Quoted child property |
| `*`, `[*]` | Every direct child |
| `[0]` | Array element by index |
| `[-1]` | Array element relative to the end |
| `[start:end:step]` | Array slice |
| `[0,2,'name']` | Union of selectors |
| `..name` | Recursively select a property |
| `..*` | Recursively select every descendant |
| `[?(expression)]` | Filter array elements or object values |

Use quoted selectors for property names containing dots, brackets, quotes, whitespace, or an empty string:

```javascript
jq.first(document, "payload['property.with.dots']['x[y]']", null);
jq.first(document, "payload['']", null);
```

## Core API

### `parse(text)`

Strictly parses JSON text and returns its native JavaScript value. Invalid JSON throws a `JSONQueryError` with code `E_JSON_PARSE`.

```javascript
var document = jq.parse('[{"id":1},{"id":2}]');
```

### `all(document, path, options)`

Returns all matching values in query order. `query()` is an alias.

```javascript
var ids = jq.all(document, "[*].id");
```

A missing path returns an empty array. It is not an error.

### `first(document, path, defaultValue, options)`

Returns the first match, or `defaultValue` when no node matches.

```javascript
var username = jq.first(document, "user.username", null);
```

### `exists(document, path, options)`

Returns `true` when at least one node matches. A matching value of `null`, `false`, `0`, or an empty string still counts as an existing node.

### `count(document, path, options)`

Returns the number of matching nodes.

### `nodes(document, path, options)`

Returns both canonical paths and values:

```javascript
var matches = jq.nodes(document, "users[*].email");
// [{ path: "$['users'][0]['email']", value: "..." }, ...]
```

### `paths(document, path, options)`

Returns only the canonical paths of matching nodes.

### `compile(path, options)`

Parses and caches a path for repeated use.

```javascript
var emailPath = jq.compile("users[*].email");
var firstRun = jq.all(documentA, emailPath);
var secondRun = jq.all(documentB, emailPath);
```

### `queryJSON(text, path, options)`

Parses JSON text, then returns all path matches.

```javascript
var values = jq.queryJSON(xhrBody, "data.items[*].value");
```

### `validate(document, options)`

Validates that a native JavaScript value is representable as JSON. It rejects unsupported values, non-finite numbers, sparse arrays, excessive depth, excessive node counts, and cycles.

### `typeOf(value)`

Returns one of `object`, `array`, `string`, `number`, `boolean`, `null`, or the underlying JavaScript type for unsupported values.

## Record projection

`project()` selects base records and extracts a stable output shape from each record.

```javascript
var records = jq.project(
    params.json_data,
    "data.posts[*]",
    {
        id: "id",
        username: "author.username",
        text: "content.text",
        likes: { path: "metrics.like_count", "default": 0 },
        tags: { path: "tags[*]", mode: "all" },
        tag_count: { path: "tags[*]", mode: "count" },
        has_location: { path: "location", mode: "exists" },
        source_name: "$.meta.source_name"
    }
);

result = { records: records };
```

Each mapping value may be a path string or an object:

```javascript
{
    path: "metrics.like_count",
    mode: "first",
    "default": 0,
    required: false
}
```

Supported modes:

| Mode | Result |
| --- | --- |
| `first` | First match or the configured default |
| `all` | Array containing every match |
| `count` | Number of matches |
| `exists` | Boolean indicating whether a node matched |

Relative paths are evaluated from each base node. Paths beginning with `$` are evaluated from the original document root. Paths beginning with `@` are accepted as relative paths.

Projection rejects the output keys `__proto__`, `prototype`, and `constructor` to prevent prototype pollution.

## Filters

Filters do not use `eval`. They are parsed by the library.

```javascript
var active = jq.all(
    document,
    "users[?(@.active == true && @.score >= 0.8)]"
);
```

Supported operators:

```text
&&  ||  !
==  !=  ===  !==
<   <=  >   >=
=~  !~
```

Regular expressions use ES5 flags supported by the CROWler JavaScript runtime:

```javascript
jq.all(document, "users[?(@.email =~ /@example\\.com$/i)]");
```

Filter paths may reference the candidate node with `@` and the original document with `$`:

```javascript
jq.all(document, "rows[?(@.score >= $.minimum_score)]");
```

Supported filter functions:

| Function | Meaning |
| --- | --- |
| `exists(valueOrPath)` | Whether an expression resolves to at least one value |
| `length(valueOrPath)` | String length, array length, object key count, or path result count |
| `type(valueOrPath)` | JSON value type |
| `contains(container, value)` | String substring, array member, or object key check |
| `startsWith(string, prefix)` | Prefix check |
| `endsWith(string, suffix)` | Suffix check |
| `match(string, regex)` | Regular expression check |

Use `exists(@.field)` when node existence must be distinguished from JavaScript truthiness.

## Array slices

Slices support omitted and negative bounds:

```javascript
jq.all(document, "items[1:5]");
jq.all(document, "items[::2]");
jq.all(document, "items[::-1]");
```

A step of zero is invalid.

## Safety limits

Every query accepts an optional limits object:

```javascript
var values = jq.all(document, "..*", {
    maxDepth: 128,
    maxNodes: 250000,
    maxResults: 25000,
    maxPathLength: 4096,
    maxTokens: 256,
    deterministic: true,
    detectCycles: true,
    validateDocument: false,
    cache: true
});
```

Defaults are deliberately bounded:

| Option | Default |
| --- | ---: |
| `maxDepth` | 256 |
| `maxNodes` | 1,000,000 |
| `maxResults` | 100,000 |
| `maxPathLength` | 16,384 |
| `maxTokens` | 1,024 |

Object wildcard traversal is deterministic by default and sorts property names. Array order is always preserved.

Native objects may contain cycles even though JSON cannot. Recursive descent and `validate()` detect cycles by default.

## Cache controls

Compiled paths use a bounded in-memory cache.

```javascript
jq.setCacheSize(512);
var stats = jq.cacheStats();
jq.clearCache();
```

Set the cache size to `0` to disable retained entries. A query may also set `cache: false`.

## Error handling

Invalid JSON, malformed paths, malformed filters, unsafe projections, cycles, and limit violations throw `JSONQueryError`.

```javascript
try {
    var values = jq.all(document, "items[::0]");
} catch (err) {
    console.log(err.code + ": " + err.message);
}
```

Common error codes include:

- `E_JSON_PARSE`
- `E_JSON_VALUE`
- `E_PATH_SYNTAX`
- `E_PATH_SLICE`
- `E_FILTER_SYNTAX`
- `E_CYCLE`
- `E_DEPTH_LIMIT`
- `E_NODE_LIMIT`
- `E_RESULT_LIMIT`
- `E_PROJECT_REQUIRED`

## Numeric precision

JSON numbers are represented by the CROWler JavaScript runtime as JavaScript numbers. Integer values beyond the runtime's exact integer range may lose precision. Preserve such identifiers as JSON strings when exact decimal representation is required.

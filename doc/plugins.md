# CROWler's Plugin

The CROWler supports plugins to extend its functionalities, enabling a wide range of tasks. These include enhancing the effectiveness of Action Rules, scraping data as a selector and/or processing it in the post_processing phase of a scraping rule, waiting for elements to render on a webpage, and much more.

Plugins are the primary means by which users can customize and adapt the CROWler to meet their specific needs. The combination of its highly customizable architecture, powerful ruleset framework, and plugin support makes the CROWler a true Content Discovery Development Platform.

## Writing Plugins for the CROWler

The CROWler plugin architecture leverages JavaScript to allow users to write powerful plugins that can interact with a web page (or an API) or perform data analysis and transformation (ETL) tasks on collected data. This guide explains the structure of a plugin, how to use the custom API functions for engine's plugins, and best practices to build both robust ETL pipelines and effective data collection plugins.

---

## Plugin Structure

A plugin is a JavaScript file that must start with a set of header comments to define its metadata. These headers should include:

- **Plugin Name:** Indicated by `// @name:`
- **Description:** Indicated by `// @description:`
- **Plugin Type:** Indicated by `// @type:`
  (Valid values include: `engine_plugin`, `vdi_plugin`, `api_plugin`, `lib_plugin` or `event_plugin`. There is also a special type of plugin called `test_plugin` used for unit testing purposes.)
- **Version (optional):** Indicated by `// @version:`
- **Event Type (optional):** Indicated by `// @event_type:`

Some plugin types may require additional headers based on their functionality.

### Example Plugin Header

```js
// @name: sample_transform_plugin
// @description: Transforms contacts data by adding greetings and exporting as CSV.
// @type: engine_plugin
// @version: 1.0.0
// @event_type: crawl_completed
```

Note: The `@event_type` header is only required for `engine_plugin` type plugins and specifies the event that triggers the plugin execution. The events types can be decided by the user, so they can be customized to fit the specific needs. There are some pre-defined event types like:

- `crawl_started`: Triggered when the crawling process for a given Source starts.
- `crawl_completed`: Triggered when the crawling process for a given Source is completed.
- `started_batch_crawling`: Triggered when a batch crawling process starts.
- `completed_batch_crawling`: Triggered when a batch crawling process completes.
- `crowler_heartbeat`: Triggered periodically as a heartbeat signal from the CROWler.
- `crowler_heartbeat_response`: Triggered in response to a heartbeat signal.
- `vdi_failed_to_get_url`: Triggered when the Virtual Desktop Image (VDI) fails to retrieve a URL.
- `system_event`: Triggered when a system event occurs (e.g., a new Source is added).

---

## Plugin Types

The CROWler supports three types of plugins:

1. **Engine Plugins:**
   These plugins are executed by the CROWler engine and can interact with the collected data. They can be triggered by a rule in a post_processing section, by the Agents Engine (either traditional agents or Agentic AI), or by specific events like `crawl_completed`, `system_event`, etc. (in this case, the `@event_type` header is required and they must be declared as `event_plugin`).

   Engine's plugins have to be written in ES5.1 JavaScript and should not use any external libraries or Node.js modules. They can use the provided API functions to interact with the collected data. More info on this extended API can be found in the next section.

   Minimalistic example of Engine's plugin:

   ```js
    // name: greetings_plugin
    // type: engine_plugin

    // Let's assume you want to manipulate or use the data somehow
    function processData(data) {
        // Example manipulation: create a greeting message
        return "Hello, " + data.name + " from " + data.city + "!";
    }

    // Call processData with the parsed object
    var result = processData(params.json_data);
    result; // This will be the return value to the CROWler
    ```

2. **VDI Plugins:**
    These plugins are executed by the Virtual Data Integrator (VDI) and can be used to interact with a Web Page or an API being crawled. VDI plugins can be written in JavaScript ES5.1 or ES6. They have access to the Browser DOM and APIs to interact with the web page.

    Minimalistic example of VDI plugin:

    ```js
    // name: detect_dynamic_inputs
    // description: Plugin to detect dynamically added input fields
    // type: vdi_plugin

    Return (function() { var inputFields = [];
    var observer = new MutationObserver(function(mutations){
        mutations.forEach(function(mutation) {
        if (mutation.addedNodes && mutation.addedNodes.length > 0) {
            mutation.addedNodes.forEach(function(node) {
            if (node.tagName === 'INPUT') {
                inputFields.push(node);
            }
            });
        }
        });
    });
    observer.observe(document.body, { childList: true, subtree: true });

    // Wait for 5 seconds to allow detection
    setTimeout(function() {
        observer.disconnect();
        var result = { detected: inputFields.length > 0, count: inputFields.length };
        return JSON.stringify(result); }, 5000); // Timeout is set to 5000 milliseconds (5 seconds)
    })();
    ```

3. **Event Plugins:**
    These plugins are executed by the CROWler engine and can be triggered by specific events like `crawl_completed`, `system_event`, etc. Currently, event plugins must be written in JavaScript ES5.1. They have access to the CROWler's internal APIs and can interact with the collected data.

    Minimalistic example of Event's plugin:

    ```js
    // name: sample_event_plugin
    // description: Sample event plugin to log a message
    // type: event_plugin
    // event_type: crawl_completed

    // Let's assume you want to manipulate or use the data somehow
    function processData(data) {
        // Example manipulation: create a greeting message
        return "Hello, " + data.name + " from " + data.city + "!";
    }

    // Call processData with the parsed object
    var result = processData(params.json_data);
    result; // This will be the return value to the CROWler
    ```

## The Engine and Event Plugin's API

CROWler exposes a rich set of helper functions in the JS environment for both the engine's plugins and the event plugins to facilitate ETL tasks. All data is normalized to JSON, so plugins always work with JSON documents. Key helper functions include:

- **Common Functions:**
  - `console.log(message)`: Log a message to the console.
  - `console.error(message)`: Log an error message to the console.
  - `console.warn(message)`: Log a warning message to the console.
  - `getDebugLevel`: To get the current debug level set for the CROWler.

- **Crypto Functions:**
  - `generateHash(data, algorithm)`: Generate a hash of the data using the specified algorithm (e.g., "sha256").

- **HTTP Functions:**
  - `apiClient`: A helper object to make HTTP requests.
  - `fetch`: A wrapper around the `fetch` API to make HTTP requests.
  - `httpRequest`: Make an HTTP request using the provided options.

- **Database Functions:**
  - `dbQuery(config, query)`: Execute queries on the internal CROWler database.
  - `externalDBQuery(config, query)`: Query external databases (PostgreSQL, MySQL, SQLite, MongoDB, Neo4J).

- **Data Conversion:**
  - `tableToJSON(data)`: Convert table-like results (array of objects) into a JSON string.
  - `jsonToCSV(data)`: Convert an array of objects into a CSV string.
  - `csvToJSON(csvString)`: Convert CSV data to a JSON array.
  - `xmlToJSON(xmlString)`: Convert XML data into a JSON object.
  - `jsonToXML(jsonObject)`: Convert a JSON object into an XML string.

- **Data Transformation:**
  - `filterJSON(document, keys)`: Return a new JSON object containing only the specified keys.
  - `mapJSON(array, callback)`: Map a callback function over each element in a JSON array.
  - `reduceJSON(array, callback, initial)`: Reduce a JSON array into a single value using a callback.
  - `joinJSON(leftArray, rightArray, joinKey)`: Join two JSON arrays on a common key.
  - `sortJSON(array, key, order)`: Sort a JSON array of objects by a given key (order: "asc" or "desc").
  - `pipeJSON(initialValue, [callback1, callback2, ...])`: Compose a sequence of transformation callbacks that are applied in order.

These functions ensure that regardless of the data source or format, plugins can perform consistent and reliable ETL operations.

---

## Writing a Plugin

A plugin should be self-contained and use the provided API functions to fetch, transform, and output data. Plugins run within the CROWler Agents engine and receive input via the global `params` object. The plugin is expected to place its final result into a global variable named `result` or return a value directly.

Plugins will ALWAYS receive parameters specified in a rule, or in an agent configuration via the `params` object. The `params` object will contain the passed parameters as properties. For example, if a rule passes the parameters `url` and `selector`, the `params` object will have the following structure:

```js
var url = params.url;
var selector = params.selector;
```

On top of that the current processed JSON document for the specific pipeline will be available in the `params` object as `json_data`. Already-collected XHR entries (if XHR collection has been enabled in the global configuration) are exposed separately as `params.xhr` rather than inside `params.json_data`, so engine plugins and agents can inspect those immutable captures without returning them as modified scraped data. And if there are pre-defined metadata for the pipeline, it will be available in the `params` object as `meta_data`.

### Example Plugin: Data Transformer

Below is an example plugin that demonstrates how to:

- Add a new field to each contact (using `mapJSON`)
- Filter the JSON object to only include selected fields (using `filterJSON`)
- Convert the filtered data to CSV (using `jsonToCSV`)

```js
// @name: sample_transform_plugin
// @description: Transforms contacts data by adding greetings and exporting as CSV.
// @type: engine_plugin
// @version: 1.0.0
// @event_type: crawl_completed

// Assume 'params' is an object provided by the CROWler engine containing collected data.
var contacts = params.contacts; // Expected to be an array of contact objects

// 1. Add a greeting to each contact.
var transformed = mapJSON(contacts, function(contact) {
  contact.greeting = "Hello " + contact.first_name + " " + contact.last_name + "!";
  return contact;
});

// 2. Filter the transformed data to only include selected keys.
var filtered = filterJSON(transformed, ["id", "first_name", "last_name", "greeting"]);

// 3. Convert the filtered data to CSV.
var csvExport = toCSV(filtered);

// 4. Set the final result.
result = {
  transformedData: transformed,
  csvExport: csvExport
};
```

---


### Information Seed discovery metadata

Plugins that discover candidate sources for an Information Seed should keep
configuration and provenance separate. Put source-wide crawling behavior in
`Sources.config`: rulesets, execution-plan options, restrictions, scheduling,
and plugin parameters that should apply every time the source is crawled. Put
per-seed discovery evidence on the `SourceInformationSeedIndex` relationship:
`discovery_provider`, `discovery_query`, `discovery_rank`, `candidate_score`,
`candidate_reason`, and any provider-specific `discovery_metadata` JSON.

This separation is important because the same URL can be discovered by multiple
seeds. Updating relationship metadata for one `(source_id, information_seed_id)`
pair must not rewrite `Sources.config` or any other source/seed relationship.
When using database helpers or `dbQuery`, prefer an idempotent link for plain
associations and the richer metadata upsert only when the plugin has discovery
evidence to record.

### Information Seed candidate plugins

Information Seed candidate plugins are engine plugins registered with
`event_type: information_seed_candidate`. They run after provider discovery,
URL normalization, de-duplication, and candidate limiting, and before Sources
are persisted. An empty candidate plugin chain is a pass-through: normalized
candidates are persisted with the proposed source defaults.

Each plugin receives a strict JSON input through `params`:

* `seed`: the seed object (`id`, `information_seed`, `category_id`, `usr_id`,
  `status`, and `priority`).
* `candidate`: the normalized candidate object (`url`, `host`, `title`,
  `provider`, `query`, `rank`, `score`, `reason`, and provider metadata).
* `metadata`: provider/query/rank metadata copied from the candidate for easy
  policy checks.
* `source_defaults`: the Source values the runner proposes to create when the
  candidate is accepted (`name`, `priority`, `category_id`, `usr_id`,
  `restricted`, `flags`, and optional `source_config`).

The plugin output is schema validated and must be an object with:

* `accepted` *(boolean, required)*: `false` rejects the candidate without
  failing the whole seed when the output is otherwise valid.
* `score` *(number, required)*: the final candidate score persisted in
  discovery metadata.
* `reason` *(string, required)*: the explanation persisted as the candidate
  reason.
* `source_overrides` *(object, optional)*: safe per-candidate overrides. Only
  `name`, `priority`, `restricted`, `flags`, and `source_config` are accepted;
  plugins cannot override URL, category, user ownership, or seed linkage.
* `tags` *(array of strings, optional)* and `metadata` *(object, optional)*:
  custom metadata persisted with discovery metadata under `tags` and
  `plugin_metadata`.

```js
// @name: seed_candidate_quality_gate
// @description: Accepts or rejects information-seed candidates.
// @type: engine_plugin
// @event_type: information_seed_candidate
// @version: 1.0.0

var candidate = params.candidate;
var defaults = params.source_defaults;
var trusted = candidate.host.endsWith(".example.com");

result = {
  accepted: trusted,
  score: trusted ? 0.92 : 0.1,
  reason: trusted ? "trusted example.com host" : "host outside trusted scope",
  source_overrides: trusted ? {
    name: defaults.name || candidate.title || candidate.host,
    priority: "normal",
    restricted: 1,
    source_config: {
      version: "1.0",
      format_version: "1.0",
      source_name: "information-seed-candidate",
      crawling_config: {
        site: candidate.url,
        source_type: "website"
      },
      custom: {
        configured_by: "seed_candidate_quality_gate"
      }
    }
  } : undefined,
  tags: trusted ? ["trusted-host"] : [],
  metadata: { provider_rank: params.metadata.rank }
};
```


There are two supported places to provide source crawler configuration:

1. Put a default `SourceConfig` in the seed request at
   `config.source_config`. Every accepted candidate inherits that object unless
   a plugin overrides it.
2. Put a complete candidate-specific `SourceConfig` in plugin output at
   `source_overrides.source_config`, as shown above. Use this form when
   `crawling_config.site`, rules, execution plans, or custom values depend on
   `params.candidate`.

The override is a full replacement, not a deep merge. Both forms are validated
with the normal Source configuration validator before `Sources.config` is
created or updated.

Information Seed plugin execution is bounded by `information_seed.plugin_limits`:
`timeout` enforces per-plugin runtime and `max_output_size_bytes` rejects overly
large JSON output. Malformed output, timeouts, oversize output, and unsafe
`source_overrides` reject the current candidate and are collected as candidate
processor errors. If every candidate is rejected by such processor errors, the
seed run ends in error; otherwise the runner persists the remaining accepted
candidates and returns the processor error for observability. The payload
contract is also captured in
[`schemas/crowler-infoseed-candidate-plugin-schema.json`](../schemas/crowler-infoseed-candidate-plugin-schema.json).


### Built-in phases versus custom plugin/agent phases

Information Seed plugins are intentionally scoped to user/plugin phases. The
platform-owned phases are provider discovery, URL normalization,
URL/host/domain de-duplication, built-in allow/deny/scheme/score/cardinality
filters, source configuration validation, source persistence/linking, lifecycle
finalization, and event/diagnostic redaction. Candidate plugins run after the
built-in filters and before persistence; they may reject a candidate, adjust the
final score/reason, and provide only the documented safe source overrides.

Do not use plugins or agents to bypass provider policy, robots.txt, consent
flows, or terms-of-service restrictions. A plugin can enforce stricter local
policy (for example, reject hosts outside an approved domain or lower scores for
untrusted providers), but it cannot make a disallowed provider compliant. Keep
plugin output small, deterministic, and free of secrets because selected
metadata may be persisted in candidate evidence and redacted run diagnostics.

## Best Practices

- **Modularity:**
  Write plugins that perform a single, well-defined transformation. Chain multiple plugins if complex ETL processes are needed.

- **Consistent Data Format:**
  All functions work with JSON objects. This ensures that data is normalized regardless of its source (RDBMS, MongoDB, Neo4J, etc.).

- **Use the Provided API:**
  Leverage helper functions like `csvToJSON`, `xmlToJSON`, `filterJSON`, and `pipeJSON` to simplify data transformation tasks.

- **Error Handling:**
  Ensure your plugin gracefully handles errors and logs issues using `console.error` or `console.warn`.

- **Security:**
  Plugins in the CROWler cannot read or write files on the filesystem. They can only gater data either from the current pipeline, or by emitting web requests or database queries.

- **Testing:**
  Test your plugin in isolation with sample JSON data to ensure it behaves as expected before deploying in production.

---

## Time-series integration

Plugins do not receive an implicit telemetry channel. A plugin can rely on the normal emitter for a supported artifact it causes to be persisted, or application code can register and emit a `custom` metric through the shipped repository/emitter interfaces after durable persistence. Use bounded cardinality/privacy settings and stable dedupe identity. See [Time-series observations and aggregates](timeseries.md#source-kinds-and-emission-timing).

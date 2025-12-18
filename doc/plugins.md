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
  (Valid values include: `engine_plugin`, `vdi_plugin`, or `event_plugin`)
- **Version (optional):** Indicated by `// @version:`
- **Event Type (optional):** Indicated by `// @event_type:`

### Example Plugin Header

```js
// @name: sample_transform_plugin
// @description: Transforms contacts data by adding greetings and exporting as CSV.
// @type: engine_plugin
// @version: 1.0.0
// @event_type: crawl_completed
```

Note: The `@event_type` header is only required for `engine_plugin` type plugins and specifies the event that triggers the plugin execution. The events types can be decided by the user, so they can be customized to fit the specific needs. There are some pre-defined event types like:

- `crawl_completed`: Triggered when the crawling process for a given Source is completed.
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

On top of that the current processed JSON document for the specific pipeline will be available in the `params` object as `json_data`. And if there are pre-defined metadata for the pipeline, it will be available in the `params` object as `meta_data`.

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

# Using Events Polling in Engine Plugins

Example:

```js
// name: crawl_event_observer
// description: Observes crawl-related events for a source during plugin execution
// type: engine_plugin

// Subscribe to events
var subId = subscribeEvents(
    {
        type_prefix: "crawl_",   // Match crawl_* events
        source_id: 12345         // Only for this source
    },
    32 // Buffer size (optional, default is 16)
);

console.log("Subscribed to crawl events, subscription ID:", subId);

// Simulate some work
for (var i = 0; i < 10; i++) {
    // Poll for at most 1 second
    var ev = pollEvent(subId, 1000);

    if (ev !== null) {
        console.log(
            "Received event:",
            ev.Type,
            "severity:",
            ev.Severity
        );

        // Access event details
        if (ev.Details && ev.Details.pagesCrawled !== undefined) {
            console.log("Pages crawled:", ev.Details.pagesCrawled);
        }
    } else {
        console.log("No event received in this interval");
    }

    // Do other work
    sleep(500);
}

// Always unsubscribe explicitly (also done automatically on plugin exit)
unsubscribeEvents(subId);

result = {
    status: "completed",
    observed_events: true
};
```

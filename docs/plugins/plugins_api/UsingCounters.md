# Using Counters (in Engine's and Events Plugins)

Counters are a feature offered by the CROWler's KVStore that allow plugins to manage
shared resources in a concurrent-safe manner. They are particularly useful for limiting
the number of concurrent operations, such as network requests or database connections.

## Counter Basics

A counter is defined by a maximum value, which represents the total number of concurrent slots available. Plugins can acquire and release slots from the counter as needed. When a plugin
acquires slots, the counter's available slots decrease.

When slots are released, the available slots increase. Available slots are derived from the counter state and are not modified directly by plugins.

Counters are identified by a unique name within the KVStore. Plugins can create counters, acquire and release capacity using leases, and query counter state using the KVStore API.

## Using Counters in Plugins

To use counters in your plugin, you will typically follow these steps:

```javascript
/*
===========================================
 CROWler JS Counter API – Reference Example
===========================================

Counters are used to limit concurrency and/or approximate
rate limits across the entire CROWler fleet.

Counters are:
- global (no context ID)
- lease-based
- atomic
- crash-safe via TTL

IMPORTANT:
Counters are NOT incremented or decremented directly.
Capacity is acquired via leases and released explicitly.

-------------------------------------------
 Available JS APIs
-------------------------------------------

createCounter(key, { max, source })

tryAcquireCounter(key, {
  slots:   number,
  ttl_ms: number,
  owner:  string
}) -> { ok: boolean, lease_id: string }

releaseCounter(key, lease_id)

getCounter(key) -> {
  max,
  current,
  available,
  leases,
  version
}

-------------------------------------------
 Example: API Rate Limiting with Retry
-------------------------------------------
*/

/**
 * Acquire a counter lease with retry.
 *
 * @param {string} key - Counter key
 * @param {Object} acquireCfg - { slots, ttl_ms, owner }
 * @param {Object} retryCfg - { max_attempts, wait_ms }
 * @returns {Object|null} Lease object or null if acquisition failed
 */
function acquireWithRetry(key, acquireCfg, retryCfg) {
    var attempts = 0;
    var maxAttempts = retryCfg.max_attempts || 20;
    var waitMs = retryCfg.wait_ms || 100;

    while (attempts < maxAttempts) {
        var lease = tryAcquireCounter(key, acquireCfg);

        if (lease && lease.ok) {
            return lease;
        }

        attempts++;
        sleep(waitMs);
    }

    return null;
}

/*
-------------------------------------------
 Step 1: Create the counter (safe to call once per plugin startup)
-------------------------------------------
*/

createCounter("api:example_service", {
    max: 60,                  // maximum concurrent slots
    source: "example_plugin"  // for audit/debugging
});

/*
-------------------------------------------
 Step 2: Acquire capacity with retry
-------------------------------------------
*/

var lease = acquireWithRetry(
    "api:example_service",
    {
        slots: 1,                 // acquire one slot
        ttl_ms: 1500,             // must exceed worst-case API latency
        owner: "example_plugin"   // identifies the caller
    },
    {
        max_attempts: 30,         // total retry attempts
        wait_ms: 100              // delay between retries
    }
);

if (!lease) {
    // Could not acquire capacity within retry budget
    console.log("Rate limit reached, giving up.");
    return;
}

/*
-------------------------------------------
 Step 3: Use the protected resource
-------------------------------------------
*/

try {
    // Perform the rate-limited operation here
    // Example:
    // var response = apiClient.get("https://example.api/endpoint");
    // process(response);

} finally {
    /*
    -------------------------------------------
     Step 4: Always release the lease
    -------------------------------------------
    */
    releaseCounter("api:example_service", lease.lease_id);
}

/*
-------------------------------------------
 Optional: Inspect counter state
-------------------------------------------
*/

var info = getCounter("api:example_service");
console.log(
    "Counter state:",
    "max =", info.max,
    "current =", info.current,
    "available =", info.available,
    "leases =", info.leases
);

/*
-------------------------------------------
 Notes for Plugin Authors
-------------------------------------------

- Counters are global across all engines and workers.
- Never attempt to increment or decrement counters directly.
- Always release leases in a finally block.
- TTL is a safety net, not a replacement for release(). TTL should always be greater than the expected duration of the protected operation.
- Retry logic belongs in the plugin, not in the counter.
*/
```

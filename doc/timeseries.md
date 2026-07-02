# Time-series observations and aggregates (v1)

The v1 time-series feature turns selected **persisted user-data facts** into typed observations and materialized aggregate buckets. It is an analytical projection over the crawler's existing search and discovery data, not a replacement for it.

> The examples in [`examples/timeseries/`](../examples/timeseries/) are deployment examples only. They do not define built-in metrics, keywords, brands, performers, products, research topics, headers, ports, entities, or correlation rules.

## Boundary and source of truth

The existing search tables remain authoritative. `SearchIndex`, `Keywords`/`KeywordIndex`, `MetaTags`/`MetaTagsIndex`, `WebObjects`, `HTTPInfo`, `NetInfo`, screenshots/files, Information Seed tables, entities, memberships, and correlations own the indexed facts. Time-series rows retain IDs and provenance that point back to those records and may be deleted/rebuilt without changing the search result.

The feature deliberately excludes infrastructure telemetry. **Worker health, VDI health, API latency, queue depth, scheduler health, database performance, and process telemetry are not time-series source kinds.** Export those signals to Prometheus, logging, tracing, or administrative health tooling.

Configuration and registration are separate in v1:

1. YAML under `timeseries` validates extraction defaults and supplies runtime selector/privacy/cardinality overrides.
2. An enabled definition must also exist in `TimeSeriesMetrics`. The shipped registration interface is `database.UpsertTimeSeriesMetric`; at crawler startup and configuration reload, `database.SyncConfiguredTimeSeriesMetrics` also upserts declarative YAML metrics into `TimeSeriesMetrics`.
3. Emitters read enabled definitions from `TimeSeriesMetrics` when the corresponding durable fact is written.

Changing a registered metric's `value_type` is rejected. Create a new metric key for a type change. A provisioning integration can register a definition as follows (error handling omitted here only for brevity):

```go
selector, _ := json.Marshal(map[string]interface{}{"field": "value"})
metric, err := database.UpsertTimeSeriesMetric(db, &database.TimeSeriesMetric{
    Key: "example.metric", DisplayName: "Example metric",
    SourceKind: config.TimeSeriesSourceCustom, ValueType: config.TimeSeriesValueDecimal,
    Aggregate: config.TimeSeriesAggregateAverage, Bucket: config.TimeSeriesBucketOneHour,
    TimeBasis: config.TimeSeriesTimeObservedAt, DedupeScope: config.TimeSeriesDedupeObject,
    FailurePolicy: config.TimeSeriesFailureLogSkip, Selector: selector, Enabled: true,
    HashOnly: true, StoreValueText: false,
})
```

Registration does not itself emit data. A custom integration must persist its source fact and then write through the repository/emitter path; built-in source kinds emit at the durable points listed below.

## End-user quick start

A working deployment has three separate pieces:

1. **Enable the time-series runtime in configuration.** Set `timeseries.enabled: true`, choose safe defaults, and enable aggregation if you want chart buckets to be maintained automatically by the events service.
2. **Register metric definitions in the database.** The crawler now synchronizes YAML `timeseries.metrics` entries into `TimeSeriesMetrics` during startup and configuration reload. Provisioning code, migrations, or an operator integration may still call `database.UpsertTimeSeriesMetric` for custom metrics or preloading.
3. **Crawl or persist supported facts.** Observations are emitted only after the underlying durable fact exists: a keyword/index row, metatag/index row, object attribute, web object, HTTPInfo, NetInfo, screenshot/file metadata, Information Seed transition, discovery link, entity membership, object correlation, correlation-rule result, or explicit custom integration.

Minimal safe configuration:

```yaml
timeseries:
  enabled: true
  defaults:
    value_type: integer
    aggregates: [count]
    bucket_interval: 1h
    time_basis: observed_at
    dedupe_scope: object
    failure_policy: log_skip
  aggregation:
    enabled: true
    schedule: 5m
    batch_size: 1000
    max_batches: 10
    overlap: 15m
  retention:
    raw: 30d
    aggregated: 365d
  cardinality:
    max_series_per_metric: 10000
    max_dimensions: 4
    max_values_per_dimension: 1000
    overflow: drop
  privacy:
    hash_only: true
    store_value_text: false
    max_value_length: 256
    redact_patterns:
      - '(?i)(authorization|cookie|token|secret|password)'
  metrics:
    - key: example.product.price
      enabled: true
      description: Example price history from a persisted web-object attribute.
      source_kind: object_attribute
      object_type: webobject
      selector:
        attribute_key: price
        from: normalized
      value_type: decimal
      unit: deployment_currency
      aggregates: [average, min, max, first, last]
      bucket_interval: 1d
      dimensions:
        - key: object_family
          selector:
            from: metric
            path: object_type
```

After the metric is registered and matching crawls have produced observations, query definitions with `/v1/timeseries/metrics`, read charts with `/v1/timeseries`, and drill into bounded raw observations with `/v1/timeseries/drilldown` or `/v1/timeseries/observations`.

## How time-series extraction works

A time-series metric is a recipe for turning a durable crawler fact into one or more observations. The recipe answers four questions:

1. **Which persisted fact should be watched?** `source_kind` chooses the emitter family, such as `object_attribute`, `keyword`, `httpinfo`, or `information_seed`.
2. **Which value should be measured?** `selector` identifies the value inside that fact, and `value_type` tells the emitter how to coerce it.
3. **When and where should the value be counted?** `time_basis`, `timestamp_selector`, `bucket_interval`, `dedupe_scope`, and the resolved scope columns decide the timestamp, chart bucket, and duplicate boundary.
4. **How should chart groups be formed safely?** `dimensions`, `cardinality`, and `privacy` decide what labels are attached and how much value text can be stored or returned.

The flow for an `object_attribute` metric is typical:

```text
crawler/parser/ruleset
        │
        ▼
persisted object + normalized ObjectAttributes row
        │       (for example: webobject 55 has normalized attribute price=42.50)
        ▼
time-series emitter evaluates enabled TimeSeriesMetrics with source_kind=object_attribute
        │
        ├─ selector.attribute_key chooses the persisted attribute (`price`)
        ├─ value_type converts it (`decimal`)
        ├─ timestamp rules choose observed/effective time
        ├─ scope resolver attaches durable IDs (source/index/object/seed/entity when known)
        ├─ dimension selectors attach low-cardinality labels
        └─ privacy/cardinality guards hash, redact, drop, or overflow unsafe data
        ▼
TimeSeriesObservations raw row
        │
        ▼
optional events-service aggregation into TimeSeriesAggregates buckets
        │
        ▼
/v1/timeseries chart, /v1/timeseries/dimensions comparison, /v1/timeseries/drilldown raw context
```

The important operational consequence is that time-series configuration never invents data. If the crawler has not persisted the selected keyword, metatag, object attribute, HTTPInfo field, NetInfo field, seed transition, entity membership, or correlation result, no observation can be emitted for that metric. When you design a metric, first confirm where your crawler stores the source fact and what durable ID or attribute key references it.

## Reading the YAML example field by field

This section explains the quick-start example as a user guide rather than as a schema fragment.

### Top-level switch

```yaml
timeseries:
  enabled: true
```

`enabled` turns on runtime evaluation of registered metrics. Leave it `false` in shared templates or dry-run configurations. Set it to `true` only in deployments where the database contains the metric definitions you expect and where emitters should write observations as crawler facts are persisted.

### Defaults inherited by each metric

```yaml
  defaults:
    value_type: integer
    aggregates: [count]
    bucket_interval: 1h
    time_basis: observed_at
    dedupe_scope: object
    failure_policy: log_skip
```

Defaults keep repetitive metric entries short. A metric-specific field always wins over the default.

| Field | What it controls | Why you choose it |
| --- | --- | --- |
| `value_type` | How selected source values are coerced before storage and aggregation. | Use `integer`/`decimal`/`duration`/`count` for numeric charts, `string` for first/last or distinct tracking, `boolean` for yes/no presence, `timestamp` when the measured value is itself a time, and `json` only when you need structured value hashing/counting. |
| `aggregates` | Which materialized calculations are maintained for each bucket. | Keep only aggregates you will query. Numeric metrics commonly use `[average, min, max, p95]`; event metrics usually use `[count]`; state metrics often use `[first, last]` or `[distinct_count]`. |
| `bucket_interval` | The UTC chart grain for materialized buckets. | Choose the largest interval that answers the business question. High-frequency buckets multiply storage and cardinality. Use `none` only when you need raw observations without aggregate charts. |
| `time_basis` | Which timestamp places the observation on the timeline. | `observed_at` is safest because it is the crawler/database observation time. Use `event_at` or `source_timestamp` only when a reliable timestamp is persisted in the source fact and provide `timestamp_selector`. |
| `dedupe_scope` | The identity boundary for duplicate suppression. | `object` is a safe default for object facts because retries of the same object converge. `source` groups by source ownership, `global` suppresses equivalent values across all scopes, and `none` preserves every emitter invocation. |
| `failure_policy` | Whether emitter errors are fatal to indexing. | Start with `log_skip` while designing metrics. Use `fail_indexing` only for metrics that are mandatory for your pipeline, because an extraction error can roll back the enclosing durable write. |

### Aggregation scheduler

```yaml
  aggregation:
    enabled: true
    schedule: 5m
    batch_size: 1000
    max_batches: 10
    overlap: 15m
```

Aggregation reads raw observations and materializes query-friendly buckets. The events service runs it only when both `timeseries.enabled` and `timeseries.aggregation.enabled` are true. `schedule` is how often the service wakes up. `batch_size * max_batches` is the maximum work per run, so the example processes at most 10,000 observations before yielding. `overlap` rewinds the checkpoint by 15 minutes so late observations can repair recently completed buckets.

Use a short schedule for dashboards, a larger batch window for backfills, and enough overlap to cover normal crawl/indexing delay. If aggregation is disabled, raw observations can still exist, but aggregate-first chart routes have no new materialized buckets until you run aggregation manually.

### Retention policy

```yaml
  retention:
    raw: 30d
    aggregated: 365d
```

Raw observations contain the most detailed lineage and are expensive to keep indefinitely. Aggregates are smaller and are normally kept longer for trend charts. In v1 these values are policy inputs for administrative pruning; they are not automatically scheduled by the time-series config alone. Run `PruneTimeSeriesRetention` from operational code or maintenance jobs when you want retention applied.

### Cardinality guardrails

```yaml
  cardinality:
    max_series_per_metric: 10000
    max_dimensions: 4
    max_values_per_dimension: 1000
    overflow: drop
```

A time-series **series** is effectively a metric plus scope plus dimension set. Dimensions such as product category, HTTP status class, or source type are useful; dimensions such as full URL, raw product title, email address, or session token can create unbounded series and leak sensitive data. The example permits up to four dimensions, up to 1,000 distinct values per dimension, and drops observations that would exceed the guardrail.

Choose `drop` for safety, `hash` when preserving a hashed observation is more important than retaining plain values, and `overflow_bucket` when you prefer to collapse excess groups into a single `{"overflow":"__overflow__"}` dimension set.

### Privacy controls

```yaml
  privacy:
    hash_only: true
    store_value_text: false
    max_value_length: 256
    redact_patterns:
      - '(?i)(authorization|cookie|token|secret|password)'
```

These settings protect values before they become long-lived analytical data. `hash_only: true` stores a deterministic hash and omits direct value storage. `store_value_text: false` prevents raw text values from being stored for API display. `max_value_length` limits accidental large values, and `redact_patterns` remove sensitive substrings from values/dimensions handled by the shared emitter. You cannot combine `hash_only: true` with `store_value_text: true`.

### Metric definition

```yaml
  metrics:
    - key: example.product.price
      enabled: true
      description: Example price history from a persisted web-object attribute.
      source_kind: object_attribute
      object_type: webobject
      selector:
        attribute_key: price
        from: normalized
      value_type: decimal
      unit: deployment_currency
      aggregates: [average, min, max, first, last]
      bucket_interval: 1d
      dimensions:
        - key: object_family
          selector:
            from: metric
            path: object_type
```

This metric says: whenever a persisted `webobject` has a normalized object attribute with key `price`, convert that attribute to a decimal observation, bucket it daily, and materialize average/min/max/first/last price values. `unit` is descriptive metadata for clients; use deployment-specific values such as `USD`, `EUR`, `ms`, `bytes`, or `deployment_currency`.

The `selector` is the reference to the source data. For `object_attribute`, `attribute_key: price` points to the persisted normalized attribute key, not to an arbitrary YAML variable. `from: normalized` documents that the metric expects the normalized form of the attribute rather than a raw parser string. If your deployment stores the same concept under `sale_price`, `amount`, or `attributes.pricing.current`, use that persisted key/path instead.

The dimension `object_family` is intentionally low cardinality: it reads `object_type` from metric context, which will normally be `webobject` for this example. In a real price-tracking deployment you might use dimensions such as `currency`, `seller_type`, `country`, or `availability_state` if those values are normalized to a small set. Avoid dimensions like SKU, URL, product title, or extracted free text unless you deliberately want one series per value and have raised limits accordingly.

## Designing your own metric

Use this checklist when moving from examples to your own crawler data:

1. **Find the durable fact.** Decide whether the source is a keyword occurrence, metatag, normalized object attribute, web object field, HTTPInfo/NetInfo field, screenshot/file metadata, Information Seed transition, entity membership, correlation, or custom integration. The source must already be persisted.
2. **Name the metric stably.** Use a namespaced key such as `commerce.product.price`, `security.http.hsts_present`, or `research.topic.mention_count`. Do not change the meaning or value type of an existing key; create a new key when semantics change.
3. **Choose the selector.** Reference the persisted field, attribute key, event, or path. For object attributes, prefer normalized attribute keys. For lifecycle sources, filter by event/transition and select the field you want to measure.
4. **Choose the value type and aggregates together.** Make sure the selected value can be converted to the configured type and that every aggregate is valid for that type.
5. **Choose the time basis.** Use `observed_at` unless your source fact contains a trustworthy event/source timestamp. If using `event_at` or `source_timestamp`, configure `timestamp_selector`.
6. **Choose dedupe and scope.** Most persisted object facts should use `object`; lifecycle transition metrics often rely on their stable transition identity; use `none` only for intentional event streams.
7. **Add only bounded dimensions.** Dimensions should be useful filters with low cardinality. If a field can grow with every page, product, user, URL, or token, keep it in provenance/source data rather than as a dimension.
8. **Set privacy before enabling.** Start with `hash_only: true`, `store_value_text: false`, short `max_value_length`, and redaction patterns. Loosen only when the value is safe and needed in API responses.
9. **Register the metric.** Start or reload the crawler so `SyncConfiguredTimeSeriesMetrics` mirrors the YAML entry into `TimeSeriesMetrics`, or upsert it explicitly from provisioning code, a migration, or an operator integration.
10. **Verify with a small crawl.** Confirm the source fact exists, then query `/v1/timeseries/metrics`, `/v1/timeseries/observations`, and finally `/v1/timeseries` after aggregation runs.

### Common metric patterns

```yaml
# Count mentions of a normalized keyword per source.
- key: research.topic.ai_mentions
  source_kind: keyword
  selector:
    keyword: artificial intelligence
  value_type: count
  aggregates: [count, sum]
  bucket_interval: 1d
  dedupe_scope: source
  dimensions:
    - key: source_type
      selector: {from: subject, path: type}
```

```yaml
# Track whether a security header is present on persisted HTTPInfo attributes.
- key: security.http.hsts_present
  source_kind: object_attribute
  object_type: httpinfo
  selector:
    attribute_key: strict_transport_security
    derivation: presence
  value_type: boolean
  aggregates: [count, distinct_count]
  bucket_interval: 1d
  dimensions:
    - key: status_class
      selector: {path: status_class}
```

```yaml
# Measure confidence emitted by persisted object correlations.
- key: intelligence.correlation.confidence
  source_kind: object_correlation
  selector:
    event: correlation_persisted
    field: confidence
  value_type: decimal
  aggregates: [average, p50, p95, min, max]
  bucket_interval: 1h
  dimensions:
    - key: rule_id
      selector: {field: rule_id}
```

Treat these as patterns to adapt, not as built-in metrics. The selector keys must match what your deployment actually persists.

## Data model

* **Metric definition** — stable key, source kind, value type, default aggregate, bucket, time basis, dedupe scope, selectors, dimensions, retention/cardinality/privacy flags, unit, and enabled state.
* **Raw observation** — one typed value plus observed/effective/collection times, bucket bounds, resolved scope, dimensions, value hash, change metadata, dedupe key, and privacy-aware provenance.
* **Aggregate bucket** — materialized values for one metric, bucket, scope, and dimension set. It stores count, occurrence total, sum, average, min/max, distinct count, first/last values, percentiles, and first/last-seen times as applicable.
* **Aggregation run** — optional operational record/checkpoint used by bounded incremental or explicit-range aggregation. Runs coordinate through a process lock and a backend lock; another owner returns `ErrTimeSeriesAggregationRunning`.

A bucket of `none` records raw observations but is skipped by aggregation.

## Complete enum reference

### Source kinds and emission timing

| `source_kind` | Durable fact and emission point | Duplicate behavior |
| --- | --- | --- |
| `keyword` | After a keyword and its `KeywordIndex` link/occurrence count are upserted. | A crawl retry reaches the same durable link; dedupe follows the metric scope. `none` adds an event nonce. |
| `metatag` | After a metatag and `MetaTagsIndex` link are upserted. Names match case-insensitively after normalization. | Same persisted tag/link converges under scoped dedupe. |
| `object_attribute` | After a normalized `ObjectAttributes` value is persisted for `webobject`, `httpinfo`, or `netinfo`. | One observation per resolved scope; a matching normalized attribute is preferred over a generic artifact path to avoid double emission. |
| `webobject` | After the web object and index ownership link persist. | Scoped dedupe suppresses equivalent retries; hash/content selectors can track object change. |
| `httpinfo` | After HTTPInfo and its index link persist. | Same as other index-owned artifacts. |
| `netinfo` | After NetInfo and its index link persist. | Same as other index-owned artifacts. |
| `screenshot` | In the screenshot transaction after its row is resolved. | Uses screenshot row/object identity. |
| `file` | At the same persisted screenshot/file artifact point; it is a separate source-kind evaluation over the stored file metadata. | Uses the same durable row with a distinct source kind/metric identity. |
| `information_seed` | In the same transaction as a persisted seed lifecycle transition. | Stable transition identity and value/dimensions make retries idempotent. |
| `information_seed_candidate` | In the same transaction as persisted candidate creation/decision transitions. | Stable candidate-transition identity makes retries idempotent. |
| `source_discovery` | In the same transaction as persisted discovery promotion/linkage. | Stable discovery transition identity makes retries idempotent. |
| `entity_membership` | In the same transaction as membership persistence. | Identity is entity + object type + object ID. |
| `object_correlation` | In the same transaction as correlation persistence. | Identity is the normalized object pair + rule ID. |
| `correlation_rule` | Alongside a persisted correlation result, with event `correlation_result`. | Shares the correlation identity but remains a separate source kind. |
| `custom` | Only when plugin/ruleset/application integration explicitly calls the shipped repository/emitter interfaces after persisting its fact. | The integration must provide stable scope/provenance and use the shared dedupe path; no custom event is emitted merely because YAML exists. |

Rulesets and plugins therefore do not gain implicit observations. They must first persist one of the supported artifacts, or explicitly implement a `custom` observation integration. See [Ruleset architecture](ruleset_architecture.md) and [Plugins](plugins.md).

### Value types

`integer`, `decimal`, `duration`, `boolean`, `string`, `json`, `count`, `timestamp`.

`duration` is stored as a numeric value chosen by the metric/integration; the unit should be declared. `timestamp` input must be RFC 3339/RFC 3339 Nano. `count` converts collections/strings to length, numeric input to that count, and other present values to one in indexed-artifact emitters; lifecycle emitters record one matching event.

### Aggregate types

`count`, `sum`, `average`, `min`, `max`, `distinct_count`, `first`, `last`, `p50`, `p75`, `p90`, `p95`, `p99`.

Numeric aggregates (`sum`, `average`, `min`, `max`, and percentiles) require `integer`, `decimal`, `duration`, or `count`. Boolean/JSON definitions are limited to count/distinct counting; strings may also use first/last. `count` is the sample count; `occurrence_total` preserves occurrence/count magnitude separately.

### Buckets

`none`, `1m`, `5m`, `15m`, `1h`, `1d`, `1w`, `1mo`. Bounds are UTC half-open intervals `[start,end)`. Weeks and calendar months use the database helper's UTC boundaries rather than fixed-duration approximations.

### Time basis

* `observed_at` — crawler/database observation time; the default.
* `event_at` — timestamp selected from persisted event data.
* `source_timestamp` — timestamp selected from the source artifact.

`event_at` and `source_timestamp` require `timestamp_selector`. The observation always retains collection time. API filtering uses the metric's selected basis while bucket boundaries are materialized with the observation.

### Dedupe scopes

* `none` — preserve each emitter invocation by adding a nonce.
* `source` — dedupe within the resolved source.
* `object` — dedupe within the persisted subject/object identity.
* `global` — dedupe equivalent metric/value/dimensions globally.

Dedupe is enforced by a deterministic key and a unique database constraint. A duplicate insert returns the existing observation rather than creating another row. Information Seed and correlation lifecycle writers additionally use stable transition identities.

### Failure policies

* `log_skip` — log when a logger is available, then continue indexing (default).
* `log` — log and continue.
* `skip` — silently continue.
* `retry` — return the emitter error so the enclosing durable operation can retry.
* `fail_indexing` — return the error and fail/roll back the enclosing indexing transaction.

The lifecycle persistence path treats `retry` and `fail_indexing` as fatal and keeps `log`, `log_skip`, and `skip` non-fatal.

### Change kinds

* `new` — no previous comparable observation.
* `unchanged` — value hash equals the previous value hash.
* `changed` — value hash differs; numeric values may include a delta.
* `reappeared` — indexed-artifact change detection sees a value again after the prior source update boundary.

Information Seed/correlation lifecycle emitters produce `new`, `unchanged`, and `changed`; `reappeared` belongs to indexed artifacts.

### Cardinality overflow

`drop`, `hash`, `overflow_bucket`. Limits are `max_series_per_metric`, `max_dimensions`, and `max_values_per_dimension`. Metric-level settings may override global settings. `drop` is the safest default. When the crawler cardinality guard reports an overflow, `hash` switches the observation value to hash-only storage while retaining its dimensions; `overflow_bucket` replaces the dimension set with `{"overflow":"__overflow__"}`. These behaviors prevent direct value retention or unbounded new groups, but operators should still choose conservative limits.

## Selectors and examples

Selectors are open, domain-agnostic maps; schema descriptions intentionally do not encode domain field names.

Indexed artifacts can select `from: value|content|raw_value|subject|subject_key|name|keyword|occurrences|details|stored_details|attributes|metadata|hash|content_hash|metric|artifact` and an optional dotted `path`. Supported derivations are `presence`/`present`/`exists`, `count`/`length`, `sha256`/`hash`, `days_until`/`remaining_days`, `duration_until`/`seconds_until`, `days_since`, and `duration_since`/`seconds_since`. Keyword/metatag selectors also support exact names, regex, and rule operators `equals`, `contains`, `prefix`/`starts_with`, `suffix`/`ends_with`, and `regex`.

Information Seed/entity/correlation selectors may filter `event` or `transition`, apply exact `where` fields, and select a dotted `field`/`path` or fixed `value`. Dimension selectors use the same source-specific resolver.

The complete, schema-valid examples are:

* [Brand/company monitoring](../examples/timeseries/brand-company-monitoring.yaml)
* [Cybersecurity header tracking](../examples/timeseries/cybersecurity-header-tracking.yaml)
* [Network intelligence](../examples/timeseries/network-intelligence.yaml)
* [Product price tracking](../examples/timeseries/product-price-tracking.yaml)
* [Research topic tracking](../examples/timeseries/research-topic-tracking.yaml)
* [Information Seed discovery quality](../examples/timeseries/information-seed-discovery-quality.yaml)
* [Entity-correlation confidence](../examples/timeseries/entity-correlation-confidence.yaml)

Each defaults to bounded dimensions, `drop` overflow, hash-only value storage, no plaintext value storage, short maximum values, and redaction patterns. Replace example selectors only with fields your deployment actually persists.

## Configuration reference

The top-level `timeseries` object is optional and defaults to disabled. Durations use Go-style units plus `d` for 24-hour days and `w` for seven-day weeks. The schema rejects unknown fields, invalid enum values, empty metric keys, duplicate metric keys, invalid aggregate/value combinations, `event_at` or `source_timestamp` metrics without `timestamp_selector`, too many configured dimensions, duplicate dimension keys, and privacy settings that set both `hash_only: true` and `store_value_text: true`.

| Field | Purpose | Default |
| --- | --- | --- |
| `enabled` | Enables emitter/runtime evaluation. When false, configured metrics do not emit observations. | `false` |
| `defaults.value_type` | Value type inherited by metrics that omit `value_type`. | `integer` |
| `defaults.aggregates` | Aggregates inherited by metrics that omit `aggregates`. Must be compatible with the value type. | `[count]` |
| `defaults.bucket_interval` | Bucket inherited by metrics that omit `bucket_interval`. | `1h` |
| `defaults.time_basis` | Timestamp basis inherited by metrics. | `observed_at` |
| `defaults.dedupe_scope` | Duplicate-suppression scope inherited by metrics. | `none` |
| `defaults.failure_policy` | Error handling inherited by metrics. | `log_skip` |
| `retention.raw` | Administrative raw-observation retention horizon. Not scheduled automatically in v1. | `30d` |
| `retention.aggregated` | Administrative aggregate retention horizon. Not scheduled automatically in v1. | `365d` |
| `aggregation.enabled` | Lets the events service run incremental aggregation when the top-level feature is also enabled. | `false` |
| `aggregation.schedule` | Events-service aggregation interval. | `5m` |
| `aggregation.batch_size` / `max_batches` | Upper bound on observations processed per run. | `1000` / `10` |
| `aggregation.overlap` | Rewind applied to the checkpoint so late observations can repair complete buckets. | `15m` |
| `storage.backend` | Declarative storage/partitioning backend. Currently only `postgres`. | `postgres` |
| `storage.table_prefix` | Validated prefix for deployment policy; shipped table names are fixed. | `timeseries` |
| `storage.partitioning.*` | Optional PostgreSQL partitioning policy for external DBA/migration automation. | disabled, `1d`, `7` |
| `cardinality.*` | Global limits and overflow behavior for series/dimension growth. | `100000`, `10`, `10000`, `drop` |
| `privacy.*` | Global hash/text/truncation/redaction behavior. | no hash-only, no text storage, `2048`, `[]` |
| `metrics[]` | Declarative metric entries and per-metric overrides. The crawler synchronizes these entries into `TimeSeriesMetrics` on startup and reload. | `[]` |

Each `metrics[]` item requires `key` and `source_kind`; `object_attribute` metrics also require `object_type` and `selector.attribute_key`, while `metatag` metrics require `selector.metatag_name`. Metric-level `cardinality` and `privacy` objects override global policy for that metric. `dimensions` must use stable, low-cardinality keys because API dimension comparison refuses high-cardinality output rather than silently truncating it.

## Scope resolution

For index-owned artifacts, the resolver starts at `index_id`, follows source ownership, and emits one scope for each `SourceInformationSeedIndex` association. With Information Seed ownership, a scope can include `information_seed_id`, `source_id`, `source_information_seed_id`, and `index_id`. Without an Information Seed association, the fallback scope retains source/index ownership; if no ownership row resolves, the emitter still records the index (or object for an object attribute).

Lifecycle sources set their native scope directly: seed/candidate IDs, discovery source/link IDs, entity IDs, object IDs/types, and correlation rule/object pairs. Scope columns remain nullable because not every persisted fact belongs to every identity family.

Entity assignment is **immediate** when the resolver can see an `EntityMemberships` row as the observation is emitted. It is **delayed** when membership is persisted later. `BackfillObservationEntities`/`RunEntityObservationBackfillJob` fill only missing `entity_id` values by object identity, preserve existing confidence, append membership evidence to provenance, and report the affected observed-time range. Existing entity assignments are never overwritten.

## Aggregation, late data, reaggregation, and retention

The events service starts incremental aggregation only when both `timeseries.enabled` and `timeseries.aggregation.enabled` are true. `schedule` is a Go duration. Each run is bounded by `batch_size * max_batches` and stores a checkpoint. The next run starts at `checkpoint - overlap`, allowing late observations to replace already materialized complete buckets. Aggregation failure is logged and does not fail indexing/event work.

An explicit range is UTC and half-open. `AggregateTimeSeriesRange` expands the request to complete metric buckets, deletes/replaces affected aggregate groups in one transaction, and therefore repairs grouping after scope changes. `ReaggregateTimeSeriesBackfill` converts the entity backfill's inclusive affected end to the required half-open range.

Retention is not scheduled by the v1 events scheduler. Call `PruneTimeSeriesRetention` from administrative code. It applies global raw/aggregate durations, honors per-metric retention JSON when registered, counts candidates in dry-run mode, deletes in bounded batches otherwise, and never deletes metric definitions.

### Runnable operational call patterns

These are Go API examples, not HTTP routes. Assume `db` is an initialized `*database.Handler`.

```go
// Incremental run: resume the durable checkpoint and revisit 15 minutes.
result, err := database.RunTimeSeriesAggregation(ctx, db, database.TimeSeriesAggregationOptions{
    Overlap: 15 * time.Minute, BatchSize: 1000, MaxBatches: 10,
    RunKey: "timeseries-aggregation", Now: time.Now().UTC(),
})
```

```go
// Explicit date-range reaggregation; End is exclusive.
result, err := database.AggregateTimeSeriesRange(ctx, db, database.TimeSeriesRange{
    Start: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
    End:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
}, database.TimeSeriesAggregationOptions{BatchSize: 1000, MaxBatches: 100, RunKey: "may-2026-rebuild"})
```

```go
// Retention dry run: reports candidates and deletes nothing.
result, err := database.PruneTimeSeriesRetention(ctx, db, database.TimeSeriesRetentionOptions{
    Now: time.Now().UTC(), RawRetention: 30 * 24 * time.Hour,
    AggregateRetention: 365 * 24 * time.Hour,
    BatchSize: 1000, MaxBatches: 10, DryRun: true,
})
```

```go
// Persistently checkpointed delayed-entity repair, followed by required reaggregation.
backfill, err := database.RunEntityObservationBackfillJob(db, "entity-observations", 1000, 10)
if err == nil {
    _, err = database.ReaggregateTimeSeriesBackfill(ctx, db, backfill,
        database.TimeSeriesAggregationOptions{BatchSize: 1000, MaxBatches: 100, RunKey: "entity-backfill"})
}
```

## Storage portability and PostgreSQL partitioning

The v1 database schema, metric/observation/aggregate CRUD, query, dedupe, aggregation, run checkpoints, backfill, and retention layers support PostgreSQL, MySQL, and SQLite through backend-specific SQL. JSON is canonicalized so hashing/grouping remains stable across engines. The currently shipped crawler-side scope/cardinality adapter uses PostgreSQL transaction SQL, so automatic index-owned artifact emission is the PostgreSQL-wired end-to-end path; integrations targeting MySQL or SQLite must use the portable database repository interfaces (and backend-aware lifecycle writers) rather than assuming that PostgreSQL adapter is portable.

The configuration field `timeseries.storage.backend` currently accepts only `postgres`; it describes the optional time-series storage/partitioning policy and does not override the main `database.type`. MySQL and SQLite use their normal unpartitioned time-series tables. `storage.table_prefix` is validated but the shipped table names remain `TimeSeriesMetrics`, `TimeSeriesObservations`, `TimeSeriesAggregates`, and `TimeSeriesAggregationRuns`.

`storage.partitioning` is PostgreSQL-only and optional. In v1 it is declarative configuration (`enabled`, interval `1m|5m|15m|1h|1d|1w|1mo`, and `precreate`); the shipped startup path does not automatically rewrite existing tables or create/manage time-series partitions. Keep it disabled unless deployment migrations/DBA automation provide compatible PostgreSQL partitions. This limitation avoids claiming partition automation that is not delivered.

## Privacy and provenance

`max_value_length` truncates stored text before persistence; truncation is marked in provenance. `hash_only: true` retains a deterministic value hash but omits the direct value. `store_value_text: false` is the safe default. `redact_patterns` are compiled as Go regular expressions and applied to values/dimensions handled by the shared emitter. `hash_only` and `store_value_text: true` are mutually exclusive.

Provenance records technical lineage such as source kind, durable row/link/index IDs, selector path, parser/transformations, timestamp source, and redacted/hashed/truncated flags. Public API responses intentionally omit provenance, selector bodies, dedupe keys, previous hashes, deltas, and private cardinality/retention policy. Treat database-level provenance as sensitive because IDs and selector metadata can reveal relationships even when values are hashed.

## Aggregate-first HTTP API

All routes are `GET` and are registered only when the default API is enabled:

* `/v1/timeseries/metrics`
* `/v1/timeseries`
* `/v1/timeseries/observations`
* `/v1/timeseries/drilldown`
* `/v1/timeseries/dimensions`

Dates accept either `YYYY-MM-DD` or RFC 3339/RFC 3339 Nano with an explicit offset, for example `2026-05-01`, `2026-05-01T00:00:00Z`, or `2026-05-01T00:00:00.123456789-04:00`. A date-only `from` means UTC midnight; a date-only `to` is exclusive midnight after that date. Responses format timestamps as UTC RFC 3339 Nano. Ranges are half-open `[from,to)`, `from` must precede `to`, aggregate range is at most 366 days, and raw/drill-down range is at most 31 days.

Every query requires exactly one of `metric_id` or `metric_key` except metric listing. Common filters include all scope ID fields, `subject_type`, `subject`, `object_type`, correlation object fields, repeated `dimension=key=<JSON value>` (or comma-separated `dimensions`), `bucket`, `time_basis`, `aggregate`, `order=asc|desc`, `limit`, and `offset`. Unknown dimension keys are rejected.

Pagination is deterministic offset pagination. The default limit is 100. Aggregate/metric maximum is 1000, raw maximum is 200, and drill-down maximum is 100. `pagination` contains `limit`, `offset`, returned `count`, and `has_more`; request the next page with `offset + count`. Dimension comparison internally caps the aggregate scope at 10,000 rows and groups at the configured per-dimension limit, never above 100.

Metric discovery accepts `metric_id`, `metric_key`, `source`, `enabled`, `limit`, and `offset`:

```console
curl --get 'http://localhost:8080/v1/timeseries/metrics' \
  --data-urlencode 'source=object_attribute' \
  --data-urlencode 'enabled=true'
```

Each item exposes only `id`, `key`, `display_name`, optional `description`, `source_kind`, `value_type`, `default_aggregate`, `bucket`, `time_basis`, optional `unit`, `dimension_keys`, and `enabled`; selectors and private policies are omitted.

### Aggregate-first chart query

```console
curl --get 'http://localhost:8080/v1/timeseries' \
  --data-urlencode 'metric_key=example.product.price' \
  --data-urlencode 'from=2026-05-01T00:00:00Z' \
  --data-urlencode 'to=2026-06-01T00:00:00Z' \
  --data-urlencode 'aggregate=average' \
  --data-urlencode 'order=asc' \
  --data-urlencode 'limit=100' \
  --data-urlencode 'offset=0'
```

```json
{
  "items": [{
    "aggregate_hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "metric_id": 7,
    "metric_key": "example.product.price",
    "bucket_start": "2026-05-01T00:00:00Z",
    "bucket_end": "2026-05-02T00:00:00Z",
    "aggregate": "average",
    "value": 42.5,
    "values": {"count": 2, "occurrence_total": 2, "distinct_count": 2, "numeric_count": 2, "sum": 85, "min": 40, "max": 45, "average": 42.5, "p50": 42.5, "p75": 43.75, "p90": 44.5, "p95": 44.75, "p99": 44.95, "first": 40, "last": 45, "change_count": 1},
    "sample_count": 2,
    "occurrence_total": 2,
    "dimensions": {"object_family": "webobject"},
    "scope": {"source_id": 3, "index_id": 91, "object_type": "webobject", "object_id": 55}
  }],
  "pagination": {"limit": 100, "offset": 0, "count": 1, "has_more": false}
}
```

Nullable aggregate fields in `values` may be omitted/`null` when they do not apply to the metric value type. `value` is selected by the requested `aggregate`; it does not imply an on-demand recomputation.

### Drill down from a bucket

```console
curl --get 'http://localhost:8080/v1/timeseries/drilldown' \
  --data-urlencode 'aggregate_hash=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' \
  --data-urlencode 'limit=100' \
  --data-urlencode 'offset=0'
```

The response contains `aggregate_hash`, the matched `aggregate`, `observations`, and `pagination`. Hash drill-down ignores conflicting caller scope and reconstructs metric, exact bucket, scope, and dimensions from the aggregate. Without a hash, provide a metric plus both `from` and `to` and the complete aggregate scope yourself.

Dimension comparison groups already materialized buckets by one configured key:

```console
curl --get 'http://localhost:8080/v1/timeseries/dimensions' \
  --data-urlencode 'metric_key=example.product.price' \
  --data-urlencode 'dimension_key=object_family' \
  --data-urlencode 'from=2026-05-01' \
  --data-urlencode 'to=2026-05-31'
```

Its response fields are `dimension_key`, `groups` (each with `dimension_value` and `buckets`), `cardinality`, and `limit`. It returns `422` rather than truncating when the aggregate scope exceeds 10,000 rows or dimension cardinality exceeds the privacy limit.

Direct raw access is explicit and bounded:

```console
curl --get 'http://localhost:8080/v1/timeseries/observations' \
  --data-urlencode 'metric_key=example.product.price' \
  --data-urlencode 'from=2026-05-01' \
  --data-urlencode 'to=2026-05-07' \
  --data-urlencode 'limit=50'
```

Observation fields are `id`, `metric_id`, `metric_key`, `observed_at`, optional `effective_at`, `collected_at`, bucket bounds, privacy-filtered `value`, `is_changed`, optional `change_type`, `dimensions`, and `scope`. Hash-only metrics omit `value`.

For the exact public structures and filters, see [API reference](api.md#aggregate-first-time-series-api) and the registered response types in the OpenAPI document exposed by the API service.

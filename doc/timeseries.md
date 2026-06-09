# Time-series observations and aggregates (v1)

The v1 time-series feature turns selected **persisted user-data facts** into typed observations and materialized aggregate buckets. It is an analytical projection over the crawler's existing search and discovery data, not a replacement for it.

> The examples in [`examples/timeseries/`](../examples/timeseries/) are deployment examples only. They do not define built-in metrics, keywords, brands, performers, products, research topics, headers, ports, entities, or correlation rules.

## Boundary and source of truth

The existing search tables remain authoritative. `SearchIndex`, `Keywords`/`KeywordIndex`, `MetaTags`/`MetaTagsIndex`, `WebObjects`, `HTTPInfo`, `NetInfo`, screenshots/files, Information Seed tables, entities, memberships, and correlations own the indexed facts. Time-series rows retain IDs and provenance that point back to those records and may be deleted/rebuilt without changing the search result.

The feature deliberately excludes infrastructure telemetry. **Worker health, VDI health, API latency, queue depth, scheduler health, database performance, and process telemetry are not time-series source kinds.** Export those signals to Prometheus, logging, tracing, or administrative health tooling.

Configuration and registration are separate in v1:

1. YAML under `timeseries` validates extraction defaults and supplies runtime selector/privacy/cardinality overrides.
2. An enabled definition must also exist in `TimeSeriesMetrics`. The shipped registration interface is `database.UpsertTimeSeriesMetric`; there is no public metric-creation HTTP route or automatic YAML-to-table synchronizer.
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

The eight complete, schema-valid examples are:

* [Music performer monitoring](../examples/timeseries/music-performer-monitoring.yaml)
* [Brand/company monitoring](../examples/timeseries/brand-company-monitoring.yaml)
* [Cybersecurity header tracking](../examples/timeseries/cybersecurity-header-tracking.yaml)
* [Network intelligence](../examples/timeseries/network-intelligence.yaml)
* [Product price tracking](../examples/timeseries/product-price-tracking.yaml)
* [Research topic tracking](../examples/timeseries/research-topic-tracking.yaml)
* [Information Seed discovery quality](../examples/timeseries/information-seed-discovery-quality.yaml)
* [Entity-correlation confidence](../examples/timeseries/entity-correlation-confidence.yaml)

Each defaults to bounded dimensions, `drop` overflow, hash-only value storage, no plaintext value storage, short maximum values, and redaction patterns. Replace example selectors only with fields your deployment actually persists.

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

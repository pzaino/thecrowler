# Time-series API

The `/v1/timeseries` namespace is aggregate-first: chart requests read `TimeSeriesAggregates`. Raw facts are available only from the explicit observations and drill-down endpoints and always require a bounded time range.

All endpoints use the API service's existing CIDR filtering, CORS/security/keep-alive headers, rate limiting, recovery middleware, and JSON error envelope.

## Endpoints

### `GET /v1/timeseries/metrics`

Lists active metric definitions in stable metric-ID order. Filters are `metric_id`, `metric_key`, `source` (metric source kind), `enabled`, `limit`, and `offset`. Definitions expose display and aggregation metadata and configured dimension keys, but not selectors, retention internals, cardinality policies, or privacy rules.

### `GET /v1/timeseries`

Returns materialized aggregate buckets. `metric_id` or `metric_key` is required. The response includes:

* `bucket_start` and `bucket_end`;
* `value`, selected by the requested `aggregate`, or by the metric's `default_aggregate` when omitted;
* explicit `values` fields for count, occurrence total, distinct count, numeric count, sum, min, max, average, percentiles, first/last, and change count;
* `sample_count`, `occurrence_total`, configured dimensions, public scope identifiers, and `aggregate_hash` for safe drill-down.

A selected aggregate does not remove the explicit aggregate fields. For example, `aggregate=p95` makes `value` equal `values.p95`, while `values.average`, `values.max`, and the remaining materialized values stay available.

### `GET /v1/timeseries/observations`

Returns deterministic raw pages ordered by observation time and observation ID. `metric_id` or `metric_key`, `from`, and `to` are required. Page size is capped at 200. Value hashes, dedupe keys, provenance, provenance hashes, previous hashes, and deletion/storage metadata are never returned. Text/JSON values are omitted for hash-only or non-text-storing metrics, and redaction sentinels are not returned.

### `GET /v1/timeseries/drilldown`

Use `aggregate_hash=<64 hex characters>` to resolve the complete server-stored aggregate scope and fetch only its matching observations. Client-provided scope fields are overwritten when a hash is present. Alternatively, provide a complete scope containing a metric plus `from` and `to`; the normal typed scope and dimension filters apply. SQL fragments or selector expressions are not accepted.

Drill-down pages are capped at 100.

### `GET /v1/timeseries/dimensions`

Requires `dimension_key` and a metric. The key must be configured on that metric. Results group aggregate buckets by the requested scalar dimension value. Cardinality is limited by `timeseries.cardinality.max_values_per_dimension`, with an absolute API ceiling of 100; requests exceeding the policy return `422` rather than leaking or truncating high-cardinality groups.

## Composable filters

Aggregate, observation, drill-down, and dimension queries support:

* Metric: `metric_id` or `metric_key` (mutually exclusive).
* Scope IDs: `information_seed_id`, `information_seed_candidate_id`, `source_id`, `source_information_seed_id`, `index_id`, `entity_id`, `subject_id`, `object_id`, correlation rule/object IDs. Short aliases `information_seed`, `source`, `index`, `entity`, and `object` are also accepted.
* Scope types: `subject_type`, `object_type`, correlation object types. `subject` accepts a numeric subject ID or an exact subject text filter.
* Dimensions: repeated `dimension=key=value`, or comma-separated `dimensions=key=value,key2=value2`. Keys must be configured for the metric and values must be scalar JSON or strings.
* Time: `from` and `to` in RFC3339 or `YYYY-MM-DD`. A date-only `to` is exclusive of the following midnight, so `to=2026-06-06` includes June 6. Inverted ranges are rejected. Aggregate ranges are capped at 366 days; raw/drill-down ranges are capped at 31 days.
* `bucket`: `none`, `1m`, `5m`, `15m`, `1h`, `1d`, `1w`, or `1mo`.
* `time_basis`: `observed_at`, `event_at`, or `source_timestamp`.
* `aggregate`: `count`, `sum`, `average`, `min`, `max`, `distinct_count`, `first`, `last`, `p50`, `p75`, `p90`, `p95`, or `p99`.
* Pagination: positive `limit`, non-negative `offset`, and optional `order=desc`.

Example:

```text
GET /v1/timeseries?metric_key=pages.changed&source_id=42&dimension=region=eu&bucket=1h&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&aggregate=sum
```

Errors use the existing JSON error response and return `400` for malformed enums, IDs, dimensions, pagination, or ranges; `404` for an unknown metric; `405` for a non-GET method; and `422` for a dimension comparison exceeding privacy cardinality policy.

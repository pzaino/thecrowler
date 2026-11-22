# Time-series retention and cardinality hardening (2.3.1)

This note records the design baseline, the schema decision, the delivered
contract, and the operating limits for the 2.3.1 time-series retention work.

## Baseline

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Baseline commit | `e2d3db6fdf9d77ed5e64f912a0d49f818f660db3` |
| Previous release | `v2.3.0` |
| Delivered release | `v2.3.1` |
| Database schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Working tree at inspection | clean |

## Schema decision: remain on 1.17

No database object, column, index, constraint, migration, capability marker, or
writer generation changes are required for this work.

The feature uses existing objects:

- `TimeSeriesMetrics.retention_policy` stores the effective raw and aggregated
  window. The column already existed but synchronization did not populate it.
- `TimeSeriesMetrics.cardinality_policy` stores the effective metric/global
  cardinality policy, including inherited values.
- `TimeSeriesObservations` provides the selected `observed_at`, `effective_at`,
  or `source_updated_at` clock used for raw pruning.
- `TimeSeriesActiveSeries`, `TimeSeriesActiveDimensionValues`,
  `TimeSeriesObservationSeries`, `TimeSeriesObservationDimensions`,
  `TimeSeriesSeriesSlots`, and `TimeSeriesDimensionSlots` provide transactional
  cardinality accounting. Retention reuses the existing release path.
- `TimeSeriesAggregates` and `TimeSeriesAggregationRuns` provide aggregate
  storage and durable run/checkpoint state. The reserved
  `timeseries-retention:v1:<metric_id>` run-key family records a per-metric raw
  retention watermark in the existing run table.

Schema 1.18 is therefore not required and no `DB118-*` approval is requested.
`RequiredSchemaVersion` and `WriterGeneration` remain unchanged, and no existing
1.17 migration was modified.

## Configuration contract

```yaml
timeseries:
  enabled: true
  retention:
    enabled: false          # opt-in; 2.3.0 upgrades never start deleting
    schedule: 1h
    batch_size: 1000
    max_batches: 10
    max_duration: 1m
    dry_run: false
    raw: 30d
    aggregated: 365d
  aggregation:
    enabled: true
    overlap: 15m
  metrics:
    - key: example.metric
      source_kind: custom
      selector: {field: value}
      retention:
        raw: 0              # keep this metric's raw facts forever
        aggregated: 1y      # calendar year for aggregates
```

Rules:

- `retention.enabled` defaults to `false`. Global raw/aggregated defaults remain
  `30d` and `365d`, but no scheduled deletion occurs until the operator opts in.
- A per-metric `retention.raw` or `retention.aggregated` replaces the global
  window for that metric. An omitted or empty field inherits the global value.
- `0`, `0d`, `0mo`, `0y`, and the other zero forms mean "keep forever". They are
  not confused with an omitted value.
- Supported units are `ns`, `us`, `µs`, `ms`, `s`, `m`, `h`, `d`, `w`, `mo`, and
  `y`. Fixed windows preserve the historical Go duration grammar, including
  composites (`1h30m`) and fractions (`1.5h`), extended with `d` and `w`
  (`1d12h`, `1.5d`). Calendar windows are integer `y`/`mo` compositions
  (`1y6mo`) and cannot be mixed with fixed units. Calendar horizons are capped
  at 100 years (1200 months). `d` and `w` are exact durations; `mo` and `y` are
  calendar windows.
- Calendar arithmetic is UTC and clamps to the end of the target month. 31 March
  minus one month is 28 February (29 February in a leap year); 29 February minus
  one year is 28 February.
- Schedule, batch size, batch count, and maximum duration are validated before
  startup. A metric cannot use `mo`/`y` for `schedule` or `max_duration`.

## Retention execution

- The Events master starts the retention scheduler only when the top-level
  feature and `retention.enabled` are both true. Other replicas do not run it.
- A process mutex excludes local overlap. PostgreSQL additionally takes a
  session-level advisory lease (`crowler:timeseries:retention`) on a dedicated
  connection, so replicas cannot prune concurrently. Retention also acquires the
  aggregation maintenance lease; if an aggregation run owns it, retention yields
  with `ErrTimeSeriesRetentionAggregationBusy` instead of deleting observations
  while aggregates are being computed. SQLite has only the process mutex,
  consistent with its documented single-writer deployment limitation.
- `max_batches` is a global per-run batch budget, not a per-metric allowance.
  `max_duration` is a soft wall-clock budget checked between bounded
  transactions. A hard context deadline bounds a stalled statement. In-process
  aggregation waits while a retention pass owns the shared maintenance mutex;
  the pass is bounded by the same budgets.
- Metrics are processed in ascending metric ID. A consistently high-volume early
  metric can consume one pass's batch budget before later metrics are reached;
  later passes continue the work, and operators can raise `max_batches` when the
  backlog is large.
- Signal shutdown cancels the maintenance context before process exit. A lost
  connection releases the PostgreSQL session lease automatically.
- Raw deletion selects the metric's time basis and skips rows whose selected
  timestamp is NULL. Each batch runs in its own bounded transaction and calls
  the existing cardinality release path before deleting the observation.
- Aggregate deletion is partitioned by `bucket_end` and commits one bounded
  batch per transaction. Metric definitions and unexpired buckets are never
  touched.
- Dry-run counts candidates without writing or advancing a watermark.

### Aggregation checkpoint and overlap

When aggregation is enabled, the raw cutoff is capped at:

```text
min(requested raw cutoff, bucket_start(aggregation checkpoint - aggregation overlap))
```

The bucket start matters because aggregation expands a window to whole metric
buckets; retaining from the overlap instant alone could still delete the first
part of a bucket that the next run recomputes. The overlap is still required to
recompute late-arriving observations. If no aggregation checkpoint exists yet,
raw pruning is skipped and reported as `aggregation_checkpoint_missing`. If
aggregation is disabled, the checkpoint guard is not requested by the scheduler
and raw retention follows its window.

### Incomplete aggregate rebuilds

After the first physical raw deletion for a metric, retention records the
cutoff in the existing `TimeSeriesAggregationRuns` table. An explicit
`AggregateTimeSeriesRange` whose bucket-expanded start precedes that watermark is
incomplete: replacing its aggregates would silently drop values that no longer
have raw facts. The rebuild omits that metric when other metrics are safe, and
returns `ErrTimeSeriesAggregationIncompleteHistory` when every eligible metric is
blocked. Incremental aggregation applies the same test per window: if a
retention watermark is ahead of the checkpoint (for example because aggregation
was disabled while retention ran), that metric is skipped for the affected
window and its aggregate rows are left intact. Existing aggregate rows are left
intact in both paths. No aggregate is ever rebuilt from pruned history.

## Cardinality recovery

Pruning a series' last live observation releases its membership, the active
series row, and (on PostgreSQL) its reservation slot. A later observation with a
new identity can therefore claim capacity without restarting the process or
weakening the configured limit. The same applies to dimension values. This is
the expected operational recovery path when a high-cardinality source rotates;
it is not a reason to raise limits.

## Observability

New Prometheus metrics:

- `crowler_timeseries_retention_runs_total{outcome}`;
- `crowler_timeseries_retention_rows_deleted_total{family}`;
- `crowler_timeseries_retention_skipped_metrics_total{reason}`;
- `crowler_timeseries_retention_budget_stops_total`;
- `crowler_timeseries_retention_run_duration_seconds`;
- `crowler_timeseries_aggregation_skipped_metrics_total{reason}`.

Labels are bounded reason codes and family names. Metric keys, URLs, source
IDs, dimensions, and raw values are never labels. `TimeSeriesRetentionResult`
reports processed/skipped metrics, candidates, deletions, watermark updates,
batch count, budget exhaustion, and up to 16 stable skip reasons.

## Rollout and rollback

Rollout:

1. Deploy 2.3.1 with `retention.enabled: false` (the default).
2. Confirm emitters and aggregation are healthy.
3. Set `dry_run: true` with the intended windows and inspect candidate counts.
4. Enable retention with conservative `batch_size`, `max_batches`, and
   `max_duration`.
5. Watch deleted-row counters, aggregation catch-up, and API latency.

Rollback:

- Set `retention.enabled: false` and restart the Events service. No schema or
  data rollback is needed.
- Restoring deleted raw observations requires the database backup; aggregate
  rows are retained according to their own window.
- Reverting the binary restores the previous release without schema changes.
- Do not drop `TimeSeriesAggregationRuns`: it also holds aggregation checkpoints
  and the retention watermarks that protect aggregate rebuilds.

## Known limitations

- A metric with a NULL retention policy (created directly through
  `UpsertTimeSeriesMetric` rather than synchronized from configuration) uses
  the policy passed to the retention call; automatic scheduled pruning passes
  the global windows.
- Retention cannot reconstruct the exact cutoff used by an out-of-band manual
  `DELETE`; the durable watermark covers deletions made through
  `PruneTimeSeriesRetention`.
- Aggregates whose raw history was pruned cannot be rebuilt from raw facts. This
  is intentional; the watermark blocks the destructive replacement. If retention
  outruns aggregation so that a bucket straddles a watermark, that bucket is not
  partially rebuilt: its existing aggregate is left as-is, retained suffix
  observations remain available through raw queries, and aggregation resumes at
  the next bucket boundary. The scheduled path prevents this by capping raw
  pruning at a bucket boundary; the case requires aggregation to have been
  disabled or misconfigured during pruning.
- SQLite retention is coordinated only within one process.
- On PostgreSQL, retention holds one dedicated connection for the retention and
  aggregation advisory leases and needs a second pool connection for each batch
  transaction. A pool quota below two will make passes wait and time out. The
  fleet quota logic normally reserves three administration connections; size the
  engine pool for at least two.
- MySQL is not an enabled runtime backend in 2.3.x; retention lease acquisition
  rejects it explicitly.
- `max_dimensions: 0` means "inherit the built-in default (10)" for configured
  metrics. It does not mean unlimited. Legacy NULL/zero policies still behave as
  "no dimension cap" at the emitter boundary.

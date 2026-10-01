# CROWler Fleets

A CROWler fleet is a set of specific CROWler portions (API, engine, VDI and Events) that are configured to run multiple replicas of themselves. Fleets are optional: a single CROWler instance can operate independently without any coordination.

Fleets are used to scale horizontally, improve availability, and provide redundancy.

When a fleet is configured (even if for a single portion of the entire cluster), the user must remember to enable heartbeat coordination in the configuration file. This is required to ensure that all fleet members are aware of each other and can coordinate their activities effectively.

The CROWler heartbeat is an event-driven mechanism that allows fleet members to communicate their status and share information about the overall state of the fleet. This coordination is essential for maintaining consistency and preventing conflicts between different instances of the CROWler.

An important note for users who deploy different CROWler's clusters in the same network: the heartbeat of each cluster is isolated and does not interfere with others. However it's essential to deploy different crowler-db instances for each cluster, as the heartbeat coordination relies on the database to store and share information about the fleet members.

## Fleet database connection budget

When heartbeat coordination is enabled, the effective configured PostgreSQL
maximum has three connections reserved for administration and health checks:
`effective configured max-open - 3 = fleet SQL-pool budget`. The Events
heartbeat coordinator owns the census and publishes the authoritative,
finalized report. It deterministically divides that budget among the active,
recognized API, engine, and Events instances (using normalized service type and
instance identity, with any remainder assigned in stable sorted order).

Consumers apply the quotas supplied by that report and never independently
recalculate them. A heartbeat round therefore converges the fleet on one
allocation; before the first valid report, each dynamic consumer uses the
conservative bootstrap quota of 1. Reducing a pool limit prevents excess new
admissions while preserving queries already in flight.

When heartbeats are disabled, configured static pool behavior is retained. This
coordination is **not** a distributed semaphore, lease, or request dispatcher:
it allocates local `database/sql` pool limits. Dedicated notification/listener
connections are separate connections and remain outside this SQL-pool budget.

## Measuring pool capacity

Do not derive the database limit from host CPU count. Use
`tests/perf/pool_sweep.py` after query-shape or query-amplification changes have
landed. The harness controls the engine, Events, and API pools independently,
runs every setting at least three times, and writes both raw samples and a
median CSV. Its workload command must report PostgreSQL CPU, completed-page
throughput, transaction latency, pool waits, aggregation catch-up time, and API
latency; a run with any missing signal is rejected.

For example, the following evaluates deliberately unequal capacities rather
than accidentally coupling the three services:

```shell
python3 tests/perf/pool_sweep.py \
  --setting 4:2:2 --setting 8:4:3 --setting 12:6:4 --setting 16:8:6 \
  --repeats 3 \
  --apply-command './deployment/apply-pool-setting-and-wait' \
  --workload-command './deployment/run-steady-workload-and-measure' \
  --output pool-sweep.csv
```

The apply command receives `POOL_SWEEP_ENGINE_POOL`,
`POOL_SWEEP_EVENTS_POOL`, and `POOL_SWEEP_API_POOL`. It must restart or
reconfigure the processes, wait for the reported quotas to match, and reset the
workload fixture. The workload command prints one JSON object shaped like
`tests/perf/pool_sweep_measurement.example.json`. Measure a steady interval long
enough to include an aggregation schedule and define catch-up as the time from
the final ingested observation until its last eligible bucket is materialized.
Use p95 for transaction and API latency, PostgreSQL process CPU averaged over
the interval, and deltas for completed pages and pool waits. Keep crawler
`workers` and `crawler.indexing_concurrency` fixed for a pool sweep; then sweep
indexing admission separately at the chosen pool capacity. This preserves the
three distinct controls: browser/crawler concurrency, persistence admission,
and SQL-pool capacity.

Choose the smallest setting before the knee of the throughput curve: increasing
the pool must produce a repeatable throughput or catch-up improvement without
materially worsening API latency. Treat rising PostgreSQL CPU or transaction
latency, increasing pool waits, stalled aggregation catch-up, or less than a
5% throughput gain as saturation/diminishing return. Compare medians and retain
the raw CSV with the deployment record; do not average away transient API or
aggregation starvation.

The shipped defaults remain intentionally conservative: `optimize_for: write`
selects a maximum of 10 and two idle connections per process; an unset/`none`
mode selects the explicit `database.max_conns` and `database.max_idle_conns`.
Crawler workers (3 in `config.default`) and synchronous persistence admission
(1) remain independent. A deployment should promote a measured setting through
its configuration rather than changing these defaults based only on core
count. Since this exercise does not change defaults or accepted ranges, no
configuration validation change is required.

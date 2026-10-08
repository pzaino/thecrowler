# Time-series cardinality identity and lifecycle contract

Status: implemented. Active identities are derived from normalized, transactional observation membership rows; PostgreSQL capacity is admitted through fine-grained reservation slots.

## Scope and current-state audit

The current PostgreSQL crawler guard answers prospective cardinality questions
by scanning `TimeSeriesObservations`. Its series existence query uses only
`source_id`, `index_id`, `entity_id`, `object_type`, `object_id`, and dimensions,
while its distinct-series query concatenates a different subset. Concatenation
also makes SQL `NULL` handling part of identity. Dimension values are counted
directly from JSON. These queries therefore do not implement the complete
logical series identity already represented by `TimeSeriesSeriesHash`.

Observation inserts are transactional and dedupe on `dedupe_key`, but there is
no durable cardinality accounting state. Retention physically deletes bounded
batches, including logically deleted rows. Queries and aggregation normally
ignore `deleted_at`, although no public observation logical-delete operation is
currently provided. Metric definitions are soft-deletable in the schema and
metric lookup excludes deleted definitions; observation and aggregate foreign
keys restrict physical metric deletion.

Source ownership differs across shipped schema histories: some definitions
use `ON DELETE CASCADE`, while PostgreSQL v1.09 used `ON DELETE SET NULL`.
Application `DeleteSource` issues a direct source delete and does not perform
time-series cleanup itself. Consequently, source deletion cannot safely update
future accounting merely by relying on the current foreign key. A migration
that implements this contract must normalize that behavior on every backend.

## Canonical identities

All identity hashes use SHA-256, lowercase hexadecimal output, and the existing
length framing (`decimal UTF-8 byte length`, `:`, bytes, `|`) for every part.
The first part is a versioned domain separator. Absence is distinct from an
empty string, JSON `null`, numeric zero, and the empty object. JSON is canonical
UTF-8 JSON: object keys sorted as in Go `encoding/json`, no insignificant
whitespace, strings escaped by that encoder, and numbers retaining the decoded
JSON number spelling. Hashes accelerate indexed lookup; equality of the
authoritative columns/canonical bytes must be checked after a hash match.

### Series identity

`series_identity_v1` is the framed SHA-256 of, in this exact order:

1. domain `timeseries-series-identity-v1`;
2. `metric_id` (present unsigned decimal; it is never absent);
3. every scope component, each encoded as `absent` or `present:<value>`:
   `information_seed_id`, `information_seed_candidate_id`, `source_id`,
   `source_information_seed_id`, `index_id`, `entity_id`, `subject_type`,
   `subject_id`, `object_type`, `object_id`, `correlation_rule_id`,
   `correlation_object_type_1`, `correlation_object_id_1`,
   `correlation_object_type_2`, and `correlation_object_id_2`;
4. dimensions encoded as `absent` when the column/map is absent, otherwise
   `present:` followed by canonical JSON (so `{}` is not absence).

This is the exact series counted by `max_series_per_metric`. Time, value,
provenance, dedupe key, and observation ID are deliberately excluded. The
existing `series_hash` has the same logical field set, but its current domain
and nested dimension-hash encoding are not to be silently reinterpreted as
this version: migration must calculate and store the versioned accounting key.

### Per-dimension-value identity

For each own key present in the top-level dimensions object, one
`dimension_value_identity_v1` is the framed SHA-256 of:

1. domain `timeseries-dimension-value-identity-v1`;
2. `metric_id` as unsigned decimal;
3. the dimension key as its exact UTF-8 string; and
4. the value's canonical JSON bytes, including JSON `null`.

Scope is intentionally excluded: `max_values_per_dimension` is per metric and
dimension key across all scopes. An absent key contributes no identity; a key
whose value is JSON `null` contributes one. Values of different JSON types are
different (`1`, `"1"`, and `true`). Dimension selectors are expected to yield
scalars; if historical data contains an array or object it is nevertheless
hashed as canonical JSON and counts as one value, so rebuild is deterministic.
The unhashed metric ID, dimension key, and canonical value must remain available
for collision verification (the value may be protected storage, not public API
output).

## Accounting model and transitions

`TimeSeriesObservationSeries` records one immutable `(observation_id, metric_id,
series_hash)` membership. `TimeSeriesObservationDimensions` records one immutable
`(observation_id, metric_id, dimension_key, value_hash)` membership for each
dimension. Their observation-bearing primary keys make retries idempotent. Active
identity rows exist exactly while at least one membership exists; deletion removes
the identity and its PostgreSQL reservation only after deleting the final
membership. The legacy `reference_count` columns remain fixed at one solely so
upgrades from schema 1.14 remain compatible; they are never accounting counters.

Observation insertion, membership insertion, reservation admission, logical or
physical deletion, identity replacement, and rebuild all execute in the caller's
transaction. A dedupe conflict creates no membership. Identity replacement claims
and adds the new membership before removing the old membership, so rollback cannot
expose partial state.

## Transaction and concurrency requirements

Observation mutation and all accounting deltas are one database transaction on
the same connection. Commit makes both visible; any error rolls both back.
There must be no post-commit best-effort accounting. The dedupe decision must
be known before incrementing, or increments must be conditional on the insert's
actual returned row. Batch insertion applies deltas only for rows actually
inserted and rolls the entire batch back on any non-dedupe error.

PostgreSQL admission uses bounded slot rows and `SKIP LOCKED`, rather than a metric-wide or dimension-value counter lock. Inserts sharing a dimension value add independent membership rows and take only compatible foreign-key locks on the immutable active identity. Hash matches compare authoritative identity bytes before membership insertion.

## Lifecycle policies

### Overflow

`max_dimensions` is a structural precondition, not a reference-count limit. An
observation with too many top-level keys is rejected before identity lookup,
regardless of the overflow policy, and changes neither observations nor
accounting.

Overflow is decided only for identities not already present. An observation
that references an existing series and existing dimension values is admissible
at the limit. Check series and all sorted dimension identities before mutation.

* `drop`: insert nothing and change no accounting state.
* `overflow_bucket`: replace the complete dimension object with exactly
  `{"overflow":"__overflow__"}`, recompute series and dimension identities,
  and evaluate/admit that transformed observation atomically. If the transformed
  identity is itself inadmissible, drop it; never recursively overflow.
* `hash`: retain the original dimensions and store the observation value using
  hash-only value storage, matching current policy intent. It does **not**
  collapse series or dimension identities and therefore does not reduce either
  cardinality. The over-limit insert is an explicit bypass and its identities
  and reference counts must still be recorded. Operators must not treat `hash`
  as a hard cardinality cap.

The check order is series first, then dimension keys in UTF-8 byte order.
Admission is all-or-nothing; one overflowing dimension applies the single
configured policy to the observation.

### Retention and logical deletion

Raw retention selects candidate observation IDs as today, but each physical
batch must delete observations and decrement accounting in one transaction.
It must distinguish live from already logically deleted rows. Aggregate
retention never affects raw-observation cardinality. Logical deletion, where an
integration uses it, removes cardinality immediately; later retention is only
physical reclamation. Reaggregation continues to derive aggregates solely from
live observations.

### Source deletion

`Source` remains the ownership boundary. Source deletion must, in one
transaction, lock the source/metric boundaries, delete all observations and
aggregates with that `source_id`, apply accounting decrements for live
observations, and then delete the Source. This explicit workflow is required on
all backends; do not depend on `CASCADE` or `SET NULL`. Rows owned through
another Source are untouched even when they refer to the same artifact.
Failure rolls back the entire source deletion. Large-source cleanup may use a
tombstoned source plus restartable batches only if new writes are fenced and
the Source row is retained until every batch and final reconciliation commits.

### Metric constraints

Disabling or logically deleting a metric forbids new observations but does not
remove its observations or accounting. Retention and source cleanup continue
to process it. A metric may be physically deleted only when it has no raw
observations (live or logically deleted), aggregates, aggregation/checkpoint
state, series-accounting rows, or dimension-accounting rows. The operation must
check these conditions under the metric lock and fail with a conflict rather
than cascade. Reusing a deleted metric key revives the same metric ID only when
the existing immutable value type remains compatible, as today.

## Administrative rebuild and reconciliation

The sole source of truth for rebuilding cardinality accounting is retained,
non-deleted `TimeSeriesObservations`; aggregates, old accounting rows, config
limits, and Source existence are not inputs.

1. Enter accounting maintenance mode and fence observation insert, logical
   delete/undelete, identity update, raw retention, and Source deletion. Reads
   and aggregate maintenance may continue.
2. At one consistent database snapshot, scan
   `TimeSeriesObservations WHERE deleted_at IS NULL` in bounded
   `observation_id` order. Recompute the versioned series identity from
   `metric_id`, every stored scope field, and canonical dimensions. Expand each
   present top-level dimension key into its versioned value identity.
3. Accumulate counts into shadow tables with uniqueness constraints on the
   authoritative identity plus hash. Reject malformed dimensions, hash/column
   collisions, counter overflow, or missing metric references; do not silently
   skip a row. Persist a snapshot/checkpoint identifier and scanned-row count.
4. Independently reconcile invariants: sum of series reference counts equals
   the number of live retained observations; for each metric/dimension key, the
   sum of dimension reference counts equals the number of live observations
   containing that key; every count is positive; and a second grouped scan has
   exactly the same keys and counts. Compare stored observation `series_hash`
   only as diagnostic information because the versioned key is recomputed from
   authoritative columns.
5. Under the same mutation fence, atomically swap or generation-flip the
   validated shadow tables into service. Record counts, mismatches, start/end
   observation IDs, snapshot/generation, and completion time in an audit row.
   On failure, leave current accounting active and shadow state available for
   diagnosis.
6. Release the fence. Run a read-only reconciliation by repeating the snapshot
   grouping and diffing it against active accounting. Repair requires another
   shadow rebuild and atomic cutover, never ad hoc counter edits.

This procedure intentionally forgets physically retained-out and logically
deleted observations. If a Source was deleted incorrectly with `SET NULL`, its
still-live observations remain in the rebuilt identities exactly as stored;
rebuild must not infer former ownership or delete them.

## Required implementation tests for the follow-up card

Tests must cover deterministic identity across map order and all supported
backends; absent versus empty/null/zero distinctions; all scope fields;
dimension key/type distinctions; synthetic hash-collision verification; first
insert, repeated insert, and final-reference removal; dedupe with no increment;
logical-delete idempotence and physical deletion afterward; identity-moving
entity backfill; mixed live/deleted retention; Source cleanup isolation and
rollback; disabled/soft-deleted/hard-deleted metric constraints; every overflow
policy; concurrent last-slot admission; transaction rollback; and rebuild plus
reconciliation from only retained live observations.

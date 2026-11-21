# Information Seed Claim Indexes

The Information Seed claim path uses these indexes:

- `idx_informationseed_claim_queue` on
  `(disabled, status, priority, created_at, information_seed_id)`
- `idx_informationseed_lease_expiry` on
  `(status, disabled, lease_expires_at)`
- `idx_informationseed_next_retry` on
  `(status, disabled, next_retry_at)`
- `idx_informationseed_claim_token` on `(claim_token)`

The claim predicates combine a small status set, the disabled flag, priority,
lease expiry, retry eligibility and deterministic ordering. `LOWER(TRIM(status))`
is retained for legacy compatibility; do not assume an index-only scan on very
small tables, because PostgreSQL may correctly choose a sequential scan.

To collect plan evidence on a populated test database, never production data:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT information_seed_id
FROM InformationSeed
WHERE COALESCE(disabled, FALSE) = FALSE
  AND COALESCE(attempts, 0) < 5
  AND LOWER(TRIM(status)) IN ('new', 'pending')
  AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
ORDER BY created_at, information_seed_id
LIMIT 2;
```

Repeat with `status = 'error'`, an elapsed `next_retry_at`, and a `priority`
filter. Record plan shape, actual rows, and timing for the same fixture size
before and after adding or changing indexes. A sequential scan on a tiny fixture
is not a regression by itself; correctness and lock ordering always take
precedence over a microbenchmark.

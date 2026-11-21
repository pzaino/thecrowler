# WP-04C2E PostgreSQL CI Integration

## Problem

`TestPostgresMCPMutationDurableStore` requires all of:

```text
CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1
CROWLER_MCP_POSTGRES_DSN=<disposable PostgreSQL URL>
```

The existing `postgresql-integration.yml` workflow supplied neither variable.
The Go test therefore skipped, while the workflow could still appear green.
That was not evidence that the MCP durable-store path ran.

## Implemented CI step

`.github/workflows/postgresql-integration.yml` now has a dedicated step before
the broad PostgreSQL suite:

```yaml
- name: Run MCP mutation PostgreSQL integration
  timeout-minutes: 10
  run: |
    set -euo pipefail
    export CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1
    export CROWLER_MCP_POSTGRES_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:5432/${POSTGRES_DB}?sslmode=disable"
    log="$(mktemp)"
    trap 'rm -f "$log"' EXIT
    go test -tags=integration ./pkg/database -run '^TestPostgresMCPMutationDurableStore$' -count=1 -v 2>&1 | tee "$log"
    grep -F -- '=== RUN   TestPostgresMCPMutationDurableStore' "$log"
    grep -F -- '--- PASS: TestPostgresMCPMutationDurableStore' "$log"
    if grep -Fq -- '--- SKIP' "$log"; then
      echo "MCP mutation PostgreSQL integration was skipped" >&2
      exit 1
    fi
```

The workflow's job already provides a `postgres:17` service with
`POSTGRES_DB=SitesIndex`, `POSTGRES_USER=postgres` and
`POSTGRES_PASSWORD=postgres`, plus matching runner-level environment variables.
The step reuses only those disposable service values. No repository secret,
shared cluster, production runtime DSN or remote database is referenced.

The test itself still requires the explicit opt-in and DSN, creates a randomly
named child database, applies the additive migration/clean-install block,
creates its sentinel inside that child database, closes the pools and drops only
that database. A missing `CREATEDB` privilege or connection failure fails with
a setup error; it does not fall back to an arbitrary database. Cleanup failure
is a test error.

The RUN/PASS grep makes a silent skip fail the step. The explicit `--- SKIP`
check adds the same guarantee for a future `t.Skip` path.

## Local simulation

I ran the exact step command against a disposable local `postgres:17`
container with the same database, user and password values.

- Log: `/home/pzaino/tmp/opencode/wp04c2e-ci-mcp-pg.log`
- Result:

```text
=== RUN   TestPostgresMCPMutationDurableStore
--- PASS: TestPostgresMCPMutationDurableStore (1.46s)
PASS
ok  	github.com/pzaino/thecrowler/pkg/database	1.464s
```

- `grep` confirmed `=== RUN` and `--- PASS`.
- `grep` confirmed no `--- SKIP`.
- The opt-in/DSN guard tests
  (`TestMCPPostgresDisposableTargetRequiresExplicitOptIn`,
  `TestMCPPostgresDisposableTargetRequiresDSN`) pass in the focused suite.

## CI status

The patch was not pushed, so GitHub did not run this workflow for WP-04C2E.
There is no job URL or GitHub `RUN/PASS` evidence to report. The local
simulation proves the command and assertions are viable; it is **not** a
substitute for a GitHub run. The maintainer must push/rerun the workflow and
confirm the dedicated step executed before the CI gate is called green.

The Information Seed Compose suite remains a separate opt-in integration suite.
It is not started or modified by this step.

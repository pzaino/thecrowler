# Test Policy for TheCROWler

## Introduction

This document outlines the testing policy for "TheCROWler" project. The
objective is to maintain high code quality, functionality, and reliability
of the application.

## Testing Tools

- Go Test: Primary tool for running tests.
- Selenium WebDriver: For browser-based tests.

## Test Types

- Unit Testing: To test individual components in isolation.
- Integration Testing: To ensure modules work together as expected.
- Functional Testing: To verify the software performs its intended
 functions.
- Regression Testing: To confirm that a recent program change has not
 adversely affected existing features.
- Browser Compatibility Testing: Using Selenium WebDriver to ensure
 compatibility across different web browsers.

## Test Coverage

Strive for >80% test coverage.
Include both positive and negative test scenarios.

## Test Data

- Use a combination of real and synthetic data.
- Ensure data privacy and compliance with relevant regulations.

## Code Review and Merge Policy

- All new code must include relevant tests.
- Pull requests must pass all tests before merging.
- Regular code reviews to ensure adherence to testing standards.

## Continuous Integration

- Integrate with a CI tool (e.g., GitHub Actions) for automated testing.
- Tests should run on every commit to the main branch and all pull requests.

## Reporting and Documentation

Document all tests and update regularly.
Use tools for clear reporting of test results.
Track bugs and fixes in a dedicated system (e.g., GitHub Issues).

## Responsibility

All contributors are responsible for writing and maintaining tests for their
code. Project maintainers will oversee adherence to this policy.

## Policy Review

This policy will be reviewed and updated regularly to adapt to project needs
 and technological advancements.

## Hermetic information-seed browser discovery

The default information-seed browser-discovery tests use an in-memory fake
WebDriver with the production action executor and scraper. They do not require
network access and never contact a public search engine:

```bash
go test ./pkg/infoseed/... ./pkg/browser/... ./pkg/scraper/...
```

A real Selenium/Chrome check is separately compiled with the `integration`
build tag and must also be explicitly enabled. It serves
`pkg/infoseed/testdata/browser_search/` fixture pages from a loopback HTTP server,
so the browser still uses only a repository-owned fixture:

```bash
THECROWLER_E2E_WEBDRIVER=1 go test -tags=integration ./pkg/infoseed -run BrowserSearch
```

The test expects Selenium at `http://127.0.0.1:4444/wd/hub` by default. Set
`THECROWLER_E2E_WEBDRIVER_URL` to use another WebDriver endpoint. Selenium must
run where it can reach the loopback fixture server created by the Go test (for
example, as a local process or with compatible host networking). When the
environment variable is absent, or Selenium/Chrome cannot create a session,
the optional test skips and reports the exact prerequisite that is missing.

## PostgreSQL integration tests

Tests that exercise PostgreSQL session semantics are compiled behind the
`integration` build tag and require an explicitly enabled, already initialized
TheCROWler PostgreSQL database:

```bash
THECROWLER_POSTGRES_INTEGRATION=1 go test -tags=integration ./pkg/database -run PostgresTimeSeriesAggregation
```

The harness uses the repository's `DOCKER_POSTGRES_DB_HOST`,
`DOCKER_POSTGRES_DB_PORT`, `DOCKER_POSTGRES_DB_NAME`,
`DOCKER_POSTGRES_DB_USER` (or `DOCKER_POSTGRES_USER`),
`DOCKER_POSTGRES_PASSWORD`, and `DOCKER_POSTGRES_SSL_MODE` conventions. Defaults
match the local PostgreSQL container (`127.0.0.1:5432`, database `SitesIndex`,
user/password `postgres`, and disabled SSL). The database must have been
initialized with `pkg/database/postgresql-setup.pgsql`.


## MCP and goal-evidence tests

Read-only stdio MCP tests use deterministic fixture servers and do not require
an external MCP service. Changes to MCP execution must verify all of the
following:

- an MCP `tools/call` response with `IsError=true` is reported as a failed tool
  call, preserves bounded diagnostics, and never creates successful
  `tool_observation` evidence;
- the SDK envelope, not a remote text or structured field named `is_error`,
  determines execution success;
- MCP startup, protocol negotiation, discovery, model calls, tool calls and
  cleanup share one authoritative run deadline; a short multi-server
  initialization budget must stop before provider execution, start no further
  server, and clean up every process already started;
- denied, failed, timed-out, malformed and unsupported MCP calls cannot satisfy
  `PredicateEvidenceExists`, while a successful MCP call still can;
- native read-only tools and WP-03 proposal/approval/scheduling behaviour
  remain unchanged.

The live Ollama and MCP tests are opt-in and independently reported. They must
use a real configured Ollama endpoint and model, and must not be treated as
passing because the package compiled or a test was skipped.

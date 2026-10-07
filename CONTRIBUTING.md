# Contributing to The CROWler

Thank you for your interest in contributing to **The CROWler**.

The CROWler is a distributed content-discovery and intelligence framework. Its code operates across databases, networks, browsers, external services, plugins, rulesets, agents, and concurrent workers. Contributions therefore need to be not only functional, but also maintainable, testable, secure, predictable, and safe under failure.

Contributions are welcome in many forms, including:

- Bug fixes
- New features
- Documentation and tutorials
- Rulesets
- JavaScript plugins
- Tests and test infrastructure
- Database improvements
- Performance improvements
- Security improvements
- API and tooling improvements
- Deployment and containerization work
- Code review and issue analysis

Before making substantial changes, review the documentation relevant to the subsystem you intend to modify.

Useful starting points include:

- [README.md](README.md)
- [Documentation](doc/)
- [Test Policy](doc/test_policy.md)
- [Ruleset Architecture](doc/ruleset_architecture.md)
- [Ruleset Reference](doc/ruleset_reference.md)
- [Plugin Documentation](doc/plugins.md)
- [Database Architecture](doc/database_architecture.md)
- [Security Policy](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)

Contributions should follow the architecture, schemas, interfaces, security assumptions, and engineering principles documented by the project.

---

## Development Model

The CROWler uses two primary branches:

- **`develop`**: active development and integration branch.
- **`main`**: stable integration branch used for wider testing and releases.

New work should normally start from `develop`.

Create a dedicated branch for your work and open pull requests against `develop`.

Changes are promoted from `develop` to `main` after integration testing and review. Releases are tagged from `main`.

Do not base new feature development on `main` unless there is a specific reason to do so.

---

## Development Environment

Use the Go version declared in `go.mod`.

The repository uses pre-commit hooks for formatting, tests, security checks, secret detection, shell validation, and commit-message validation.

Install `pre-commit` using the appropriate method for your operating system.

For example:

```bash
pip install pre-commit
```

On macOS:

```bash
brew install pre-commit
```

Install the hooks from the repository root:

```bash
pre-commit install
```

Also install the commit-message hook:

```bash
pre-commit install --hook-type commit-msg
```

Verify your setup with:

```bash
pre-commit run --all-files
```

Useful Go development tools include:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
```

and, when performing extended local linting:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

The repository configuration is authoritative regarding which checks are currently automated.

---

# Engineering Principles

The CROWler is intended to be extended and operated in environments ranging from small installations to distributed deployments. Code should therefore be designed around the following principles.

### Maintainability

Code must be understandable, auditable, and maintainable by developers other than its original author.

Prefer straightforward implementations over clever ones.

Avoid hidden behaviour, surprising side effects, unnecessary abstraction, and unnecessary indirection.

Write your code to be secure by default, minimizing potential vulnerabilities and adhering to best security practices.

### Extensibility

New functionality should fit the existing architecture and reuse existing interfaces where practical.

Avoid designing a solution only around the immediate use case if a small amount of additional structure can make it reusable without introducing unnecessary complexity.

Do not create a new abstraction when an existing one already expresses the required behaviour correctly.

### Reliability

Failure modes must be explicit and predictable.

Code interacting with databases, networks, browsers, files, plugins, external APIs, or other fallible systems must assume those dependencies can fail, timeout, disconnect, return malformed data, or behave unexpectedly.

### Security

Treat all external input as untrusted.

This includes:

- HTTP responses
- URLs
- HTML
- JavaScript
- API payloads
- Rulesets
- Configuration files
- Plugin output
- Agent output
- Database content originating from external systems
- Environment variables
- Files
- Email data
- Network discovery results

Validate data at trust boundaries rather than relying on downstream code to detect invalid state.

### Performance

Performance matters, particularly in crawling, indexing, database, browser, and concurrent-worker paths.

However, performance optimizations must not weaken correctness, security, maintainability, or observability.

Measure before introducing complex optimization.

### Portability

Avoid unnecessary assumptions about CPU architecture, filesystem layout, operating system behaviour, container runtime, network topology, or deployment environment.

Where platform-specific behaviour is required, isolate it clearly.

### Least Complexity

Code is a maintenance and security liability.

Prefer the smallest solution that correctly solves the problem.

Avoid unnecessary:

- dependencies;
- global state;
- goroutines;
- channels;
- configuration options;
- public APIs;
- duplicated code;
- reflection;
- background workers;
- database queries;
- retries;
- implicit conversions; and
- compatibility paths.

---

# Go Engineering Standards

## Follow idiomatic Go

Follow established Go conventions and the existing conventions in the CROWler codebase.

Code must be formatted with:

```bash
gofmt
```

and imports should be normalized using:

```bash
goimports
```

The repository pre-commit hooks perform these operations automatically.

Do not manually format Go code in a style that conflicts with `gofmt`.

---

## Errors

Functions that can fail should normally return an `error`.

Errors should provide enough context for the caller or operator to understand what operation failed.

Prefer wrapped errors where the underlying cause remains useful:

```go
return fmt.Errorf("loading ruleset %q: %w", path, err)
```

Do not silently discard meaningful errors.

Do not convert recoverable errors into panics.

`panic` should be reserved for conditions that represent impossible internal invariants, initialization failures where execution cannot meaningfully continue, controlled runtime interruption mechanisms, or test code specifically exercising panic behaviour.

User input, network failures, malformed data, missing files, database failures, browser failures, and plugin failures are normally errors, not panics.

---

## Context and Cancellation

Long-running or externally blocking operations should accept or propagate `context.Context` where appropriate.

This includes operations involving:

- database queries;
- HTTP requests;
- browser sessions;
- plugins;
- agents;
- external services;
- retries;
- waits;
- event processing; and
- worker pipelines.

Do not replace an existing request or operation context with `context.Background()` unless deliberately detaching the operation is required and documented.

Cancellation should stop unnecessary work promptly.

Timeouts should be bounded and configurable where appropriate.

Never create an unbounded retry loop around a failing external dependency.

---

# Concurrency and Goroutine Safety

The CROWler performs significant concurrent work. Concurrency correctness is therefore a core engineering requirement.

Any contribution involving goroutines, shared state, worker pools, database claims, schedulers, caches, browser leases, events, channels, or asynchronous callbacks must consider:

- data races;
- ownership of shared state;
- cancellation;
- goroutine leaks;
- channel lifecycle;
- deadlocks;
- lock ordering;
- starvation;
- duplicated work;
- idempotency;
- atomicity;
- retry behaviour;
- timeout behaviour; and
- shutdown semantics.

Shared mutable state must have a clearly identifiable synchronization strategy.

Do not rely on timing to guarantee correctness.

Do not use sleeps as synchronization primitives.

Where concurrency is materially changed, contributors should run relevant tests with the Go race detector where practical:

```bash
go test -race ./path/to/affected/package/...
```

For broad concurrency changes, running:

```bash
go test -race ./...
```

is strongly recommended when the development environment can support it.

A race detector failure is a correctness defect even when ordinary tests pass.

---

# Resource Management

Resources must have explicit ownership and cleanup behaviour.

Examples include:

- database transactions;
- database rows;
- HTTP response bodies;
- files;
- sockets;
- browser sessions;
- WebDriver instances;
- timers;
- tickers;
- goroutines;
- subprocesses;
- plugin runtimes; and
- temporary files.

Use `defer` where it makes ownership clearer and does not create unacceptable resource retention.

Every started transaction must have a deterministic commit or rollback path.

Every goroutine should have a defined termination condition.

Every external resource should have a defined cleanup path.

Do not rely on process termination as normal resource cleanup.

---

# Database Engineering

Database changes require particular care because the CROWler uses the database as durable shared state between multiple components.

## Transactions

Operations that must be atomic should use transactions.

Transaction boundaries should match the logical consistency boundary of the operation.

Avoid performing slow network calls, browser operations, or other unrelated blocking work while holding a database transaction unless the design specifically requires it.

Rollback paths must remain safe after partial failure.

## SQL

Always use parameterized queries for untrusted or variable values.

Do not construct SQL by concatenating external input.

Dynamic identifiers such as table names or sort expressions require explicit allow-list validation because they generally cannot be safely parameterized in the same way as ordinary values.

## Concurrency

Database code must account for multiple CROWler instances operating concurrently.

Do not assume a row is exclusively owned merely because it was read first.

Where claiming, scheduling, deduplication, cardinality, or idempotency is involved, use database semantics that make the intended concurrency guarantee explicit.

## Migrations

Database schema changes must include the appropriate migration path.

Migrations should be:

- deterministic;
- safe to apply in the documented upgrade sequence;
- tested;
- consistent with the setup schema; and
- idempotent where the migration design requires repeatability.

Do not modify an old released migration to represent a new schema state. Add the appropriate new migration.

When a migration changes runtime assumptions, update the corresponding Go code, tests, and documentation in the same contribution.

---

# Schema and Configuration Engineering

The CROWler uses JSON Schema as an executable contract for multiple configuration formats.

Schemas are not merely documentation.

Changes involving configuration, sources, rulesets, agents, events, or other schema-backed structures must keep the following consistent:

- JSON Schema;
- Go structures;
- validation logic;
- runtime behaviour;
- dispatch/executor logic;
- examples;
- tests; and
- documentation.

Do not add runtime behaviour that cannot be represented by the corresponding schema.

Do not add schema fields or enum values that have no runtime implementation.

Where a schema defines a closed set of values, the runtime implementation must match that set.

For dispatch-style features, such as rule or action types, the supported schema values and runtime executors should form a closed contract.

If one side changes, update and test the other side.

Unknown fields should not be silently accepted where the schema intentionally uses `additionalProperties: false`.

Do not weaken validation merely to accept malformed examples or legacy configuration.

If backward-compatibility handling is deliberately supported, keep it isolated, documented, and tested.

---

# Ruleset Engineering

The ruleset architecture is a public contract.

When modifying scraping, action, detection, crawling, wait-condition, post-processing, selector, or related semantics:

1. update the schema;
2. update the Go representation where necessary;
3. update validation;
4. update runtime execution;
5. add positive tests;
6. add negative tests;
7. update canonical examples; and
8. update the relevant documentation.

Ruleset behaviour should be deterministic given the same page state, ruleset, configuration, and runtime inputs, except where randomness is an explicit part of the rule.

Do not invent undocumented aliases, implicit fields, or fallback behaviour merely to make a malformed ruleset execute.

Prefer failing validation clearly over silently interpreting ambiguous configuration.

---

# Plugin Engineering

The CROWler supports JavaScript plugins in several runtime environments.

Plugin changes must respect the JavaScript runtime documented for that plugin type.

In particular, do not assume that Node.js APIs, npm modules, browser globals, or modern ECMAScript features are available unless that specific plugin runtime documents them.

Engine-side plugins use the embedded JavaScript runtime and must remain compatible with its documented ECMAScript constraints.

VDI plugins execute in a browser-oriented environment and have different capabilities.

Plugin interfaces should:

- validate parameters;
- bound execution time;
- respect cancellation;
- bound output size where applicable;
- avoid leaking credentials;
- return useful errors;
- avoid uncontrolled external side effects; and
- maintain compatibility with the documented plugin API.

Plugin execution is a trust boundary. Treat plugin-produced data as untrusted until validated for the receiving subsystem.

---

# External Input and Network Safety

The CROWler intentionally interacts with external systems.

Code that parses or acts upon external input must consider malformed, hostile, oversized, or unexpected data.

Where applicable, enforce:

- maximum response sizes;
- request timeouts;
- bounded retries;
- URL validation;
- protocol restrictions;
- allow-lists;
- authentication boundaries;
- TLS requirements;
- rate limits;
- resource limits; and
- redaction of secrets.

Do not log authentication tokens, passwords, session cookies, API keys, private credentials, or equivalent secrets.

Diagnostic and event data must preserve useful troubleshooting information without persisting sensitive values unnecessarily.

---

# Browser and Automation Engineering

Browser automation is inherently asynchronous and failure-prone.

Changes involving Selenium, WebDriver, VDI management, browser actions, or Human Behaviour Simulation must consider:

- browser/session loss;
- stale elements;
- navigation races;
- redirects;
- timeout handling;
- window and frame state;
- cancellation;
- retries;
- VDI lease cleanup; and
- source-specific timing.

Avoid adding fixed sleeps when a state-based wait can be used.

Where randomized timing is intentionally part of Human Behaviour Simulation, keep it explicit and bounded.

Automation code must not assume that an element remains valid after navigation or major DOM mutation.

---

# Security Engineering

Security is part of correctness.

Contributions should consider, where applicable:

- authentication;
- authorization;
- injection attacks;
- SQL injection;
- command injection;
- path traversal;
- SSRF;
- unsafe URL handling;
- unsafe deserialization;
- XSS in browser/plugin contexts;
- CSRF in applicable browser/plugin contexts;
- secret exposure;
- credential lifecycle;
- cryptographic correctness;
- privilege boundaries;
- race conditions;
- denial-of-service conditions;
- resource exhaustion;
- excessive recursion;
- uncontrolled retries;
- unsafe concurrency; and
- dependency vulnerabilities.

Security controls must not depend solely on UI behaviour or documentation.

Enforce important boundaries in code.

Security vulnerabilities should be reported according to [SECURITY.md](SECURITY.md), not disclosed through a public issue containing exploitable details.

---

# Compatibility and Public Contracts

Backward compatibility is desirable, but it is not absolute.

The CROWler is an evolving platform, and occasionally a breaking change may be the correct engineering decision.

However, breaking changes must be deliberate.

Public contracts include, among other things:

- API routes and response structures;
- database schemas;
- configuration fields;
- ruleset schemas;
- plugin APIs;
- event formats;
- agent formats;
- environment variables;
- command-line interfaces; and
- documented runtime semantics.

A breaking change should normally include:

- clear justification;
- updated version markers where applicable;
- migration or upgrade instructions;
- updated schemas;
- updated examples;
- updated tests;
- updated documentation; and
- removal of obsolete compatibility paths when they are no longer supported.

Do not accidentally introduce a breaking change as a side effect of an unrelated refactor.

---

# Dependencies and Supply-Chain Safety

Avoid unnecessary external dependencies.

Before introducing a new dependency, consider:

- whether the standard library or an existing dependency already solves the problem;
- project maintenance activity;
- security history;
- transitive dependency cost;
- license compatibility;
- binary-size impact;
- supported architectures;
- whether the dependency is needed at runtime; and
- whether the dependency expands a privileged or network-facing attack surface.

Dependency upgrades should be deliberate and reviewable.

Avoid mixing broad dependency upgrades with unrelated feature work.

In particular, do not use broad dependency-update commands such as:

```bash
go get -u ./...
```

as part of an unrelated change and commit the resulting dependency graph without reviewing each material update.

When changing dependencies:

```bash
go mod tidy
```

should leave `go.mod` and `go.sum` in the expected state.

Run vulnerability checks where appropriate:

```bash
govulncheck ./...
```

Dependency-related CI and repository security tooling may perform additional checks.

---

# Logging and Observability

Logs should help operators understand what the system is doing without exposing sensitive information.

Use the project's logging facilities and existing log-level conventions.

Avoid:

- logging secrets;
- logging entire untrusted payloads unnecessarily;
- excessive logs in hot loops;
- ambiguous error messages;
- duplicate logging at every stack layer.

When an error is propagated to a caller that will log it, avoid automatically logging the same error at every intermediate layer unless the additional context is operationally useful.

Metrics and events should have stable semantics.

Do not change the meaning of an existing metric or event field without treating it as a compatibility change.

---

# Testing Standards

All meaningful code changes should include appropriate tests.

The project's testing policy is documented in [doc/test_policy.md](doc/test_policy.md).

Tests should cover both successful and failing behaviour.

Depending on the subsystem, appropriate tests may include:

- unit tests;
- table-driven tests;
- validation tests;
- negative tests;
- regression tests;
- concurrency tests;
- database integration tests;
- browser integration tests;
- schema-contract tests; and
- end-to-end tests.

A regression fix should normally include a test that fails without the fix and passes with it.

Tests should be deterministic.

Do not make ordinary unit tests depend on public Internet services.

Use repository-owned fixtures, mocks, fakes, local servers, or explicitly enabled integration environments.

Do not hide flaky behaviour by adding arbitrary sleeps or excessive retries.

---

## Standard Local Validation

Before submitting a significant Go change, run at minimum:

```bash
go build ./...
go test ./...
pre-commit run --all-files
```

For code affecting concurrency, also run the race detector on the relevant packages:

```bash
go test -race ./path/to/package/...
```

For dependency or security-sensitive changes:

```bash
govulncheck ./...
```

For PostgreSQL changes, run the relevant PostgreSQL integration tests described in [doc/test_policy.md](doc/test_policy.md).

For browser-related changes, run the relevant hermetic tests and, when appropriate, the explicitly enabled Selenium integration tests documented there.

The exact required test set depends on the subsystem changed.

Passing `go test ./...` is not sufficient justification for skipping integration tests when the behaviour being modified only exists at an integration boundary.

---

# Code Coverage

Coverage is a tool for identifying missing tests, not a substitute for meaningful tests.

The project aims for strong test coverage and the existing test policy recommends coverage above 80%.

Do not add low-value tests solely to increase the numerical coverage percentage.

Critical parsing, validation, concurrency, security, transaction, and compatibility behaviour should receive explicit tests even when aggregate coverage is already high.

---

# Documentation Requirements

Update documentation when behaviour changes.

Documentation updates are required when changing:

- public APIs;
- configuration;
- environment variables;
- rulesets;
- schema fields;
- database behaviour;
- plugin APIs;
- agent behaviour;
- event contracts;
- installation steps;
- deployment assumptions;
- CLI arguments; or
- externally visible semantics.

Examples should be schema-valid and reflect actual runtime behaviour.

Do not document functionality that the implementation does not support.

When documentation and code disagree, treat the disagreement as a defect.

---

# Commit Standards

The repository uses conventional commit-message validation.

Use concise commit messages that explain what changed.

Examples:

```text
fix(database): prevent duplicate source claims
```

```text
feat(ruleset): add explicit action validation
```

```text
test(browser): cover navigation retry handling
```

```text
docs(contributing): document concurrency requirements
```

Keep unrelated changes in separate commits where practical.

Do not mix formatting changes, dependency upgrades, major refactoring, and functional changes into one commit unless they are inseparable.

---

# Pull Requests

Pull requests should normally target `develop`.

A pull request should explain:

- the problem being solved;
- the selected approach;
- important design decisions;
- affected components;
- compatibility implications;
- security implications;
- concurrency implications where applicable;
- database or migration implications;
- schemas or public contracts changed;
- tests performed;
- relevant integration tests performed;
- known limitations;
- follow-up work; and
- related issues.

Small pull requests are easier to review correctly than large unrelated collections of changes.

Substantial architectural changes should be discussed before investing significant implementation effort.

All required CI checks must pass before merge.

Review comments should be resolved technically rather than cosmetically. If a reviewer identifies a correctness or safety concern, understand the underlying issue rather than making the smallest possible change merely to silence the comment.

---

# Review Standard

Code review should evaluate more than whether the change appears to work.

Reviewers should consider:

- correctness;
- failure behaviour;
- security;
- maintainability;
- concurrency;
- transaction boundaries;
- resource cleanup;
- cancellation;
- compatibility;
- schema consistency;
- test quality;
- documentation;
- performance;
- external dependencies; and
- operational impact.

A contribution that works in the happy path but introduces unsafe failure behaviour is not ready to merge.

---

# AI-Assisted and AI-Generated Contributions

The CROWler does not prohibit the use of AI-assisted development tools.

AI systems may be used for:

- code generation;
- code review;
- documentation;
- test generation;
- debugging;
- analysis;
- refactoring;
- ruleset development;
- plugin development; and
- other development activities.

However, the contributor remains responsible for everything they submit.

Using an AI system does not transfer responsibility for correctness, security, licensing, provenance, or maintainability to the AI provider.

Before submitting AI-assisted material, the contributor must:

1. understand the submitted code or documentation well enough to explain and maintain it;
2. verify that it is technically correct;
3. verify that it matches the CROWler architecture and public contracts;
4. verify that it does not invent nonexistent APIs, schema fields, rules, configuration options, or runtime behaviour;
5. test it to the same standard expected for manually written code;
6. review it for security vulnerabilities;
7. review it for concurrency and resource-lifecycle problems where applicable;
8. verify dependency and licensing implications;
9. ensure that secrets, credentials, confidential information, private source code, or restricted data were not improperly provided to an external AI service; and
10. take responsibility for the final contribution.

Material AI assistance should be disclosed in the pull request when it produced a substantial portion of the implementation or materially influenced the architecture or design.

Exact prompts or complete AI transcripts are not normally required.

AI-generated code should never be merged merely because it compiles or passes a limited test suite.

AI tools are development aids, not substitutes for engineering understanding or review.

---

# Reporting Bugs

Use GitHub Issues for ordinary bug reports.

A useful bug report should include:

- a concise description;
- the CROWler version or commit;
- operating system and architecture;
- relevant deployment model;
- relevant configuration with secrets removed;
- steps to reproduce;
- expected behaviour;
- actual behaviour;
- relevant logs;
- whether the issue is reproducible;
- any suspected regression point; and
- any troubleshooting already performed.

Never post credentials, access tokens, private keys, cookies, or other secrets in an issue.

Security vulnerabilities should be reported according to [SECURITY.md](SECURITY.md).

---

# Finding Work

Useful ways to contribute include:

- reviewing open issues;
- reviewing pull requests;
- searching for `TODO` items;
- improving tests;
- improving documentation;
- adding regression coverage;
- improving validation;
- simplifying complex code;
- reducing duplication;
- improving concurrency safety;
- improving resource cleanup;
- improving error handling; and
- identifying mismatches between schemas, documentation, and runtime behaviour.

Changes that reduce complexity while preserving behaviour are valuable contributions.

---

# Contributor Responsibility

Contributors are responsible for the code and other material they submit.

Do not submit code, documentation, generated material, or other content if you do not have the right to contribute it under the project's license.

Do not submit confidential or proprietary material without authorization.

Any contribution accepted into the project is licensed under the same Apache License 2.0 terms that apply to the project.

See [LICENSE](LICENSE) for the authoritative license terms.

---

# Code of Conduct

Participation in the CROWler community is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

Technical disagreements should focus on facts, evidence, design trade-offs, reproducible behaviour, and project requirements.

---

# Final Engineering Checklist

Before considering a contribution complete, ask:

- Does it solve the intended problem?
- Is the implementation simpler than the alternatives?
- Are external inputs validated?
- Are errors handled correctly?
- Are resources always cleaned up?
- Does cancellation work?
- Are retries bounded?
- Is concurrent behaviour safe?
- Are database operations atomic where required?
- Are schema and runtime behaviour consistent?
- Are public contracts preserved or intentionally versioned?
- Are secrets protected?
- Are tests meaningful and deterministic?
- Are negative cases tested?
- Are relevant integration tests included?
- Is the documentation accurate?
- Are new dependencies genuinely necessary?
- Can another developer understand and maintain the code?

A useful development progression is:

> **Make it work. Make it correct. Make it safe. Make it secure. Then optimize it.**

---

## License

By contributing to The CROWler, you agree that your contributions will be licensed under the project's [Apache License 2.0](LICENSE).

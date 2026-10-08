# The CROWler

The CROWler is an open-source, feature-rich web crawler designed with a unique
philosophy at its core: to be as gentle and low-noise as possible. In other
words, The CROWler tries to stand out by ensuring minimal impact on the
websites it crawls while maximizing convenience for its users.

Additionally, the system is equipped with an API, providing a streamlined
interface for data queries. This feature ensures easy integration and
access to indexed data for various applications.

The CROWler is designed to be micro-services based, so it can be easily
deployed in a containerized environment.

## Content

- [Features](./features.md)
- [General Architecture](./architecture/architecture.md)
- [Database Architecture](./database/database_architecture.md)
- [Rulesets Architecture](./rules-and-rulesets/ruleset_architecture.md)
- [Installation](./installation.md)
- [Usage](./usage.md)
  - [Configuration](./configuration/config.md)
  - [Environment Variables](./configuration/env_vars.md)
  - [What are CROWler's "sources"?](./sources.md)
  - [Rulesets](./rules-and-rulesets/rulesets.md)
- [API](./api/README.md)
- [Plugins](./plugins/plugins.md)
- [Traditional and modern Agents in the CROWler](./crowler-agents/crowler_agents_reference.md)
- [Contributing](../CONTRIBUTING.md)
- [Test Policy](./test_policy.md)
- [License](../LICENSE.md)

## Feature guides

* [Information Seed discovery](information-seed/information_seed.md) — configure providers, submit seeds, filter candidates, create or link Sources, and inspect diagnostics.
* [Time-series observations and aggregates](time_series/timeseries.md) — metric definitions, emitters, scope/dedupe/change behavior, aggregation, API queries, retention, privacy, portability, and runnable examples.

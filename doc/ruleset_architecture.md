# The Ruleset Architecture

The crowler uses a rules engine to determine which:

- URLs to visit (crawl)
- Actions to take (how to interact with the page)
- Data to collect (scraping and indexing)
- Data to store (saving to a database, filesystem, etc.)
- Entities to identify/detect (e.g. products, technologies, etc.)

One way to describe what the CROWler is at its essence,
is: **The CROWler is as smart as your ruleset is.**

Rulesets can be expressed either as JSON files or YAML files. They
can be provided locally with the crowler engine or fetched from a
remote distribution server.

Rules are generally declarative, however some rules type (like for
example the Action rules) may be extended via imperative code (in
Javascript).

The combination of the CROWler configuration, the Source configuration
and the rulesets gives the CROWler the ability to adapt to a large set
of scenarios, together with the ability to be easily extended.

Scraping rules, for example, can be used to also extend the CROWler's
data model, by defining new entities and relationships between collected
data.

## Rules Architecture Hierarchy

### Rules Engine

At the top of the hierarchy is the Rules Engine (Rulesengine). The Rules
Engine is responsible for orchestrating all the rulesets and provides
methods to access them all.

A rule engine is fundamentally a collection of rulesets.

The Rules Engine is responsible for:

- Loading all the rulesets
- Provides methods for easy access to all the rulesets from all the
CROWler components that requires it

### Ruleset

The Ruleset is a collection of rule groups.

A ruleset is a single file on the filesystem. The way you should think
of a ruleset is a collection of rules that are related to each other and
organized in groups.

The Ruleset is responsible for:

- Provide methods to access all the rule groups
- Provide methods to access all the rules

### Rule Group

The Rule Group is a collection of rules. It usually represents the
concept that a set of rules are trying to achieve.

A rules group can contain rules of different types, but they should be
related to each other conceptually.

The Rule Group is responsible for:

- Provide methods to access all the rules
- Provide properties that are common to all the rules in the group (like
the group name, description, etc.)
- Provide properties that would easily determine if a set of rules are
enabled or disabled, valid in a certain context, etc.

### Rule

The Rule is the smallest unit of the ruleset hierarchy. Each rule has a
**rule type** and a set of conditions.

The Rule is responsible for:

- Representing a single activity that the CROWler should perform
- Providing "what" the CROWler should do (not how it should do it)
- Providing the conditions that must be met for the rule to be executed

#### Rule Types

The CROWler supports the following rule types:

- Crawling rules  (describe what we wish to crawl on a given site)
- Action rules    (describe what we wish to interact with on a page)
- Scraping rules  (describe what we wish to scrape on a page)
- Detection rules (describe what we wish to detect on a page)

#### Conditions

Conditions are the criteria that must be met for the rule to be executed.
Each rule type may present different types of conditions.

## Ruleset Reference

Check this [link](doc/ruleset_reference.md) for a detailed reference of
the ruleset schema.

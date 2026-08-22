# CROWler Database

The CROWler database is designed as an agnostic data model, closer in philosophy to a data lake
than to a traditional application-specific database schema.

Its purpose is to support the different classes of vertical applications that can be built using
the CROWler, without forcing the core data model to depend on any particular domain.

## Responsibilities

CROWler defines database-independent semantic operations, while each database backend is free
to implement those operations using its strongest native mechanisms.

Backend-specific functions, procedures, triggers, indexes, constraints, and transactional primitives
should be used where they improve correctness, atomicity, consistency, or performance, provided that
the externally observable CROWler semantics remain consistent across supported database backends.

Database portability does not imply lowest-common-denominator SQL. PostgreSQL, MySQL/MariaDB, and
SQLite are expected to use the mechanisms that best fit their respective capabilities and intended
deployment models.

## `pkg/database`

`pkg/database` is where the Go database abstraction layer lives.

The goal is for all other CROWler packages to use `pkg/database` to access and persist data rather
than interacting with the database directly. Database operations should ideally be exposed through
public methods provided by `pkg/database`, allowing the rest of the codebase to depend on CROWler
database semantics rather than backend-specific implementation details.

Given the development history and time constraints of the project, some parts of the codebase
currently bypass this abstraction. As the project evolves, these cases should progressively be
refactored so that `pkg/database` becomes the authoritative database access layer.

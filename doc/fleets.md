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

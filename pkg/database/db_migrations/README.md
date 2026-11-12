# Migrating CROWler's DB

This directory contains SQL migration scripts for updating the CROWler database schema. Each migration script is named according to the version it applies to, e.g., `postgresql-migration-v1.14.pgsql`. Before running a migration, ensure that you have backed up your database and that you have the necessary permissions to alter the schema.

A migration script only applies changes from the previous release to the one it declares it updates. In other words you cannot migrate directly from version 1.12 to 1.14 without first applying the 1.13 migration.

To migrate you must use the `postgres` admin account, not the `crowler` one.

Each migration script should be executed in the order of their version numbers to ensure the database schema remains consistent. After running a migration, verify that the changes have been applied correctly before proceeding to the next one.

If you encounter any issues during the migration process, consult the migration script for details on the changes being applied and check the database logs for any errors. It is recommended to test the migration on a staging environment before applying it to the production database.

BEFORE you run a migration script open it and read it, sometimes I have to leave special instructions or notes that are crucial for the migration to succeed.

Current schema target: **1.17**. The v1.17 migration adds verified source
ownership, durable approvals, and Information Seed claim fencing, lease
heartbeats, explicit retry metadata and a bounded terminal exhausted state. It is
additive and keeps the existing writer-generation marker. Stop every old writer
before applying it, then start only v1.17-compatible services. The PostgreSQL
migration is transactional and safe to re-run. SQLite uses runtime guarded
column repair because its supported versions cannot conditionally add columns in
SQL; the SQL ledger is safe to re-run. MySQL uses guarded column/index procedures
and is safe to re-run. The migration does not bulk-verify historical `usr_id`
values.

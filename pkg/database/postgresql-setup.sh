#!/bin/bash
set -e

export PGPASSWORD="$POSTGRES_PASSWORD"

if [ -z "${CROWLER_DB_SHARED_BUFFERS:-}" ]; then
    CROWLER_DB_SHARED_BUFFERS="128MB"
fi
if [ -z "${CROWLER_DB_WORK_MEM:-}" ]; then
    CROWLER_DB_WORK_MEM="4MB"
fi
if [ -z "${CROWLER_DB_MAINTENANCE_WORK_MEM:-}" ]; then
    CROWLER_DB_MAINTENANCE_WORK_MEM="64MB"
fi
if [ -z "${CROWLER_DB_EFFECTIVE_CACHE_SIZE:-}" ]; then
    CROWLER_DB_EFFECTIVE_CACHE_SIZE="4GB"
fi
if [ -z "${CROWLER_DB_AUTOVACUUM_WORK_MEM:-}" ]; then
    CROWLER_DB_AUTOVACUUM_WORK_MEM="-1"
fi

PSQL_BASE_ARGS=(
     -v ON_ERROR_STOP=1
     --username "$POSTGRES_USER"
     --dbname "$POSTGRES_DB"
     -v POSTGRES_DB="$POSTGRES_DB"
     -v CROWLER_DB_USER="$CROWLER_DB_USER"
     -v CROWLER_DB_PASSWORD="$CROWLER_DB_PASSWORD"
     -v DB_SHARED_BUFFERS="$CROWLER_DB_SHARED_BUFFERS"
     -v DB_WORK_MEM="$CROWLER_DB_WORK_MEM"
     -v DB_MAINTENANCE_WORK_MEM="$CROWLER_DB_MAINTENANCE_WORK_MEM"
     -v DB_EFFECTIVE_CACHE_SIZE="$CROWLER_DB_EFFECTIVE_CACHE_SIZE"
     -v DB_AUTOVACUUM_WORK_MEM="$CROWLER_DB_AUTOVACUUM_WORK_MEM"
)

# Read extra arguments from CROWLER_DB_EXTRA_ARGS environment variable
# This allow users to pass extra arguments to psql initialization command if needed
PSQL_EXTRA_ARGS=()
if [ -n "${CROWLER_DB_EXTRA_ARGS:-}" ]; then
    read -r -a PSQL_EXTRA_ARGS <<< "$CROWLER_DB_EXTRA_ARGS"
fi

psql \
    "${PSQL_BASE_ARGS[@]}" \
    "${PSQL_EXTRA_ARGS[@]}" \
    -f /docker-entrypoint-initdb.d/postgresql-setup-v1.5.pgsql

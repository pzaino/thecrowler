#!/bin/bash
set -e

export PGPASSWORD="$POSTGRES_PASSWORD"

PSQL_BASE_ARGS=(
    -v ON_ERROR_STOP=1
    --username "$POSTGRES_USER"
    --dbname "$POSTGRES_DB"
    -v POSTGRES_DB="$POSTGRES_DB"
    -v CROWLER_DB_USER="$CROWLER_DB_USER"
    -v CROWLER_DB_PASSWORD="$CROWLER_DB_PASSWORD"
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

#!/bin/bash
set -e

# Perform all actions as $DOCKER_POSTGRES_USER
export PGPASSWORD="$POSTGRES_PASSWORD"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
     -v POSTGRES_DB="$POSTGRES_DB" \
     -v CROWLER_DB_USER="$CROWLER_DB_USER" \
     -v CROWLER_DB_PASSWORD="$CROWLER_DB_PASSWORD" \
     -f /docker-entrypoint-initdb.d/postgresql-setup-v1.5.pgsql

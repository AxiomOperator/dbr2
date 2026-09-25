#!/bin/sh
# Temporal schema setup, upstream pattern (temporalio/samples-server compose/scripts/setup-postgres.sh),
# minus `create`: databases are pre-created by the Postgres init script and owned by role `temporal`.
# Idempotent: setup-schema -v 0.0 is skipped when schema_version already exists; update-schema is a no-op when current.
set -eu
: "${POSTGRES_SEEDS:?}"; : "${POSTGRES_USER:?}"; : "${SQL_PASSWORD:?}"
P="${DB_PORT:-5432}"
nc -z -w 10 "$POSTGRES_SEEDS" "$P"
for db in temporal temporal_visibility; do
  dir=temporal; [ "$db" = temporal_visibility ] && dir=visibility
  if ! temporal-sql-tool --plugin postgres12 --ep "$POSTGRES_SEEDS" -u "$POSTGRES_USER" -p "$P" --db "$db" \
       update-schema -d /etc/temporal/schema/postgresql/v12/$dir/versioned 2>/dev/null; then
    temporal-sql-tool --plugin postgres12 --ep "$POSTGRES_SEEDS" -u "$POSTGRES_USER" -p "$P" --db "$db" setup-schema -v 0.0
    temporal-sql-tool --plugin postgres12 --ep "$POSTGRES_SEEDS" -u "$POSTGRES_USER" -p "$P" --db "$db" \
       update-schema -d /etc/temporal/schema/postgresql/v12/$dir/versioned
  fi
done
echo 'Temporal schema setup complete'

#!/bin/bash
# Runs once on first init of the PG18 data dir (docker-entrypoint-initdb.d).
# Creates separate roles + databases: dbr2 (application) and temporal, temporal_visibility (Temporal, ADR-0009).
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
CREATE ROLE temporal LOGIN PASSWORD '${TEMPORAL_DB_PASSWORD}';
CREATE ROLE dbr2     LOGIN PASSWORD '${DBR2_DB_PASSWORD}';
CREATE DATABASE temporal            OWNER temporal;
CREATE DATABASE temporal_visibility OWNER temporal;
CREATE DATABASE dbr2                OWNER dbr2;
REVOKE ALL ON DATABASE temporal, temporal_visibility FROM PUBLIC;
REVOKE ALL ON DATABASE dbr2 FROM PUBLIC;
SQL

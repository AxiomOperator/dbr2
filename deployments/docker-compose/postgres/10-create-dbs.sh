#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Runs once on first initialization of the PG18 data directory. Creates
# separate roles and databases: dbr2 (platform) and temporal +
# temporal_visibility (ADR-0009). Passwords come from Compose secrets.
set -euo pipefail
temporal_pw=$(cat /run/secrets/temporal_db_password)
dbr2_pw=$(cat /run/secrets/dbr2_db_password)
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v temporal_pw="$temporal_pw" -v dbr2_pw="$dbr2_pw" <<'SQL'
CREATE ROLE temporal LOGIN PASSWORD :'temporal_pw';
CREATE ROLE dbr2     LOGIN PASSWORD :'dbr2_pw';
CREATE DATABASE temporal            OWNER temporal;
CREATE DATABASE temporal_visibility OWNER temporal;
CREATE DATABASE dbr2                OWNER dbr2;
REVOKE ALL ON DATABASE temporal, temporal_visibility FROM PUBLIC;
REVOKE ALL ON DATABASE dbr2 FROM PUBLIC;
SQL

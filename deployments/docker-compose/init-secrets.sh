#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Generates the Compose secrets and .env for a new DBR² installation. Never
# overwrites existing values. Keep secrets/ backed up with the platform
# self-backup (ADR-0008).
set -euo pipefail
cd "$(dirname "$0")"
umask 077
mkdir -p secrets certs
chmod 700 secrets

rand() { openssl rand -base64 "$1" | tr -d '\n/+=' | cut -c1-"$2"; }
put() { # name value
  if [[ ! -s "secrets/$1" ]]; then printf '%s' "$2" > "secrets/$1"; echo "created secrets/$1"; fi
  # Readable by the non-root service users inside containers; the directory stays 0700.
  chmod 644 "secrets/$1"
}

put postgres_password "$(rand 48 40)"
put temporal_db_password "$(rand 48 40)"
put dbr2_db_password "$(rand 48 40)"
put dbr2_database_url "postgres://dbr2:$(cat secrets/dbr2_db_password)@postgres:5432/dbr2?sslmode=disable"
put dbr2_secret_key "$(openssl rand -base64 32 | tr -d '\n')"
[[ -e secrets/dbr2_entra_client_secret ]] || { : > secrets/dbr2_entra_client_secret; chmod 644 secrets/dbr2_entra_client_secret; }

if [[ ! -e .env ]]; then
  cat > .env <<ENV
# DBR² deployment settings (see README.md)
DBR2_HOSTNAME=localhost
# "internal" = Caddy local CA; or "/certs/cert.pem /certs/key.pem"
DBR2_TLS=internal
DBR2_HTTPS_PORT=443
# Repository storage: NFS mount on this Docker host (v1.0: single NAS over NFS)
DBR2_REPO_HOST_PATH=/mnt/dbr2-repo
DBR2_REPOSITORY_ID=primary
# Entra ID (optional). Client secret goes in secrets/dbr2_entra_client_secret.
DBR2_ENTRA_TENANT_ID=
DBR2_ENTRA_CLIENT_ID=
# Temporal's server reads its DB password from the environment.
TEMPORAL_DB_PASSWORD=$(cat secrets/temporal_db_password)
ENV
  chmod 600 .env
  echo "created .env — review it before 'docker compose up -d'"
fi

#!/usr/bin/env bash
# Q3: fresh repository + upstream Kopia server (TLS 127.0.0.1:51515) + one agent user.
set -euo pipefail
. "$(dirname "$0")/env.sh"
export W="$SPIKE/.work/q3"
rm -rf "$W"; mkdir -p "$W"
ksrv() { "$KOPIA_BIN" --config-file="$W/server.config" --log-dir="$W/logs" --password="$REPO_PASSWORD" "$@"; }
# SPLITTER can be overridden, e.g. SPLITTER=DYNAMIC-1M-BUZHASH (default DYNAMIC-4M-BUZHASH)
ksrv repository create filesystem --path="$W/repo" --cache-directory="$W/cache" \
  --object-splitter="${SPLITTER:-DYNAMIC-4M-BUZHASH}" \
  --override-username=reposerver --override-hostname=dbr2 >/dev/null 2>&1
ksrv server user add agent-a@hosta --user-password=pw-agent-a >/dev/null 2>&1
ksrv repository status | grep -E "Hash|Encryption|Splitter|Format version|Content compression|Index Format"
W="$W" "$(dirname "$0")/q2-server.sh" | tail -1

#!/usr/bin/env bash
# Start the pinned upstream Kopia repository server (TLS, 127.0.0.1:51515) in the background.
set -euo pipefail
. "$(dirname "$0")/env.sh"

args=(server start --address="$SERVER_URL" --no-ui
  --server-control-password="$CTRL_PASS"
  --tls-cert-file="$W/tls.crt" --tls-key-file="$W/tls.key")
[ -f "$W/tls.crt" ] || args+=(--tls-generate-cert --tls-generate-rsa-key-size=2048)

# --log-dir comes from kopia's internal/logfile package, unavailable to an embedding binary.
logargs=(--log-dir="$W/logs"); [[ "$KOPIA_BIN" == *embedded-kopia ]] && logargs=()
nohup "$KOPIA_BIN" --config-file="$W/server.config" "${logargs[@]}" \
  --password="$REPO_PASSWORD" "${args[@]}" >"$W/server.out" 2>&1 &
echo $! >"$W/server.pid"
for _ in $(seq 1 60); do grep -q "SERVER CERT SHA256\|Server will allow connections" "$W/server.out" 2>/dev/null && break; sleep 0.5; done
sleep 1
cat "$W/server.out"
openssl x509 -in "$W/tls.crt" -noout -fingerprint -sha256 | sed 's/.*=//; s/://g' | tr 'A-F' 'a-f' >"$W/cert.sha256"
echo "pid=$(cat "$W/server.pid") fingerprint=$(cat "$W/cert.sha256")"

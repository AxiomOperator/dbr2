# Common environment for the spike scripts. Source it: `. scripts/env.sh`
SPIKE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export SPIKE
export KOPIA_CHECK_FOR_UPDATES=false
export KOPIA_BIN="${KOPIA_BIN:-$SPIKE/bin/kopia}"
export W="${W:-$SPIKE/.work/q2}"                     # server-side state
export REPO_PASSWORD="spike-repo-password"     # held ONLY by the server process
export SERVER_URL="https://127.0.0.1:51515"
export CTRL_PASS="spike-control-password"
# kopia invoked with the *server's* direct repository config
ksrv() { "$KOPIA_BIN" --config-file="$W/server.config" --log-dir="$W/logs" --password="$REPO_PASSWORD" "$@"; }

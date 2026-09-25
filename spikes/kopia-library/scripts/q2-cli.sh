#!/usr/bin/env bash
# Q2: the same ACL claims exercised with the stock kopia CLI as a server client,
# followed by deletion + server-side GC (maintenance).
set -uo pipefail
. "$(dirname "$0")/env.sh"
FP=$(cat "$W/cert.sha256")
C="$W/cli"; rm -rf "$C"; mkdir -p "$C"
# kcli <user@host> <password> <args...>
kcli() { local id=$1 pw=$2; shift 2
  "$KOPIA_BIN" --config-file="$C/$id.config" --log-dir="$C/logs" --password="$pw" "$@"; }
connect() { kcli "$1" "$2" repository connect server --url="$SERVER_URL" --server-cert-fingerprint="$FP" \
  --override-username="${1%@*}" --override-hostname="${1#*@}" --cache-directory="$C/cache-$1" >/dev/null 2>&1 \
  && echo "connected as $1"; }
run() { echo "\$ kopia [$1] ${*:3}"; kcli "$@" 2>&1 | grep -v "^\s*$" | sed 's/^/    /' | head -${LINES_MAX:-8}; echo "    exit=${PIPESTATUS[0]}"; }

mkdir -p "$W/src/cli-a" "$W/src/cli-b"
head -c 50000000 /dev/urandom >"$W/src/cli-a/big.bin"     # 50 MB so GC is visible
head -c 1000000  /dev/urandom >"$W/src/cli-b/b.bin"

connect agent-a@hosta pw-agent-a; connect agent-b@hostb pw-agent-b; connect maint@dbr2 pw-maint

echo; echo "### agent-a: create, list, delete own, delete other, maintenance"
run agent-a@hosta pw-agent-a snapshot create "$W/src/cli-a" --tags=dbr2-kind:volume
run agent-b@hostb pw-agent-b snapshot create "$W/src/cli-b"
run agent-a@hosta pw-agent-a snapshot list --all
AID=$(kcli agent-a@hosta pw-agent-a snapshot list "$W/src/cli-a" --json | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["id"])')
BID=$(kcli maint@dbr2 pw-maint snapshot list --all --json | python3 -c 'import json,sys;print([s["id"] for s in json.load(sys.stdin) if s["source"]["host"]=="hostb"][0])')
run agent-a@hosta pw-agent-a snapshot delete "$AID" --delete
run agent-a@hosta pw-agent-a snapshot delete "$BID" --delete
run agent-a@hosta pw-agent-a policy set "$W/src/cli-a" --keep-latest=1
run agent-a@hosta pw-agent-a maintenance run --full
run agent-a@hosta pw-agent-a server acl list

echo; echo "### maint@dbr2: list all, delete agent-a snapshot, try maintenance"
run maint@dbr2 pw-maint snapshot list --all
run maint@dbr2 pw-maint snapshot delete "$AID" --delete
run maint@dbr2 pw-maint maintenance run --full

echo; echo "### reposerver (direct connection, maintenance owner): GC"
blobsize() { du -sb "$W/repo" | cut -f1; }
echo "repo bytes on disk before GC: $(blobsize)"
echo "$ kopia [reposerver direct] maintenance info"; ksrv maintenance info 2>&1 | sed "s/^/    /" | head -12
# --safety=full (default) waits >=24h before content is removed; none is for demo only.
LINES_MAX=30 ksrv maintenance run --full --safety=none 2>&1 | sed 's/^/    /'
echo "repo bytes on disk after GC (safety=none): $(blobsize)"

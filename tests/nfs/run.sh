#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Functional NFS test for the reposerver storage guard (ADR-0002;
# final_stack → Testing → Storage mocking). Runs a userspace nfs-ganesha
# server and a privileged client container that mounts the export with the
# production options, then drives the real dbr2-reposerver binary.
# FUNCTIONAL ONLY — no throughput testing (the real NAS is not connected).
#
# Requires Docker with --privileged (GitHub-hosted runners allow it). Creates
# only resources prefixed dbr2test-nfs- and removes them on exit.
set -euo pipefail
cd "$(dirname "$0")/../.."
P=dbr2test-nfs
NET=$P-net
pass=0 fail=0
ok()  { echo "PASS: $*"; pass=$((pass+1)); }
bad() { echo "FAIL: $*"; fail=$((fail+1)); }

cleanup() {
  # Tear down clients first while the server is still up: a `hard` NFS mount
  # whose server has gone blocks its processes in the kernel, and the
  # container then cannot be killed.
  for c in $P-client $P-other; do
    docker exec $c bash -c "pkill -9 dbr2-reposerver; umount -f -l /mnt/dbr2-repo /mnt/x" >/dev/null 2>&1 || true
  done
  docker unpause $P-server >/dev/null 2>&1 || true
  docker rm -f $P-client $P-other >/dev/null 2>&1 || true
  docker rm -f $P-server >/dev/null 2>&1 || true
  docker network rm $NET >/dev/null 2>&1 || true
  docker volume rm $P-export >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

echo "== build"
mkdir -p bin
CGO_ENABLED=0 go build -o bin/dbr2-reposerver ./cmd/reposerver
docker build -q -t $P-ganesha -f tests/nfs/Dockerfile.ganesha tests/nfs >/dev/null
docker build -q -t $P-client -f tests/nfs/Dockerfile.client tests/nfs >/dev/null

docker network create --subnet 172.31.231.0/24 $NET >/dev/null
# ganesha's VFS FSAL cannot export the container's overlay root, so the
# export lives on a named volume.
docker run -d --name $P-server --network $NET --ip 172.31.231.2 --privileged \
  -e CLIENTS=172.31.231.3 -v $P-export:/export $P-ganesha >/dev/null
client() { docker run -d --name "$1" --network $NET --ip "$2" --privileged \
  -v "$PWD/bin/dbr2-reposerver:/usr/local/bin/dbr2-reposerver:ro,z" \
  -e DBR2_REPOSITORY_ID=nfs-test -e DBR2_REPOSERVER_PATH=/mnt/dbr2-repo \
  -e DBR2_REPOSERVER_EXPECT_FSTYPE=nfs4 -e DBR2_HEALTH_ADDR=:8081 \
  -e DBR2_REPOSERVER_WATCHDOG_INTERVAL=2s -e DBR2_REPOSERVER_WATCHDOG_TIMEOUT=3s \
  $P-client sleep infinity >/dev/null; }
client $P-client 172.31.231.3
client $P-other 172.31.231.4
cx() { docker exec $P-client bash -c "$*"; }
# expect_err <pattern> <command>: the command must fail with output matching pattern.
expect_err() { local out; out=$(cx "$2" 2>&1) && return 1; grep -q "$1" <<<"$out"; }
MOUNT="mount -t nfs4 -o hard,timeo=600,retrans=2,noatime 172.31.231.2:/export /mnt/dbr2-repo"

echo "== wait for the NFS server"
for _ in $(seq 1 30); do cx "mkdir -p /mnt/dbr2-repo && $MOUNT" 2>/dev/null && break; sleep 2; done
cx "findmnt -n -o FSTYPE /mnt/dbr2-repo" | grep -q nfs4 && ok "client mounted the export as nfs4" || bad "nfs4 mount"

echo "== export restriction (only meaningful once the allowed client mounted)"
if docker exec $P-other bash -c "mkdir -p /mnt/x && timeout 20 mount -t nfs4 -o soft,timeo=50,retrans=1 172.31.231.2:/export /mnt/x" 2>/dev/null; then
  bad "non-allowed client could mount the export"
else ok "non-allowed client was refused"; fi

echo "== storage guard on a real nfs4 mount"
expect_err "sentinel file missing" "dbr2-reposerver check" && ok "check fails before init (no sentinel)" || bad "check before init"
cx "dbr2-reposerver init" >/dev/null && ok "init wrote the sentinel on the empty nfs4 export" || bad "init"
cx "dbr2-reposerver check" >/dev/null && ok "check passes after init" || bad "check after init"
expect_err "does not match" "DBR2_REPOSITORY_ID=other dbr2-reposerver check" && ok "wrong repository ID rejected" || bad "wrong repository ID"

echo "== unmounted mount point is refused (spike failure mode)"
cx "umount /mnt/dbr2-repo"
expect_err "not a mount point" "dbr2-reposerver init" && ok "init refuses an unmounted path" || bad "init on unmounted path"
cx "test ! -e /mnt/dbr2-repo/.dbr2-repository-id" && ok "nothing was written to the local disk" || bad "local disk written"
cx "mount -t tmpfs tmpfs /mnt/dbr2-repo"
expect_err "want nfs4" "dbr2-reposerver check" && ok "wrong filesystem type (tmpfs) rejected" || bad "fstype check"
cx "umount /mnt/dbr2-repo && $MOUNT"

echo "== watchdog detects a stalled hard mount, then recovery"
cx "nohup dbr2-reposerver serve >/tmp/rs.log 2>&1 &"
for _ in $(seq 1 15); do cx "curl -sf localhost:8081/healthz" >/dev/null 2>&1 && break; sleep 1; done
cx "curl -sf localhost:8081/healthz" | grep -q '"healthy":true' && ok "serve healthy on nfs4" || bad "serve healthy"
docker pause $P-server >/dev/null
sleep 10
cx "curl -s localhost:8081/healthz" | grep -q 'stalled' && ok "watchdog reports stalled storage while NFS is paused" || bad "stall detection"
docker unpause $P-server >/dev/null
for _ in $(seq 1 30); do cx "curl -sf localhost:8081/healthz" >/dev/null 2>&1 && break; sleep 2; done
cx "curl -sf localhost:8081/healthz" | grep -q '"healthy":true' && ok "healthy again after the NFS server resumed" || bad "recovery"

echo "== $pass passed, $fail failed"
[[ $fail -eq 0 ]]

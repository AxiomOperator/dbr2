#!/usr/bin/env bash
# Q3c: NFS outage during snapshot. Orchestrated from the host; kopia runs in dbr2spike-nfs-client.
# usage: q3c_outage.sh pause|stop   (outage duration OUTAGE=30)
set -uo pipefail
MODE=${1:-pause}; OUT=${OUTAGE:-30}; C=dbr2spike-nfs-client; SV=dbr2spike-nfs-server
ts(){ date +%H:%M:%S; }
docker exec $C bash -c '
export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=/root/q3/repo.config KOPIA_CACHE_DIRECTORY=/root/q3/cache KOPIA_LOG_DIR=/root/q3/logs KOPIA_CHECK_FOR_UPDATES=false
rm -rf /root/big; mkdir -p /root/big; for i in $(seq 1 12); do head -c 128M /dev/urandom > /root/big/r$i; done; du -sh /root/big
/spike/bin/kopia policy set /root/big --compression=none >/dev/null
/spike/bin/kopia repo throttle set --upload-bytes-per-second=$0 >/dev/null 2>&1; /spike/bin/kopia repo throttle get | grep -i upload' ${THROTTLE:-25000000}
echo "$(ts) start snapshot (mode=$MODE)"
docker exec -d $C bash -c 'export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=/root/q3/repo.config KOPIA_CACHE_DIRECTORY=/root/q3/cache KOPIA_LOG_DIR=/root/q3/logs KOPIA_CHECK_FOR_UPDATES=false; s=$(date +%s); /spike/bin/kopia snapshot create /root/big > /root/snap.out 2>&1; echo "rc=$? elapsed=$(( $(date +%s)-s ))s" >> /root/snap.out'
sleep 4; echo "$(ts) outage begins: docker $MODE $SV"
if [ "$MODE" = pause ]; then docker pause $SV; else docker stop -t 2 $SV; fi
for t in $(seq 10 10 $OUT); do sleep 10; echo "$(ts) t+${t}s kopia state: $(docker exec $C bash -c "ps -o stat=,wchan:24= -C kopia | head -1 | tr -s \" \"")"; done
if [ "$MODE" = pause ]; then docker unpause $SV; else docker start $SV; fi
echo "$(ts) server back"
for i in $(seq 1 60); do docker exec $C test -n "$(docker exec $C grep rc= /root/snap.out 2>/dev/null)" 2>/dev/null; if docker exec $C grep -q rc= /root/snap.out; then break; fi; sleep 5; done
echo "$(ts) snapshot finished:"; docker exec $C bash -c 'grep -E "Created|ERROR|error|rc=" /root/snap.out | tail -5'
docker exec $C bash -c 'export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=/root/q3/repo.config KOPIA_CACHE_DIRECTORY=/root/q3/cache KOPIA_LOG_DIR=/root/q3/logs KOPIA_CHECK_FOR_UPDATES=false
/spike/bin/kopia snapshot list /root/big
/spike/bin/kopia snapshot verify --verify-files-percent=100 2>&1 | tail -1
/spike/bin/kopia content verify --full 2>&1 | grep -E "verifyCounters|error" | tail -2
rm -rf /root/rbig; /spike/bin/kopia restore /root/big /root/rbig >/dev/null 2>&1; diff -r /root/big /root/rbig && echo "RESTORE CONTENT IDENTICAL"; rm -rf /root/rbig'
echo "server log (last lines):"; docker logs --since 3m $SV 2>&1 | grep -viE 'dbus|gss|rdma' | tail -6

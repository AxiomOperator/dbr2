#!/usr/bin/env bash
# Q2: SELinux labels on Kopia restore (non-root host user) + docker :z/:Z behaviour.
set -uo pipefail
S=$(cd "$(dirname "$0")/.." && pwd); K=$S/bin/kopia; W=$S/.work/q2
rm -rf "$W"; mkdir -p "$W"/src
export KOPIA_PASSWORD=p KOPIA_CONFIG_PATH=$W/c.config KOPIA_CACHE_DIRECTORY=$W/cache KOPIA_LOG_DIR=$W/logs KOPIA_CHECK_FOR_UPDATES=false
echo "getenforce=$(getenforce)  docker SecurityOptions=$(docker info --format '{{json .SecurityOptions}}')"
echo "dockerd selinux flag: $(ps -o args= -C dockerd | tr ' ' '\n' | grep -i selinux || echo none)"
echo "daemon.json: $(cat /etc/docker/daemon.json 2>/dev/null || echo 'absent/unreadable')"
echo "== default file contexts (policy) for docker paths"
for p in /var/lib/docker/volumes/x/_data/f /var/lib/docker/overlay2/x/diff/f /srv/app/data/f /home/garrettpost/x; do matchpathcon "$p"; done
echo "dockerd label: $(ps -eZ | awk '/dockerd/{print $1; exit}')  containerd: $(ps -eZ | awk '/containerd$/{print $1; exit}')"
mkdir -p "$W/src/vol"; echo db > "$W/src/vol/data"; chcon -R -t container_file_t -l s0:c10,c20 "$W/src/vol"
$K repo create filesystem --path "$W/repo" >/dev/null 2>&1; $K snapshot create "$W/src/vol" >/dev/null 2>&1
echo "== (a1) restore into plain user dir"; $K restore "$W/src/vol" "$W/r-user" >/dev/null 2>&1; ls -Zd "$W/src/vol/data" "$W/r-user" "$W/r-user/data"
echo "== (a2) restore into pre-created container_file_t:s0:c1,c2 dir"
mkdir -p "$W/r-ctr"; chcon -t container_file_t -l s0:c1,c2 "$W/r-ctr"; $K restore "$W/src/vol" "$W/r-ctr" >/dev/null 2>&1; ls -Zd "$W/r-ctr" "$W/r-ctr/data"
echo "== (b1) re-apply recorded context with chcon --reference / explicit context"
chcon -R --reference="$W/src/vol" "$W/r-user" && ls -Zd "$W/r-user" "$W/r-user/data"
chcon -R "unconfined_u:object_r:container_file_t:s0:c10,c20" "$W/r-ctr" && ls -Zd "$W/r-ctr/data"
echo "== (b2) restorecon resets to policy default (user_home_t under ~)"
restorecon -Rv "$W/r-user" | head -3; ls -Zd "$W/r-user/data"
echo "== (c) docker bind mounts"
mkdir -p "$W/bind-none" "$W/bind-z" "$W/bind-Z"; for d in none z Z; do echo x > "$W/bind-$d/f"; done
docker run --rm --name dbr2spike-nfs-sel0 -v "$W/bind-none:/m" fedora:44 sh -c 'cat /proc/self/attr/current; echo; ls -Z /m/f; echo w > /m/w && echo write-ok' 2>&1
docker run --rm --name dbr2spike-nfs-selz -v "$W/bind-z:/m:z" fedora:44 sh -c 'cat /proc/self/attr/current; echo; ls -Z /m/f' 2>&1
docker run --rm --name dbr2spike-nfs-selZ -v "$W/bind-Z:/m:Z" fedora:44 sh -c 'cat /proc/self/attr/current; echo; ls -Z /m/f' 2>&1
echo "host view after runs:"; ls -Zd "$W"/bind-*/f
echo "== (c2) same with podman (rootless, SELinux-enabled runtime) for contrast"
podman run --rm -v "$W/bind-none:/m" registry.fedoraproject.org/fedora-minimal:44 sh -c 'cat /proc/self/attr/current; echo; ls /m >/dev/null && echo read-ok || echo read-denied' 2>&1 | tail -3
podman run --rm -v "$W/bind-Z:/m:Z" registry.fedoraproject.org/fedora-minimal:44 sh -c 'cat /proc/self/attr/current; echo; ls -Z /m/f' 2>&1 | tail -2
ls -Zd "$W/bind-Z/f"

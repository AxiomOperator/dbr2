#!/usr/bin/env bash
# DBR2 spike: Kopia v0.23.1 metadata fidelity, SELinux restore labels, mocked NFS.
# Usage: ./run.sh [all|build|q1|q2|q3|cleanup]   Logs land in .work/*.log
# Creates only dbr2spike-nfs-* Docker resources and removes them in cleanup.
set -uo pipefail
S=$(cd "$(dirname "$0")" && pwd); cd "$S"
NET=dbr2spike-nfs-net; SUB=10.252.77.0/24; SRV_IP=10.252.77.10; CLI_IP=10.252.77.20; BAD_IP=10.252.77.30
MOPTS=hard,timeo=600,retrans=2,noatime,vers=4.1
build(){
  [ -x bin/kopia ] || GOBIN=$S/bin go install github.com/kopia/kopia@v0.23.1
  docker build -q -t dbr2spike-nfs-ganesha:local -f nfs/Dockerfile.ganesha nfs
  docker build -q -t dbr2spike-nfs-client:local -f nfs/Dockerfile.client nfs
}
q1(){
  scripts/q1_fidelity.sh | tee .work/q1.log
  docker run --rm --name dbr2spike-nfs-rootown -v "$S:/spike" dbr2spike-nfs-client:local /spike/scripts/q1_root_inner.sh | tee .work/q1root.log
}
q2(){ scripts/q2_selinux.sh | tee .work/q2.log; }
server(){ # $1 = allowed clients
  docker rm -f dbr2spike-nfs-server >/dev/null 2>&1
  docker run -d --name dbr2spike-nfs-server --network $NET --ip $SRV_IP --privileged -e CLIENTS="$1" \
    -v "$S/.work/nfs-export:/export" dbr2spike-nfs-ganesha:local >/dev/null; sleep 5; }
q3(){
  mkdir -p .work/local-repo .work/nfs-export
  docker run --rm --name dbr2spike-nfs-localmock -v "$S:/spike" -v "$S/.work/local-repo:/mnt/dbr2-repo" dbr2spike-nfs-client:local /spike/scripts/q3a_inner.sh | tee .work/q3a.log
  docker network inspect $NET >/dev/null 2>&1 || docker network create --subnet $SUB $NET >/dev/null
  server $CLI_IP
  docker run -d --name dbr2spike-nfs-client --network $NET --ip $CLI_IP --privileged -v "$S:/spike" dbr2spike-nfs-client:local sleep infinity >/dev/null
  docker exec dbr2spike-nfs-client bash -c "mkdir -p /mnt/dbr2-repo && mount -t nfs4 -o $MOPTS $SRV_IP:/export /mnt/dbr2-repo && grep dbr2-repo /proc/mounts"
  docker exec dbr2spike-nfs-client /spike/scripts/q3b_inner.sh | tee .work/q3b.log
  scripts/q3c_outage.sh pause | tee .work/q3c-pause.log
  scripts/q3c_outage.sh stop  | tee .work/q3c-stop.log
  echo "== Q3d export restriction (only $CLI_IP allowed)" | tee .work/q3d.log
  docker run --rm --name dbr2spike-nfs-intruder --network $NET --ip $BAD_IP --privileged dbr2spike-nfs-client:local \
    bash -c "mkdir -p /m; timeout -s KILL 60 mount -t nfs4 -o $MOPTS $SRV_IP:/export /m; echo intruder-rc=\$?" 2>&1 | tee -a .work/q3d.log
}
cleanup(){
  # root-owned files written by containers must be removed from inside a container
  if docker image inspect dbr2spike-nfs-client:local >/dev/null 2>&1; then
    docker exec dbr2spike-nfs-client umount -f -l /mnt/dbr2-repo >/dev/null 2>&1
    docker run --rm --name dbr2spike-nfs-cleaner -v "$S:/spike" dbr2spike-nfs-client:local rm -rf /spike/.work/q1root /spike/.work/nfs-export /spike/.work/local-repo
  fi
  docker rm -f dbr2spike-nfs-client dbr2spike-nfs-server dbr2spike-nfs-intruder dbr2spike-nfs-localmock dbr2spike-nfs-rootown >/dev/null 2>&1
  docker network rm $NET >/dev/null 2>&1
  docker rmi dbr2spike-nfs-ganesha:local dbr2spike-nfs-client:local >/dev/null 2>&1
  rm -rf .work
  echo "remaining dbr2spike-nfs-* resources:"
  docker ps -a --format '{{.Names}}' | grep dbr2spike-nfs- ; docker network ls --format '{{.Name}}' | grep dbr2spike-nfs- ; docker volume ls --format '{{.Name}}' | grep dbr2spike-nfs- ; docker images --format '{{.Repository}}' | grep dbr2spike-nfs- ; true
}
mkdir -p .work
case "${1:-all}" in
  build) build;; q1) q1;; q2) q2;; q3) q3;; cleanup) cleanup;;
  all) build; q1; q2; q3; cleanup;;
  *) echo "usage: $0 [all|build|q1|q2|q3|cleanup]"; exit 2;;
esac

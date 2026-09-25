#!/usr/bin/env bash
# Q3a: default dev mock = local directory bind-mounted at the production NFS path /mnt/dbr2-repo.
set -uo pipefail
K=/spike/bin/kopia; L=/root/q3a; rm -rf $L; mkdir -p $L/src
export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=$L/repo.config KOPIA_CACHE_DIRECTORY=$L/cache KOPIA_LOG_DIR=$L/logs KOPIA_CHECK_FOR_UPDATES=false
for i in 1 2 3; do head -c 50000 /dev/urandom > $L/src/f$i; done; chown 70:70 $L/src/f1
echo "mount source for /mnt/dbr2-repo: $(awk '$2=="/mnt/dbr2-repo"{print $1, $3}' /proc/mounts)"
rm -rf /mnt/dbr2-repo/repo-a; $K repo create filesystem --path /mnt/dbr2-repo/repo-a 2>&1 | grep -E 'Connected|ERROR'
$K snapshot create $L/src 2>&1 | grep -E 'Created|ERROR'; $K restore $L/src $L/r 2>&1 | tail -1
diff -r $L/src $L/r && diff <(cd $L/src && find . -printf '%p %U:%G %#m\n'|sort) <(cd $L/r && find . -printf '%p %U:%G %#m\n'|sort) && echo "Q3a OK: content+owner+mode identical"
$K snapshot verify 2>&1 | tail -1

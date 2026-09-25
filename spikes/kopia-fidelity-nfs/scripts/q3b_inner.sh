#!/usr/bin/env bash
# runs inside dbr2spike-nfs-client (root, NFS mounted at /mnt/dbr2-repo)
set -uo pipefail
K=/spike/bin/kopia; L=/root/q3; rm -rf $L; mkdir -p $L/src/sub
export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=$L/repo.config KOPIA_CACHE_DIRECTORY=$L/cache KOPIA_LOG_DIR=$L/logs KOPIA_CHECK_FOR_UPDATES=false
for i in $(seq 1 20); do head -c 100000 /dev/urandom > $L/src/f$i; done; echo hi > $L/src/sub/x; chown -R 70:70 $L/src/sub; ln -s f1 $L/src/link
rm -rf /mnt/dbr2-repo/repo-b
$K repo create filesystem --path /mnt/dbr2-repo/repo-b 2>&1 | grep -iE 'error|Initializing|connected' ; echo "create rc=$?"
$K snapshot create $L/src 2>&1 | grep -E 'Created|ERROR'
$K restore $L/src $L/restore 2>&1 | tail -1
diff -r --no-dereference $L/src $L/restore && echo "CONTENT IDENTICAL"
diff <(cd $L/src && find . -printf '%p %U:%G %#m %y\n'|sort) <(cd $L/restore && find . -printf '%p %U:%G %#m %y\n'|sort) && echo "OWNER/MODE IDENTICAL"
$K snapshot verify --verify-files-percent=100 2>&1 | tail -2
$K content verify 2>&1 | tail -1
echo "repo files on NFS:"; ls /mnt/dbr2-repo/repo-b | head; stat -c '%U:%G %a %n' /mnt/dbr2-repo/repo-b/kopia.repository.f

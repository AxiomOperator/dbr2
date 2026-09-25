#!/usr/bin/env bash
# runs INSIDE a fedora container as root; /spike is the bind-mounted spike dir
set -uo pipefail
K=/spike/bin/kopia; W=/spike/.work/q1root
rm -rf $W; mkdir -p $W/{src,repo,cfg,restore}
export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=$W/cfg/repo.config KOPIA_CACHE_DIRECTORY=$W/cfg/cache KOPIA_LOG_DIR=$W/cfg/logs KOPIA_CHECK_FOR_UPDATES=false
echo "whoami=$(id) proc-label=$(cat /proc/self/attr/current 2>/dev/null)"
F=$W/src/pgdata; mkdir -p $F/base/1
echo PG_VERSION > $F/PG_VERSION; echo data > $F/base/1/1259; echo cfg > $F/postgresql.conf
chown -R 70:70 $F; chmod 0700 $F; chown 999:999 $F/postgresql.conf; chmod 0600 $F/postgresql.conf
echo other > $F/owned-12345-54321; chown 12345:54321 $F/owned-12345-54321
echo t > $F/trusted.txt; setfattr -n trusted.dbr2 -v t $F/trusted.txt 2>&1 || echo "trusted.* setfattr failed"
echo s > $F/sel.txt; setfattr -n security.selinux -v 'system_u:object_r:container_file_t:s0:c5,c6' $F/sel.txt 2>&1 || echo "security.selinux set failed"
setfacl -m u:999:rw $F/postgresql.conf 2>&1
$K repo create filesystem --path $W/repo >/dev/null 2>&1 && echo "repo created"
$K snapshot create $F 2>&1 | grep -iE 'error|Created' 
$K restore $F $W/restore/pgdata 2>&1 | tail -1
dump(){ (cd $1 && find . -printf '%p %U:%G %#m\n' | sort); }
echo "== ownership/mode diff"; diff <(dump $F) <(dump $W/restore/pgdata) && echo "IDENTICAL uid/gid/mode (as root)"
dump $W/restore/pgdata
echo "== xattrs src"; getfattr -h -d -m - $F/trusted.txt $F/sel.txt $F/postgresql.conf 2>/dev/null | grep -v '^$'
echo "== xattrs restored"; getfattr -h -d -m - $W/restore/pgdata/trusted.txt $W/restore/pgdata/sel.txt $W/restore/pgdata/postgresql.conf 2>/dev/null | grep -v '^$'
echo "== restore with --skip-owners"; $K restore $F $W/restore/skip --skip-owners >/dev/null 2>&1; dump $W/restore/skip | head -3

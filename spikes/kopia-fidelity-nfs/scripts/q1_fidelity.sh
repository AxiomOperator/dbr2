#!/usr/bin/env bash
# Q1: Kopia metadata fidelity as the current (non-root) user on the host.
set -uo pipefail
S=$(cd "$(dirname "$0")/.." && pwd); K=$S/bin/kopia; W=$S/.work/q1
rm -rf "$W"; mkdir -p "$W"/{src,repo,cfg,restore}
export KOPIA_PASSWORD=spike-pass KOPIA_CONFIG_PATH=$W/cfg/repo.config KOPIA_CACHE_DIRECTORY=$W/cfg/cache KOPIA_LOG_DIR=$W/cfg/logs KOPIA_CHECK_FOR_UPDATES=false
F=$W/src/fixture; mkdir -p "$F"/{emptydir,sub}
echo "hello content" > "$F/plain.txt"
echo "exec" > "$F/mode0751.sh"; chmod 0751 "$F/mode0751.sh"
echo "setgid dir" > "$F/sub/f"; chmod 2750 "$F/sub"
echo "private" > "$F/mode0600"; chmod 0600 "$F/mode0600"
touch -d '2001-02-03 04:05:06.123456789' "$F/plain.txt"
ln -s plain.txt "$F/symlink-rel"; ln -s /nonexistent/target "$F/symlink-dangling"
echo "hl" > "$F/hard-a"; ln "$F/hard-a" "$F/hard-b"
truncate -s 64M "$F/sparse.img"; printf 'X' | dd of="$F/sparse.img" bs=1 seek=33554432 conv=notrunc status=none
echo x > "$F/xattr.txt"; setfattr -n user.dbr2 -v x "$F/xattr.txt"; setfattr -n user.other -v 0sAAEC "$F/xattr.txt"
echo acl > "$F/acl.txt"; setfacl -m u:nobody:r "$F/acl.txt"; setfacl -d -m u:nobody:rx "$F/sub" 2>&1
echo lbl > "$F/selinux.txt"; chcon -t container_file_t "$F/selinux.txt" 2>&1 && echo "chcon container_file_t: OK" || echo "chcon container_file_t: DENIED"
chcon -l s0:c1,c2 "$F/selinux.txt" 2>&1 && echo "chcon MCS s0:c1,c2: OK" || echo "chcon MCS: DENIED"
echo "== kopia $($K --version)"
$K repo create filesystem --path "$W/repo" >/dev/null 2>&1 && echo "repo created"
$K snapshot create "$F" 2>&1 | tail -3
SID=$($K snapshot list "$F" --json | python3 -c 'import json,sys;print(json.load(sys.stdin)[-1]["id"])')
$K restore "$SID" "$W/restore/fixture" ${RESTORE_FLAGS:-} 2>&1 | tail -2
R=$W/restore/fixture
dump(){ (cd "$1" && find . -printf '%p|type=%y|mode=%#m|uid=%U|gid=%G|mtime=%T@|nlink=%n|ino=%i|size=%s|blocks=%b|link=%l\n' | sort); }
dump "$F" > "$W/src.stat"; dump "$R" > "$W/dst.stat"
echo "== stat diff (src vs restore)"; diff <(sed 's/|ino=[0-9]*//' "$W/src.stat") <(sed 's/|ino=[0-9]*//' "$W/dst.stat")
echo "== content diff"; diff -r --no-dereference "$F" "$R" && echo "content identical"
echo "== hardlink inodes"; stat -c '%n ino=%i nlink=%h' "$F"/hard-* "$R"/hard-*
echo "== sparse"; du -k --apparent-size "$F/sparse.img" "$R/sparse.img"; du -k "$F/sparse.img" "$R/sparse.img"
echo "== xattrs (all namespaces)"; for d in "$F" "$R"; do echo "-- $d"; (cd "$d" && getfattr -R -h -d -m - . 2>/dev/null | grep -vE '^$'); done
echo "== ACLs"; getfacl -p "$F/acl.txt" "$R/acl.txt" "$F/sub" "$R/sub" 2>/dev/null | grep -E '^# file|user:|default'
echo "== SELinux labels"; ls -Zd "$F/selinux.txt" "$R/selinux.txt" "$F/plain.txt" "$R/plain.txt"

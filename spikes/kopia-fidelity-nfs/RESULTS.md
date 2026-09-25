# Spike results: Kopia metadata fidelity, SELinux on restore, mocked NFS

- **Date:** 2026-09-25 (time-boxed, about 75 minutes)
- **Kopia:** v0.23.1, built locally with `GOBIN=spikes/kopia-fidelity-nfs/bin go install github.com/kopia/kopia@v0.23.1`
- **Host:** Fedora 44, kernel 7.2.7, SELinux **Enforcing**, uid 1000 in `docker` group, no sudo, `/home` on btrfs
- **Docker:** Engine 29.8.1, `SecurityOptions=["name=seccomp,profile=builtin","name=cgroupns"]`, no `--selinux-enabled` on the dockerd command line, `/etc/docker/daemon.json` absent. **Verified:** the daemon does not enforce SELinux for containers (containers run as `spc_t`, see Q2c).
- **Related ADRs:** ADR-0006 (native agent and volume access), ADR-0002 (repository server), `final_stack.md` sections "Storage" and "Testing → Storage mocking".

Legend: **Verified** = observed in this spike. **Inferred** = read from source or reasoned, not executed.

---

## Q1: Metadata fidelity (ADR-0006)

### Method

1. `scripts/q1_fidelity.sh` runs as uid 1000 on the host. It builds a fixture with every case below, creates a Kopia filesystem repo under `.work/`, snapshots the fixture, restores it to a new directory, and diffs `find -printf` stat output, content (`diff -r`), `getfattr -d -m -` (all namespaces), `getfacl` and `ls -Z`.
2. `scripts/q1_root_inner.sh` runs as root inside a throwaway `fedora:44`-based container (`dbr2spike-nfs-rootown`), with only the spike directory bind-mounted. It creates a postgres-like tree owned by 70:70 plus files owned by 999:999 and 12345:54321, then snapshots and restores it as root.
3. A focused directory-mtime test and a `--write-sparse-files` test were run by hand (commands below).
4. Kopia source code in `GOMODCACHE` was read to explain the results.

### Evidence (trimmed)

```
chcon container_file_t: OK          # chcon on own files is allowed for unconfined_t users
chcon MCS s0:c1,c2: OK
Restored 10 files, 3 directories and 2 symbolic links (67.1 MB).
== stat diff (src vs restore)
< ./hard-a|...|nlink=2      > ./hard-a|...|nlink=1          # hardlink broken into 2 copies
< ./sparse.img|...|blocks=8 > ./sparse.img|...|blocks=131072 # sparse file fully allocated (default restore)
< ./sub|type=d|mode=02750|...|mtime=...6332189910
> ./sub|type=d|mode=02750|...|mtime=...6338416860            # subdir mtime = newest child mtime (see below)
< ./symlink-rel|...|mtime=1790348651.6410441580
> ./symlink-rel|...|mtime=1790348651.6410450000              # symlink mtime truncated to microseconds
== content diff: content identical
== xattrs: src xattr.txt has user.dbr2="x", user.other=0sAAEC; restored xattr.txt has only security.selinux
== ACLs: src acl.txt user:nobody:r--   ; restored: user::rw- only (ACL gone)
          src sub default:user:nobody:r-x ; restored: no default ACL
== SELinux: src selinux.txt container_file_t:s0:c1,c2 ; restored user_home_t:s0 (inherited from parent)
```

Directory mtime test (the stored value is correct, but restore uses a different value):

```
src d1 mtime 2010-01-01, d1/f mtime 2001-01-01
kopia show <root>: {"name":"d1","mtime":"2010-01-01T06:00:00Z",...,"summ":{"maxTime":"2001-01-01T06:00:00Z"}}
restored d1 mtime 2001-01-01       # also with --parallel=1
```

Sparse restore with the opt-in flag:

```
kopia restore <snap> .work/q1/restore/sparse --write-sparse-files  ->  du -k = 4 (sparse), cmp equal
```

ACL removal widens group access (security-relevant):

```
src   secret: mode 0600 + setfacl u:nobody:rw -> user::rw- user:nobody:rw- group::--- mask::rw-  (ls shows 660)
restored secret: user::rw- group::rw- other::---   # the mask bits became real group rw permission
```

Root container (`q1_root_inner.sh`):

```
whoami=uid=0(root) proc-label=system_u:system_r:spc_t:s0
setfattr trusted.dbr2: Operation not permitted   # no CAP_SYS_ADMIN in an unprivileged container; trusted.* untested
== ownership/mode diff: IDENTICAL uid/gid/mode (as root)
. 70:70 0700 | ./base/1/1259 70:70 0644 | ./postgresql.conf 999:999 0660 | ./owned-12345-54321 12345:54321 0644
src sel.txt security.selinux=system_u:object_r:container_file_t:s0:c5,c6 -> restored system_u:object_r:user_home_t:s0
src postgresql.conf system.posix_acl_access=...        -> restored: absent
restore --skip-owners -> everything 0:0
```

### Source citations (`$(go env GOMODCACHE)/github.com/kopia/kopia@v0.23.1/`)

- `snapshot/manifest.go:121-131`: `DirEntry` stores only `name, type, mode, size, mtime, uid, gid, obj, summ`. There is **no field for xattrs, ACLs, SELinux labels, link count/inode, or sparse layout**. A grep for `xattr|Setxattr|posix_acl` across `*.go` and `*.md` finds nothing apart from a shallow-restore test helper that is misleadingly named `validateXattr` (`tests/end_to_end_test/shallowrestore_test.go:708`).
- `snapshot/restore/local_fs_output.go:273-316` (`setAttributes`): restore applies only chown, chmod and chtimes (atime is set equal to mtime).
- `cli/command_restore.go:154`: `--ignore-permission-errors` **defaults to true**, so a failed chown or chmod (for example a restore that is not running as root) is silently skipped.
- `snapshot/restore/local_fs_output.go:34-50`, `:108-109`: sparse writing happens only when `WriteSparseFiles` / `--write-sparse-files` is set.
- `snapshot/snapshotfs/repofs.go:188-191`: when a directory is loaded, every **subdirectory's** `ModTime` is replaced with `DirSummary.MaxModTime` (the newest mtime among its descendants). Restore then applies that value. The root of the snapshot is unaffected.
- `snapshot/restore/local_fs_output_unix.go:23-28`: symlink times are set with `unix.Lutimes` (`Timeval`), which has microsecond precision.

### Results

| Attribute | Preserved? | Notes |
|---|---|---|
| File content | **Yes** (verified) | `diff -r` identical; also 1.6 GB random data over NFS |
| Mode bits incl. setgid/sticky | **Yes** (verified) | 0600, 0751, 02750, 0700 all correct |
| Numeric uid/gid (root restore) | **Yes** (verified) | 70:70, 999:999, 12345:54321 restored exactly as root |
| Numeric uid/gid (non-root restore) | Silently skipped (inferred from source) | default `--ignore-permission-errors=true` |
| File mtime | **Yes** (verified), nanosecond precision | |
| Root directory mtime | **Yes** (verified) | |
| **Subdirectory mtime** | **No** (verified) | restored value is the newest descendant mtime (`repofs.go:190`) |
| Symlinks (relative, dangling) | **Yes** (verified) | target text kept; mtime truncated to microseconds |
| Hardlinks | **No** (verified) | each link is restored as an independent file (nlink 2 becomes 1+1); content is deduplicated in the repo only |
| Sparse files | **Only with `--write-sparse-files`** (verified) | default restore fully allocates the file (64 MiB file: 4 KiB to 64 MiB on disk) |
| Empty directories | **Yes** (verified) | |
| `user.*` xattrs | **No** (verified) | not captured at all |
| `trusted.*` xattrs | not tested (EPERM without CAP_SYS_ADMIN) | inferred **No** (no xattr code) |
| POSIX access ACL (`system.posix_acl_access`) | **No** (verified) | dropped; the ACL mask becomes plain group bits, which **widens group access** |
| POSIX default ACL | **No** (verified) | |
| `security.selinux` | **No** (verified) | the restored file gets the label inherited from the target directory or policy |
| atime | No (by design) | set equal to mtime |

**Conclusion: Partial.** Kopia keeps content, mode, numeric ownership (when restoring as root), file mtime, symlinks and empty directories. It does **not** keep xattrs, ACLs, SELinux labels or hardlinks. Sparse files are kept only when the flag is set. Subdirectory mtimes are restored incorrectly.

### Proposed ADR impact

- **ADR-0006 "Ownership" bullet:** add "Restores call Kopia with `IgnorePermissionErrors=false` (CLI `--no-ignore-permission-errors`) so that a failed chown or chmod fails the restore instead of being silently skipped. `WriteSparseFiles=true` is always set."
- **ADR-0006 new "Metadata Kopia does not carry" subsection:** Kopia v0.23.1 does not capture xattrs (`user.*`, `trusted.*`, `security.*`), POSIX ACLs or hardlinks. Options, in order of preference:
  1. **Sidecar metadata stream (recommended for v1.0).** At capture, the agent walks the source and records, for every path that has any of these, its xattrs (all namespaces except `security.selinux`, which is handled separately), `system.posix_acl_*`, and hardlink groups (`dev:ino` → paths). It stores this as a versioned JSON/NDJSON object snapshotted alongside the data, referenced from the manifest (ADR-0004). At restore, the agent reapplies ACLs and xattrs and recreates hardlinks (replacing the duplicate copies with `link()`). Cost: one extra `lgetxattr`/`llistxattr` walk, which is cheap next to hashing.
  2. Upstream a Kopia feature (xattr and hardlink support has been requested upstream for a long time). This is not something to depend on for v1.0.
  3. Declare these attributes unsupported. This is **not acceptable** for ACLs, because the ACL mask turns into group permissions and **widens access** on restore.
- **ADR-0006:** add "Directory mtimes are reapplied from the sidecar or source walk after restore (Kopia restores a subdirectory's mtime as its newest descendant's mtime, `snapshot/snapshotfs/repofs.go:190`)." Alternatively, accept this and document it (most applications, including PostgreSQL, ignore directory mtimes). **Recommendation:** fix it with a cheap post-pass, because the sidecar walk already has the data.
- **ADR-0004 manifest:** add a `metadata_sidecar` reference and a `fidelity` block that lists what was captured, so a restore can report "ACLs reapplied: n".
- **Test plan:** make the Q1 fixture a Go golden test in the engine abstraction (`internal/engine/kopia`), run as root in CI (container) and on the SELinux test host. Include a hardlink, a sparse file, `user.*` and `trusted.*` xattrs, an ACL with a mask, and a subdirectory whose mtime is older than its children.

---

## Q2: SELinux on restore (ADR-0006)

### Method

`scripts/q2_selinux.sh`: it queries the policy defaults with `matchpathcon`, restores a `container_file_t:s0:c10,c20` snapshot into (a1) a normal home directory and (a2) a pre-labelled `container_file_t:s0:c1,c2` directory, reapplies labels with `chcon` and `restorecon`, then runs throwaway Docker containers with bind mounts using no option, `:z` and `:Z`. Rootless Podman is used as an SELinux-enforcing contrast.

### Evidence (trimmed)

```
getenforce=Enforcing  docker SecurityOptions=["name=seccomp,profile=builtin","name=cgroupns"]
dockerd selinux flag: none ; daemon.json absent ; dockerd label system_u:system_r:container_runtime_t:s0
matchpathcon: /var/lib/docker/volumes/x/_data/f  system_u:object_r:container_var_lib_t:s0
              /var/lib/docker/overlay2/x/diff/f  system_u:object_r:container_ro_file_t:s0
              /srv/app/data/f                    system_u:object_r:var_t:s0
(a1) restore into ~ : r-user/data  user_home_t:s0            (source was container_file_t:s0:c10,c20)
(a2) restore into container_file_t:s0:c1,c2 dir : data -> container_file_t:s0:c1,c2 (inherits the parent's type AND MCS, not the source's)
(b1) chcon -R --reference=src/vol r-user  -> container_file_t:s0:c10,c20   OK
     chcon -R unconfined_u:object_r:container_file_t:s0:c10,c20 r-ctr -> OK
(b2) restorecon -Rv r-user : "not reset as customized by admin to ...container_file_t..."   (no change!)
     restorecon -RFv r-user: Relabeled ... to unconfined_u:object_r:user_home_t:s0
     /etc/selinux/targeted/contexts/customizable_types contains container_file_t
(c) docker, no opt : proc label system_u:system_r:spc_t:s0 ; /m/f user_home_t ; write-ok
    docker, :z     : spc_t ; /m/f user_home_t     (NOT relabelled)
    docker, :Z     : spc_t ; /m/f user_home_t     (NOT relabelled)
(c2) podman, no opt: container_t:s0:c436,c953 ; ls /m -> Permission denied
     podman, :Z    : container_t:s0:c480,c658 ; /m/f container_file_t:s0:c480,c658 (relabelled)
```

### Results

| Question | Result |
|---|---|
| (a) Label of a restored file under a user directory | **Verified:** the label is inherited from the parent (`user_home_t:s0`). The source label is lost. |
| (a) Label under a `container_file_t` directory | **Verified:** inherits the parent's type **and MCS pair** (`s0:c1,c2`), not the recorded `s0:c10,c20`. |
| (b) `chcon` reapplies the recorded context (own files) | **Verified**, including `--reference` and an explicit full context with MCS. |
| (b) `restorecon -R` resets to the policy default | **Verified with a caveat:** `container_file_t` is a *customizable type*, so `restorecon` **leaves it untouched unless `-F` is given**. With `-F` it resets to the policy default. |
| (c) Does this Docker daemon enforce SELinux? | **Verified no:** every container runs as `spc_t` (unconfined super-privileged). Bind mounts are readable and writable whatever their label. |
| (c) Do `:z`/`:Z` relabel without `selinux-enabled`? | **Verified no:** they are silently ignored and the host label stays `user_home_t`. With an SELinux-enabled runtime (Podman) `:Z` relabels to a private `container_file_t:s0:cX,cY` and an unlabelled mount is denied. |
| Policy default for Docker volumes | **Verified:** `container_var_lib_t`, not `container_file_t` |

**Conclusion: Partial (Confirmed for the non-root parts).** Kopia never preserves `security.selinux`, so every restore must relabel. ADR-0006's current plan ("`restorecon -R` on paths under the Docker data root") has two problems:

1. `restorecon` without `-F` does not touch `container_file_t`.
2. With `-F`, volume data gets the policy default `container_var_lib_t`. A confined `container_t` process on an SELinux-enabled daemon **cannot** use that (inferred from policy; Docker/Podman label volume contents `container_file_t` at creation). Also, the MCS categories matter for `:Z`-mounted and privately labelled data.

### Proposed ADR impact (ADR-0006 SELinux → Restore)

Replace the restore bullets with:

1. **Capture:** record the full SELinux context (`user:role:type:level` including MCS) of each volume `_data` root and bind-mount root, **and** a per-path exceptions list (in the Q1 sidecar) wherever a label differs from its parent. Also record whether the daemon runs with `selinux-enabled` (`docker info` SecurityOptions contains `name=selinux`).
2. **Restore:** restore into a staging directory, then apply the recorded contexts (`lsetfilecon` or `chcon -R` with the root context, then the exceptions) **before** the staging directory is swapped into place. Do not rely on `restorecon` for container data.
3. Use `restorecon -RF` **only** for paths the agent created that have no recorded label, and never inside `/var/lib/docker/volumes/*/_data`.
4. `:z`/`:Z` relabelling is a safety net only when the daemon is SELinux-enabled. Restore validation must not assume it happens, because it is a no-op when `selinux-enabled` is off (the Fedora/Docker CE default observed here).
5. **Supported-hosts note:** Docker CE on Fedora/Rocky ships without `selinux-enabled` unless configured. Containers then run `spc_t`, and the label mismatch problem is latent: it breaks only when an operator later enables `selinux-enabled`. The agent should warn when a restored label differs from the recorded one, whatever the daemon mode.

### Deferred to a root-capable Rocky/Fedora test host

These could not be tested here: no sudo, the daemon is not SELinux-enabled, and there is no systemd unit install.

1. **Agent as `unconfined_service_t` reading other users' `container_file_t` data.**
   ```
   sudo install -m0755 bin/kopia /usr/local/bin/dbr2-kopia && sudo restorecon -v /usr/local/bin/dbr2-kopia   # expect bin_t
   sudo systemd-run --unit dbr2-spike -p Type=oneshot --wait --pipe \
     bash -c 'cat /proc/self/attr/current; /usr/local/bin/dbr2-kopia snapshot create /var/lib/docker/volumes/<vol>/_data'
   # expect: unconfined_service_t; no AVCs:  sudo ausearch -m AVC -ts recent
   ```
2. **SELinux-enabled daemon.** Put `{"selinux-enabled": true}` in `/etc/docker/daemon.json`, then `systemctl restart docker`.
   - Create a named volume and a postgres container. Check `ls -Z` on `_data` (expect `container_file_t:s0:cX,cY` or `s0`).
   - Back up with the agent. Delete the volume. Restore:
     - (i) without relabelling: expect the container to fail with an AVC denial
     - (ii) with `restorecon -RF`: expect `container_var_lib_t` and failure
     - (iii) with the recorded context applied: expect success
   - Repeat for a bind mount with `:Z` and for one without it.
3. **Restore as root of files carrying `security.selinux`, `trusted.*` and ACLs.** Run the Q1 fixture as real root on the host (not `spc_t` in a container). Confirm the same loss and that the sidecar reapply works with `lsetxattr`, including `trusted.*`.
4. **Policy check for the agent unit.** `sesearch -A -s unconfined_service_t -t container_file_t -c file` and `-t container_var_lib_t`. Record the results.

---

## Q3: Mocked NFS storage (functional only, no performance testing)

### a. Default dev mock (local directory at the production path)

`scripts/q3a_inner.sh` in a non-privileged container, with `-v .work/local-repo:/mnt/dbr2-repo`:

```
mount source for /mnt/dbr2-repo: /dev/nvme0n1p3 btrfs
Connected to repository.
Created snapshot with root k8cf7a06... in 0s
Q3a OK: content+owner+mode identical
```

**Verified.** The configuration is identical to production except for the mount source. On the host itself the path would be `/mnt/dbr2-repo` (creating it needs root once: `sudo install -d -o $USER /mnt/dbr2-repo`). Without root, use a bind mount into the dev container.

### b. Containerized NFS (userspace nfs-ganesha)

- **Server:** image `dbr2spike-nfs-ganesha:local` (`nfs/Dockerfile.ganesha`: `fedora:44` + `nfs-ganesha` + `nfs-ganesha-vfs`).
  - Runs `--privileged` on the user-defined network `dbr2spike-nfs-net` (10.252.77.0/24) at 10.252.77.10.
  - Configuration: NFSv4-only, `FSAL VFS`, export `/export` (bind-mounted from `.work/nfs-export`), `CLIENT { Clients = 10.252.77.20; Access_Type = RW; }`, `No_Root_Squash`.
  - **No host kernel `nfsd` is used or loaded.** No ports are published.
- **Client:** `dbr2spike-nfs-client` (`--privileged`, 10.252.77.20), mounted with production options:

```
10.252.77.10:/export /mnt/dbr2-repo nfs4 rw,noatime,vers=4.1,rsize=1048576,wsize=1048576,namlen=255,hard,
  fatal_neterrors=ENETDOWN:ENETUNREACH,proto=tcp,timeo=600,retrans=2,sec=sys,clientaddr=10.252.77.20,...
== q3b_inner.sh
Initializing repository ... Connected to repository.   create rc=0
Created snapshot ... CONTENT IDENTICAL ... OWNER/MODE IDENTICAL
snapshot verify: Finished processing 24 objects (2 MB)
content verify:  {"verifiedContents":26,"totalErrorCount":0,...}
```

**Verified.** Side effect on the host: mounting from the privileged client **auto-loaded the host's `nfs` and `nfsv4` client kernel modules**. Before the spike only `sunrpc` was loaded. They cannot be unloaded without root, and they are harmless. `nfsd` was **not** loaded. Kernel 7.x adds a `fatal_neterrors=ENETDOWN:ENETUNREACH` default mount option. With it, a *hard* mount still returns an error for those network errors, which is worth knowing for the reposerver.

### c. Outage behaviour

`scripts/q3c_outage.sh pause|stop`:
- Generates 1.5 GB of random data in the client, with compression off.
- Throttles Kopia to 25 MB/s (without the throttle the snapshot finishes in about 2 s into the page cache, before any outage).
- Starts `kopia snapshot create`, then after 4 s runs `docker pause` or `docker stop` on the server for 30 s, then resumes or starts it.

```
pause:  10:10:30 outage begins ... t+10/20/30s kopia state: Dl rpc_wait_bit_killable
        10:11:01 server back -> Created snapshot ... in 58s, rc=0
        snapshot verify --verify-files-percent=100: 3.2 GB, errors 0
        content verify --full: totalErrorCount 0, missingPacks 0, truncatedPacks 0 ; restore diff: identical
stop:   same pattern (server process killed; ganesha restarted with Graceless=true, same IP)
        Created snapshot ... in 57s, rc=0 ; content verify --full: totalErrorCount 0 ; restore identical
        orphan *.tmp blobs in repo afterwards: 0
Connect-time unavailability (server stopped, mount present):
        kopia repo connect ... -> hung, killed by SIGKILL after 90s (rc=137); stat /mnt/dbr2-repo also hangs
        fresh mount while server down -> hung, killed after 60s
Mountpoint present but NFS NOT mounted:
        kopia repo connect -> "cannot access storage path: stat /mnt/dbr2-repo/repo-b: no such file or directory" (good)
        kopia repo create  -> "Connected to repository." -- SILENTLY CREATED A REPO ON THE LOCAL DISK
```

| Scenario | Kopia behaviour (verified) | Repo integrity |
|---|---|---|
| Server paused 30 s mid-snapshot | Blocks in D state (`rpc_wait_bit_killable`), resumes automatically, exits 0 | snapshot and full content verify clean; restore identical |
| Server stopped and restarted mid-snapshot | Same: blocks, resumes after NFSv4 state recovery, exits 0 | clean; no orphan temp blobs |
| Server down at connect time (mount present) | Hangs indefinitely (hard mount). Only SIGKILL ends it. | n/a |
| Server down at mount time | `mount` hangs (at least 60 s here) | n/a |
| Mount missing, empty mountpoint | `connect` fails cleanly. **`create` writes a repo on the local disk.** | wrong location |

**Conclusion: Confirmed.** Containerized NFS works without host changes (userspace ganesha plus a privileged client). A `hard` mount turns outages into indefinite stalls rather than errors, and Kopia recovers transparently with a consistent repository.

### d. Export restriction

```
server CLIENTS=10.252.77.20:
  intruder 10.252.77.30: mount.nfs4: ... failed, reason given by server: No such file or directory   rc=32
  allowed  10.252.77.20: ls /mnt/dbr2-repo -> repo-b ; touch -> allowed client write OK
control, server CLIENTS=10.252.77.20,10.252.77.30:
  10.252.77.30: control mount rc=0, ls -> repo-b
```

**Verified.** The export is restricted by client IP. Refused clients see `ENOENT`, because the NFSv4 pseudo-filesystem hides the export from them, rather than `EACCES`. Tests should assert on "mount fails", not on a specific errno. Root-squash behaviour was not exercised in the time box. The config knob is `Squash = Root_Squash`, a one-line change to `nfs/ganesha-entrypoint.sh`.

### Proposed ADR / final_stack impact

- **final_stack "Testing → Storage mocking":**
  - Name the implementation: "Functional NFS tests use a **userspace nfs-ganesha (VFS FSAL, NFSv4.1 only) container** built from Fedora packages, plus a **privileged client container** on a user-defined network with fixed IPs. No host `nfsd` is used. The host's `nfs`/`nfsv4` client modules are auto-loaded by the client mount."
  - Record the requirements: privileged server and client, and a CI runner that allows `--privileged` (GitHub-hosted runners do; rootless Docker/Podman does not).
  - For outage tests, add a **throttle** (`kopia repo throttle set --upload-bytes-per-second`) or a large enough dataset so the outage really lands mid-write, because the page cache absorbs small snapshots.
  - Assert on "mount refused", not a specific errno.
- **final_stack "NFS guidance" / ADR-0002 (reposerver):**
  - `hard` means **indefinite stalls**, not errors. `dbr2-reposerver` and workflows need their own health and timeouts:
    - a watchdog on reposerver progress, and an alert when the NFS mount stops responding (for example, a `stat` of a sentinel file under a timeout in a separate process)
    - workflow activity heartbeats, so Temporal times out the activity rather than the process
  - Stuck processes are only SIGKILL-able (`rpc_wait_bit_killable`).
  - **Mount guard (new requirement):** `dbr2-reposerver` must refuse to start, and must **never run `repository create`**, unless the repo path is an active NFS mount (`mountpoint -q`, plus an fs-type check `nfs4`, plus a sentinel file such as `.dbr2-repo-id` that matches the Repository ID).
    - Use systemd `RequiresMountsFor=/mnt/dbr2-repo` and `ConditionPathIsMountPoint=`.
    - Without this guard, Kopia silently creates a repository on the local root disk.
  - Consider `vers=4.1` explicitly, and document the kernel's `fatal_neterrors` default.
- **ADR-0002:** no change to the decision. Add an item to "Open items": "reposerver behaviour when its NFS backend stalls (hard mount): client-visible timeouts and error surfacing to agents."

### Deferred to real hardware / root host

- Throughput and seed/incremental timing on the real NAS (explicitly out of scope).
- Kernel-nfsd-backed NAS semantics (NetApp, Synology, TrueNAS), Root_Squash/All_Squash with Kopia's file ownership (the repo files are created `root:root 0600` by a root reposerver), and NAS snapshot consistency with Kopia pack writes.

---

## How to rerun

```
cd spikes/kopia-fidelity-nfs
./run.sh all        # build kopia v0.23.1 into bin/, build images, run Q1, Q2, Q3, then clean up
./run.sh build | q1 | q2 | q3 | cleanup
```

- Needs Go, Docker (with `--privileged` allowed), and Podman (Q2 contrast only). Logs are written to `.work/*.log`.
- Q1 and Q2 host parts run as a normal user. Q2's `restorecon`/`chcon` steps are only meaningful on an SELinux-enforcing host.
- `cleanup` removes files that containers wrote as root (via a container), all `dbr2spike-nfs-*` containers, the `dbr2spike-nfs-net` network and the `dbr2spike-nfs-*` images. It then lists anything left over (expected: nothing).
- **Files:**
  - `scripts/q1_fidelity.sh`, `scripts/q1_root_inner.sh`
  - `scripts/q2_selinux.sh`
  - `scripts/q3a_inner.sh`, `scripts/q3b_inner.sh`, `scripts/q3c_outage.sh`
  - `nfs/Dockerfile.ganesha`, `nfs/Dockerfile.client`, `nfs/ganesha-entrypoint.sh`

## Cleanup state at end of spike

- All `dbr2spike-nfs-*` containers, the network and the images were removed. `docker ps -a`, `network ls`, `volume ls` and `images` are empty for the prefix. No volumes were created.
- Not removed:
  - BuildKit layer cache from the two image builds (no global prune was run, per the rules)
  - the host's auto-loaded `nfs`/`nfsv4` client kernel modules
  - the rootless-Podman image `registry.fedoraproject.org/fedora-minimal:44` used for the Q2 contrast, if it was not already present
- `bin/kopia` is kept, and it is gitignored.

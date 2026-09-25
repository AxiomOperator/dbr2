# ADR-0006: Native agent and host-level volume access

- **Status:** Accepted (owner confirmed 2026-09-25)
- **Date:** 2026-09-25

## Context

The earlier concept (`platform/primary.md`) read volumes through temporary helper containers. The final stack describes a native systemd agent that traverses the host filesystem. The decision has to cover:

- rootless Docker
- non-default `data-root` locations
- volumes that use volume drivers
- SELinux (Fedora and RHEL hosts)

## Options

| | Native systemd agent | Agent in a container |
|---|---|---|
| Survives Docker daemon restart or upgrade | **Yes** | No: in-flight backups and restores are killed |
| Works when Docker is absent or broken (bare-host recovery, reinstalling Docker) | **Yes** | No |
| Access to volumes and bind mounts | Direct, at their real host paths | Needs `/`, `/var/lib/docker`, and every bind path mounted at the same paths |
| Isolation actually gained | — | Little: it still needs `--privileged`, the Docker socket and `label=disable`, which is effectively root on the host |
| Filesystem snapshots (LVM, ZFS, Btrfs) | Host tools available | Awkward: host device and tool access required |
| Offline-mode safety (dead-man switch, ADR-0005) | Independent of Docker | Depends on the Docker daemon it is protecting |
| Installation and packaging | RPM, DEB and tarball, plus a systemd unit | One `docker run` |

## Decision

**The agent is a native, statically linked Go binary running as a systemd service** (`dbr2-agent.service`, running as root).

- **No containerized agent is supported initially.** It could be offered later as a convenience for appliances, with its limitations documented.
- **Helper containers** are used only for opt-in cases where Docker must mount something the host cannot read directly (see volume drivers below).

### Volume access rules

- **Paths:** never hard-code `/var/lib/docker`.
  - Resolve each volume's path from `VolumeInspect().Mountpoint`.
  - Resolve the engine root from `Info().DockerRootDir`.
  - Resolve bind mounts from container inspection (`Mounts[].Source`).
- **Local driver:** the agent reads the mountpoint directly (or a filesystem snapshot of it, ADR-0005).
- **Local driver with network options** (`type=nfs|cifs` in the driver options) and **non-local volume drivers:**
  - classified **External**
  - **not backed up by default** (the data belongs to an external storage system)
  - flagged as a recovery dependency
  - opt-in backup through a helper container that Docker mounts the volume into (v2)
- **Ownership:** numeric UID and GID, modes and timestamps are preserved. Restores run as root and restore numeric ownership.

### Rootless Docker

- **MVP:** rootful Docker only.
- **v2:**
  - The agent discovers rootless engines at `/run/user/<uid>/docker.sock` and reads each engine's `DockerRootDir`.
  - It preserves the on-disk (subuid-shifted) numeric ownership.
  - It restores into the same user's engine.

### SELinux (Fedora and RHEL)

- The agent binary is labeled `bin_t` and runs as `unconfined_service_t` initially. A dedicated confined policy module is a later hardening item.
- **Capture:** the agent records the SELinux context of each volume and bind-mount root in the manifest (ADR-0004).
- **Restore (amended after the spike):**
  - Kopia does **not** preserve `security.selinux`.
  - Restored files inherit the parent directory's label, including its MCS categories.
  - Plain `restorecon` does not change `container_file_t`, and `restorecon -F` would set Docker volume data to `container_var_lib_t`, which is the wrong type.
  - Therefore the agent **records the full SELinux context, including the MCS level, for every entry** in the filesystem metadata record (below), and **reapplies it in the staging directory before swap-in**.
  - `restorecon -RF` is used only for paths that have no recorded label.
  - **Do not rely on Compose `:z`/`:Z`.** Docker CE commonly runs without `selinux-enabled` (containers run as `spc_t`), in which case `:z`/`:Z` have no effect. Restore validation still checks that the containers start.
- **Tests:** the real-Docker integration suite includes an SELinux-enforcing Fedora host.

### Supported hosts

- **v1.0:** AMD64 only. Rocky Linux (primary) and Fedora, both SELinux-enforcing; RPM packages.
- **v2:** Debian and Ubuntu (DEB packages; AppArmor considerations).
- ARM64: Later.

### Unsupported hosts

Docker Desktop on macOS or Windows, and Windows containers, are out of scope.

### Filesystem metadata fidelity (spike result, 2026-09-25: `spikes/kopia-fidelity-nfs/RESULTS.md`)

Kopia v0.23.1 snapshot and restore:

| Preserved | Lost |
|---|---|
| Content | `user.*` extended attributes |
| Mode bits (including setgid) | POSIX ACLs (access and default) |
| Numeric UID/GID (when restoring as root) | `security.selinux` |
| File mtime | Hardlinks |
| Symlinks | |
| Empty directories | |

Additional Kopia restore behavior:

- **Sparse files** are only preserved with sparse writing enabled.
- **Directory mtimes** are restored incorrectly: they become the newest child's mtime.
- **Failed ownership changes are silent** by default (`IgnorePermissionErrors` defaults to true).
- **Security consequence:** losing an ACL can **widen access**. A 0600 file with an ACL restored with group read/write.

**Decision: a DBR² filesystem metadata record for each filesystem component.**

- **Capture:** during capture, the agent walks the component and writes a compressed JSON Lines record containing:
  - extended attributes (`user.*`; `security.selinux` with the full context; `trusted.*` and `security.*` when running as root)
  - POSIX access and default ACLs (`system.posix_acl_*`)
  - hardlink groups
  - directory mtimes
- **Storage:** the record is stored as its own tagged snapshot (`dbr2-kind=fsmeta`, `dbr2-component=<name>`), referenced from the recovery manifest (ADR-0004). It is a **required** part of its component.
- **Restore sequence:**
  1. Kopia restore into a **staging directory** next to the target, with `IgnorePermissionErrors = false` and sparse writing on. Any ownership or permission failure fails the restore.
  2. Recreate hardlinks.
  3. Apply ACLs and extended attributes.
  4. Apply SELinux contexts.
  5. Apply directory mtimes, deepest first.
  6. Verify against the record.
  7. **Swap in** the staging directory with an atomic rename where it is on the same filesystem.
- **Tests:** the real-Docker integration suite asserts full fidelity for every attribute in the table above.
- **Needs a root-capable Rocky and Fedora test host** (procedures are in the spike's RESULTS.md):
  - reading data as `unconfined_service_t` under systemd
  - a daemon with `selinux-enabled`
  - restoring `trusted.*` and SELinux labels as root

## Consequences

- Packaging work: RPM and DEB builds, systemd unit hardening where it is compatible with restore (`ReadWritePaths` rather than a blanket `ProtectSystem`), and signed releases.
- The agent must be kept independent of Docker's availability for its own operation.

## Open items / validation

- ~~Phase 0 spike: check whether Kopia preserves extended attributes and ACLs~~. Done: they are not preserved; see the filesystem metadata record above.
- Root-level SELinux validation on Rocky and Fedora test hosts (the procedures in `spikes/kopia-fidelity-nfs/RESULTS.md`). Tracked in the roadmap.

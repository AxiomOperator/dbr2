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
- **Restore:**
  - Do not assume the repository engine preserves `security.selinux` extended attributes.
  - After restoring, the agent runs `restorecon -R` on paths covered by the default file-context policy (the Docker data root).
  - It reapplies the recorded context to bind-mount roots.
  - Compose `:z`/`:Z` mount options relabel when containers start, and restore validation checks that the containers start.
- **Tests:** the real-Docker integration suite includes an SELinux-enforcing Fedora host.

### Supported hosts

- **v1.0:** AMD64 only. Rocky Linux (primary) and Fedora, both SELinux-enforcing; RPM packages.
- **v2:** Debian and Ubuntu (DEB packages; AppArmor considerations).
- ARM64: Later.

### Unsupported hosts

Docker Desktop on macOS or Windows, and Windows containers, are out of scope.

## Consequences

- Packaging work: RPM and DEB builds, systemd unit hardening where it is compatible with restore (`ReadWritePaths` rather than a blanket `ProtectSystem`), and signed releases.
- The agent must be kept independent of Docker's availability for its own operation.

## Open items / validation

- Phase 0 spike: check whether Kopia preserves extended attributes and ACLs, and on SELinux-enforcing Fedora, whether the unconfined service can read `container_file_t` data and restore it correctly.

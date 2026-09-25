<!-- SPDX-License-Identifier: Apache-2.0 -->
# dbr2-agent packaging (RPM)

The DBR² agent is a native, statically linked Go binary that runs as the systemd service `dbr2-agent.service`, as root (ADR-0006). v1.0 ships an x86_64 RPM for **Rocky Linux 9** (primary) and **Fedora**. DEB packages come in v2.

| Path | Purpose |
|---|---|
| `/usr/bin/dbr2-agent` | Agent binary (0755) |
| `/usr/lib/systemd/system/dbr2-agent.service` | systemd unit |
| `/etc/dbr2/` (0700) | `agent.yaml`, written by `dbr2-agent enroll`. No default config is shipped. |
| `/var/lib/dbr2/agent/` (0700) | Private key, certificates and command journal |
| `/usr/share/doc/dbr2-agent/README.md` | This file |

Files in this directory:

- `nfpm-agent.yaml`: [nfpm](https://nfpm.goreleaser.com/) recipe (nfpm is pinned in the root `Makefile`).
- `systemd/dbr2-agent.service`: the unit. Every hardening directive is commented, including the ones left out on purpose.
- `scripts/`: RPM scriptlets (`%post`, `%preun`, `%postun`).

## Build

```sh
make rpm BUILD=<n>        # -> dist/dbr2-agent-<VERSION>-<n>.x86_64.rpm
make rpm-test BUILD=<n>   # build, then install/verify/upgrade/remove in Rocky Linux 9 and Fedora 44 containers
```

`VERSION` comes from `cmd/agent/VERSION`. Following ADR-0015, the RPM `Version` is `X.Y.Z` and `Release` is the build number, so `rpm -q dbr2-agent` shows `dbr2-agent-X.Y.Z-BUILD.x86_64` and `dbr2-agent version` reports `X.Y.Z.BUILD`. The binary is static, so a single RPM works on every supported distribution and there is no `%{?dist}` suffix. `make rpm` installs the pinned nfpm into `./bin`, and needs nothing system-wide.

Release RPMs are attached to the GitHub Release, and their SHA-256 is listed in the cosign-signed `SHA256SUMS`. The RPM itself is not GPG-signed yet.

## Install

```sh
sudo dnf install ./dbr2-agent-<VERSION>-<BUILD>.x86_64.rpm
```

The package does **not** enable or start the service: until the host is enrolled there is nothing to run. The unit also has `ConditionPathExists=/etc/dbr2/agent.yaml`, so starting it on a host that is not enrolled is skipped cleanly and does not crash-loop.

Docker is not a package dependency. The unit is ordered `After=docker.service` but does not require it, so the agent keeps running when Docker is absent or broken, for example during a bare-host recovery.

## Enroll

In the DBR² console (or through the API), create a **registration token** for the host. The console shows the three values to pass:

```sh
sudo dbr2-agent enroll \
  --server dbr2.example.com:8443 \
  --token <registration token> \
  --ca-sha256 <hex SHA-256 fingerprint of the DBR² CA>
```

`enroll` writes `/etc/dbr2/agent.yaml` and stores the key and certificates in `/var/lib/dbr2/agent`. Pass `--config <path>` to write the config somewhere else, but the unit reads `/etc/dbr2/agent.yaml`. The config keys are `server`, `state_dir` (default `/var/lib/dbr2/agent`), `docker_host` (default `unix:///var/run/docker.sock`), `discovery_interval` (default `5m`) and `log_level`.

## Enable, start, check

```sh
sudo systemctl enable --now dbr2-agent
systemctl status dbr2-agent
sudo dbr2-agent status
journalctl -u dbr2-agent -f
```

Local changes to the unit belong in a drop-in (`sudo systemctl edit dbr2-agent`), which survives upgrades.

## Upgrade

```sh
sudo dnf upgrade ./dbr2-agent-<NEW>.x86_64.rpm
```

On upgrade, the package reloads systemd and restarts the agent only if it was running (`try-restart`). Enablement, config and state are kept.

## Uninstall and purge

```sh
sudo dnf remove dbr2-agent
```

Removing the package stops and disables the service. It deliberately **keeps** `/etc/dbr2` and `/var/lib/dbr2/agent`, so the host's identity (private key and certificates) and the command journal survive an accidental removal, and reinstalling the package brings the agent back as the same host. To remove everything:

```sh
sudo dnf remove dbr2-agent
sudo rm -rf /etc/dbr2 /var/lib/dbr2/agent
sudo rmdir /var/lib/dbr2 2>/dev/null || true
```

Then revoke or delete the host in the DBR² console, so that its certificate can no longer be used.

## SELinux

On SELinux-enforcing Rocky and Fedora hosts, `/usr/bin/dbr2-agent` gets the default `bin_t` label, and systemd runs the service as `unconfined_service_t` (ADR-0006). That domain is what lets the agent read and restore any volume or bind-mount path and reapply SELinux labels. No policy module is shipped. A dedicated confined policy module is a later hardening item.

## Service hardening

The unit only uses sandboxing that keeps backups and restores working on arbitrary paths, with full fidelity. That includes numeric ownership, setuid/setgid bits, xattrs, ACLs and SELinux labels.

- **Enabled:** `ProtectSystem=yes`, `NoNewPrivileges`, `ProtectKernelTunables`/`Modules`/`Logs`, `ProtectControlGroups`, `ProtectClock`, `ProtectHostname`, `LockPersonality`, `RestrictRealtime`, `RestrictNamespaces`, `SystemCallArchitectures=native`, `MemoryDenyWriteExecute`, `RestrictAddressFamilies` and `UMask=0077`.
- **Left out on purpose:** `PrivateTmp`, `RestrictSUIDSGID`, `ProtectHome`, `PrivateDevices`, `ProtectSystem=full`, `CapabilityBoundingSet` and `SystemCallFilter`. The unit file explains why for each one.
- **Bind mounts under `/usr`:** these cannot be restored in place with `ProtectSystem=yes`. On such hosts, add a drop-in with `ProtectSystem=no`.

# Changelog — agent

All notable changes to the `agent` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Backup commands (Phase 4): `ConfigureRepository` (connection persisted 0600 under `<state>/repositories/<id>/`, verified by connecting; one cached Kopia session per repository, reconnected after reconfiguration), `Quiesce`/`Resume` (pause or stop only running containers, exact pre-state restore, idempotent by lease id, conflicting leases refused), `RunHooks` (docker exec, per-hook timeout default 300 s, last 8 KiB of output) and `SnapshotComponents` (config staging incl. unredacted container inspect documents, volumes, bind mounts incl. single files, per-component fsmeta records, `dbr2-*` tags, pinned, seed passes tagged `dbr2-kind=seed`, progress every 5 s; database/image components are skipped until Phase 8).
- Quiesce dead-man switch (ADR-0005 layer 2): leases journaled in `<state>/leases.json`; the agent resumes the application itself when a lease expires (`quiesce.auto_resumed`, critical), warns at 80 % of the lease (`quiesce.lease_warning`), reports failed resumes (`quiesce.resume_failed`) and resumes expired leases immediately after a restart. Events raised while disconnected are sent when the next session starts.
- `internal/fsmeta`: zstd-compressed JSON Lines record of extended attributes (incl. SELinux contexts and POSIX ACLs), hardlink groups and directory mtimes, with a reader for restores.
- Data-moving commands are limited to the gateway's `max_concurrent_jobs` (default 2); queued commands report `{"queued":true}` progress.
- `runtime.ContainerControl` (inspect state/raw, pause, unpause, stop, start, exec), implemented by `DockerRuntime`.
- `Agent.FirstInventoryDelay` (default 10 s) controls when the first periodic inventory push happens.
- `dbr2-agent enroll` (CA fingerprint pinning, local key + CSR, config written 0600), `run` (outbound mTLS session with backoff honouring the gateway's `retry_after`, heartbeat echo, health reports, periodic inventory push, certificate renewal at 2/3 lifetime with a fresh key), `status` (offline), exit code 3 when not enrolled.
- Durable command journal (`journal.jsonl`, fsync per state change): a `command_id` executes at most once; results survive disconnects and restarts and are replayed until acknowledged.
- `DockerRuntime` discovery through the Moby Go SDK: host facts, containers (labels, env, mounts, networks, ports, writable-layer changes), volumes, networks, images with digests and platform, original Compose and `.env` files of hand-deployed projects. The agent keeps running when Docker is unavailable.
- Component scaffold (Phase 1).
- `dbr2-agent` binary with `version` (reports agent and agent-protocol versions). Enrollment, the gateway session and discovery arrive in Phases 2–3.
- RPM package for Rocky Linux 9 and Fedora (`make rpm BUILD=<n>` → `dist/dbr2-agent-<VERSION>-<BUILD>.x86_64.rpm`): installs `/usr/bin/dbr2-agent` and the hardened `dbr2-agent.service` unit, and creates `/etc/dbr2` and `/var/lib/dbr2/agent` (0700). The service is not enabled automatically. It starts only once `/etc/dbr2/agent.yaml` exists (written by `dbr2-agent enroll`), and it does not require Docker. Removing the package keeps the config and state. See `deployments/packaging/README.md`.

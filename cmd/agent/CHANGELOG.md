# Changelog — agent

All notable changes to the `agent` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- `dbr2-agent enroll` (CA fingerprint pinning, local key + CSR, config written 0600), `run` (outbound mTLS session with backoff honouring the gateway's `retry_after`, heartbeat echo, health reports, periodic inventory push, certificate renewal at 2/3 lifetime with a fresh key), `status` (offline), exit code 3 when not enrolled.
- Durable command journal (`journal.jsonl`, fsync per state change): a `command_id` executes at most once; results survive disconnects and restarts and are replayed until acknowledged.
- `DockerRuntime` discovery through the Moby Go SDK: host facts, containers (labels, env, mounts, networks, ports, writable-layer changes), volumes, networks, images with digests and platform, original Compose and `.env` files of hand-deployed projects. The agent keeps running when Docker is unavailable.
- Component scaffold (Phase 1).
- `dbr2-agent` binary with `version` (reports agent and agent-protocol versions). Enrollment, the gateway session and discovery arrive in Phases 2–3.
- RPM package for Rocky Linux 9 and Fedora (`make rpm BUILD=<n>` → `dist/dbr2-agent-<VERSION>-<BUILD>.x86_64.rpm`): installs `/usr/bin/dbr2-agent` and the hardened `dbr2-agent.service` unit, and creates `/etc/dbr2` and `/var/lib/dbr2/agent` (0700). The service is not enabled automatically. It starts only once `/etc/dbr2/agent.yaml` exists (written by `dbr2-agent enroll`), and it does not require Docker. Removing the package keeps the config and state. See `deployments/packaging/README.md`.

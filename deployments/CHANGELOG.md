# Changelog — deployment

All notable changes to the `deployment` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Fixed
- `compose.dev.yaml` builds with `network: host`: on hosts using systemd-resolved with an IPv6 upstream resolver, the default build network could not resolve DNS and `make dev-up` failed in `go mod download` / `npm ci`.

### Added
- dbr2-reposerver: state volume, internal token, `DBR2_REPOSERVER_TLS_NAMES`, Kopia server published on `${DBR2_REPOSERVER_PORT:-51515}` (dev: loopback only), `stop_grace_period: 30s`; `make dev-up` pre-creates `.dev/reposerver-state`.
- Root `.dockerignore`: `.dev`, secrets, `.env`, build output and `node_modules` no longer enter the service build context.
- README: Repository creation with key escrow and first backup; `docs/operations/nas-snapshots.md` (NFS export, mount options, snapshot schedule, restore and verification).
- Agent Gateway published by `dbr2-server` (`DBR2_GATEWAY_PORT`, default 8443; dev 18443), `DBR2_GATEWAY_HOSTNAMES`/`DBR2_GATEWAY_PUBLIC_ADDRESS`; internal control listener and `dbr2_internal_token` secret shared by server and worker (`init-secrets.sh`, idempotent — `make dev-up` now always runs it).
- Agent RPM packaging (`deployments/packaging/`): nfpm recipe (nfpm v2.47.0 pinned, installed into `./bin`), `dbr2-agent.service` systemd unit and upgrade-aware RPM scriptlets. Hardening keeps restores to arbitrary paths working, so `PrivateTmp`, `RestrictSUIDSGID` and `ProtectSystem=full` are left out. The unit is ordered after `docker.service` without requiring it and is gated on `/etc/dbr2/agent.yaml`. New `make rpm` and `make rpm-test` targets. `tests/packaging/rpm-test.sh` installs, verifies, upgrades and removes the RPM in Rocky Linux 9.8 and Fedora 44 containers, and runs as the CI job `rpm`. Release builds attach the RPM to the GitHub Release and include it in the signed `SHA256SUMS`.
- Caddy internal health endpoint (`http://:8090/healthz`, not published) and proxy container HEALTHCHECK. dbr2-server probes the proxy, worker and reposerver through `DBR2_READY_HTTP_CHECKS`, so they appear on the Platform status card.
- Component scaffold (Phase 1).
- Docker Compose deployment: Caddy edge proxy (TLS; `/api/*` → dbr2-server, rest → dbr2-web; discards client `X-Forwarded-*`), dbr2-web, dbr2-server, dbr2-worker, dbr2-reposerver, PostgreSQL 18.6, Valkey 8.1.4 (cache only), Temporal 1.32.0 (schema and namespace jobs), Temporal UI 2.54.1 (loopback only).
- `init-secrets.sh` (Compose secrets + `.env`), PostgreSQL init script (separate roles/databases for dbr2 and Temporal).
- `compose.dev.yaml`: builds from source and mocks the NFS Repository with a local directory.
- `make dev-password`: prints the dev stack's master admin initial password. The README now says to run the production command from `deployments/docker-compose/`.
- `deployments/docker/Dockerfile.services`: one distroless (nonroot) image recipe for the Go services with binary-based HEALTHCHECK.

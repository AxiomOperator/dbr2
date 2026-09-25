# Changelog — deployment

All notable changes to the `deployment` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Caddy internal health endpoint (`http://:8090/healthz`, not published) and proxy container HEALTHCHECK. dbr2-server probes the proxy, worker and reposerver through `DBR2_READY_HTTP_CHECKS`, so they appear on the Platform status card.
- Component scaffold (Phase 1).
- Docker Compose deployment: Caddy edge proxy (TLS; `/api/*` → dbr2-server, rest → dbr2-web; discards client `X-Forwarded-*`), dbr2-web, dbr2-server, dbr2-worker, dbr2-reposerver, PostgreSQL 18.6, Valkey 8.1.4 (cache only), Temporal 1.32.0 (schema and namespace jobs), Temporal UI 2.54.1 (loopback only).
- `init-secrets.sh` (Compose secrets + `.env`), PostgreSQL init script (separate roles/databases for dbr2 and Temporal).
- `compose.dev.yaml`: builds from source and mocks the NFS Repository with a local directory.
- `make dev-password`: prints the dev stack's master admin initial password. The README now says to run the production command from `deployments/docker-compose/`.
- `deployments/docker/Dockerfile.services`: one distroless (nonroot) image recipe for the Go services with binary-based HEALTHCHECK.

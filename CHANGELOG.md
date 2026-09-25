# Changelog — DBR² platform

Platform releases pin component versions in `release-manifest.json` (ADR-0015). Component changes are recorded in each component's own `CHANGELOG.md`:

| Component | Changelog |
|---|---|
| `api` | [`api/CHANGELOG.md`](api/CHANGELOG.md) |
| `server` | [`cmd/server/CHANGELOG.md`](cmd/server/CHANGELOG.md) |
| `worker` | [`cmd/worker/CHANGELOG.md`](cmd/worker/CHANGELOG.md) |
| `agent` | [`cmd/agent/CHANGELOG.md`](cmd/agent/CHANGELOG.md) |
| `reposerver` | [`cmd/reposerver/CHANGELOG.md`](cmd/reposerver/CHANGELOG.md) |
| `cli` | [`cmd/dbr2/CHANGELOG.md`](cmd/dbr2/CHANGELOG.md) |
| `web` | [`web/CHANGELOG.md`](web/CHANGELOG.md) |
| `agent-protocol` | [`proto/agent/CHANGELOG.md`](proto/agent/CHANGELOG.md) |
| `manifest-schema` | [`internal/manifest/CHANGELOG.md`](internal/manifest/CHANGELOG.md) |
| `db-schema` | [`db/CHANGELOG.md`](db/CHANGELOG.md) |
| `deployment` | [`deployments/CHANGELOG.md`](deployments/CHANGELOG.md) |

## [Unreleased]

### Added
- Phase 1 (Foundations): monorepo scaffold, control plane, authentication, RBAC, audit, observability, Temporal worker, Compose deployment and CI.

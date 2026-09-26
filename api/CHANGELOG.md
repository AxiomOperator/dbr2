# Changelog — api

All notable changes to the `api` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Policies (`/policies`, `/applications/{id}/policy`), contracts (`/contracts`, `/applications/{id}/contract`), deletion with grace (`/recovery-points/{id}/delete|undelete`, `/repositories/{id}/delete|undelete`), `/repositories/{id}/verify`, escrow health/regeneration/drills (`/escrow/health`, `/repositories/{id}/escrow/regenerate`, `/escrow/drills`), `database_strategy` in backup settings; recovery points gain deletion and verification fields, Repositories `last_verified_at`/`delete_after` and the `pending_deletion` status. Additive only.
- Platform protection (ADR-0008): `GET /platform/backups` (runs with state, size, SHA-256, file name, snapshot, bundle path and the plaintext manifest), `POST /platform/backups` (202, run now; 409 while one runs), `PUT /repositories/{id}/system` (designate the System Repository). All require `repository.manage`.
- Notifications: `GET/POST /notification-channels`, `GET/PUT/DELETE /notification-channels/{id}`, `POST /notification-channels/{id}/test` (sends a `notification.test` message; the outcome is in the body), `GET /notification-channels/{id}/deliveries`; `GET/PUT /settings/smtp`. Webhook secrets and the SMTP password are write-only (`secret_set`, `password_set`). Reading needs `policy.read`, changing `policy.manage`. Additive only.
- `GET /events` (Server-Sent Events: `job.progress`, `backup.updated`, `restore.updated`, `agent.status`, `alert.created`, `inventory.updated`); `protection` on applications; `GET /jobs`; `GET /containers`; `GET /volumes`. Additive only.
- Restores: `POST /recovery-points/{id}/restore-preview`, `POST /recovery-points/{id}/restores` (202; 403 without `restore.production` for production restores; 400 without typed confirmation or reason; 409 when blocked by collisions), `GET /restores`, `GET /restores/{id}` (preview and result). Additive only.
- Repositories: `GET/POST /repositories`, `GET /repositories/{id}` (live status, `usage_by_host`), `GET /repositories/{id}/escrow-package`, `POST /repositories/{id}/escrow/confirm`, `POST /repositories/{id}/reindex` (202); escrow recipients `GET/POST /escrow/recipients`, `DELETE /escrow/recipients/{id}`.
- Backups: `POST /applications/{id}/backups` (202; 409 while an operation runs or the Repository is not ready), `GET/PUT /applications/{id}/backup-settings`, `GET /recovery-points`, `GET /recovery-points/{id}` (with manifest), `GET /alerts`, `POST /alerts/{id}/acknowledge`; host limits `GET/PUT /agents/{id}/settings`. Additive only.
- Hosts: `GET /agents`, `GET /agents/{id}`, `POST /agents/{id}/{approve,suspend,resume,revoke}` (reason required), `POST /agents/{id}/discover` (202), `GET /agents/{id}/inventory` (secrets masked), registration tokens (`GET`/`POST`/`DELETE /agents/registration-tokens`; token and join command shown once).
- Applications: `GET /applications`, `GET /applications/{id}` (analysis: services, volumes with class, bind mounts, networks, images with digest/platform, dependencies, unprotected paths; containers with masked env), `PATCH /applications/{id}` (owner, environment, criticality, display name), `POST /applications` (manual grouping), `DELETE /applications/{id}` (manual only), `GET /applications/{id}/compose` (original masked or reconstructed; `reveal=true` requires `secrets.read`, audited).
- `GET /health/ready` description documents the platform-service checks (proxy, worker, reposerver). No schema change.
- Component scaffold (Phase 1).
- `/api/v1` REST API (Huma v2 + Chi), OpenAPI 3.1 document committed as `api/openapi.yaml` (22 paths, 24 operations).
- System: `GET /version`, `GET /health/live`, `GET /health/ready` (PostgreSQL critical; Temporal, Valkey degrade).
- Authentication: master admin login (TOTP step, `account_locked` 423 and `rate_limited` 429 with `Retry-After`), logout, `/auth/me`, password change, TOTP enroll/confirm/disable, Entra ID OIDC login/callback (PKCE, nonce, browser-bound state), personal API tokens (`/tokens`).
- Users: users list, roles list, manual role assignment, enable/disable, Entra ID group → role mappings (`user.read` / `user.manage`).
- Audit: paged `GET /audit-events` (`audit.read`).
- Swagger UI at `/api/docs` from embedded, pinned Swagger UI 5.31.1 assets; `/api/openapi.json` and `/api/openapi.yaml`; login required unless `DBR2_API_DOCS_PUBLIC=true` (browsers are redirected to the console login).
- Error model: RFC 9457 problem+json with a stable `code` field.
- Security schemes: `bearerAuth` (API tokens) and `sessionCookie`; per-operation permission published as `x-dbr2-permission` and enforced from the same metadata.

### Notes
- Decision: Huma's default `$schema` body field and `Link` response header are **disabled** (they pointed at an example schema host and add noise to the contract).
- Spec lint test (`TestSpecLint`) fails on undocumented operations or unknown permissions.

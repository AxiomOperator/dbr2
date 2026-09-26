# Changelog — api

All notable changes to the `api` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
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

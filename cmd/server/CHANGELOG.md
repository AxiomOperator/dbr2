# Changelog — server

All notable changes to the `server` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Changed
- `golang.org/x/oauth2` 0.36.0 → 0.37.0 (Dependabot PR #3; used by Entra ID sign-in; applies to every Go binary).

### Added
- `DBR2_READY_HTTP_CHECKS` (`name=url,…`): non-critical readiness checks for other platform services. The dashboard's Platform status card now lists the Caddy edge proxy, dbr2-worker and dbr2-reposerver next to PostgreSQL, Temporal and Valkey (owner request).
- Component scaffold (Phase 1).
- `dbr2-server` subcommands: `serve` (default), `migrate [up|status]`, `openapi -o <file>`, `admin reset-master-password [--password-file] [--disable-totp]`, `healthcheck`, `version`.
- Master admin bootstrap on first start: random initial password written to a root-only file (`/var/lib/dbr2/master-admin-initial-password`, 0600), never logged.
- Authentication service: Argon2id passwords, progressive lockout (5 failures → 1 min … 1 h), per-IP login rate limit, replay-safe TOTP (AES-256-GCM-sealed seeds), server-side sessions (SHA-256 of token stored), idle timeout, personal API tokens, Entra ID OIDC with group-to-role sync (users without a role are denied).
- RBAC (six roles from final_stack plus `user.read`/`user.manage`), CSRF same-origin check for cookie-authenticated unsafe requests, client IP resolved only through trusted proxies.
- Append-only audit log with stable event IDs, mirrored to structured logs; notification outbox row for every master admin login.
- `slog` JSON logging with secret redaction and trace correlation; OpenTelemetry traces/metrics via OTLP (`DBR2_OTEL_ENABLED`).
- Readiness checks for PostgreSQL, Temporal and Valkey; hourly purge of expired sessions and OIDC state.

### Security
- Go toolchain pinned to go1.26.8 (`toolchain` directive in `go.mod`) so CI and release builds include the standard-library fixes for GO-2026-6218, GO-2026-6091, GO-2026-6090, GO-2026-6089, GO-2026-6088 and GO-2026-5972 (found by govulncheck when CI resolved `go 1.26` to go1.26.0). Applies to every Go binary.

### Notes
- The offline reset command is `dbr2-server admin reset-master-password` (run on the server host, e.g. `docker compose exec dbr2-server …`), not the `dbr2` CLI: it needs direct database access.

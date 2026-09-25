# Changelog — server

All notable changes to the `server` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- **Repositories and key escrow (Phase 4, ADR-0008):** escrow recipients (age X25519 or SSH public keys; private keys rejected); Repository creation initializes Kopia on the reposerver with a generated password that is sealed into an age escrow package and **not stored**; Repositories stay `awaiting_escrow` until the package's confirmation code is entered; live reposerver status, certificate-fingerprint drift detection, per-host usage.
- **Backups (Phase 4):** manual backup start with a fast pre-check (host active, Repository ready); capture plan from the inventory (config with redacted metadata, Local volumes, bind mounts minus host plumbing, fsmeta per filesystem component, hooks resolved by service/container name, seed components, backup window for scheduled runs); application backup settings and host limits (audited before/after; host changes make the agent reconnect).
- Internal `PlatformService` on the control listener for dbr2-worker: `PrepareBackup` (idempotent per run), `EnsureAgentAccess` (creates `agent@<id>` on the reposerver and configures the agent over mTLS; the password is never stored), `CompleteBackup`, `GetRepository`, `ListRepositories`, `IndexRecoveryPoints` (the Repository wins; missing rows flagged), `RecordEvent`.
- Recovery-point index, alerts (notification outbox with acknowledgement), gateway handling of agent events (`quiesce.*` audit + alerts) and `Welcome.max_concurrent_jobs` from host settings.
- **Agent Gateway (Phase 2, ADR-0001/0016):** mTLS gRPC listener (`DBR2_GATEWAY_ADDR`, TLS 1.3); agent CA created on first start and stored sealed in PostgreSQL; enrollment with single-use registration tokens; `Connect` sessions with PostgreSQL leases, status, revocation and protocol-major checks, heartbeat round-trip latency; idempotent command dispatcher (re-sends on reconnect); certificate renewal with revocation of older certificates; `ForceReconnect`.
- Internal control listener (`DBR2_CONTROL_ADDR`, `DBR2_INTERNAL_TOKEN[_FILE]`) used by dbr2-worker to dispatch commands.
- **Inventory ingestion and analysis (Phase 3):** secrets sealed before storage; applications reconciled per host (Compose, standalone container, manual); missing applications flagged.
- Fleet service: registration tokens with join command, approve/suspend/resume/revoke with enforced transitions, on-demand discovery via Temporal, application metadata, manual grouping, Compose view and audited secret reveal.
- OpenTelemetry metrics `agent.connection_state` and `agent.latency`.

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

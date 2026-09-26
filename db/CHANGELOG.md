# Changelog — db-schema

All notable changes to the `db-schema` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Migration `00005_policies_retention_contracts`: `protection_policies`, `applications.policy_id`, `recovery_contracts`, recovery point deletion/verification columns and states (`deleted`), Repository `pending_deletion`/`delete_after`/`last_verified_at`, `application_backup_settings.database_strategy`, `escrow_drills`.
- Migration `00007_platform_backups`: `platform_backups` (`pb_<ULID>`, state running/succeeded/partial/failed, trigger, workflow, size, SHA-256, file name, snapshot, System Repository, bundle path, error, plaintext manifest) and `repositories.is_system` (at most one per organization, partial unique index).
- Migration `00006_notifications`: `notification_channels` (email/webhook, sealed webhook secret, event filter, minimum severity, last delivery/error), `notification_deliveries` (per channel; state, attempts, next attempt, error; unique per notification and channel), `platform_settings` (per-organization settings such as `smtp`, with a sealed secret), `agents.offline_alerted_at`.
- Migration `00004_restores`: `restore_runs` (recovery history: request, preview, step, result, outcome).
- Migration `00003_repositories_and_backups`: `escrow_recipients`, `repositories` (escrow package ciphertext, confirmation-code hash, pinned certificate, internal URL; one default), `agent_repository_access`, `recovery_points` (index; state, status, verification, manifest), `application_backup_settings`, `host_settings` (concurrency, backup window); `notification_outbox` gains target, message and acknowledgement.
- Migration `00002_agents_and_inventory`: `pki_authorities` (sealed CA key), `agents` (status lifecycle, versions, health, latency, certificate), `agent_registration_tokens`, `agent_certificates` (revocation history), `agent_sessions` (lease), `agent_commands`, `inventory_snapshots` (sealed), `applications` (kind, ownership metadata, manual containers, missing flag).
- Component scaffold (Phase 1).
- Migration `00001_foundations`: organizations (default org; multi-tenancy seam), users (master admin / OIDC), local credentials, user roles, OIDC group mappings, sessions, API tokens, OIDC auth requests, append-only `audit_events` (UPDATE/DELETE/TRUNCATE rejected by trigger), notification outbox.
- sqlc queries generating `internal/store` (sqlc 1.30.0).

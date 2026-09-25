# Changelog — db-schema

All notable changes to the `db-schema` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Component scaffold (Phase 1).
- Migration `00001_foundations`: organizations (default org; multi-tenancy seam), users (master admin / OIDC), local credentials, user roles, OIDC group mappings, sessions, API tokens, OIDC auth requests, append-only `audit_events` (UPDATE/DELETE/TRUNCATE rejected by trigger), notification outbox.
- sqlc queries generating `internal/store` (sqlc 1.30.0).

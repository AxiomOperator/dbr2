# Changelog — manifest-schema

All notable changes to the `manifest-schema` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Schema 1, additive: optional `topology` (containers with ports, mounts and networks; networks; volumes), component `file_name`, and `database` (`engine`, `format`, `service`, `container`) for dump components (formats `pg_dumpall-sql-zstd`, `rdb`; ADR-0017).
- **Recovery manifest schema v1** (ADR-0004): Go types, `Parse` (accepts additive fields, rejects newer majors), `Validate` (status matches component outcomes, fsmeta required with its parent, unique names, live ⇒ crash-consistent), `ValidateSources` (components written by the recovery point's agent), `StatusFor`, `NewRecoveryPointID` (`rp_<ULID>`); JSON Schema published as `internal/manifest/schema/v1.json` (draft 2020-12) and checked in tests.
- Component scaffold (Phase 1).


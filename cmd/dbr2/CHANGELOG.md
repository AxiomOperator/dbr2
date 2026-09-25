# Changelog — cli

All notable changes to the `cli` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- `dbr2 backup --app APP [--mode live|quiesced|offline] [--wait]` (applications by ID, name or name@host), `dbr2 recovery-points [--app APP]`, `dbr2 admin reindex --repository REPO`; `DBR2_CA_FILE` for private CAs.
- Component scaffold (Phase 1).
- `dbr2 version [--server URL]` and `dbr2 whoami` (personal API token from `DBR2_TOKEN`).

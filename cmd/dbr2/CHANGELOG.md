# Changelog — cli

All notable changes to the `cli` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- `dbr2 admin restore-platform` explains that platform recovery runs on the server host (`dbr2-server admin restore-platform`), because it needs direct database access on a fresh installation.
- `dbr2 restore --rp RP [--target-host ID] [--component NAME]... [--remap FROM=TO]... [--preview] [--reason TEXT] [--confirm APP] [--wait]` (prints the impact preview first) and `dbr2 restores [--app APP]` (recovery history).
- `dbr2 backup --app APP [--mode live|quiesced|offline] [--wait]` (applications by ID, name or name@host), `dbr2 recovery-points [--app APP]`, `dbr2 admin reindex --repository REPO`; `DBR2_CA_FILE` for private CAs.
- Component scaffold (Phase 1).
- `dbr2 version [--server URL]` and `dbr2 whoami` (personal API token from `DBR2_TOKEN`).

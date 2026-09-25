# Changelog — reposerver

All notable changes to the `reposerver` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Component scaffold (Phase 1).
- `dbr2-reposerver` storage-safety layer (ADR-0002): mount guard (active mount of the expected type, parsed from `/proc/self/mountinfo`), `.dbr2-repository-id` sentinel, `init`/`check`/`serve`/`healthcheck`/`version` subcommands, stall watchdog reporting a hung `hard` NFS mount on `/healthz`.
- `DBR2_REPOSERVER_INIT_IF_EMPTY` (development profile only).
- Functional test on a real nfs4 mount (`tests/nfs/run.sh`, 12 checks). The embedded Kopia repository server arrives in Phase 4.

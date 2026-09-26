# Changelog — reposerver

All notable changes to the `reposerver` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Platform self-protection endpoints (Phase 9, ADR-0008) on the management API: `GET /v1/repository-password` (`{"password"}`, to rebuild escrow packages; 409 `not_initialized`), `GET /v1/state-export` (`application/x-tar`, members 0600: `repository-password`, `control-password`, `tls.crt`, `tls.key`, `repository.json`, `kopia/repository.config`; no caches or logs) and `POST /v1/state-import` (platform recovery: only while not initialized and without a repository password in the state dir, else 409 `already_initialized`/`state_present`; archive validated — expected names only, regular files, no traversal, matching repository ID, certificate/key pair — 400 `invalid_state`; storage guard applies and the storage must hold a repository; the Kopia config's storage path is set to this reposerver's; the password is verified with `repository status` and everything rolled back on failure; then served with the imported certificate, so clients keep their pinned fingerprint).
- Component scaffold (Phase 1).
- `dbr2-reposerver` storage-safety layer (ADR-0002): mount guard (active mount of the expected type, parsed from `/proc/self/mountinfo`), `.dbr2-repository-id` sentinel, `init`/`check`/`serve`/`healthcheck`/`version` subcommands, stall watchdog reporting a hung `hard` NFS mount on `/healthz`.
- `DBR2_REPOSERVER_INIT_IF_EMPTY` (development profile only).
- Functional test on a real nfs4 mount (`tests/nfs/run.sh`, 12 checks). The embedded Kopia repository server arrives in Phase 4.
- Embedded Kopia v0.23.1 repository server (Phase 4; ADR-0002, ADR-0007): Kopia's public `cli` package runs as a supervised child process of the same binary (hidden `dbr2-reposerver kopia …` subcommand), restarted with backoff, stopped with SIGTERM then SIGKILL after 20 s. Repository password via `KOPIA_PASSWORD`, user passwords passed in-memory, never on argv; Kopia credential persistence disabled.
- State directory `DBR2_REPOSERVER_STATE_DIR` (0700): `repository-password`, `control-password`, Kopia config/cache, self-signed ECDSA P-256 server certificate (10 y; clients pin its SHA-256 fingerprint, SANs from `DBR2_REPOSERVER_TLS_NAMES` + localhost + hostname).
- Internal management API on `DBR2_REPOSERVER_MGMT_ADDR` (bearer `DBR2_INTERNAL_TOKEN[_FILE]`, problem+json errors): `GET /v1/status`, `POST /v1/initialize` (creates the repository as the maintenance owner `reposerver@dbr2`, global policy zstd-fastest + never-deleting retention, spike-verified ACL set replacing Kopia's defaults; re-attaches to an existing repository on the path), `PUT`/`DELETE /v1/users/{username}`, `POST /v1/acl/read-grants`, `DELETE /v1/acl/read-grants/{id}`; operations serialized and gated by the storage guard.
- `/healthz` reports `initialized` and `server_running` and is unhealthy when an initialized repository's Kopia server is down.
- Integration test (`go test -tags integration ./internal/reposerver/`): ACL isolation, read grants, pins, global policy, restart, fingerprint pinning.

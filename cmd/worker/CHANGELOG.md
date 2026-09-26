# Changelog — worker

All notable changes to the `worker` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- `RetentionWorkflow` (daily `platform-retention`: manifest first, then components, as maint@dbr2), `VerifyRepositoryWorkflow` / `VerifyAllWorkflow` (weekly `platform-verify`, `DBR2_VERIFY_READ_PERCENT`), database dumps run before the quiesce window, manifests carry `database`, `validation` and the capture-time `contract`; the schedule trigger records skipped runs.
- **Platform Protection workflow (Phase 9, ADR-0008, `workflows/platform`):** `PlatformProtectionWorkflow` (`platform/protection`; daily schedule `platform-protection`, `DBR2_PLATFORM_BACKUP_CRON`, default `15 2 * * *`, overlap skip) streams `ExportPlatform` and tees the ciphertext into the System Repository (pinned snapshot `maint@dbr2:/platform`, tag `dbr2-kind=platform`, through the shared `MaintSessions`) and into `DBR2_PLATFORM_BUNDLE_DIR` (default `/var/lib/dbr2/platform-bundles`; temp file + fsync + rename, newest `DBR2_PLATFORM_BUNDLE_KEEP` = 14 kept). One target failing (or no System Repository designated) makes the run Partial; both failing fails it. SHA-256 and size recorded via `RecordPlatformBackup`; heartbeats per chunk.
- Workflows renamed `BackupWorkflow` / `RestoreWorkflow` (unique Temporal type names); a registration test catches duplicate workflow or activity names. Re-created containers are started from the manifest topology (`running_at_capture`), not the captured inspect state.
- **Restore workflow** (`restore.Workflow` on `application/<id>`): cross-host grant (revoked by compensation) → agent access → images by digest → stop the target (dead-man lease) → staged restore with fsmeta verification and swap → re-create containers → start → database dumps → health check → commit; any failure after the stop runs `FinalizeRestore(ROLLBACK)` and records `rolled_back`.
- Manifests include `topology` and per-component `file_name`.
- **Backup workflow (Phase 4):** `backup.Workflow` on `application/<id>`: prepare → agent access → seed pass → pre hooks → quiesce (lease = max quiesce + 10 min) → protect → resume → post hooks → commit. Resume and post hooks are saga compensations (success, failure, cancellation); quiesce-window activities are bounded by the maximum quiesce and wait for cancellation; a failed resume raises `application.not_resumed`; an agent auto-resume fails the run.
- Commit as `maint@dbr2`: every component snapshot is checked (source `agent@<agent id>`, `dbr2-rp`/`dbr2-component` tags, complete) before the manifest is written last, pinned; idempotent on retry.
- `OrphanGC` workflow (schedule `platform-orphan-gc`, `DBR2_ORPHAN_GC_CRON`, grace `DBR2_ORPHAN_GRACE` = 7 days) and `Reindex` workflow (`repository/<id>/reindex`; trusts only manifests from `maint@dbr2`).
- `maint@dbr2` sessions: a fresh random password is set on the reposerver for every new session (every worker start) and kept only in memory; `DBR2_WORKER_STATE_DIR` holds the disposable Kopia client cache; Repositories may define an `internal_server_url` for the worker.
- `workflows/agentcmd`: shared gateway dispatcher (used by host and backup activities).
- `temporalx.StartRepositoryOperation` (`repository/<id>/<operation>`); the Temporal usage lint now flags any `.ExecuteWorkflow(` outside `internal/temporalx` and skips dot-directories.
- `DiscoverHost` workflow (ID `host/<agent>/discover`, one at a time) and the gateway-dispatching activity: stable `command_id` across retries, heartbeats with agent progress, non-retryable failure for non-active agents; control-channel client (`DBR2_GATEWAY_CONTROL_ADDR`, `DBR2_INTERNAL_TOKEN[_FILE]`).
- `temporalx.StartHostOperation` for host-level operations.
- Component scaffold (Phase 1).
- `dbr2-worker` Temporal worker (task queue `dbr2-control`, namespace `dbr2`) with `/healthz` and `healthcheck`/`version` subcommands.
- `temporalx.StartApplicationOperation`: the only sanctioned start path for application operations (conflict policy FAIL, reuse ALLOW_DUPLICATE, `WorkflowExecutionErrorWhenAlreadyStarted`, no workflow-level timeouts).
- `ScheduledOperationTrigger` (schedule → trigger → child with `application/{id}`, ParentClosePolicy ABANDON, records `skipped_overlap`).
- `saga` package: compensations run in a disconnected context on success, failure and cancellation.
- `ApplicationSelfTest` diagnostic workflow.
- Lint test forbidding `TerminateWorkflow` and direct `ExecuteWorkflow` outside `internal/temporalx`; integration test against a real Temporal 1.32.0 dev server.

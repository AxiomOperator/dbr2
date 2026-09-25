# Changelog — worker

All notable changes to the `worker` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
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

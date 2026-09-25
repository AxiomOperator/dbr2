# Changelog — worker

All notable changes to the `worker` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Component scaffold (Phase 1).
- `dbr2-worker` Temporal worker (task queue `dbr2-control`, namespace `dbr2`) with `/healthz` and `healthcheck`/`version` subcommands.
- `temporalx.StartApplicationOperation`: the only sanctioned start path for application operations (conflict policy FAIL, reuse ALLOW_DUPLICATE, `WorkflowExecutionErrorWhenAlreadyStarted`, no workflow-level timeouts).
- `ScheduledOperationTrigger` (schedule → trigger → child with `application/{id}`, ParentClosePolicy ABANDON, records `skipped_overlap`).
- `saga` package: compensations run in a disconnected context on success, failure and cancellation.
- `ApplicationSelfTest` diagnostic workflow.
- Lint test forbidding `TerminateWorkflow` and direct `ExecuteWorkflow` outside `internal/temporalx`; integration test against a real Temporal 1.32.0 dev server.

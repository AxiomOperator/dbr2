# Temporal guidelines

Rules for every workflow in `workflows/` (ADR-0005, ADR-0009, ADR-0011). CI enforces the first two.

1. **Start application operations only with `temporalx.StartApplicationOperation`.**
   - It sets the workflow ID `application/{id}`, conflict policy FAIL, reuse ALLOW_DUPLICATE and `WorkflowExecutionErrorWhenAlreadyStarted`.
   - Without that last option, the Go SDK silently returns the already-running workflow.
   - `TestTemporalUsageLint` fails on `ExecuteWorkflow` outside `internal/temporalx`.
2. **Never terminate.**
   - Termination and workflow-level timeouts skip saga compensation.
   - DBR² offers **Cancel only**, and `TestTemporalUsageLint` fails on `TerminateWorkflow`.
   - Do not set `WorkflowRunTimeout` or `WorkflowExecutionTimeout` on workflows that quiesce an application.
3. **Guarantee compensation with `saga`.**
   - Register the undo step (for example resume the application) immediately after the step it undoes succeeds.
   - Run the saga from a `defer`. It uses a disconnected context, so it also runs on cancellation.
4. **Scheduled operations go through `ops.ScheduledOperationTrigger`.**
   - Schedules use overlap policy SKIP. The trigger starts the operation as a child with `application/{id}` and ParentClosePolicy ABANDON.
   - When the application is busy, it records `skipped_overlap`.
5. **Determinism.** Workflow code must be deterministic:
   - use `workflow.Now`, `workflow.Sleep` and `workflow.SideEffect`;
   - no goroutines, maps iterated for output, network or file I/O;
   - put all I/O in activities.
6. **Versioning.** Changing a workflow that may have running executions requires `workflow.GetVersion` branches. Worker identities carry the four-part worker version (ADR-0015).
7. **Activities are idempotent.**
   - They may be retried.
   - Agent commands carry `command_id` = workflow ID + activity ID + attempt (ADR-0001).
   - Long activities heartbeat. Set the heartbeat timeout above the gateway's reconnect grace period, and put the agent's checkpoint in the heartbeat payload.
8. **No secrets in workflow inputs.** Workflow histories are visible in the Temporal UI. Pass references, never credentials.
9. **Namespace and task queue.** Use namespace `dbr2` and task queue `dbr2-control` (configurable). Repository-wide operations use `temporalx.RepositoryWorkflowID(repo, op)`.

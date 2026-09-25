# Spike: Temporal on PG18, workflow-ID exclusivity, saga compensation

- **Date:** 2026-09-25 · **Time spent:** about 45 minutes · **Branch:** `docs/architecture-foundation`
- **Scope:** ADR-0009 (Temporal orchestration), ADR-0011 (concurrency control), ADR-0005 Layer 1 (quiesce saga), ADR-0001 (heartbeats)
- **Overall:** all three questions **Confirmed**. The run ended with 0 assertion failures. The spike also found one SDK footgun and one schedule limitation, and both need ADR text.

| Component | Version / image |
|---|---|
| PostgreSQL | `postgres:18.6-trixie` (server reports `PostgreSQL 18.6 (Debian 18.6-1.pgdg13+2)`) |
| Temporal server | `temporalio/server:1.32.0` (latest; the host's unrelated `pm-temporal` also runs 1.32.0) |
| Schema and CLI tools | `temporalio/admin-tools:1.32.0` (`temporal` CLI 1.9.0; **`tctl` is no longer shipped**) |
| Temporal UI | `temporalio/ui:2.54.1` |
| Go SDK | `go.temporal.io/sdk v1.49.0`, `go.temporal.io/api v1.63.5`, Go 1.26.8 |
| Persistence plugin | `postgres12` (the SQL plugin for PostgreSQL 12 and later) |

---

## Q1: Temporal in Docker Compose on PostgreSQL 18 with separate databases

### Method

1. **Chose images from current upstream guidance:**
   - `temporalio/auto-setup` is effectively **deprecated**. Its last tag on Docker Hub is `1.29.7` (2026-06), while `server` and `admin-tools` are at `1.32.0`.
   - The old `temporalio/docker-compose` repository is **archived**. Its README says to use `temporalio/samples-server/compose`.
   - The upstream pattern is `temporalio/server` plus a one-shot `temporalio/admin-tools` container. That container runs `temporal-sql-tool setup-schema` and `update-schema`, and a second admin-tools job creates the namespace with `temporal operator namespace create`. Upstream still pins **PG16**. This spike used **PG18**.
2. **`compose.yaml`** (project `name: dbr2spike-temporal`). All ports are bound to 127.0.0.1, and I confirmed with `ss -ltn` that they were free first.
   - `postgres` (host port 15432). The init script `initdb/10-create-dbs.sh` creates:
     - roles `temporal` and `dbr2`
     - databases `temporal` and `temporal_visibility` (owner `temporal`) and `dbr2` (owner `dbr2`)
     - `REVOKE ALL ... FROM PUBLIC` on all three
   - `temporal-schema`: admin-tools running `scripts/setup-temporal-schema.sh`, gated on `service_completed_successfully`. It uses no `create` step, because the databases already exist. It is idempotent: it runs `update-schema` first and falls back to `setup-schema -v 0.0`.
   - `temporal` (host port 17233), `temporal-create-namespace` (namespace `dbr2`, retention 72h), `temporal-admin-tools` (a long-running CLI shell) and `temporal-ui` (host port 18233).

### Evidence

```text
$ docker compose up -d && docker compose ps -a
postgres                   running  Up (healthy)
temporal                   running  Up (healthy)
temporal-schema            exited   Exited (0)
temporal-create-namespace  exited   Exited (0)     -> "Namespace dbr2 successfully registered."
temporal-admin-tools       running
temporal-ui                running                   -> curl http://127.0.0.1:18233/ = HTTP 200

$ psql -U postgres -c '\l'   (trimmed)
dbr2|dbr2|...|dbr2=CTc/dbr2
temporal|temporal|...|temporal=CTc/temporal
temporal_visibility|temporal|...|temporal=CTc/temporal

$ psql -d temporal -c 'select * from schema_version'             -> temporal|1.19|1.0
$ psql -d temporal_visibility -c 'select * from schema_version'  -> temporal_visibility|1.14|0.1
$ psql -U temporal -d dbr2 -> FATAL: permission denied for database "dbr2"  (isolation OK)

$ docker compose exec temporal-admin-tools temporal operator namespace list --address temporal:7233
  NamespaceInfo.Name  temporal-system ... Registered
  NamespaceInfo.Name  dbr2            ... Registered   Config.WorkflowExecutionRetentionTtl 72h0m0s
$ ... temporal operator cluster health   -> SERVING
$ ... temporal operator cluster system   -> ServerVersion 1.32.0  SupportsSchedules true
$ ... temporal --version                 -> temporal version 1.9.0 (Server 1.32.0, UI 2.54.1)
$ ... tctl --version                     -> sh: tctl: not found
$ docker compose run --rm temporal-schema  -> "Temporal schema setup complete" (re-run is a no-op)
```

- **Logs:** the schema log contains no ERROR or WARN lines. On PG18 the visibility migrations (`CREATE INDEX CONCURRENTLY`, GIN `jsonb_path_ops`, generated `STORED` columns) all apply cleanly.
- **Server startup:** the server logged a few `shard status unknown` and `Not enough hosts` messages during its first second. These are normal ring-membership warm-up.

### PG18 notes and incompatibilities

- **No Temporal incompatibility found** with the `postgres12` plugin on PG18.6.
- **PG18 image change, which is not a Temporal issue:** the official `postgres:18` images set `PGDATA=/var/lib/postgresql/18/docker`, and the volume must be mounted at **`/var/lib/postgresql`**, not `/var/lib/postgresql/data` as in PG ≤17 examples. The upstream Temporal compose files, which target PG16, use the old path.
- **Server configuration comes from environment variables only.** `temporalio/server` 1.32 has no `config_template.yaml` in the image; it reads the variables directly: `DB=postgres12`, `DB_PORT`, `POSTGRES_SEEDS`, `POSTGRES_USER`, `POSTGRES_PWD`, `DBNAME=temporal`, `VISIBILITY_DBNAME=temporal_visibility`, `BIND_ON_IP=0.0.0.0` and `DYNAMIC_CONFIG_FILE_PATH=config/dynamicconfig/development-sql.yaml`, with `./dynamicconfig` mounted at `/etc/temporal/config/dynamicconfig`. The admin-tools schema job also needs `SQL_PASSWORD`.
- **Least privilege:** Temporal's role does not need `CREATEDB` or superuser, because the init script creates the databases and the role owns them. PG15+ `public` schema ownership (`pg_database_owner`) makes this work.

### Conclusion: **Confirmed**

Temporal 1.32.0 runs on PG18.6 in its own `temporal` and `temporal_visibility` databases, isolated from `dbr2`. The upstream-recommended layout is `temporalio/server` plus an admin-tools schema job; `auto-setup` should not be used.

### Proposed ADR impact

- **ADR-0009 and the Workflows section of `final_stack.md`**, add:
  - **Images:** `temporalio/server`, `temporalio/admin-tools` and `temporalio/ui`, pinned to exact versions (currently 1.32.0 / 1.32.0 / 2.54.1). **`temporalio/auto-setup` must not be used.** It is deprecated and unpublished since 1.29.x, and it performs implicit schema migrations on start.
  - **Schema migrations** run as an explicit one-shot Compose job (`temporal-sql-tool --plugin postgres12 setup-schema` / `update-schema`). The server starts only after that job succeeds (`depends_on: service_completed_successfully`). Temporal upgrades (already a roadmap item) therefore follow this order: bump the admin-tools image → run the schema job → bump the server image.
  - **Database provisioning:** the `dbr2` Postgres init step (or installer) creates the roles `temporal` and `dbr2` and the three databases with separate owners, plus `REVOKE CONNECT ... FROM PUBLIC`. Temporal's role does not get `CREATEDB` or superuser.
  - **Namespace:** DBR² uses a dedicated Temporal namespace, `dbr2` (not `default`), created by an idempotent admin-tools job. Retention is a setting (72h was used in the spike).
  - **CLI:** operator documentation uses the `temporal` CLI (from admin-tools). `tctl` is gone.
- **`final_stack.md` Data Layer:** note the PG18 volume mount path (`/var/lib/postgresql`).

---

## Q2: Workflow-ID exclusivity (ADR-0011)

### Method

The Go program is `spike run q2`. Scenarios are in `scenarios.go`, and the workflows are `BackupWorkflow`, `RestoreWorkflow` and `ScheduledBackupTrigger` in `workflows.go`. Every start uses these options:

```go
client.StartWorkflowOptions{
  ID: "application/app-1", TaskQueue: "dbr2-spike",
  WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
  WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
  WorkflowExecutionErrorWhenAlreadyStarted: true,   // <-- REQUIRED, see footgun
}
```

For the schedule scenarios, a Temporal Schedule (`backup/app-1/<mode>`, overlap `SKIP`) starts `ScheduledBackupTrigger`. The trigger starts `BackupWorkflow` under `application/app-1` in one of two ways:

- **child mode:** `ExecuteChildWorkflow` with `ParentClosePolicy=ABANDON`, waiting on `GetChildWorkflowExecution()`. A conflict surfaces as `*temporal.ChildWorkflowExecutionAlreadyStartedError`.
- **activity mode:** an activity calls `client.ExecuteWorkflow` with the options above. A conflict surfaces as `*serviceerror.WorkflowExecutionAlreadyStarted`.

On a conflict, the trigger returns `Outcome: "skipped_overlap"` instead of failing, and a `RecordScheduleOutcome` activity records it. In DBR², that activity would insert a `schedule_runs` row. The schedule was fired with `ScheduleHandle.Trigger` so that timing is deterministic.

### Evidence (trimmed from `bin/spike run`)

```text
=== Q2a: second BackupWorkflow start with same ID while running (ConflictPolicy=FAIL)
  PASS second start rejected: *serviceerror.WorkflowExecutionAlreadyStarted: Workflow execution is already running.
       WorkflowId: application/app-1, RunId: 01a0d91c-76c6-...
  PASS error carries running RunId=01a0d91c-76c6-... (for 'operation already in progress' UI)
=== Q2a': SDK footgun - WorkflowExecutionErrorWhenAlreadyStarted=false (the default)
  PASS with default flag, ExecuteWorkflow returns err=<nil> and silently hands back the EXISTING run 01a0d91c-76c6-...
  PASS ...even for a different workflow type (RestoreWorkflow): err=<nil> run=01a0d91c-76c6-...
=== Q2b: RestoreWorkflow (different type) with same ID while BackupWorkflow runs
  PASS restore start rejected: Workflow execution is already running. WorkflowId: application/app-1, ...
=== Q2c: after first completes, same ID can be started again (ReusePolicy=ALLOW_DUPLICATE)
  PASS new start (RestoreWorkflow) accepted ... PASS new start (BackupWorkflow) accepted
=== Q2d (child): Schedule -> ScheduledBackupTrigger -> BackupWorkflow{ID=application/app-1}
  trigger workflow ID = schedule-trigger/app-1/child-2026-09-25T15:08:46Z
  PASS schedule-started workflow ID has timestamp suffix (so it cannot be application/app-1)
  PASS schedule fired while manual running -> outcome=skipped_overlap detail="child workflow execution error
       (type: BackupWorkflow, workflowID: application/app-1, runID: , ...): child workflow execution already started"
  PASS manual backup unaffected (still running)
  PASS schedule fired while idle -> outcome=started run=01a0d91c-b5a0-...
  PASS application/app-1 run ... is a BackupWorkflow ; child's parent execution = schedule-trigger/app-1/child-...
  PASS scheduled BackupWorkflow completed (outlived its trigger: ABANDON)
=== Q2d (activity): ...
  PASS schedule fired while manual running -> outcome=skipped_overlap ... conflictRun=01a0d91c-bed6-...
  PASS schedule fired while idle -> outcome=started ...
  PASS scheduled BackupWorkflow completed (outlived its trigger: ABANDON)
```

Q3(iv) below also confirms that **terminating** a run releases the ID: a new start succeeds afterwards.

### Findings

1. **SDK footgun.** In the Go SDK, `client.ExecuteWorkflow` **swallows** `WorkflowExecutionAlreadyStarted` unless `WorkflowExecutionErrorWhenAlreadyStarted: true` is set. The default is `false`, and `internal_workflow_client.go` substitutes the existing run's ID. The call then returns a handle to the *other* running workflow, even when that workflow is of a different type. Without the flag, the "operation already in progress" check silently does nothing, and a caller that then calls `run.Get()` would wait on someone else's restore.
2. **Rejection works across types.** A different workflow type is rejected in the same way, so ADR-0011's "regardless of workflow type" statement holds.
3. **Child mode versus activity mode:**
   - **Child mode** is deterministic and exactly-once, because the start is a workflow command and needs no client inside workers. Its drawback is that `ChildWorkflowExecutionAlreadyStartedError` does **not** include the running RunID (`runID:` is empty). Getting the running RunID for the skip record needs a `DescribeWorkflowExecution` activity.
   - **Activity mode** gets the running RunID for free. However, the worker needs a Temporal client, and the start is **not idempotent across activity retries**: the Go SDK has no public `RequestID`. If the start succeeded but the activity's completion was lost, the retry sees AlreadyStarted and misreports a skip.
   - **Recommendation: child mode.**
4. **Schedule-level overlap SKIP** only guards against overlapping *trigger* workflows. Those finish in milliseconds, so the real exclusion is the workflow ID. The schedule's `Overlap` setting is kept at SKIP for defense in depth.

### Conclusion: **Confirmed**

This includes the schedule-trigger pattern, with one footgun (Finding 1) and one limitation (the child error carries no RunID).

### Proposed ADR impact

- **ADR-0011, Decision › Application operations.** Replace "is started with a workflow ID conflict policy that rejects the start" with:
  > Every start of an application operation, whether manual, API, scheduled or child, uses `WorkflowID = application/{application_id}`, `WorkflowIDConflictPolicy = FAIL` and `WorkflowIDReusePolicy = ALLOW_DUPLICATE`. Client starts must set `WorkflowExecutionErrorWhenAlreadyStarted = true`, because the Go SDK otherwise returns the existing run without an error. A single `StartApplicationOperation` helper wraps this, and a unit test asserts the flag. A lint check forbids direct `ExecuteWorkflow` calls for `application/*` IDs.
- **ADR-0011, new bullet "Scheduled runs":**
  > Temporal Schedules append a timestamp to the workflow ID of each action, so a Schedule cannot start `application/{id}` directly. Each protection policy's Schedule (`schedule/application/{id}/backup`, overlap `SKIP`) starts a short `ScheduledBackupTrigger` workflow. The trigger starts `BackupWorkflow` as a **child** with `WorkflowID = application/{id}` and `ParentClosePolicy = ABANDON`, and waits only for the child to *start*. On `ChildWorkflowExecutionAlreadyStartedError` it records a `skipped_overlap` schedule run: an activity writes a row in the `dbr2` database, and a `DescribeWorkflowExecution` lookup gets the running operation's ID. The trigger then completes successfully. Starting the backup from an activity is rejected, because it is not idempotent across retries.
- **ADR-0011, Consequences:** the "operation already in progress" error comes from `serviceerror.WorkflowExecutionAlreadyStarted.RunId` for API starts, and from Describe for scheduled starts.
- **`final_stack.md`, Concurrency Control:** its statement "released when the workflow completes, fails, times out or is terminated" is confirmed. Add a cross-reference to the termination caveat in Q3.

---

## Q3: Saga compensation guarantee (ADR-0005 Layer 1) and heartbeats (ADR-0001)

### Method

`BackupWorkflow` runs RecordPreState → Quiesce → *(register compensation)* → ProtectVolumes → Resume. After `Quiesce` succeeds, it registers compensation:

```go
resumed := false
defer func() {
    if resumed { return }
    dctx, _ := workflow.NewDisconnectedContext(ctx)
    dctx = workflow.WithActivityOptions(dctx, workflow.ActivityOptions{StartToCloseTimeout: 30*time.Second,
        ScheduleToCloseTimeout: 30*time.Minute,
        RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2,
            MaximumInterval: 30*time.Second, MaximumAttempts: 0}})
    _ = workflow.ExecuteActivity(dctx, a.Resume, in.AppID, preState).Get(dctx, nil) // on error: Needs Attention
}()
```

`ProtectVolumes` is configured as follows:

- `StartToCloseTimeout = MaxQuiesce` (60 minutes)
- `HeartbeatTimeout = 3s`
- `WaitForCancellation = true`

It heartbeats its progress every second. On a retry it resumes from `activity.GetHeartbeatDetails`, which is the checkpoint.

**Test harness:** the `spike run` process starts the worker as a **separate process** (`spike worker`), so it can `SIGKILL` and restart it. Assertions read the workflow **history** (the activity trail) and `DescribeWorkflowExecution` status.

### Evidence (trimmed)

```text
=== Q3 baseline: success path
  history: RecordPreState -> Quiesce -> scheduled:ProtectVolumes -> completed:ProtectVolumes -> scheduled:Resume -> completed:Resume
=== Q3(i): later activity fails permanently
  history: ... completed:Quiesce -> scheduled:ProtectVolumes -> failed:ProtectVolumes(disk full on repository) -> scheduled:Resume -> completed:Resume
  PASS status FAILED   PASS compensation Resume completed
=== Q3(ii): workflow cancelled by client mid-way
  history: ... completed:Quiesce -> scheduled:ProtectVolumes -> WF_CANCEL_REQUESTED -> canceled:ProtectVolumes -> scheduled:Resume -> completed:Resume
  PASS status CANCELED  PASS compensation Resume completed
  PASS ProtectVolumes acknowledged cancel BEFORE Resume was scheduled (WaitForCancellation=true)
=== Q3(iii): worker process SIGKILLed mid-ProtectVolumes, restarted
  [worker pid=954590 SIGKILLed]  (5 s down)  [worker started pid=959755]
  PASS workflow completed after restart (12.2s after kill)
  PASS ProtectVolumes retried after heartbeat timeout: final attempt=2
  PASS retry resumed from heartbeat checkpoint=1 (not from 0)
  PASS Resume ran exactly once
=== Q3(iv): workflow TERMINATED mid-way (expect: NO compensation)
  history: ... completed:Quiesce -> scheduled:ProtectVolumes -> WF_TERMINATED
  PASS status TERMINATED
  PASS Resume was NEVER scheduled -> app left quiesced (Layer 2 dead-man switch must cover)
  PASS terminated run released the application ID (new start ok)
=== Q3(v): stalled ProtectVolumes (no heartbeats), MaxAttempts=1 -> heartbeat timeout -> compensation
  PASS failed with HEARTBEAT timeout after 4.3s (StartToClose is 60m)
  history: ... scheduled:ProtectVolumes -> timedout:ProtectVolumes(activity Heartbeat timeout) -> scheduled:Resume -> completed:Resume
=== Q3(v'): stalled ProtectVolumes with retries -> retried from checkpoint, completes
  PASS completed on attempt 2, resumed from checkpoint 1
=== DONE: 0 failure(s)
```

### Findings

- **(i) Permanent failure and (ii) cancellation:** the `defer` plus disconnected-context compensation runs in both cases.
  - `WaitForCancellation: true` matters. Without it, the workflow sees `CanceledError` immediately and would call Resume while the agent may still be reading the volume.
- **(iii) Worker crash:** the workflow replays on the new worker. The killed attempt is detected by the **3s heartbeat timeout**, not the 60-minute StartToClose. The retry resumes from the heartbeat checkpoint, and Resume runs exactly once.
  - Heartbeats are throttled to about 0.8 × HeartbeatTimeout, so the checkpoint lags a little. Agent commands must be idempotent anyway (ADR-0001 `command_id`).
- **(iv) Termination:** confirmed that it runs **no** workflow code. The `defer` never executes and Resume is never scheduled, so the application stays quiesced. Only ADR-0005 Layer 2, the agent's dead-man switch lease, recovers it.
  - Termination *does* release the `application/{id}` lock, so a new operation such as a restore could start against an application that is still quiesced.
  - A Temporal-side timeout (`WorkflowExecutionTimeout` or `WorkflowRunTimeout`) behaves like termination, because no workflow code runs. This is documented Temporal behavior; the spike did not exercise it. **Do not set workflow-level timeouts on `BackupWorkflow` or `RestoreWorkflow`.** Bound activities instead, as ADR-0005 already requires.
- **(v) Stalled agent (ADR-0001):** a stalled `ProtectVolumes` is detected within `HeartbeatTimeout` (about 4s here), regardless of the 60-minute StartToClose. With retries it is re-dispatched; with no retries left, the compensation runs.

### Conclusion: **Confirmed** for (i), (ii), (iii) and (v). **Confirmed as a negative result** for (iv): termination bypasses compensation.

### Proposed ADR impact

- **ADR-0005, Layer 1, "Register compensation":** change "so it executes on success, failure **and** cancellation" to:
  > …on failure **and** cancellation, and after worker or server crashes, because the workflow replays. It does **not** run when a workflow is **terminated** or hits a workflow-level timeout (`WorkflowExecutionTimeout` / `WorkflowRunTimeout`), since Temporal runs no workflow code in those cases. Therefore:
  > - (a) DBR² never terminates operation workflows. The UI and API expose **Cancel** only. Operator runbooks forbid `temporal workflow terminate` on `application/*` IDs, and access to the Temporal UI and CLI is admin-only (ADR-0009). An emergency "force stop" issues Cancel and relies on Layer 2.
  > - (b) Quiescing workflows set no workflow-level execution or run timeout. Bounds live on activities (max quiesce duration).
  > - (c) Layer 2, the agent dead-man switch, is the **only** protection against termination and is therefore mandatory, not optional.
  > - (d) Before a new application operation quiesces or writes, it must check the agent's lease journal for an unexpired quiesce lease on that application. It either waits for auto-resume or resumes first, because a terminated run released the `application/{id}` lock while the application may still be quiesced.
- **ADR-0005, Layer 1:** add "Activities inside the quiesce window use `WaitForCancellation = true` so that Resume is issued only after the agent has stopped reading." Also add "Resume compensation uses a retry policy with a ScheduleToClose bound (for example 30 minutes); on exhaustion the application is flagged *Needs Attention: not resumed*."
- **ADR-0001, Disconnect semantics:** add "Activities that relay agent progress set `HeartbeatTimeout`, 3s in the spike, with the production value to be tuned (for example 30–60s), to be larger than the gateway's reconnect grace. The heartbeat payload is the agent's progress checkpoint, and retries pass it back to the agent with the same `command_id`." The spike showed that stall detection is independent of StartToClose.
- **ADR-0009, Consequences:** add "Workflow termination is an operator-only emergency action that bypasses compensation (see ADR-0005)."

---

## How to rerun

```bash
cd spikes/temporal
ss -ltn | grep -E ':(15432|17233|18233)\b'          # must print nothing
docker compose up -d                                  # project name dbr2spike-temporal (set in compose.yaml)
docker compose ps -a                                  # schema/namespace jobs Exited (0); temporal healthy
docker compose exec temporal-admin-tools temporal operator namespace list --address temporal:7233
# UI: http://127.0.0.1:18233  (namespace dbr2)

go build -o bin/spike .
./bin/spike run            # all scenarios; or: ./bin/spike run q2   /   ./bin/spike run q3
                           # worker subprocess logs: bin/worker.log ; exit code != 0 on any FAIL
docker compose down -v     # removes containers, network and the pgdata volume
```

The runner terminates any leftover spike workflows and deletes its schedules at startup, so it can be rerun against the same stack. The whole run takes about 2 minutes. `.env` holds spike-only credentials.

## Files

- `compose.yaml`, `.env`, `initdb/10-create-dbs.sh`, `scripts/setup-temporal-schema.sh`, `scripts/create-namespace.sh`, `dynamicconfig/development-sql.yaml`
- `main.go` (the worker/run entry point), `workflows.go`, `activities.go`, `scenarios.go` (assertions)
- `go.mod` (module `github.com/AxiomOperator/dbr2/spikes/temporal`)
- `.gitignore` (`bin/`, `*.log`)

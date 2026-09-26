# ADR-0018: Scheduling, retention, recovery contracts, notifications and verification

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Phases 7–9 turn one-off backups into continuous protection. They add:

- schedules
- retention that deletes data
- a promise to meet (the recovery contract)
- outbound notifications
- proof that stored data is still readable
- health checks for the keys that make it readable

Each of these can destroy data or give false assurance if done wrong.

## Decision

### Scheduling (ADR-0011)

- A **Protection Policy** holds:
  - a schedule: a 5-field cron expression or a preset (`hourly`, `daily`, `weekly`, `monthly`), plus a timezone
  - an optional consistency mode and Repository, used when the application sets none
  - retention
- Applications are assigned to at most one policy.
- `dbr2-server` keeps **one Temporal schedule per application**, `backup/<application id>`, in line with the assignments. It re-syncs after each change and every 10 minutes. A schedule starts `ScheduledOperationTrigger`, which starts `BackupWorkflow` as a child on `application/<id>`.
  - **Overlap:** the schedule itself uses SKIP. When another operation holds the application, the trigger records the skip: an audit event and a warning alert (`backup.skipped`, notified as a missed backup).
  - **Backup windows:** a host's backup window delays scheduled runs only.

### Retention and deletion

- **Retention** is grandfather-father-son, evaluated per application in the policy's timezone. It keeps:
  - the newest `keep_last` recovery points
  - the newest recovery point in each of the latest N hours, days, ISO weeks, months and years
  - the latest recovery point of an application, always
  - any recovery point used by an active restore
- A daily `RetentionWorkflow` (the `platform-retention` schedule) deletes, **as `maint@dbr2`**, first the manifest (which un-commits the recovery point), then every snapshot tagged with it. Recovery points become `deleting`, then `deleted`, and the index row is kept for history.
- **Manual deletions have a 7-day grace period (ADR-0014):**
  - typed confirmation of the application name, plus a reason
  - the recovery point stays committed and restorable until `delete_after`, and can be undeleted until then
  - the retention workflow executes it afterwards
- **Repositories** are "deleted" the same way. They leave service immediately (`pending_deletion`, no longer the default) and are **retired** after the grace period. DBR² never erases stored data; the share is removed by its administrator.

### Recovery Contract (v1.0 subset)

- **Terms:** a maximum RPO and required components.
- **Evaluation:** every 5 minutes and after each change, against the latest committed recovery point: its age, and whether the required components succeeded.
- **State changes** raise `contract.violated` (critical) or `contract.satisfied` (info). This is the RPO-violation reporting.
- **At capture:** each manifest also records the contract evaluation at capture time (`contract.satisfied` with the missing components).

### Notifications

- **Source:** alerts are the rows of the notification outbox. The **dispatcher** in `dbr2-server` fans them out to enabled channels whose event filter (exact names or `prefix.*`) and minimum severity match.
- **Channels:**
  - **Email:** SMTP with STARTTLS or implicit TLS; plain SMTP only to loopback.
  - **Webhook:** HTTPS; `X-DBR2-Signature: sha256=HMAC(secret, timestamp + "." + body)`.
- **Retries:** 1 m, 5 m, 30 m, 2 h and 6 h, then `failed`.
- **Multi-instance safety:** claims use `FOR UPDATE SKIP LOCKED`, and deliveries are unique per channel and alert. Sending happens outside transactions under a 5-minute lease.
- **Secrets** (webhook secret, SMTP password) are sealed with `DBR2_SECRET_KEY` and are write-only.
- **Agent offline/online** is detected after 10 minutes without a session, and is raised exactly once per outage.
- **Stale alerts:** outbox rows older than 24 hours when first seen are not sent, so an outage does not produce a flood.

### Verification (Phase 9)

- **Workflow:** `repository/<id>/verify`, run on demand and weekly for every Repository (`platform-verify`). It verifies the least recently verified recovery points of each Repository.
- **Check:** for every captured component, the engine walks the snapshot tree and checks that every object is present. It then fully reads (hash-verifies) a deterministic sample of files, 10 % by default (`DBR2_VERIFY_READ_PERCENT`).
- **Result:** `verified` or `verification_failed`, with per-component details. A failure raises a critical alert.
- **Limit of the maint session:** over the repository-server session, missing pack blobs are found only by the read sample. A fuller check runs on the reposerver host (the stock `kopia snapshot verify`).

### Escrow health (ADR-0008)

- **Checked hourly.** Each of these raises `escrow.unhealthy` when the set of problems changes:
  - fewer recipients than required
  - an unconfirmed package
  - **recipients changed since a package was generated**
  - a package not re-confirmed for 90 days
  - no escrow drill in 12 months
- **Regenerate:** re-seals the repository password, read from the reposerver and never stored, to the current recipients, and requires a new confirmation.
- **Drills:** a drill package seals a random secret. An escrow holder decrypts it with the identity from the safe and enters its code.

### Database-aware backups (Phase 8)

- **Detection:** PostgreSQL and Redis-compatible containers are detected by image name. Tooling images are excluded: exporters, pgAdmin, poolers, UIs.
- **Online dumps:** they run **before** the quiesce window, in formats fixed by ADR-0017:
  - PostgreSQL: `pg_dumpall --clean --if-exists` streamed through zstd, validated by exit code and the dump trailer
  - Redis: `BGSAVE` completion, then the RDB (plus AOF when enabled), validated by the RDB magic
- **Strategy** per application:
  - **both** (the default): dumps plus the data volumes, which may be crash-consistent
  - **logical**: dumps only; volumes mounted only by the database are skipped
  - **volume**: no dumps
- Redis AUTH is not supported yet; the dump fails with a clear error.

## Consequences

- Deleting data always takes a policy decision (retention) or a typed, reasoned request with a week to undo it. Nothing deletes the latest recovery point.
- Operators learn about missed backups, RPO breaches, verification failures, escrow drift and offline agents without watching the console.
- Verification reads only part of the data by default. Raise `read_percent` for critical Repositories, and run restore tests (roadmap "Later").

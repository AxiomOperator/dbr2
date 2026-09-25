# ADR-0011: Concurrency control without cache locks

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Two conflicting operations on the same application (two backups, or a backup and a restore) must never run at once. The stack originally listed "Distributed locks" as a use of the cache. A lock that can disappear on restart, eviction or failover does not provide mutual exclusion.

## Decision

- **Application operations:** every workflow that operates on a live application (backup, restore, migration) uses the deterministic workflow ID `application/{application_id}`. It is started with a workflow ID conflict policy that rejects the start while an execution with that ID is running.
  - Temporal allows one running execution per workflow ID in a namespace, regardless of workflow type.
  - Manual, scheduled and API-triggered starts share this ID scheme.
  - A scheduled run that collides with a running operation is **skipped and recorded** as an overlapping run.
  - Sandbox test restores do not touch the live application and do not claim this ID.
- **Repository-wide operations** use `repository/{repository_id}/{operation}`, for example `…/maintenance`.
- **PostgreSQL** handles short control-plane critical sections:
  - declarative guarantees first: unique constraints, conditional `UPDATE … WHERE state = …`, and `SELECT … FOR UPDATE`
  - `pg_advisory_xact_lock` for critical sections that span several statements
  - no session-level advisory locks held across activities or network calls
- **Valkey** is never used for locking.

## Consequences

- The backup workflow's former "Acquire Backup Lock" step becomes "Claim Application (exclusive workflow ID)".
- The UI and API must return a clear "operation already in progress" error, including the ID of the running workflow.

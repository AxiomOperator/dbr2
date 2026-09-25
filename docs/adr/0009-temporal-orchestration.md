# ADR-0009: Temporal for durable orchestration

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Backup, restore, verification, replication and retention are long-running, multi-step operations. They must survive process restarts, agent disconnects and infrastructure interruptions. The alternative is a hand-built job system (PostgreSQL job tables, NATS and similar).

## Decision

Use **Temporal** (with the Go SDK) for all long-running orchestration, including scheduled workflows.

- Temporal uses PostgreSQL as its persistence store. It runs in the same PostgreSQL server as DBR², but in **separate databases** (`temporal`, `temporal_visibility`) from the DBR² platform database.
- Its operational cost is accepted and planned for:
  - Temporal upgrades are tracked as roadmap items.
  - Temporal is part of the Compose deployment.
  - Its database is included in the self-backup (ADR-0008) for forensics, but platform recovery starts Temporal fresh and rebuilds schedules from policies.
- Temporal also provides operation exclusivity (ADR-0011) and quiesce compensation (ADR-0005).

## Consequences

- Workflow code must follow Temporal's determinism rules. Workflow versioning (`workflow.GetVersion`) is required for changes to running workflows.
- Operators get the Temporal UI for debugging. Access to it must be restricted to administrators, because it exposes workflow inputs.

## Spike results (2026-09-25): `spikes/temporal/RESULTS.md`

Confirmed on PostgreSQL 18.

- **Images (pinned exact versions; never `latest`):**
  - `temporalio/server:1.32.0`
  - `temporalio/admin-tools:1.32.0`, used as **one-shot Compose jobs** for schema setup and namespace creation, which the server `depends_on` (`service_completed_successfully`)
  - `temporalio/ui:2.54.1`
  - `postgres:18.6-trixie`
- **`temporalio/auto-setup` is not used.** It is effectively deprecated (its last tag is 1.29.7), and the upstream `docker-compose` repository is archived in favor of `temporalio/samples-server/compose`.
- **Schema:** the `postgres12` SQL plugin works on PG18 unchanged (temporal schema 1.19, visibility schema 1.14). Schema upgrades are explicit admin-tools jobs, run as part of DBR² upgrades.
- **Databases and roles:** `temporal`, `temporal_visibility` and `dbr2` each have their **own owner role**. The Temporal role cannot connect to `dbr2` and does not need `CREATEDB`.
- **Namespace:** DBR² uses a dedicated `dbr2` namespace.
- **Tooling:** use the `temporal` CLI (from admin-tools); `tctl` is gone.
- **PG18 image change:** the data volume mounts at `/var/lib/postgresql`, not `/var/lib/postgresql/data`.

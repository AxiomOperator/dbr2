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

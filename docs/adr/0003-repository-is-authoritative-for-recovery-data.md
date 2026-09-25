# ADR-0003: Repository is authoritative for recovery data; PostgreSQL is a rebuildable index

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The stack called PostgreSQL the "system of record". DBR² also requires:

- agent-only restore
- disaster-recovery bundles with `restore.sh`
- recovery after the DBR² server itself is lost

If recovery points existed only in PostgreSQL, losing the control plane would make every backup undiscoverable.

## Decision

1. **The Repository is the source of truth for recovery data.** Everything needed to find, understand and restore a recovery point lives in the Repository itself:
   - the component snapshots
   - the versioned **recovery manifest** (ADR-0004)
2. **PostgreSQL is the system of record for platform state**: hosts, agents, users, roles, policies, recovery contracts, schedules, audit, and job history. For recovery points it is only an **index**, a cache of the manifests stored in the repositories.
3. **The index can be rebuilt.** `dbr2 admin reindex --repository <name>` scans a Repository for recovery manifests and rebuilds the PostgreSQL recovery-point index. Platform disaster recovery runs this after restoring or reinstalling the control plane.
4. **Conflict rule:** if PostgreSQL and a Repository disagree about a recovery point, the Repository wins. A recovery point that PostgreSQL lists but has no committed manifest in the Repository does not exist.
5. **Manifest schema versioning:**
   - The manifest schema is versioned from day one (`schema_version`).
   - Readers must accept every earlier schema version.
   - Schema changes are additive within a major version. A new major version requires a documented migration and reader support for the old major version.
6. **Readable with standard tools.** Manifests and component snapshots use standard Kopia structures, and manifests are plain JSON. With the repository password (from key escrow, ADR-0008), a stock `kopia` CLI can list and restore the data. Recovery never depends on DBR² being runnable.

## Consequences

- Every write path must commit data to the Repository before PostgreSQL records it as existing.
- Retention and deletion change the Repository first and the index second.
- Reindexing must be fast enough to run over large repositories, which calls for manifest tagging (ADR-0004).

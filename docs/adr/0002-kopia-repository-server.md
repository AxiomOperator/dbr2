# ADR-0002: Repository access through a Kopia Repository Server

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

When Kopia accesses a repository directly, the repository password plus the storage credentials grant full read, write and delete access to every snapshot. If every agent held them, compromising one Docker host would expose, and allow deletion of, every host's backups. That violates repository key separation, per-host credentials and ransomware resistance.

## Decision

Agents never hold repository passwords or storage credentials. Each Repository is served by a **Kopia Repository Server**, deployed as the DBR² component **`dbr2-reposerver`** (one instance per Repository).

- **Only `dbr2-reposerver` holds** the repository password and the storage backend credentials.
- **Agent identity:** each agent has a Kopia user (`agent-<agent_id>@<host>`). DBR² generates its password and delivers it over the mTLS agent channel during enrollment. The agent stores it root-only (`0600`) under `/etc/dbr2/`, and DBR² can rotate or revoke it.
- **Least privilege through Kopia ACLs:**
  - Agent users get append-level access to their **own** snapshot sources and read access to their own snapshots (for restore).
  - Agent users get no delete access and no access to other hosts' data.
- **Privileged operations** (retention, deletion, maintenance, cross-host restore, verification) run under a separate **maintenance identity** that only `dbr2-worker` holds. They are invoked through workflows and are subject to RBAC (and approval where configured).
- **Cross-host restore:** the worker grants the target agent a time-limited read grant for the specific recovery point, or stages the restore through the maintenance identity.
- **Transport:** `dbr2-reposerver` presents a TLS certificate issued by the DBR² CA.
- **Immutability:** when the storage backend supports it, S3 Object Lock / WORM retention is used underneath, so that even a compromised `dbr2-reposerver` cannot destroy data inside the lock window.

**Data path:** agent → `dbr2-reposerver` → storage backend. Payloads still never pass through the DBR² API. The reposerver is a **data-plane** component and should be deployed close to its storage (it may be co-located with the control plane in small deployments).

## Consequences

- Compromising one host exposes only that host's own backups for reading, not deletion.
- `dbr2-reposerver` becomes a high-value asset (see `../threat_model.md`) and a throughput path that needs capacity planning.
- A Repository has one reposerver. High availability of the reposerver is a later concern.

## Open items / validation (Phase 0 spike)

- Confirm that Kopia ACL levels can express "append own snapshots, no delete" for agent users, and document the exact ACL entries.
- Confirm where splitting, hashing, compression and encryption happen in repository-server mode, and whether deduplication avoids re-sending existing content from the client. This matters for WAN efficiency.
- Measure reposerver throughput with several agents running at once.

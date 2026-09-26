# ADR-0017: Restore: staging, swap and rollback

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

A restore overwrites live data on a host. The failure modes are worse than those of a backup:

- a half-restored volume
- a restored application that no longer starts
- a restore that lands on another team's containers, ports or paths
- a restore that runs concurrently with a backup

ADR-0006 fixes the per-component fidelity sequence (staging, fsmeta, verify, swap), and ADR-0014 fixes the single-operator safeguards. This ADR decides how the whole restore is orchestrated.

## Decision

### Exclusivity

A restore claims `application/<id>` (ADR-0011): the target application when one exists on the target host, otherwise the source application. It therefore never overlaps a backup or another restore of that application.

### Request, preview and collisions (server side)

Every restore starts with an **impact preview**, computed from the recovery manifest's topology (added to schema 1 additively in Phase 5) and the target host's latest inventory. It lists:

- containers that will be stopped
- volumes overwritten or created
- bind paths replaced (after path remapping)
- containers re-created
- networks created
- images pulled
- published ports

Anything the target already has that belongs to **another** application is a **collision** and blocks the restore (HTTP 409): a container name, a published port, a network, a volume, or a bind path mounted by someone else. A missing external network is a blocking dependency. There is no override in v1.0. The operator resolves the collision, or uses path remapping or another host.

### Production restores (ADR-0014)

A restore is a production restore when the target application is tagged `environment: production`, **or** it overwrites a running application in place. It then requires:

- `restore.production` (in addition to `restore.execute`)
- a typed confirmation (the application name)
- a reason

The optional two-person approval workflow stays in "Later".

### Orchestration (worker; saga plus agent journal)

1. **PrepareRestore** re-validates against the current inventory. A restore that has become blocked fails before touching anything.
2. **Cross-host restores:** grant the target agent a temporary per-source READ ACL (`agent@<target>` may read `agent@<source>`). Revoking it is a saga compensation that runs on success, failure and cancellation. Kopia checks ACLs when a session opens, so the agent uses a **fresh repository session** for the restore commands and closes it afterwards.
3. **Pull missing images by digest** (`repo@sha256:…`), verify the digest, and tag the image with its original reference.
4. **Stop the target application** with the backup quiesce command in Offline mode (`Quiesce STOP`, lease ID = restore ID). The agent's dead-man lease (24 h) restarts it if the control plane disappears. If the lease has fired, the agent refuses to swap data in.
5. **RestoreComponents:** for every component, Kopia restores into a **staging directory next to the target**, on the same filesystem, with `IgnorePermissionErrors = false` and sparse writing.
   - The fsmeta record is applied (hardlinks, ACLs, extended attributes, full SELinux contexts, then directory mtimes, deepest first) and **verified**.
   - The staging directory is then **swapped in with a rename**, and the previous content is kept as `.dbr2-old-<restore id>`.
   - Every step is journaled on the agent before it happens.
   - A failure rolls back that command's swaps.
6. **Re-create missing containers** from the raw inspect documents in the config component. Missing networks are created first, and remapped bind sources are applied. Existing containers are left alone.
7. **Start** (resume the stopped containers and start the re-created ones that were running at capture) and **restore databases**. PostgreSQL `pg_dumpall` SQL (zstd) is streamed into `psql`. A Redis RDB replaces the dump file while Redis is stopped; restoring an RDB when AOF is enabled is refused.
8. **Health check:** every container must be running, and `healthy` when it defines a healthcheck, continuously for 15 s. The timeout is 5 minutes, and log tails are captured on failure.
9. **Commit** deletes the previous content. **Any failure after step 4 runs `FinalizeRestore(ROLLBACK)`:**
   - stop the involved containers
   - swap the previous content back
   - remove the containers, networks and volumes the restore created
   - start what was running before

   The restore is then recorded as **rolled back**; it is **failed** if nothing had changed yet, or if the rollback itself failed. That case raises a critical alert.

### History

Every attempt is a `restore_runs` row. It holds:

- the request (components, remaps, reason, requester)
- the preview at request time
- the current step
- the result document (component, container and health outcomes)
- the final state: `succeeded`, `failed` or `rolled_back`

Each outcome is audited (`restore.*`) and raises an alert.

### Database dump formats (fixed now for Phase 8)

- PostgreSQL: `pg_dumpall-sql-zstd`, a plain SQL stream compressed with zstd.
- Redis: `rdb`, the RDB file.

Manifest components carry `database: {engine, format, service, container}` and `file_name`.

## Consequences

- The target filesystem needs room for a second copy of every restored component until commit. The agent checks free space before restoring.
- Restores are all-or-nothing per recovery point, and the previous data remains available until the restored application is proven healthy.
- Restoring to a new name is out of scope for v1.0 ("Later", restore mapping wizard), and so is ignoring collisions. The restore mapping wizard will build on path remapping.
- An application with no topology in its manifest (recovery points written before Phase 5) gets a weaker preview. Container re-creation still works from the config component.

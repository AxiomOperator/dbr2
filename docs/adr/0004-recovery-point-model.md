# ADR-0004: Recovery point structure and atomic commit

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

One application backup produces several Kopia snapshots:

- one per volume
- one per bind mount
- the database dumps (streamed)
- the configuration and Compose files
- optional image archives

Kopia has no notion of a multi-snapshot transaction. DBR² needs a precise definition of a recovery point, of when it exists, and of what a partial failure means.

## Decision

### Definition

A **Recovery Point (RP)** is an immutable, point-in-time, restorable capture of one Application. It is identified by a ULID (`rp_01K…`) and consists of a set of **components** plus one **recovery manifest**.

| Component kind | Content | Capture method |
|---|---|---|
| `config` | Compose files, `.env`/env files, discovery metadata (containers, networks, volumes, images, host) | Agent stages a directory, then Kopia snapshots it |
| `volume` | One named volume | Kopia snapshot of the volume mountpoint (or of a filesystem snapshot of it, per ADR-0005) |
| `bind_mount` | One bind-mount source path | Kopia snapshot of the host path |
| `database` | One logical database dump | Dump tool stdout streamed into a Kopia snapshot (Zstandard-compressed stream) |
| `image` (optional) | `docker save` output | Streamed into a Kopia snapshot |

Every component snapshot is tagged:

```text
dbr2:rp=<rp_id>
dbr2:app=<application_id>
dbr2:component=<component_name>
dbr2:kind=<component_kind>
```

### The manifest

The manifest is a JSON document (`schema_version`, RP ID, application identity, source host and runtime, consistency mode, and the quiesce window start and end). For each component it records:

- the Kopia snapshot ID and root object ID
- its kind and required flag
- its status
- its size
- its capture start and end times
- its original path or volume name
- ownership, and SELinux context when relevant (ADR-0006)

It also records the image references and digests, the recovery-contract evaluation at capture time, and the IDs of the workflow that produced it.

The manifest is written into the Repository as its own small Kopia snapshot tagged `dbr2:kind=manifest` and `dbr2:rp=<rp_id>`. Tag-based lookup makes reindexing (ADR-0003) cheap, and a stock `kopia` CLI can read the manifest.

### Atomic commit (two-phase)

1. **Prepare:** capture all components. Components are tagged but **uncommitted**. PostgreSQL records the RP as `pending`.
2. **Commit:** write the manifest **last**. **A recovery point exists if and only if its manifest exists in the Repository.** PostgreSQL then marks the RP `committed`.
3. **Uncommitted components** (tagged snapshots with no manifest) are orphans. A garbage-collection workflow deletes them after a grace period (default 7 days). Until then, deduplication still benefits the next attempt.
4. **Deletion** is the reverse: delete the manifest first (un-commit), then the components. A committed RP can never reference a missing component.
5. **Replication or copy to another Repository:** copy the components first and the manifest last.

### Partial failure semantics

Whether each component is required comes from the Protection Policy and the Recovery Contract. Defaults:

- `config`, every `volume`, every `bind_mount` and every `database` are required.
- `image` components are optional.
- A volume explicitly marked best-effort (for example a cache) is optional.

| Outcome | Result |
|---|---|
| All components succeeded | RP committed, status **Complete** |
| Only optional components failed | RP committed, status **Partial**; the manifest lists the failed components; the backup job ends with a warning |
| Any required component failed | **No manifest is written, so there is no RP**; the backup job **fails**; the components become orphans |

**Verification** is a separate, later attribute (`unverified` → `verified` or `verification_failed`), set by verification and restore-test workflows. It is never implied by commit.

### Point in time

The RP's `consistency_point` is:

- the start of the quiesce window, for Quiesced and Offline modes
- the capture start time, for Live mode

Live-mode RPs are flagged `crash_consistent_only`. Database components carry their own dump timestamp.

## Consequences

- Restore, the UI, the CLI and reindexing all key on the manifest, never on individual snapshots.
- Retention operates on whole RPs.
- Consistency Groups (several applications with a shared consistency point) extend this model later by writing a group manifest that references member RPs.

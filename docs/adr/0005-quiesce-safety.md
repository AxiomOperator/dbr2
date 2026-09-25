# ADR-0005: Quiesce safety: saga compensation, timeouts and agent dead-man switch

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The backup workflow runs Quiesce → Protect → Resume. If anything fails after Quiesce (an activity error, a worker crash, a lost agent connection, or the control plane going down), the application could stay in maintenance mode or stopped indefinitely. That is an outage caused by the backup system. Filesystem snapshots (LVM, ZFS, Btrfs) can shrink the quiesce window, but **not every host will have them**, so safety cannot depend on snapshots.

## Decision

Resuming the application is guaranteed at **two independent layers**.

### Layer 1: workflow saga (control plane)

- **Record pre-state:** before quiescing, the workflow records which containers were running, so Resume restores exactly the prior state rather than, for example, starting containers that were already stopped.
- **Register compensation:** once `Quiesce` succeeds, the workflow registers `Resume` (and the post-backup hooks) as compensation. It runs in a disconnected context (`workflow.NewDisconnectedContext`), so it executes on success, failure **and** cancellation.
- **Bound every activity:** activities inside the quiesce window have `StartToClose` timeouts bounded by the application's **maximum quiesce duration** (a policy setting; default **60 minutes**, as set by the owner on 2026-09-25; configurable per application).
- **Retry `Resume` aggressively.** If it still fails, the application is flagged **Needs Attention: not resumed** and a critical alert is raised.

### Layer 2: dead-man switch (agent)

- The `Quiesce` command carries a **quiesce lease**: the maximum quiesce duration plus a grace period.
- If the agent does not receive `Resume` or a lease renewal before the lease expires, **the agent resumes the application on its own**, using the recorded pre-state. This works even when the control plane or network is gone.
- The agent journals the lease locally. After an agent restart it checks for expired leases and resumes those applications immediately.
- An auto-resume is reported to the control plane as an event. The running backup then fails its consistency guarantee: the RP is not committed as Quiesced (ADR-0004).

### Alerts

| Condition | Severity |
|---|---|
| Quiesced longer than 80% of the maximum quiesce duration | Warning |
| Agent performed an auto-resume | Critical |
| Resume failed | Critical |
| Offline mode: application not healthy after restart | Critical |

### Filesystem snapshots: optional accelerator, never a requirement

- The agent detects snapshot capability per volume path (LVM thin, ZFS, Btrfs).
- **When a snapshot is available:** Quiesce → take a filesystem snapshot → Resume **immediately** → Kopia reads from the snapshot → release the snapshot. The quiesce window shrinks to seconds.
- **When it is not available (the common case):** Kopia reads the live path during the quiesce window. The window lasts as long as the copy takes, and the timeouts and dead-man switch above bound it.
- The manifest records which method was used for each component.

### Initial seeding of large volumes

The first backup of a large volume (up to about 500 GB in v1.0) can take longer than the quiesce window, because every byte must be uploaded. DBR² therefore runs a **seed pass**:

1. **Seed:** a Live, crash-consistent pass that is **not committed** as a recovery point. It loads the Repository's content so that deduplication applies.
2. **Consistent pass:** the normal Quiesced or Offline pass. Unchanged content is already stored, so only the changes since the seed are uploaded, and the quiesce window shrinks to roughly the time needed to scan the files and upload the delta.

A seed pass runs automatically when a component has no prior content in the Repository and its estimated size exceeds what can be uploaded within the maximum quiesce duration.

### Minimal-downtime defaults (owner preference: as little downtime as possible)

* **Databases:** online logical dumps (`pg_dump`, Redis `BGSAVE` plus the RDB file) need **no quiesce**. They are the primary restore source for database components.
* **Other volumes:** Quiesced mode when hooks are defined; otherwise Live, flagged `crash_consistent_only`. The administrator chooses Offline per application.
* **LVM:** Rocky and Fedora commonly use LVM. When the volume group has free extents, an LVM snapshot shrinks the quiesce window to seconds. This is the first snapshot provider to implement (v2), and it stays optional.

## Consequences

- Every mode (Live, Quiesced, Offline) is safe on hosts without snapshot support.
- For applications on hosts without snapshots whose data is too large to copy within the maximum quiesce duration, the policy UI must warn the administrator, for example suggesting Live mode, database dumps only, or enabling snapshots.

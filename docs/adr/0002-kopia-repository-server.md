# ADR-0002: Repository access through a Kopia Repository Server

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

When Kopia accesses a repository directly, the repository password plus the storage credentials grant full read, write and delete access to every snapshot. If every agent held them, compromising one Docker host would expose, and allow deletion of, every host's backups. That violates repository key separation, per-host credentials and ransomware resistance.

## Decision

Agents never hold repository passwords or storage credentials. Each Repository is served by a **Kopia Repository Server**, deployed as the DBR² component **`dbr2-reposerver`** (one instance per Repository).

- **Only `dbr2-reposerver` holds** the repository password and the storage backend credentials.
- **Agent identity:** each agent has a Kopia user (`agent-<agent_id>@<host>`). DBR² generates its password and delivers it over the mTLS agent channel during enrollment. The agent stores it root-only (`0600`) under `/etc/dbr2/`, and DBR² can rotate or revoke it.
- **Least privilege through Kopia ACLs** (spike-verified entry set; Kopia's defaults are replaced, because they give agents FULL access to their own snapshots):
  - Agent users: **APPEND** on their own snapshots (`type=snapshot,username=OWN_USER,hostname=OWN_HOST`) and **READ** on their own policies. No access to users, ACLs, other agents' snapshot manifests or maintenance.
  - The maintenance identity `maint@dbr2`: **FULL** on snapshots and policies (list, delete, set policy, restore across agents).
  - Verified from both the library and the CLI:
    - agents cannot delete any snapshot, list or load another agent's snapshots, forge a snapshot for another host, or change policies, ACLs or users
    - `maint@dbr2` can do all of these
- **Retention is never delegated to Kopia.** Every DBR² snapshot is **pinned** (`Pins=["dbr2"]`), and the global Kopia retention policy keeps everything. DBR² retention deletes whole recovery points through `maint@dbr2` (ADR-0004). This closes a spike-found gap: an agent could otherwise ask the server to apply Kopia retention to its own snapshots, which the server executes with its own privileges. Pinned snapshots were never deleted in the spike.
- **Maintenance and garbage collection run inside `dbr2-reposerver`**, the owner of the direct repository connection. Kopia supports maintenance only on a direct connection; the server schedules quick maintenance hourly and full maintenance daily. `dbr2-worker`'s `maint@dbr2` identity only lists, deletes, sets policy and restores; it never runs maintenance. Space is reclaimed about 24–48 hours after a deletion.
- **Privileged operations** (deletion, cross-agent listing, verification) are invoked by workflows under `maint@dbr2`, subject to RBAC (and approval where configured).
- **Cross-host restore:** ACL targets cannot express tags, so "grant read on one recovery point" is impossible. Instead, the restore workflow adds a **temporary per-source READ ACL** (the target agent may read the source host's snapshots). The workflow's saga compensation **revokes** it, and the grant is audited.
- **Transport:** `dbr2-reposerver` presents a TLS certificate issued by the DBR² CA.
- **Immutability:** when the storage backend supports it, S3 Object Lock / WORM retention is used underneath, so that even a compromised `dbr2-reposerver` cannot destroy data inside the lock window.

**Data path:** agent → `dbr2-reposerver` → storage backend. Payloads still never pass through the DBR² API. The reposerver is a **data-plane** component and should be deployed close to its storage (it may be co-located with the control plane in small deployments).

## Consequences

- **Honest isolation statement (spike-verified):**
  - Compromising one host lets the attacker read that host's own backups; it cannot delete them.
  - **But content access is not scoped per user:** any authenticated agent can read an object if it knows the object ID. Object IDs therefore act as read keys, and DBR² **must never send an agent object IDs belonging to another host**.
  - Every client also receives the repository's HMAC secret, which gives an **existence oracle**: an agent can confirm whether a piece of content it can guess exists anywhere in the Repository.
  - Deployments that need strict host-to-host confidentiality configure **one Repository per host**. The model already supports this; the cost is losing deduplication across hosts.
- Agents can fill storage with append-only data, so **per-agent storage monitoring and quotas** are required (alert on anomalous growth).
- `dbr2-reposerver` becomes a high-value asset (see `../threat_model.md`) and a throughput path that needs capacity planning.
- A Repository has one reposerver. High availability of the reposerver is a later concern.

## Open items / validation

- ~~Kopia ACLs for "append own, no delete"~~: verified. The exact entries are above; the commands are in `spikes/kopia-library/RESULTS.md`.
- ~~The data path in repository-server mode~~: verified (see below).
- Reposerver throughput with several agents at once: **deferred until the real NAS is connected** (no throughput testing on the dev box).

## Data path (spike result, 2026-09-25: `spikes/kopia-library/RESULTS.md`)

- **Client (agent):** splits and hashes the data, using the HMAC secret it receives on connect. For chunks of 50,000 bytes or more, it asks the server whether the content already exists and skips the upload if so.
- **Server (`dbr2-reposerver`):** compresses and encrypts.
- Measured on a 200 MiB data set:

| Run | Client → server |
|---|---|
| Unchanged data re-snapshot | ≈0 |
| 5% changed, 4 MiB chunking (default) | 58 MiB |
| 5% changed, 1 MiB chunking | 36 MiB |
| zstd policy on compressible data | 200 MiB on the wire, 26 MiB stored |

- **Consequences:**
  - The Kopia compression policy saves storage, not network. The agent already Zstandard-compresses database and image streams (ADR-0004).
  - Size the reposerver's CPU for compression and encryption for all agents combined.
  - **TLS on the agent–reposerver link is mandatory**, because content crosses it unencrypted by Kopia.
  - **New DBR² Repositories use the `DYNAMIC-1M-BUZHASH` splitter** for finer deduplication. The splitter cannot be changed after a repository is created, so revisit this during real-NAS validation, before the production Repository is created.

## Storage safety amendment (spike result, 2026-09-25: `spikes/kopia-fidelity-nfs/RESULTS.md`)

Tested against a mocked NFS server (nfs-ganesha in a container):

- With the NFS server down at connect or mount time, operations **hang indefinitely**.
- An outage in the middle of a snapshot stalls, then resumes successfully after recovery, with a clean verify.
- **Creating a repository on an unmounted mountpoint silently writes to the local disk.**

Therefore `dbr2-reposerver`:

1. **Refuses to start, or to create or connect a Repository,** unless the configured path:
   - is an active mount of the expected type (`nfs4` in production, checked through `/proc/self/mountinfo`), and
   - contains a **sentinel file** `.dbr2-repository-id` matching the configured Repository ID. The sentinel is written at creation time.

   On host installs, the systemd unit also uses `RequiresMountsFor=` and `ConditionPathIsMountPoint=`.
2. Runs a **stall watchdog**: a periodic `stat` of the sentinel with a timeout, performed in a separate goroutine because `hard` NFS mounts block instead of returning errors. The reposerver reports unhealthy and raises an alert when the check stalls.
3. Relies on activity heartbeats (ADR-0001) so that workflows detect a stalled data path and retry after recovery.

## Phase 4 implementation amendment (2026-09-25)

Decisions made while implementing `dbr2-reposerver` and the backup path:

1. **TLS by fingerprint pinning, not by the DBR² CA.** Kopia clients can only trust a repository server by the SHA-256 fingerprint of its certificate (`TrustedServerCertificateFingerprint`); they cannot be given a custom CA. The reposerver therefore generates a **stable self-signed certificate** in its state directory. `dbr2-server` records the fingerprint (`repositories.cert_sha256`) when the Repository is created, and agents and the worker pin it. A changed fingerprint (replaced reposerver state) is shown as a Repository error; nothing re-trusts it automatically.
2. **Repository password custody.** `dbr2-server` generates the password when a Repository is created, passes it once to the reposerver (`POST /v1/initialize`) and seals it into the age escrow package (ADR-0008). **It is not stored by `dbr2-server`.** It exists only in the reposerver state directory (0600) and in the escrow package.
3. **Kopia identities.**
   - Agents: user `agent`, host `<agent ID>` (`agent@<agent ID>`). A random 256-bit password is set on the reposerver and handed to the agent once, as a `ConfigureRepository` command over its mTLS session. It is stored only in the agent state directory (0600).
   - `maint@dbr2`: `dbr2-worker` sets a **fresh random password every time it opens a session** (every worker start), and keeps it only in memory.
4. **Management API.** Plain HTTP on `:8091`, reachable only on the deployment network. Bearer internal token (`DBR2_INTERNAL_TOKEN`, at least 32 characters). Endpoints: `GET /v1/status`, `POST /v1/initialize`, `PUT`/`DELETE /v1/users/{user}`, `POST`/`DELETE /v1/acl/read-grants`.
5. **Process model.** The Kopia server runs as a supervised **child process of the same `dbr2-reposerver` binary** (hidden `kopia` subcommand running Kopia's public `cli` package). There is still no external `kopia` binary. The child keeps secrets out of `argv`: they are passed through environment variables that are removed from the child's own environment. The child is restarted with backoff, and the storage guard is checked before every start.
6. **Policy and ACLs.**
   - Global policy: `zstd-fastest` compression; every retention bucket kept at 10⁹ (retention never deletes).
   - ACLs: Kopia's three default FULL entries are replaced by the spike-verified set (`*@*` APPEND content, APPEND own snapshots, READ own policies; `maint@dbr2` FULL on snapshots and policies).
   - **ACL and user changes only apply to new sessions**, because Kopia checks them when a session opens. After revoking a read grant, the restore workflow (Phase 5) must reconnect.
7. **Internal URL for the worker.** A Repository has a `server_url` (for agents, e.g. `https://backup.example.lan:51515`) and an optional `internal_server_url` (for `dbr2-worker` on the Compose network, e.g. `https://dbr2-reposerver:51515`). The same pinned certificate serves both.

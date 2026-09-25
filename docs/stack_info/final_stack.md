# DBR² — Final Technology Stack

> **Source of truth.** Accepted ADRs in `../adr/` are reflected here. Related documents: `../domain_model.md` (entities and glossary), `../threat_model.md`, `../roadmap.md` (phases, checklists and change log).
>
> **Terminology (ADR-0012):** *Repository* = a Kopia-backed recovery-point store; *storage backend* = its physical target; *backup engine* = the Go abstraction over Kopia; *store* = the PostgreSQL data-access layer; *source repository* = a Git repository.

---

# Context & Constraints

Owner answers recorded 2026-09-25. Release naming: **v1.0 = the MVP**; **v2** = the post-MVP tier; **Later** = unscheduled.

| Topic | Constraint | Design impact |
|---|---|---|
| Purpose and license | Internal tool, published as open source: free to use, modify and commercialize, provided the original repository is credited | **Apache-2.0** with a `NOTICE` crediting https://github.com/AxiomOperator/dbr2; copyright **DBR2 Team** (ADR-0013). Dependencies must have compatible licenses (Kopia Apache-2.0, Temporal MIT, Valkey BSD-3). |
| Operators | One operator today; must also support teams | Every feature must be operable by one person. Multi-person controls (approvals, dual authorization) are optional and switch on only when enough eligible users exist (ADR-0014). |
| Scale (v1.0) | 6 Docker hosts; largest volume about 500 GB; total data unknown | A single `dbr2-reposerver` is sufficient. The default maximum quiesce duration is **60 minutes**. Large volumes are seeded with an initial Live pass (ADR-0005). |
| Platform | AMD64 only. Rocky Linux now; Fedora required; Debian-based distributions nice to have | RPM packages for Rocky and Fedora in v1.0, DEB in v2. SELinux-enforcing hosts are the primary test target; AppArmor handling comes with Debian support. |
| Compose | Deployed by hand with `docker compose` only in v1.0 | Discovery relies on Compose labels and host-readable project directories. Portainer, Dockge and Git-driven deployments are out of scope for v1.0. |
| Sites | Single site in v1.0 | The reposerver is co-located with the control plane. Offsite replication and WAN features are v2 or Later. |
| Development environment | The dev box is **not connected to the NAS** | NFS is **mocked** in development and CI (see Testing → Storage mocking). **No throughput testing** until the NAS is connected. |
| Source control and CI | GitHub (`AxiomOperator/dbr2`), **GitHub Actions** | All CI/CD, including the versioning and changelog checks (ADR-0015), runs on GitHub Actions. |
| Versioning | Every component is versioned `MAJOR.MINOR.BUGFIX.BUILD` and keeps its own changelog | ADR-0015 |
| Storage | A single NAS over **NFS** in v1.0 | NFS is the only production storage backend in v1.0 (local filesystem for development and testing). SMB and S3-compatible storage are v2. Without S3 Object Lock, immutability relies on ADR-0002 identities, NFS export restrictions and NAS snapshots. |
| Identity | Entra ID in v1.0; other providers later. A **master admin (username and password) is required** for lockout protection | Entra ID via OIDC is the only v1.0 identity provider (group-to-role mapping). The local master admin always exists (see Authentication). |
| Compliance | None at this time | No mandated retention; legal hold and chain of custody stay Later. |
| Key escrow | 2 people hold escrow keys offline, in a safe | Two age escrow recipients. Escrow packages are encrypted, so they can be stored anywhere; only the private identities live in the safe (ADR-0008). |
| Databases (v1.0) | PostgreSQL and **Redis** | Database plugins in v1.0: PostgreSQL (`pg_dump`) and Redis (RDB via `BGSAVE`). Everything else is Later. |
| Downtime | As little as possible, but some is acceptable | Default policy favors online database dumps plus short quiesce windows; seeding and filesystem snapshots reduce the window (ADR-0005). |
| Existing backups | **Veeam** (VM-level) today | DBR² focuses on application-level protection. Bare-host recovery moves to Later. Veeam also protects the DBR² server VM as an extra layer of platform recovery. DBR² backup windows should avoid Veeam job windows. |

## Frontend

DBR² will use a modern TypeScript-based web stack focused on administrative usability, real-time visibility, and maintainable component development.

**Core technologies**

* Next.js
* React
* TypeScript
* ShadCN
* TailwindCSS
* TanStack Query
* TanStack Table
* React Flow
* Zod

**Purpose**

Next.js and React will provide the primary web application framework. TypeScript will be used throughout the frontend to maintain strong typing between API contracts, UI components, forms, and application state.

ShadCN and TailwindCSS will provide the visual and component foundation for the administrative console without introducing a heavyweight UI framework.

TanStack Query will handle server-state management, caching, refetching, mutations, and synchronization with the DBR² API.

TanStack Table will power data-intensive interfaces such as:

* Docker hosts
* Protected applications
* Containers
* Volumes
* Backup jobs
* Restore history
* Recovery points
* Repositories
* Audit events
* RPO/RTO status

React Flow will be used for topology and dependency visualization, including relationships between applications, containers, volumes, databases, networks, external services, and recovery dependencies.

Zod will provide frontend validation and strongly typed schemas for forms and API-facing data.

---

# Control Plane

The DBR² control plane will be implemented in Go.

**Core technologies**

* Go
* Chi
* Huma
* pgx
* sqlc
* OpenAPI

**Purpose**

The control plane is the authoritative management layer of DBR². It will manage:

* Docker hosts
* Agents
* Applications
* Backup policies
* Backup jobs
* Recovery points
* Restore operations
* Repositories and their repository servers
* Recovery contracts
* Users and roles
* Notifications
* Audit records
* System configuration

Chi will provide lightweight and idiomatic HTTP routing.

Huma will sit above Chi to provide schema-driven API development, request and response validation, and automatic OpenAPI generation.

pgx will be used as the native PostgreSQL driver.

sqlc will generate type-safe Go data-access code directly from SQL queries. DBR² will deliberately avoid making a traditional ORM the primary database abstraction so that complex reporting, retention, backup-history, and recovery queries remain explicit and controllable.

**Interactive API documentation (required).** The API serves a **Swagger-style interactive documentation UI**, rendered by Huma from the generated OpenAPI document (Swagger UI renderer):

```text
/api/docs            Swagger UI (browse the API, "Try it out", Authorize with a bearer token)
/api/openapi.json    OpenAPI document (JSON)
/api/openapi.yaml    OpenAPI document (YAML)
```

* **Swagger UI is served from embedded, version-pinned files** (Go `embed`; Swagger UI 5.31.1 at the time of the spike, about 1.7 MB in the binary). Huma's built-in docs route is **disabled** (`DocsPath: ""`), because every Huma renderer loads from unpkg.com, which breaks air-gapped installs. The docs page uses a strict security policy with no inline script, never stores tokens, and never calls validator.swagger.io. The Swagger UI version is recorded in the `api` changelog.
* By default the docs and spec require an authenticated session (a bearer token or the console session cookie; Swagger UI fetches on the same origin, so the cookie is sent automatically). A browser without a session is redirected to login. `api.docs.public: true` makes the docs public; `/api/v1/*` always stays protected.
* "Try it out" runs under the caller's own RBAC permissions. It never bypasses authorization. Cookie-authenticated POST, PUT and DELETE requests get a same-origin (CSRF) check.
* A **single `NewAPI(router)`** is shared by the server and the spec-export command. A bearer security scheme is applied globally. Each operation's required permission is published in the spec as `x-dbr2-permission` **and** enforced by one Huma middleware reading that same metadata, so documented and enforced security cannot drift apart. Entra ID is declared as an OAuth2/OIDC scheme in Phase 1.
* **Spec export without a server:** `dbr2-server openapi -o api/openapi.yaml`, which produces byte-identical output across runs.
* **CI (ADR-0015):**
  * `git diff --exit-code api/openapi.yaml` after the export
  * `oasdiff breaking base.yaml api/openapi.yaml --fail-on ERR -f githubactions` (oasdiff **pinned to v1.32.1**); a breaking change is allowed only with an `api` MAJOR bump
  * `oasdiff changelog -f markdown` to help draft the `api` changelog
  * a spec lint test failing on any operation without a summary, description, tags or operationId, or any secured operation that does not document 401
* The OpenAPI `info.version` is the `api` component version, injected with `-ldflags -X`. A malformed value makes the binary stop at startup (ADR-0015).
* Spike evidence: `spikes/huma-swagger/RESULTS.md`.

OpenAPI will serve as the formal external API contract and support:

* Swagger-style documentation
* API client generation
* Automation integrations
* CLI development
* External orchestration
* Future Terraform or Ansible integrations

---

# Workflows

DBR² will use Temporal for durable execution and orchestration.

**Core technologies**

* Temporal
* Temporal Go SDK

**Purpose**

Backup and restore operations are long-running, multi-stage workflows that must survive transient failures, process restarts, agent disconnects, and infrastructure interruptions.

Temporal will orchestrate workflows such as:

```text
Discover Application
        ↓
Validate Protection Policy
        ↓
Claim Application (exclusive workflow ID)
        ↓
Run Pre-Backup Hooks
        ↓
Create Database Dumps
        ↓
Quiesce Application
        ↓
Protect Volumes / Bind Mounts
        ↓
Resume Application
        ↓
Upload Backup Data
        ↓
Verify Repository Objects
        ↓
Commit Recovery Point (write manifest last)
        ↓
Replicate Backup
        ↓
Apply Retention
        ↓
Update Recovery Point Index
        ↓
Run Post-Backup Hooks
        ↓
Notify
```

Notes on the workflow:

* **Protect / Upload.** If a filesystem snapshot is available, "Protect Volumes" takes the snapshot, the application resumes immediately, and "Upload Backup Data" reads from the snapshot. Without snapshots, which is the common case and must always work, the Kopia upload runs inside the quiesce window, so Protect and Upload are effectively one step (ADR-0005).
* **Resume is guaranteed.** `Resume Application` and the post-backup hooks are registered as saga compensation as soon as Quiesce succeeds. The agent also enforces a quiesce-lease dead-man switch (ADR-0005).
* **Atomic commit.** Components are captured first. The recovery point exists only once its manifest is committed to the Repository (ADR-0004).
* **Execution.** Activities run in `dbr2-worker` and reach agents through the Agent Gateway (ADR-0001).

Temporal will also manage:

* Retry policies
* Timeouts
* Workflow state
* Job resumption
* Scheduled workflows
* Restore orchestration
* Recovery testing
* Repository verification
* Replication workflows
* Retention processing

This keeps complex workflow state out of ad-hoc job tables and prevents the control-plane API from becoming responsible for long-running execution.

## Concurrency Control

DBR² must never run two conflicting operations against the same application at once, such as two backups, or a backup and a restore. Mutual exclusion therefore relies only on durable systems: **Temporal and PostgreSQL**. It is never held in the cache, because a lock that can disappear on restart, eviction or failover does not provide exclusion.

**Application operations use Temporal workflow IDs as the lock.**

Every workflow that operates on a live application (backup, restore, migration) starts with a deterministic workflow ID derived from the application:

```text
application/{application_id}
```

Temporal allows only one running workflow execution per workflow ID within a namespace, regardless of workflow type. A second start attempt for the same application is rejected, with the ID conflict policy set to fail, until the running workflow closes. The lock is held for exactly as long as the workflow runs, it survives worker and server restarts, and it is released when the workflow completes, fails, times out or is terminated.

* Manual, scheduled and API-triggered operations all use the same ID scheme, so they exclude one another.
* When a scheduled run collides with an operation already in progress, it is skipped and recorded as an overlapping run, not queued silently.
* Test restores into an isolated sandbox do not touch the live application, so they do not claim the application ID.
* Repository-wide operations (maintenance, verification, retention across a repository) use their own ID scheme, for example `repository/{repository_id}/maintenance`.

**PostgreSQL provides exclusion for short control-plane critical sections.**

* Prefer declarative guarantees first: unique constraints, conditional updates (`UPDATE … WHERE state = …`) and `SELECT … FOR UPDATE`.
* Use transaction-scoped advisory locks (`pg_advisory_xact_lock`) where a critical section spans several statements, for example agent enrollment or policy changes.
* Do not hold session-level advisory locks across Temporal activities or network calls. Long-lived exclusion belongs to Temporal.

---

# Data Layer

## PostgreSQL 18

PostgreSQL 18 will be the primary persistent database and the system of record for **platform state**.

> **Recovery data is authoritative in the Repository, not in PostgreSQL (ADR-0003).** Every recovery point's components and its versioned recovery manifest live in the Repository. For recovery points, PostgreSQL is an **index** that can be rebuilt at any time with `dbr2 admin reindex`. If PostgreSQL and a Repository disagree, the Repository wins. Losing PostgreSQL must never make a backup unrecoverable.

Temporal's persistence uses separate databases (`temporal`, `temporal_visibility`) on the same PostgreSQL server, each with its **own owner role** (ADR-0009).

Pinned images: `temporalio/server:1.32.0` plus `temporalio/admin-tools:1.32.0` (one-shot Compose jobs for schema setup and the `dbr2` namespace), `temporalio/ui:2.54.1` and `postgres:18.6-trixie`. `auto-setup` is not used. The PG18 data volume mounts at `/var/lib/postgresql`.

It will store metadata including:

* Hosts
* Agents
* Applications
* Containers
* Volumes
* Networks
* Dependencies
* Users
* Roles
* Policies
* Repositories
* Backup jobs
* Recovery points (index only)
* Restore operations
* Restore tests
* Recovery contracts
* Notifications
* Audit events

Backup payload data will not be stored directly in PostgreSQL.

PostgreSQL stores the metadata describing where protected data resides and how it can be recovered.

---

## Valkey

Valkey will provide ephemeral storage and caching where appropriate.

Valkey is a BSD-licensed, Redis-compatible in-memory store maintained under the Linux Foundation. It was chosen over Dragonfly because its license (BSD-3 rather than BSL) is simpler for a distributable product, it has broad ecosystem and client support (including `valkey-go` and `go-redis`), and DBR²'s cache workload does not need Dragonfly's multithreaded throughput.

Primary uses may include:

* UI cache
* Short-lived application state
* Rate limiting
* Session acceleration
* Temporary job progress
* Event fanout (for example, SSE across multiple `dbr2-server` instances)
* Expiring tokens

Valkey will not be treated as a durable system of record, and it will **not** be used for distributed locks or any other correctness-critical coordination (see *Concurrency Control*). Losing all Valkey data must never cause incorrect behavior, only cache misses and slower responses.

The responsibility boundaries will remain:

```text
PostgreSQL
Durable platform state

Temporal
Durable workflow state and operation exclusivity

Valkey
Disposable high-speed state
```

---

# DBR² Agent

The DBR² Agent will be implemented in Go and installed on protected Docker hosts.

**Core technologies**

* Go
* Docker/Moby SDK
* gRPC
* mTLS

**Purpose**

The agent is the primary data-plane component and will interact directly with the Docker Engine and host filesystem.

Responsibilities include:

* Docker discovery
* Compose stack discovery
* Container inspection
* Volume inspection
* Network inspection
* Bind-mount discovery
* Compose metadata collection
* Backup execution
* Restore execution
* Database backup hooks
* Filesystem traversal
* Repository access (through `dbr2-reposerver`, limited to its own snapshots; ADR-0002)
* Health reporting
* Application control
* Quiesce dead-man switch (ADR-0005)
* Restore validation

The agent will use the Docker/Moby Go SDK instead of relying primarily on shelling out to Docker CLI commands.

**Volume access (ADR-0006).**

* The agent reads data at the host level.
* Paths are resolved from Docker (`VolumeInspect().Mountpoint`, `Info().DockerRootDir`, container `Mounts`), never hard-coded.
* Non-local driver volumes and network-backed local volumes are classified **External** and are not backed up by default.
* Rootless Docker support is v2.
* SELinux contexts are recorded at capture and reapplied or relabeled on restore.

The agent should operate as a native system service (ADR-0006), for example:

```text
/usr/local/bin/dbr2-agent

/etc/dbr2/
    agent.yaml
    certs/

systemd:
    dbr2-agent.service
```

Agents should initiate outbound connections to the control plane wherever possible.

That avoids exposing the Docker daemon remotely and reduces firewall complexity.

---

# Agent Communication

Agent communication will use:

* gRPC
* Mutual TLS

gRPC provides:

* Strong contracts
* Efficient serialization
* Bidirectional streaming
* Deadlines
* Cancellation
* Streaming status updates
* Generated client/server bindings

mTLS will provide strong authentication between agents and the DBR² control plane.

Each agent should receive its own certificate identity so that an agent can be individually:

* Approved
* Audited
* Rotated
* Suspended
* Revoked

## Agent Gateway (ADR-0001)

`dbr2-worker` runs every Temporal activity. Activities reach agents through the **Agent Gateway** hosted in `dbr2-server`:

```text
dbr2-worker (activity) ──Dispatch(agent_id, command)──► Agent Gateway (dbr2-server)
                                                             │ bidirectional stream opened by the agent
                                                             ▼
                                                        dbr2-agent
```

* Agents open a long-lived `AgentService.Connect` stream. Session leases are tracked in PostgreSQL.
* Every command carries an idempotency key: `command_id` = workflow ID + activity ID + attempt.
* The agent journals commands locally. A command keeps running if the stream drops, and its status is reported on reconnect.
* Progress is relayed as Temporal activity heartbeats. If the agent does not return, the heartbeat timeout fails the activity, and Temporal retries it with the same `command_id`.
* Backup payloads never pass through the gateway or the worker.

---

# Backup Engine

DBR² will initially use Kopia as the underlying backup repository engine.

**Core technologies**

* Kopia
* Zstandard
* age

## Kopia

Kopia will initially handle the low-level backup repository responsibilities that DBR² should not reimplement prematurely.

These include:

* Content chunking
* Deduplication
* Compression
* Encryption
* Snapshot management
* Incremental backups
* Repository maintenance
* Storage backend support
* Integrity verification

DBR² will provide the Docker-aware orchestration above Kopia.

DBR² remains responsible for understanding:

```text
Application
Compose definition
Containers
Volumes
Bind mounts
Databases
Networks
Dependencies
Secrets
Images
Recovery order
RPO/RTO requirements
```

Kopia remains responsible for efficiently storing the underlying backup data.

### Integration (ADR-0007)

* Kopia is embedded as a **Go library**, pinned to an exact version.
* No other package imports Kopia directly; it is used only through `internal/engine/kopia`.
* Kopia upgrades are deliberate roadmap items, gated by an engine compatibility suite that restores fixture repositories from every previously shipped version.
* Repositories remain standard Kopia repositories. With the escrowed repository password, a stock `kopia` CLI can still recover data if DBR² binaries are unavailable.

### Repository Server (ADR-0002)

Each Repository is served by a **Kopia Repository Server**, deployed as `dbr2-reposerver`.

* Only `dbr2-reposerver` holds the repository password and the storage backend credentials.
* Each agent authenticates as its own Kopia user. Its ACLs allow APPEND on its own snapshots and READ on its own policies, and nothing else. Agents **cannot delete** (spike-verified).
* Every DBR² snapshot is **pinned**, and Kopia's own retention keeps everything. DBR² retention deletes whole recovery points through the **`maint@dbr2`** identity, which only `dbr2-worker` holds. Recovery manifests are written only by `maint@dbr2`.
* **Maintenance and GC run inside `dbr2-reposerver`.** It runs Kopia's server in-process through Kopia's public `cli` package, because the server code itself is not importable (ADR-0007).
* The agent splits and hashes data and skips content the server already has; the reposerver compresses and encrypts. **TLS on the agent link is mandatory.** New repositories use the `DYNAMIC-1M-BUZHASH` splitter.
* Isolation caveat: object IDs act as read keys, and there is a content-existence oracle across agents. Use one Repository per host where strict host-to-host confidentiality is required (ADR-0002).
* S3 Object Lock is used underneath where the storage backend supports it (v2).

### Backup engine abstraction

A **backup engine** abstraction is maintained internally, so that DBR² is not permanently coupled to Kopia (ADR-0012 terminology):

```go
type BackupEngine interface {
    Backup(...)
    Restore(...)
    Verify(...)
    Delete(...)
    List(...)
    Stats(...)
}
```

This allows additional backup engine implementations in the future.

### Recovery points (ADR-0004)

* A recovery point is a set of tagged Kopia snapshots (config, volumes, bind mounts, database dumps, optional images) plus a versioned JSON **recovery manifest**. The manifest is stored in the Repository and written **last**, as the commit marker.
* No manifest means no recovery point.
* If an optional component fails, the recovery point is committed with status **Partial**. If a required component fails, no recovery point is committed and the job fails.

---

## Zstandard

Zstandard will be used where DBR² directly produces compressed artifacts or streams.

Potential use cases include:

* Exported volume archives
* Database dumps
* Disaster-recovery bundles
* Metadata archives
* Offline transfer packages

Example:

```text
inventory-volume.tar.zst
postgres.dump.zst
application-recovery.tar.zst
```

---

## age

age will be used for portable encrypted artifacts where a simple, well-established file and stream encryption format is desirable.

Primary use cases include:

* Offline recovery exports
* Disaster-recovery bundles
* Manually transported backup packages
* Secure external archives

The platform should avoid designing proprietary cryptography.

---

# Storage

DBR² will support multiple storage models through repository and storage abstractions.

## Storage targets

**v1.0:**

* NFS-mounted storage. This is the production target: a single NAS, mounted **only on the `dbr2-reposerver` host**, never on agents.
* Local filesystem (development and testing)

**v2:**

* SMB-mounted storage
* S3-compatible object storage (enables S3 Object Lock immutability)

**NFS guidance for v1.0:**

* Export the share only to the reposerver host's address.
* Mount it `hard`, never `soft`.
* Enable scheduled, read-only **NAS snapshots** on the share. They are the v1.0 substitute for object-lock immutability.
* **Mount guard (ADR-0002):** `dbr2-reposerver` refuses to run unless the path is an active `nfs4` mount containing the matching `.dbr2-repository-id` sentinel, because Kopia will otherwise silently create a repository on the local disk. A stall watchdog reports a hung `hard` mount.

## S3-Compatible Platforms (v2)

Support should include:

* MinIO
* AWS S3
* Cloudflare R2
* Wasabi
* Backblaze B2 where compatible

Other providers can be introduced later through the same abstraction layer.

The application should distinguish between:

```text
Repository
```

and:

```text
Storage backend
```

For example:

```text
DBR² Agent (Kopia client, per-agent identity)
    ↓
dbr2-reposerver (Kopia Repository Server)
    ↓
DBR² Repository (Kopia repository format)
    ↓
Storage backend: S3-compatible storage
    ↓
MinIO
```

This separation allows repository behavior to change independently from physical storage.

---

# Authentication

DBR² will use standards-based external authentication where possible.

**Core technologies**

* OIDC
* OAuth2
* Local master admin account (username and password; lockout protection)

**v1.0:** Microsoft Entra ID through OIDC. Entra ID groups (or app roles) map to DBR² roles.

Supported identity providers should eventually include:

* Microsoft Entra ID
* Keycloak
* Zitadel
* Authentik
* Okta
* Google
* Generic OIDC providers

DBR² should act as an OIDC client rather than becoming a full identity provider.

**Master admin (required).** A local **master admin** account, authenticated by username and password, always exists for lockout protection. It works when Entra ID is unavailable or misconfigured.

* The password is stored as an Argon2id hash. Logins are rate-limited and locked out progressively.
* TOTP is optional and recommended; it is not required.
* Every login is audited, and a notification is sent (this can be configured).
* The master admin always holds the Administrator role and cannot be deleted or demoted.
* **Last-resort reset:** `dbr2 admin reset-master-password`, run as root on the `dbr2-server` host, for the case where the password itself is lost. The reset is audited.

---

# Authorization

The initial authorization implementation will use native DBR² RBAC.

Initial roles may include:

```text
Administrator
Backup Administrator
Restore Operator
Application Operator
Auditor
Read Only
```

Permissions should be represented granularly.

Examples:

```text
host.read
host.manage

application.read
application.manage

backup.read
backup.execute
backup.delete

restore.read
restore.execute
restore.production

repository.read
repository.manage

policy.read
policy.manage

audit.read

secrets.read
```

### Production restores and approval workflows

`restore.production` is tied to the optional **production restore approval** policy:

* **What counts as production:** a restore is a *production restore* when the target Application or Host is tagged `environment: production`, or when the restore would overwrite a live application in place.
* **Permission required:** requesting a production restore requires `restore.execute` **and** `restore.production`.
* **When approval is enabled** (per environment or per Application):
  * The restore workflow waits in **Pending Approval**, as a Temporal workflow waiting on a signal with an expiry.
  * A **second user** holding `restore.production`, who is not the requester, must approve it.
  * A reason or change-ticket field (for example `INC-48391`) is mandatory.
* **Emergency override:** an Administrator can override approval. The override requires a reason, raises a critical alert, and is audited.
* **Maintenance windows:** production restores outside configured maintenance windows are treated as emergency overrides.
* **One operator or a team (ADR-0014):** approval and dual authorization can be enabled only when at least two eligible users exist (the master admin is not counted). Single-operator safeguards are always on: typed confirmation, a mandatory reason, the impact preview, and a 7-day deletion grace period.
* **Other destructive operations** use the same approval mechanism as optional **dual authorization**: deleting a recovery point, deleting a Repository, reducing retention, disabling immutability, and rotating a repository key.

If authorization requirements become substantially more complex, DBR² can later integrate:

* OPA
* OpenFGA

This allows the first implementation to remain straightforward without closing off future relationship-based or policy-based authorization.

---

# Platform Self-Protection (ADR-0008) — mandatory

DBR² must be able to survive its own loss.

**Self-backup.** A built-in Platform Protection workflow, run daily and after security-relevant configuration changes, backs up:

* the DBR² PostgreSQL database
* the Temporal databases (kept for forensics)
* configuration, including the OIDC client configuration
* the DBR² CA and TLS keys
* repository passwords, storage credentials and the maintenance identity

It writes to a dedicated **System Repository** and to an **age-encrypted Platform Recovery Bundle** stored outside that repository.

**Key escrow.**

* Every repository password and every other critical secret is sealed with age to one or more **escrow recipients**. Hardware-backed or offline keys are recommended.
* A new Repository cannot be completed until an administrator confirms that its escrow package has been stored.
* An escrow health check alerts when escrow is missing, stale or out of date.

**Platform recovery runbook.**

1. Install a fresh DBR².
2. Run `dbr2 admin restore-platform <bundle>`.
3. Run `dbr2 admin reindex` for each Repository.
4. Start Temporal fresh. Schedules are rebuilt from policies.
5. Agents reconnect with their existing certificates.

A platform recovery test is part of the release checklist.

---

# Observability

DBR² will use OpenTelemetry as the common observability standard.

**Core technologies**

* OpenTelemetry
* Go structured logging with slog
* OTLP export

DBR² services should emit:

* Logs
* Metrics
* Traces

Important platform metrics may include:

```text
backup.duration
backup.bytes_read
backup.bytes_uploaded
backup.bytes_stored
backup.failure_count

restore.duration
restore.failure_count

repository.used_bytes
repository.free_bytes

agent.connection_state
agent.latency

workflow.duration
workflow.failure_count

rpo.violation_count
restore_test.age
```

DBR² should provide its own observability dashboards while still allowing organizations to export telemetry to external monitoring platforms.

---

# Logging

Go services will use structured logging through the standard `slog` ecosystem.

Logs should be emitted as structured JSON where appropriate.

Example:

```json
{
  "time": "2026-09-25T13:30:00Z",
  "level": "INFO",
  "service": "dbr2-worker",
  "event": "volume.backup.complete",
  "application_id": "app_01",
  "backup_id": "bkp_01",
  "volume": "postgres-data",
  "bytes_processed": 8493384320
}
```

Stable event types and identifiers should be used to support:

* Troubleshooting
* SIEM ingestion
* Audit analysis
* Support diagnostics

---

# Transport

DBR² will use different protocols according to the communication pattern.

## Browser and External API

```text
REST + HTTPS
```

Used for:

* Web application
* CLI
* Automation
* Third-party integrations
* Administrative APIs

---

## Live Browser Updates

```text
Server-Sent Events
```

SSE will initially provide live updates for:

* Backup progress
* Restore progress
* Workflow state
* Logs
* Verification progress
* Agent status

SSE is preferred initially because most realtime communication is server-to-browser rather than fully bidirectional.

WebSockets can be introduced later if truly interactive features require them.

---

## Control Plane to Agent

```text
gRPC + mTLS
```

Used for:

* Commands
* Discovery
* Job execution
* Streaming progress
* Agent status
* Restore control
* Secure host communication

---

# Deployment

DBR² will support Docker Compose as the initial deployment method.

A basic deployment may contain:

```text
dbr2-web
dbr2-server
dbr2-worker
dbr2-reposerver    (one per Repository; may be co-located or deployed near storage)
postgres
valkey
temporal
```

Protected Docker hosts will run:

```text
dbr2-agent
```

Kubernetes deployment can be introduced later if customer scale or operational requirements justify it.

The internal architecture should avoid depending on Kubernetes-specific behavior so that DBR² remains equally usable in conventional Docker environments.

---

# Control Plane and Data Plane Separation

DBR² should explicitly separate management from backup data movement.

## Control Plane

```text
Next.js
Go API
PostgreSQL
Temporal
Valkey
Authentication
Authorization
Policies
Audit
Scheduling
```

The control plane decides what should happen.

## Data Plane

```text
DBR² Agent
Docker Engine
Host filesystem
Database utilities
Kopia (embedded)
dbr2-reposerver
Repository
Storage backend
```

The data plane performs the backup and restore operations.

Backup payloads never flow through the central DBR² API, the Agent Gateway or the worker.

Preferred architecture:

```text
                        DBR² Control Plane
                     (dbr2-server / dbr2-worker)
                               │
                               │ instructions (Agent Gateway)
                               ▼
Docker Host ───────────── DBR² Agent
                               │
                               │ backup data (per-agent Kopia identity)
                               ▼
                        dbr2-reposerver
                               │
                               ▼
                          Repository
                               │
                               ▼
                        Storage backend
```

This prevents the control plane from becoming a throughput bottleneck.

---

# CI/CD

DBR² uses **GitHub Actions**. The source repository is `github.com/AxiomOperator/dbr2`.

CI/CD responsibilities should include:

```text
Go builds
Frontend builds
Unit tests
Integration tests
Security scanning
Dependency scanning
Container builds
SBOM generation
Container signing
Release packaging
Agent binary publishing
Database migration validation
Changelog check per component (ADR-0015)
OpenAPI breaking-change diff (oasdiff) and proto breaking-change check (buf)
License and NOTICE / third-party notice check
```

**Versioning (ADR-0015):**

* Every component (api, server, worker, agent, reposerver, cli, web, agent-protocol, manifest-schema, db-schema, deployment) has its own `VERSION` and `CHANGELOG.md`.
* Versions use the form `MAJOR.MINOR.BUGFIX.BUILD`, where BUILD is the GitHub Actions `run_number`.
* Platform releases pin component versions in `release-manifest.json`.

The release pipeline should generate versioned artifacts for:

```text
dbr2-server
dbr2-worker
dbr2-agent (static binary, RPM, DEB)
dbr2-reposerver
dbr2 CLI
Web container
Deployment manifests
```

Every release must also pass:

```text
Engine compatibility suite (restore repositories from all prior versions)
Recovery manifest schema compatibility tests
Platform recovery test (ADR-0008)
```

---

# Testing

DBR² requires substantially more than unit testing because the product directly manipulates live container workloads and persistent data.

## Go Tests

Used for:

* Domain logic
* Backup engine abstraction
* Manifest parsing and schema versioning
* Retention logic
* Policy evaluation
* Docker metadata translation
* Recovery planning

---

## Storage mocking (development and CI)

The development box is not connected to the NAS, so **NFS is mocked**:

* **Default dev profile:** the Repository's storage backend is a local directory mounted at the same path the production NFS mount will use (for example `/mnt/dbr2-repo`). Configuration is identical to production apart from the source of the mount.
* **Functional NFS tests:** a userspace **nfs-ganesha** server container (the host's kernel `nfsd` is not used) plus a **privileged client container**, which mounts with the production options (`nfs4,hard,timeo=600,retrans=2,noatime`). CI runners must allow `--privileged` for this job. The tests cover:
  * export restriction: the allowed IP mounts, and another client is refused (assert that the mount fails, not a specific error code)
  * an **NFS outage** mid-snapshot, with Kopia uploads throttled so the outage lands mid-write: expect a stall, then completion and a clean verify
  * the server being unavailable at connect time
  * the mount guard and sentinel rejecting an unmounted path
* Validated in `spikes/kopia-fidelity-nfs/`.
* **No throughput or performance testing** until the real NAS is connected. The seed and incremental timing measurement for the ~500 GB volume is deferred until then.

---

## Testcontainers

Testcontainers will provide ephemeral infrastructure for integration testing.

Useful test targets include:

* PostgreSQL
* Docker workloads
* S3-compatible object stores
* Example application stacks
* Database backup/restore workflows

---

## Vitest

Vitest will provide fast frontend unit and component testing.

---

## Playwright

Playwright will provide browser-level end-to-end testing for workflows such as:

```text
Add Docker host
Discover application
Create backup policy
Run backup
Review recovery point
Start restore
Monitor restore
Validate completion
```

---

## Real Docker Engine Integration Testing

DBR² should maintain an integration test suite that runs against an actual Docker Engine.

This is critical.

Tests should include scenarios such as:

```text
Named volume backup and restore
Bind mount backup and restore
Compose stack discovery
Multiple Compose files
Environment files
Database dumps
Container shutdown during backup
Agent disconnect
Interrupted upload
Repository outage
Port collisions
Cross-host restore
Missing Docker image
Image digest mismatch
Corrupt backup object
Partial restore
Application health-check failure
Agent disconnect while application is quiesced (dead-man auto-resume)
Worker crash after quiesce (saga compensation resumes the application)
Required component failure (no recovery point committed)
Optional component failure (Partial recovery point)
Reindex from Repository after PostgreSQL loss
Agent credential cannot delete or read other hosts' snapshots
SELinux-enforcing host: bind-mount and volume restore
Concurrent backup and restore on same application (rejected)
```

Backup software should be tested primarily on whether it can restore correctly, not merely whether it can create an archive.

---

# Codebase Structure

A practical monorepo layout would be:

```text
dbr2/
│
├── cmd/
│   ├── server/
│   ├── worker/
│   ├── agent/
│   ├── reposerver/
│   └── dbr2/
│
├── internal/
│   ├── agentgateway/    # Agent Gateway (ADR-0001)
│   ├── api/
│   ├── auth/
│   ├── backup/
│   ├── compose/
│   ├── database/        # database-aware backup plugins (pg_dump, mysqldump, …)
│   ├── docker/
│   ├── encryption/      # age exports, escrow
│   ├── engine/          # BackupEngine interface
│   │   └── kopia/       # the only package that imports Kopia (ADR-0007)
│   ├── manifest/        # versioned recovery manifest (ADR-0004)
│   ├── policy/
│   ├── repository/      # Repository domain: configuration, reposerver management
│   ├── restore/
│   ├── runtime/
│   ├── storage/         # storage backend configuration
│   ├── store/           # PostgreSQL data access (sqlc)
│   └── workloads/
│
├── workflows/
│   ├── backup/
│   ├── restore/
│   ├── verify/
│   ├── retention/
│   └── platform/        # self-backup (ADR-0008)
│
├── proto/
│   └── agent/
│
├── db/
│   ├── migrations/
│   └── queries/
│
├── web/
│   └── Next.js application
│
├── deployments/
│   ├── docker-compose/
│   └── kubernetes/
│
├── tests/
│   ├── integration/
│   └── fixtures/
│
└── docs/
```

---

# Runtime Abstraction

Although Docker is the initial target, DBR² should not hard-code Docker terminology into every internal domain interface.

For example:

```go
type ContainerRuntime interface {
    DiscoverApplications(ctx context.Context) ([]Application, error)
    InspectApplication(ctx context.Context, id string) (*Application, error)
    ListVolumes(ctx context.Context) ([]Volume, error)
    StopApplication(ctx context.Context, id string) error
    StartApplication(ctx context.Context, id string) error
}
```

The first implementation will be:

```text
DockerRuntime
```

This leaves room for future implementations such as:

```text
PodmanRuntime
```

without redesigning the core backup model.

Domain entities should therefore favor names such as:

```text
Application
Runtime
Volume
Repository
RecoveryPoint
ProtectionPolicy
```

rather than tightly coupling every internal concept to Docker.

---

# Final Architecture

The final DBR² architecture should follow this responsibility model:

```text
Next.js
Human interface

        ↓

Go Control Plane
API, policy, security and management

        ↓

Temporal
Durable orchestration

        ↓

Go Agents / Workers
Execution and Docker interaction

        ↓

Kopia (embedded) via dbr2-reposerver
Backup repository mechanics; authoritative recovery data

        ↓

Filesystem / NAS / S3
Physical storage
```

Supporting infrastructure:

```text
PostgreSQL
Durable DBR² platform state; recovery-point index

Valkey
High-speed temporary state

OpenTelemetry
Observability

OIDC
Identity

gRPC + mTLS
Secure agent communication
```

The architectural principle behind the final stack is:

> **Go owns Docker and recovery orchestration. Temporal owns durable execution and operation exclusivity. PostgreSQL owns DBR² platform state. The Repository owns recovery data, with Kopia providing its mechanics. Next.js owns the administrative experience.**

This keeps DBR² focused on its actual value: understanding containerized applications, protecting them correctly, proving they are recoverable, and rebuilding them reliably when required.

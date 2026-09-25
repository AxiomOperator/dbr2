# DBR² — Domain Model

> Aligned with `stack_info/final_stack.md` and `adr/`. The glossary terms follow ADR-0012.

## Core idea

The **Application** is the unit of protection. Everything else either describes an Application (inventory), says how to protect it (Protection Policy), says what protection must achieve (Recovery Contract), is produced by protecting it (Recovery Point), or orchestrates recovering one or more Applications (Recovery Plan).

```text
Host 1──1 Agent
Host 1──* Runtime (DockerRuntime; rootless engines later)
Runtime 1──* Application
Application 0..1── Compose Project           (definition source)
Application 1──* Service ──* Container
Application *──* Volume / Bind Mount / Network
Application 1──* Dependency (external)
Application *──1 Protection Policy ──* Repository ──1 Storage backend
Application 1──1 Recovery Contract
Application 1──* Recovery Point 1──* Component
Recovery Point 1──1 Recovery Manifest
Consistency Group 1──* Application           (captured together)
Recovery Plan 1──* Stage 1──* (Application | Consistency Group)
```

## Entities

### Inventory

| Entity | Definition | Notes |
|---|---|---|
| **Host** | A machine running a container runtime and a DBR² Agent | Identified by the agent's certificate identity |
| **Agent** | The `dbr2-agent` instance on a Host | Lifecycle: **pending** (enrolled with a registration token, sessions refused) → **active** (approved) ⇄ **suspended**; **revoked** is permanent (ADR-0016). Identity is a CA-issued client certificate (`dbr2://agent/<id>`). It will also hold a Kopia user identity (ADR-0002, Phase 4). |
| **Runtime** | A container engine on a Host, behind the `ContainerRuntime` abstraction | Initially `DockerRuntime` (rootful). Rootless engines and `PodmanRuntime` come later. |
| **Application** | The logical unit of protection and recovery: a set of services and their resources that are backed up and restored together | Kinds: **compose** (one Compose project), **container** (a standalone container) and **manual** (standalone containers grouped by an administrator). Its Compose definition is **Original** or **Reconstructed**. Ownership metadata: owner, environment (production, staging, development, test, other), criticality (critical, high, medium, low). Marked *missing* when it disappears from the host's latest inventory. |
| **Compose Project** | The *definition source* of an Application: the Compose files, env files and project name found through `com.docker.compose.*` labels | 0..1 per Application. Its provenance is **Original** or **Reconstructed** (generated from runtime metadata). It is not itself a unit of protection. |
| **Service / Container** | A Compose service and its running containers | Containers are observations. Services are part of the definition. |
| **Volume** | A Docker named volume used by the Application | Classification: Local (protected by default), External (network or driver-backed; not protected by default), Ephemeral (cache or temp; optional) |
| **Bind Mount** | A host path mounted into a container | Records the host path, container path and SELinux context. Restore can remap the path. |
| **Network** | A Docker network used by the Application | Project-owned, or External (flagged as a dependency) |
| **Dependency** | Something outside the Application that recovery needs | Examples: external database, NFS or SMB share, external network, DNS, reverse proxy, SMTP, identity provider, private registry. Each is Protected, Unprotected or Acknowledged. |

### Protection

| Entity | Definition | Notes |
|---|---|---|
| **Protection Policy** | *How* to protect: schedule, consistency mode (Live, Quiesced, Offline), maximum quiesce duration, hooks, target Repositories, retention, and required/optional component rules | Reusable across Applications. Executed by Temporal schedules. |
| **Recovery Contract** | *What protection must achieve*: maximum RPO, target RTO, required components, maximum restore-test age, offsite and immutable copy requirements | One per Application (it may inherit a template). It is **evaluated**, not executed, and yields **Satisfied** or **Violated** with reasons. The Policy is the means; the Contract is the standard it is judged against. |
| **Repository** | A Kopia-backed store of Recovery Points, served by one `dbr2-reposerver` | See ADR-0002 and ADR-0012. Designated as Primary, Secondary, Offsite or Immutable in a Policy. |
| **Storage backend** | The physical target behind a Repository | Local filesystem, NFS, SMB, S3-compatible |

### Recovery data (ADR-0003, ADR-0004)

| Entity | Definition | Notes |
|---|---|---|
| **Recovery Point (RP)** | An immutable, point-in-time, restorable capture of one Application | It exists if and only if its manifest is committed in the Repository. Status: Complete or Partial. Verification: Unverified, Verified or Verification Failed. |
| **Component** | One captured part of an RP: config, volume, bind mount, database dump or image | Each is one tagged Kopia snapshot, marked required or optional |
| **Recovery Manifest** | A versioned JSON document describing an RP and its components | Stored in the Repository (authoritative) and indexed in PostgreSQL |
| **Backup Job** | One execution of the backup workflow for an Application | A Temporal workflow with ID `application/{id}` (ADR-0011). It produces 0 or 1 RP. |

### Recovery

| Entity | Definition | Notes |
|---|---|---|
| **Restore Operation** | Restoring one RP to a target Host, either in place or to an alternate host, name or paths | Includes mapping rules (paths, ports, names, variables). Production targets require `restore.production`, plus approval when that is enabled. |
| **Restore Test** | Restoring an RP into an isolated sandbox, validating it, then destroying the sandbox | Produces evidence (health-check results, measured RTO). It updates the RP's verification state and the Contract's restore-test age. |
| **Migration** | Backup, then restore to another Host, with source cut-over steps | A composite workflow |
| **Consistency Group** | A set of Applications captured with a shared consistency point | *Backup-time* coordination. It produces a group manifest referencing the members' RPs (ADR-0004). Later scope. |
| **Recovery Plan** | An ordered, staged recovery of several Applications or Consistency Groups, with health gates between stages | *Restore-time* orchestration: priorities, waits and synthetic checks. It does **not** define capture. Later scope. |

## Resolving overlapping concepts

- **Application vs Compose Project:** the Application is what DBR² protects. The Compose Project is where its definition came from. An Application without a Compose Project is valid (Reconstructed definition, flagged).
- **Consistency Group vs Recovery Plan:**
  - A Consistency Group answers *"which Applications must be captured at the same moment?"* (backup time).
  - A Recovery Plan answers *"in what order, and with what checks, do we bring things back?"* (restore time).
  - A Recovery Plan stage may reference a Consistency Group.
- **Protection Policy vs Recovery Contract:** the Policy says what DBR² *does*. The Contract says what the business *requires*. The dashboard reports on the Contract.
- **Recovery Point vs Kopia snapshot:** users only ever see Recovery Points. Kopia snapshots are internal components.

## Glossary (ADR-0012)

- **Repository:** a Kopia-backed recovery-point store managed by DBR².
- **Storage backend:** the physical target behind a Repository.
- **Backup engine:** the Go abstraction over Kopia (`internal/engine`).
- **Store:** the PostgreSQL data-access layer (`internal/store`, sqlc).
- **Source repository:** a Git repository holding source code, always qualified.

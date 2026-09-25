# DBR² — Platform Concept

> Aligned with `../stack_info/final_stack.md`, which wins on any conflict.

> **Select a Docker application → back up everything required to recreate it → restore it on the same or another Docker host.**

DBR² is built around **application-level backups**: a recovery point contains the Compose definition, environment/configuration, Docker metadata, persistent data, and restore instructions.

## What a recovery point contains

Every recovery point for a protected application has the following logical contents.

Inside a DBR² repository, the data is stored as Kopia snapshots, which handle chunking, deduplication, compression and encryption. A DBR² recovery manifest ties those snapshots together. The directory layout below is the **logical** view. It is materialized physically only in exported disaster-recovery bundles, which are Zstandard-compressed and optionally age-encrypted.

```text
backup/
├── manifest.json
├── compose/
│   ├── compose.yaml
│   ├── compose.original.yaml
│   ├── .env
│   └── overrides/
│
├── metadata/
│   ├── containers.json
│   ├── images.json
│   ├── networks.json
│   ├── volumes.json
│   └── host.json
│
├── data/
│   ├── volumes/
│   │   ├── postgres-data/
│   │   ├── redis-data/
│   │   └── app-data/
│   └── bind-mounts/
│       ├── etc-app/
│       └── uploads/
│
├── databases/
│   ├── postgres/
│   │   └── database.dump
│   └── mysql/
│       └── database.sql.zst
│
├── checksums/
│   └── sha256sums
│
└── logs/
    └── backup.log
```

That gives you substantially more than simply copying `/var/lib/docker/volumes`.

## The key feature: discover the application automatically

The user should connect a Docker host and immediately see something like:

```text
Docker Host: docker-prod-01

Applications
──────────────────────────────────────
✓ Inventory
  6 containers
  4 volumes
  2 bind mounts
  Compose detected

✓ Qdrant
  1 container
  1 volume
  Compose detected

✓ Nagios
  7 containers
  8 volumes
  3 bind mounts
  Compose detected

! Legacy-App
  3 containers
  2 volumes
  No Compose definition detected
```

The DBR² Agent gathers the equivalent of:

```bash
docker ps
docker inspect
docker volume ls
docker network ls
docker compose ls
```

through the **Docker/Moby Go SDK**, rather than by shelling out to the Docker CLI.

For Compose deployments, Docker already adds labels such as:

```text
com.docker.compose.project
com.docker.compose.service
com.docker.compose.project.config_files
com.docker.compose.project.working_dir
```

Those labels are extremely useful for reconstructing the stack.

---

# Architecture

The authoritative architecture and technology choices live in `../stack_info/final_stack.md`. In summary:

```text
┌──────────────────────────────────────────────────────────────┐
│                  DBR² Console (dbr2-web)                     │
│   Next.js + React + TypeScript + ShadCN + TailwindCSS        │
└────────────────────────────┬─────────────────────────────────┘
                             │ REST + HTTPS / SSE
                             ▼
┌──────────────────────────────────────────────────────────────┐
│                DBR² Server (dbr2-server)                     │
│             Go · Chi · Huma · OpenAPI · pgx/sqlc             │
│  Hosts │ Applications │ Policies │ Restore │ Repositories │  │
│  Recovery contracts │ Users/RBAC │ Audit                     │
└──────┬──────────────────┬──────────────────┬─────────────────┘
       │                  │                  │
       ▼                  ▼                  ▼
┌──────────────┐  ┌───────────────┐  ┌──────────────────────┐
│ PostgreSQL 18│  │   Temporal    │  │       Valkey         │
│ Platform     │  │ Durable       │  │ Disposable           │
│ state        │  │ workflows     │  │ high-speed state     │
└──────────────┘  └───────┬───────┘  └──────────────────────┘
                          │
                          ▼
                 DBR² Worker (dbr2-worker)
                          │ Agent Gateway (gRPC + mTLS)
                          ▼
            DBR² Agent (dbr2-agent) on each Docker host
                          │ backup data (not via the API)
                          ▼
          dbr2-reposerver → Repository (Kopia)
                          │
                          ▼
   Local filesystem / NFS / SMB / S3-compatible storage
```

| Concern | Choice |
|---|---|
| Backend / control plane | Go (Chi, Huma, pgx, sqlc, OpenAPI) |
| Frontend | Next.js + React + TypeScript + ShadCN + TailwindCSS + TanStack + React Flow + Zod |
| Database | PostgreSQL 18 (metadata only; no backup payloads) |
| Orchestration | Temporal (Go SDK) |
| Ephemeral state / cache | Valkey (never a system of record; no locks) |
| Container runtime access | DBR² Agent via Docker/Moby Go SDK, behind a `ContainerRuntime` abstraction |
| Backup engine | Kopia, embedded as a library (ADR-0007), served per Repository by `dbr2-reposerver` (ADR-0002) |
| Exported artifacts | Zstandard compression, age encryption |
| Storage | v1.0: NFS (single NAS) plus local filesystem for testing; v2: SMB, S3-compatible |
| Authentication | OIDC client (Entra ID in v1.0) plus a local master admin (username and password) |
| Observability | OpenTelemetry, `slog`, OTLP export |

---

# Host connectivity

DBR² uses a **single host connection model**: the DBR² Agent. Every protected Docker host runs the agent, including a host that also runs the DBR² control plane. The control plane never talks to a Docker daemon directly.

```text
DBR² Control Plane
       ▲
       │ gRPC + mTLS (agent-initiated, outbound)
       │
DBR² Agent (dbr2-agent)
       │
       ▼
Docker Engine (local socket, via Docker/Moby Go SDK)
```

The agent is a native system service:

```text
/usr/local/bin/dbr2-agent
/etc/dbr2/agent.yaml
/etc/dbr2/certs/
systemd: dbr2-agent.service
```

Access to the Docker socket gives **root-equivalent control of the host**. The agent is the only component that holds it, and the socket is never exposed through the DBR² API.

Each agent has its own certificate identity, so it can be individually approved, audited, rotated, suspended and revoked.

The agent:

```text
Discovers stacks
Reads Docker metadata
Coordinates container state
Streams volume backups
Executes DB backup hooks
Performs restores
Reports health
```

Because agents connect outbound, DBR² never needs a remotely exposed Docker daemon such as:

```text
tcp://docker-host:2375
```

which should never be used.

---

# Stack discovery

This should be one of the strongest parts of the product.

Suppose Docker contains:

```text
inventory-web
inventory-api
inventory-worker
inventory-postgres
inventory-dragonfly
```

The program detects:

```text
com.docker.compose.project=inventory
```

and presents:

```text
Application: inventory

Services:
  web
  api
  worker
  postgres
  dragonfly

Volumes:
  inventory_pgdata
  inventory_uploads

Bind mounts:
  /opt/inventory/config
  /srv/inventory/documents

Networks:
  inventory_backend
  inventory_frontend
```

Everything becomes one logical **application**.

That's much better UX than forcing the administrator to think in terms of individual containers.

---

# Compose backup

There are actually two cases.

### Original Compose exists

Best case.

Copy:

```text
docker-compose.yml
compose.yml
compose.yaml
.env
docker-compose.override.yml
```

into the backup.

### Compose file doesn't exist

This is where your product can differentiate itself.

Generate a **reconstructed Compose definition** from `docker inspect`.

For example:

```yaml
services:
  postgres:
    image: postgres:18
    restart: unless-stopped

    environment:
      POSTGRES_DB: inventory
      POSTGRES_USER: inventory

    volumes:
      - postgres-data:/var/lib/postgresql/data

    networks:
      - backend

volumes:
  postgres-data:

networks:
  backend:
```

You should flag it:

```text
Compose Source

✓ Original        Exact original Compose files found
⚠ Reconstructed   Generated from Docker runtime metadata
```

Never pretend a generated Compose file is identical to the original.

---

# Persistent data

You need to handle two major types.

### Named volumes

Example:

```yaml
volumes:
  - postgres-data:/var/lib/postgresql/data
```

Backup flow:

```text
Docker volume
     │
     ▼
DBR² Agent (filesystem traversal on the host)
     │
     ▼
Kopia snapshot
(chunking · dedup · compression · encryption)
     │
     ▼
DBR² repository → storage backend
```

The recovery manifest records the resulting snapshot for the volume, for example `postgres-data`. A standalone `postgres-data.tar.zst` archive is produced only when the application is exported as a disaster-recovery bundle.

### Bind mounts

Example:

```yaml
volumes:
  - /srv/inventory/uploads:/app/uploads
```

Record both:

```text
Original host location:
/srv/inventory/uploads

Container location:
/app/uploads
```

The restore UI can then offer:

```text
Restore original path

/srv/inventory/uploads

or map to:

________________________
```

This matters when migrating to another host.

---

# Databases need special treatment

This is critical.

Simply copying a live database volume isn't always safe.

You should support **application-consistent backup plugins**.

For PostgreSQL:

```bash
pg_dump
pg_dumpall
```

For MySQL/MariaDB:

```bash
mysqldump
```

v1.0 supports **PostgreSQL** and **Redis** (RDB through `BGSAVE`). Later:

```text
PostgreSQL
Redis
MySQL
MariaDB
MongoDB
Microsoft SQL Server
InfluxDB
Elasticsearch
```

Then the UI can show:

```text
inventory-postgres

Database detected: PostgreSQL 18

Backup strategy:

◉ Logical database backup
○ Volume snapshot
○ Both
```

I'd recommend **Both** by default for important databases.

That gives you, within one recovery point:

```text
Kopia snapshot of the postgres-data volume
inventory-postgres.dump.zst (logical dump, stored in the repository)
```

---

# Backup consistency

Give users three backup modes.

### Live

```text
No interruption
Fast
Potentially inconsistent filesystem state
```

Useful for:

```text
uploads
documents
static assets
```

### Quiesced

Run pre/post hooks.

Example:

```text
Pre-backup:
docker exec app /app/bin/maintenance-on

Backup

Post-backup:
docker exec app /app/bin/maintenance-off
```

### Offline

```text
Stop stack
Backup
Restart stack
```

For small internal systems, this is often the easiest way to guarantee consistency.

The UI:

```text
Backup consistency

○ Live
● Quiesced (pre/post hooks)
○ Offline (stop application during backup)
```

These modes map to the backup workflow stages in the final stack: Run Pre-Backup Hooks → Create Database Dumps → Quiesce Application → Protect Volumes / Bind Mounts → Resume Application.

---

# Backup policy

I'd make policies easy to understand.

Example:

```text
Policy: Production Daily

Schedule
Daily at 2:00 AM

Retention
Hourly:     24
Daily:      14
Weekly:      8
Monthly:    12
Yearly:      3

Repository
Primary-NAS (Kopia; compressed and encrypted)
```

Schedules run as Temporal scheduled workflows. Supported schedule types:

```text
Cron
Hourly
Daily
Weekly
Monthly
```

and later more sophisticated policies.

---

# Incremental backups

Incremental, deduplicated backups are available **from the first release**, because DBR² uses Kopia as its repository engine.

A naive backup system stores four full 100 GB copies for four backups. Kopia instead splits content into chunks, stores each chunk once, and uploads only chunks that have changed. Kopia handles:

```text
Content chunking
Deduplication
Compression
Encryption
Snapshot management
Incremental backups
Repository maintenance
Integrity verification
```

DBR² does not reimplement any of this. It provides the Docker-aware orchestration above Kopia, behind the internal `BackupEngine` interface, so that DBR² is not permanently coupled to Kopia.

---

# Storage providers

Storage targets for v1.0 (see final_stack → Context & Constraints):

```text
NFS-mounted filesystem   (production: single NAS, mounted only on dbr2-reposerver)
Local filesystem         (development and testing)
```

v2:

```text
SMB-mounted filesystem
S3-compatible object storage
```

S3-compatible platforms (v2):

```text
MinIO
AWS S3
Cloudflare R2
Wasabi
Backblaze B2 (where compatible)
```

Other providers (for example SFTP or Azure Blob) can be added later through the same abstraction layer.

DBR² distinguishes a **Repository** from a **storage backend**:

```text
DBR² Repository
    ↓
Kopia
    ↓
S3-compatible storage
    ↓
MinIO
```

The internal abstraction is the `BackupEngine` interface defined in the final stack (ADR-0012 terminology), not a raw storage interface:

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

---

# Restore experience

This is arguably more important than backup.

The restore page should look like:

```text
Restore: Inventory

Backup
September 24, 2026 02:00

Restore To
docker-prod-02

────────────────────────────

Application Definition
✓ Compose file

Environment
✓ Variables

Docker Networks
✓ 2

Volumes
✓ postgres-data
✓ uploads

Databases
✓ PostgreSQL

────────────────────────────

Options

☑ Create missing volumes
☑ Create missing networks
☑ Restore database
☑ Pull missing images
☑ Start application after restore

[ Restore Application ]
```

Then:

```text
Preparing host
✓

Creating networks
✓

Creating volumes
✓

Restoring postgres-data
█████████████████ 100%

Restoring uploads
███████████░░░░░ 73%

Deploying compose stack
Waiting

Starting containers
Pending
```

---

# Cross-host migration

This becomes a killer feature almost automatically.

Instead of:

**Restore**

you can have:

**Migrate**

```text
FROM

docker-old-01

        ↓

inventory

        ↓

docker-prod-02
```

Workflow:

```text
Discover source
        ↓
Perform backup
        ↓
Transfer data
        ↓
Rewrite paths if necessary
        ↓
Pull images
        ↓
Restore volumes
        ↓
Deploy Compose
        ↓
Health checks
```

That turns the tool into more than backup software.

---

# Backup verification

Never consider:

```text
Backup completed
```

to be enough.

Instead:

```text
Backup completed
✓ Manifest valid
✓ SHA-256 verification passed
✓ Compose validated
✓ 4/4 volumes readable
✓ Database dump validated
✓ Repository integrity passed
```

Eventually offer **automatic restore testing**:

```text
Nightly backup
       ↓
Temporary Docker network
       ↓
Restore application
       ↓
Run health check
       ↓
Destroy test environment
```

Dashboard:

```text
RESTORE VERIFIED

Last verified:
Sep 24 2026 04:16

RPO: 2 hours
RTO test: 3m 42s
```

That would be extremely useful.

---

# Secrets

This is tricky.

Compose files frequently contain:

```yaml
environment:
  DB_PASSWORD: password
```

or:

```text
.env
```

You should therefore classify configuration:

```text
Normal config
Sensitive config
Secrets
```

Encryption follows the final stack. DBR² does not design its own cryptography.

```text
Repository data       Kopia repository encryption (always on)
Exported artifacts    age (DR bundles, offline exports, transported packages)
Agent transport       gRPC + mTLS
```

External key management (for example HashiCorp Vault, AWS KMS, Azure Key Vault or hardware-backed keys) is a possible future integration. It is not part of the final stack.

---

# Images

Don't necessarily back up Docker images.

Most can simply be:

```bash
docker pull
```

during restoration.

Record:

```text
repository
tag
digest
platform
```

Example:

```json
{
  "image": "postgres:18",
  "digest": "sha256:...",
  "platform": "linux/amd64"
}
```

But offer:

```text
☐ Archive Docker images
```

for:

```text
locally-built images
air-gapped environments
private images
images that may disappear
```

using:

```bash
docker save
```

---

# Container health

I'd put a strong dashboard around backup state.

Something like:

```text
DBR²
─────────────────────────────────────────────

Hosts                 5
Applications         43
Protected            41
Unprotected           2

Last 24 Hours

Successful           39
Warning               1
Failed                1

Stored Data
2.84 TB

Deduplicated
7.19 TB

Savings
60.5%

─────────────────────────────────────────────

APPLICATION       LAST BACKUP      STATUS

Inventory         22 min ago       ✓ Protected
Wiki              47 min ago       ✓ Protected
Qdrant            1 hr ago        ✓ Protected
Nagios            3 hr ago        ⚠ Warning
Legacy ERP        Never           ✕ Unprotected
```

---

# Major application areas

I would organize the console into:

```text
Dashboard

Docker
├── Hosts
├── Applications
├── Containers
└── Volumes

Protection
├── Backup Policies
├── Backup Jobs
└── Recovery Points

Recovery
├── Restore
├── Migrations
└── Restore Testing

Storage
├── Repositories
└── Usage

System
├── Agents
├── Users
├── Notifications
├── Audit Log
└── Settings
```

Notice that **Applications**, not containers, are the primary entity.

That's important.

---

# Suggested internal services

There is no need for dozens of microservices. The initial deployment is Docker Compose:

```text
dbr2-web
dbr2-server
dbr2-worker
dbr2-reposerver   (one per Repository)
postgres
valkey
temporal
```

Each protected Docker host runs:

```text
dbr2-agent
```

```text
               ┌───────────────┐
               │   dbr2-web    │
               │    Next.js    │
               └───────┬───────┘
                       │ REST / SSE
                       ▼
               ┌───────────────┐
               │  dbr2-server  │
               │      Go       │
               └───────┬───────┘
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
     PostgreSQL    Temporal      Valkey
                       │
                       ▼
                  dbr2-worker
                       │ gRPC + mTLS
                       ▼
                  dbr2-agents
                       │
           ┌───────────┼───────────┐
           ▼           ▼           ▼
        Docker 1    Docker 2    Docker 3
                       │
                       ▼
                dbr2-reposerver
                       │
                       ▼
              Repository / storage
```

Backup payloads flow from agents through `dbr2-reposerver` to storage. They never pass through `dbr2-server` or `dbr2-worker` (ADR-0001, ADR-0002).

---

# A backup manifest

Every recovery point has a machine-readable recovery manifest, written **last** as the commit marker by the "Commit Recovery Point" workflow step (ADR-0004). The manifest lives in the Repository, which is authoritative; PostgreSQL only indexes it (ADR-0003). The schema is versioned from day one. It references the Kopia snapshots that make up the recovery point. The `archive` path below applies to exported bundles; inside the repository, each entry references its snapshot.

For example:

```json
{
  "version": "1.0",
  "application": "inventory",
  "backup_id": "01K6...",
  "created_at": "2026-09-24T02:00:00Z",

  "source": {
    "hostname": "docker-prod-01",
    "docker_version": "28.4.0",
    "architecture": "amd64"
  },

  "compose": {
    "project": "inventory",
    "source": "original",
    "file": "compose/compose.yaml"
  },

  "volumes": [
    {
      "name": "inventory_pgdata",
      "archive": "data/volumes/inventory_pgdata.tar.zst",
      "sha256": "..."
    }
  ],

  "images": [
    {
      "name": "postgres:18",
      "digest": "sha256:..."
    }
  ]
}
```

Version that schema from day one.

---

# One feature I would consider mandatory

Add a button:

> **Download Disaster Recovery Package**

It produces:

```text
inventory-dr-2026-09-24.tar.zst        (optionally age-encrypted: .tar.zst.age)
```

containing everything necessary to reconstruct the application, following the final stack's use of Zstandard and age for disaster-recovery bundles.

And ideally:

```text
restore.sh
```

so even if your backup management server itself is dead, you can recover the application manually.

For example:

```bash
./restore.sh
```

could:

```text
Check Docker
Create volumes
Restore data
Create networks
docker compose pull
docker compose up -d
```

That prevents the backup software itself from becoming a recovery dependency.

---

# Development phases

The phases, checklists and change log live in **`../roadmap.md`**, the single authoritative plan. They are not repeated here.

The MVP target is concrete:

> **A web application that detects Docker Compose stacks, allows an administrator to click Back Up, captures the Compose definition plus all persistent data, and can restore that application onto a clean Docker server with one workflow.**

If that workflow is solid, everything else (retention, cloud storage, database plugins, migration, DR testing) can grow around a very solid core.

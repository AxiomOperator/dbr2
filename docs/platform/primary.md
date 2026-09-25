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
✓ Planix
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
│ PostgreSQL 18│  │   Temporal    │  │     Dragonfly        │
│ Platform     │  │ Durable       │  │ Disposable           │
│ state        │  │ workflows     │  │ high-speed state     │
└──────────────┘  └───────┬───────┘  └──────────────────────┘
                          │
                          ▼
                 DBR² Worker (dbr2-worker)
                          │ gRPC + mTLS
                          ▼
            DBR² Agent (dbr2-agent) on each Docker host
                          │ backup data (not via the API)
                          ▼
                   Kopia repository
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
| Ephemeral state / cache | Dragonfly (never a system of record) |
| Container runtime access | DBR² Agent via Docker/Moby Go SDK, behind a `ContainerRuntime` abstraction |
| Backup repository engine | Kopia (chunking, dedup, compression, encryption, incrementals) |
| Exported artifacts | Zstandard compression, age encryption |
| Storage | Local filesystem, NFS, SMB, S3-compatible |
| Authentication | OIDC client plus a local break-glass account |
| Observability | OpenTelemetry, `slog`, OTLP export |

---

# Host connectivity

This is one architectural decision I would make very carefully.

I would support **two host connection models**.

### Local Docker socket

For the Docker server running the backup software:

```text
/var/run/docker.sock
```

Example:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

But mounting the Docker socket effectively grants **root-equivalent control of the host**, so your API should never expose the socket directly.

### Remote agent

For production, I prefer:

```text
Central Backup Server
       │
       │ TLS / mTLS
       ▼
Docker Backup Agent
       │
       ▼
Docker Engine
```

Install a tiny Go binary:

```text
dockerbackup-agent
```

on each Docker host.

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

This avoids exposing:

```text
tcp://docker-host:2375
```

which you absolutely should not do.

---

# Stack discovery

This should be one of the strongest parts of the product.

Suppose Docker contains:

```text
planix-web
planix-api
planix-worker
planix-postgres
planix-dragonfly
```

The program detects:

```text
com.docker.compose.project=planix
```

and presents:

```text
Application: planix

Services:
  web
  api
  worker
  postgres
  dragonfly

Volumes:
  planix_pgdata
  planix_uploads

Bind mounts:
  /opt/planix/config
  /srv/planix/documents

Networks:
  planix_backend
  planix_frontend
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
      POSTGRES_DB: planix
      POSTGRES_USER: planix

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
temporary helper container
     │
     ▼
tar stream
     │
     ▼
zstd
     │
     ▼
backup repository
```

Something conceptually equivalent to:

```bash
tar -C /volume -cf - . | zstd
```

would generate:

```text
postgres-data.tar.zst
```

### Bind mounts

Example:

```yaml
volumes:
  - /srv/planix/uploads:/app/uploads
```

Record both:

```text
Original host location:
/srv/planix/uploads

Container location:
/app/uploads
```

The restore UI can then offer:

```text
Restore original path

/srv/planix/uploads

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

Eventually:

```text
PostgreSQL
MySQL
MariaDB
MongoDB
Redis
Microsoft SQL Server
InfluxDB
Elasticsearch
```

Then the UI can show:

```text
planix-postgres

Database detected: PostgreSQL 18

Backup strategy:

◉ Logical database backup
○ Volume snapshot
○ Both
```

I'd recommend **Both** by default for important databases.

That gives you:

```text
postgres-data.tar.zst
planix-postgres.dump.zst
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
● Application aware
○ Stop application during backup
```

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

Compression
Zstandard Level 6

Encryption
Enabled

Storage
FBCAD-NAS
```

You could support:

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

I would absolutely put this on the roadmap.

A naive backup system creates:

```text
100GB
100GB
100GB
100GB
```

for four backups.

Instead, implement deduplicated chunks:

```text
File
 ↓
Chunking
 ↓
Hash
 ↓
Object store
```

Example:

```text
SHA256(chunk) → backup object
```

If unchanged:

```text
already exists
→ don't upload
```

Then backup manifests reference objects.

Eventually you essentially build something similar conceptually to:

```text
restic
borg
kopia
```

You could even use one of those engines underneath initially rather than implementing deduplication yourself.

I would strongly consider **Kopia or Restic as the underlying data repository engine**, while your software handles Docker awareness and orchestration.

---

# Storage providers

For V1:

```text
Local filesystem
NFS-mounted filesystem
SMB-mounted filesystem
S3 compatible
```

Then:

```text
AWS S3
Backblaze B2
Wasabi
Cloudflare R2
MinIO
SFTP
Azure Blob
```

Because the application writes through a storage abstraction:

```go
type StorageProvider interface {
    Put()
    Get()
    Delete()
    List()
    Stat()
}
```

---

# Restore experience

This is arguably more important than backup.

The restore page should look like:

```text
Restore: Planix

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

planix

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

The backup repository itself should be encrypted.

I would use:

```text
Envelope encryption

Master Key
    ↓
Data Encryption Key
    ↓
AES-256-GCM encrypted backup
```

Eventually allow:

```text
Local key
HashiCorp Vault
AWS KMS
Azure Key Vault
YubiKey-backed key
```

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
Docker Backup
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

Planix            22 min ago       ✓ Protected
Xlynk             47 min ago       ✓ Protected
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
└── Snapshots

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

You don't need dozens of microservices.

Start with:

```text
dockerbackup-api
dockerbackup-worker
dockerbackup-agent
dockerbackup-web
postgres
```

Architecture:

```text
               ┌───────────────┐
               │     Web       │
               │    Next.js    │
               └───────┬───────┘
                       │
                       ▼
               ┌───────────────┐
               │      API      │
               │      Go       │
               └───────┬───────┘
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
     PostgreSQL      Worker       Storage
                       │
                       ▼
                    Agents
                       │
           ┌───────────┼───────────┐
           ▼           ▼           ▼
        Docker 1    Docker 2    Docker 3
```

---

# A backup manifest

Every backup should have a machine-readable manifest.

For example:

```json
{
  "version": "1.0",
  "application": "planix",
  "backup_id": "01K6...",
  "created_at": "2026-09-24T02:00:00Z",

  "source": {
    "hostname": "docker-prod-01",
    "docker_version": "28.4.0",
    "architecture": "amd64"
  },

  "compose": {
    "project": "planix",
    "source": "original",
    "file": "compose/compose.yaml"
  },

  "volumes": [
    {
      "name": "planix_pgdata",
      "archive": "data/volumes/planix_pgdata.tar.zst",
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
planix-dr-2026-09-24.tar.zst
```

containing everything necessary to reconstruct the application.

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

I would build it in this order:

1. **Docker discovery** — hosts, stacks, Compose projects, containers, volumes, bind mounts and networks.
2. **Backup engine** — Compose files + named volumes + bind mounts + manifest + Zstd compression.
3. **Restore engine** — restore to original or alternate Docker host.
4. **Web console** — application inventory, backup status, manual backup/restore.
5. **Scheduling and retention** — backup policies, pruning and notifications.
6. **Database-aware backups** — PostgreSQL first, then MySQL/MariaDB.
7. **Remote agents** — securely manage multiple Docker hosts.
8. **S3/storage providers** — local/NFS first, then S3-compatible.
9. **Encryption** — encrypted repositories and protected secrets.
10. **Verification** — integrity checks and automatic test restores.
11. **Deduplication/incrementals** — probably through Kopia/restic initially.
12. **Migration/DR orchestration** — host-to-host migration, path mapping and recovery plans.

The MVP target I'd use is very concrete:

> **A web application that detects Docker Compose stacks, allows an administrator to click Back Up, captures the Compose definition plus all persistent data, and can restore that application onto a clean Docker server with one workflow.**

If you nail that workflow, everything else—retention, cloud storage, deduplication, agents, database plugins, migration, DR testing—can grow around a very solid core.

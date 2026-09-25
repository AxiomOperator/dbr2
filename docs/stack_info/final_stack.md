# DBR² — Final Technology Stack

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
* Storage repositories
* Recovery contracts
* Users and roles
* Notifications
* Audit records
* System configuration

Chi will provide lightweight and idiomatic HTTP routing.

Huma will sit above Chi to provide schema-driven API development, request and response validation, and automatic OpenAPI generation.

pgx will be used as the native PostgreSQL driver.

sqlc will generate type-safe Go data-access code directly from SQL queries. DBR² will deliberately avoid making a traditional ORM the primary database abstraction so that complex reporting, retention, backup-history, and recovery queries remain explicit and controllable.

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
Acquire Backup Lock
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
Generate Recovery Manifest
        ↓
Replicate Backup
        ↓
Apply Retention
        ↓
Update Recovery Point
        ↓
Run Post-Backup Hooks
        ↓
Notify
```

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

---

# Data Layer

## PostgreSQL 18

PostgreSQL 18 will be the primary persistent database and system of record.

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
* Recovery points
* Restore operations
* Restore tests
* Recovery contracts
* Notifications
* Audit events

Backup payload data will not be stored directly in PostgreSQL.

PostgreSQL stores the metadata describing where protected data resides and how it can be recovered.

---

## Dragonfly

Dragonfly will provide high-performance ephemeral storage and caching where appropriate.

Primary uses may include:

* UI cache
* Short-lived application state
* Rate limiting
* Session acceleration
* Distributed locks
* Temporary job progress
* Event fanout
* Expiring tokens

Dragonfly will not be treated as a durable system of record.

The responsibility boundaries will remain:

```text
PostgreSQL
Durable platform state

Temporal
Durable workflow state

Dragonfly
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
* Repository access
* Health reporting
* Application control
* Restore validation

The agent will use the Docker/Moby Go SDK instead of relying primarily on shelling out to Docker CLI commands.

The agent should operate as a native system service, for example:

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

A repository abstraction should be maintained internally so that DBR² is not permanently coupled to Kopia.

For example:

```go
type Repository interface {
    Backup(...)
    Restore(...)
    Verify(...)
    Delete(...)
    List(...)
    Stats(...)
}
```

This allows additional repository implementations in the future.

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
planix-volume.tar.zst
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

## Initial storage targets

* Local filesystem
* NFS-mounted storage
* SMB-mounted storage
* S3-compatible object storage

## S3-Compatible Platforms

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
DBR² Repository
    ↓
Kopia
    ↓
S3-compatible storage
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
* Local break-glass account

Supported identity providers should eventually include:

* Microsoft Entra ID
* Keycloak
* Zitadel
* Authentik
* Okta
* Google
* Generic OIDC providers

DBR² should act as an OIDC client rather than becoming a full identity provider.

A local emergency account should remain available for circumstances where the external identity provider is unavailable.

The local account should be heavily protected and intended strictly for break-glass administration.

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

If authorization requirements become substantially more complex, DBR² can later integrate:

* OPA
* OpenFGA

This allows the first implementation to remain straightforward without closing off future relationship-based or policy-based authorization.

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
postgres
dragonfly
temporal
```

Remote Docker hosts will run:

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
Dragonfly
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
Kopia
Backup repository
Object storage
```

The data plane performs the backup and restore operations.

Backup payloads should not normally flow through the central DBR² API.

Preferred architecture:

```text
                        DBR² Control Plane
                               │
                               │ instructions
                               ▼
Docker Host ───────────── DBR² Agent
                               │
                               │ backup data
                               ▼
                         Repository
                               │
                               ▼
                        Storage Backend
```

This prevents the control plane from becoming a throughput bottleneck.

---

# CI/CD

DBR² can support either:

* GitHub Actions
* Azure DevOps

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
```

The release pipeline should generate versioned artifacts for:

```text
dbr2-server
dbr2-worker
dbr2-agent
dbr2 CLI
Web container
Deployment manifests
```

---

# Testing

DBR² requires substantially more than unit testing because the product directly manipulates live container workloads and persistent data.

## Go Tests

Used for:

* Domain logic
* Repository abstractions
* Manifest parsing
* Retention logic
* Policy evaluation
* Docker metadata translation
* Recovery planning

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
```

Backup software should be tested primarily on whether it can restore correctly, not merely whether it can create an archive.

---

# Source Repository Structure

A practical monorepo layout would be:

```text
dbr2/
│
├── cmd/
│   ├── server/
│   ├── worker/
│   ├── agent/
│   └── dbr2/
│
├── internal/
│   ├── api/
│   ├── auth/
│   ├── backup/
│   ├── compose/
│   ├── database/
│   ├── docker/
│   ├── encryption/
│   ├── manifest/
│   ├── policy/
│   ├── repository/
│   ├── restore/
│   ├── runtime/
│   ├── storage/
│   └── workloads/
│
├── workflows/
│   ├── backup/
│   ├── restore/
│   ├── verify/
│   └── retention/
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

Kopia
Backup repository mechanics

        ↓

Filesystem / NAS / S3
Physical storage
```

Supporting infrastructure:

```text
PostgreSQL
Durable DBR² platform state

Dragonfly
High-speed temporary state

OpenTelemetry
Observability

OIDC
Identity

gRPC + mTLS
Secure agent communication
```

The architectural principle behind the final stack is:

> **Go owns Docker and recovery orchestration. Temporal owns durable execution. PostgreSQL owns DBR² platform state. Kopia owns backup repository mechanics. Next.js owns the administrative experience.**

This keeps DBR² focused on its actual value: understanding containerized applications, protecting them correctly, proving they are recoverable, and rebuilding them reliably when required.

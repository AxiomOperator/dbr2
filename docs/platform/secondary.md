# DBR² — Extended Platform Features

> Aligned with `../stack_info/final_stack.md`, which wins on any conflict.

The biggest additions are around **recovery assurance, security, portability, and operational awareness**. Those are the areas that distinguish a production backup platform from a convenient archive tool.

The most important feature I would add is **full application protection state**. The platform should be able to answer, at a glance: “If this Docker host disappeared right now, can I actually recover this application?” That means tracking more than whether a backup job succeeded.

A protected application should have a state like:

```text
Inventory
─────────────────────────────────
Protection Status       HEALTHY

Compose definition      ✓ Protected
Environment/config      ✓ Protected
Named volumes           ✓ Protected
Bind mounts             ✓ Protected
Database                ✓ App-consistent
Networks                 ✓ Captured
Secrets                  ✓ Encrypted
Images                   ✓ Re-pullable
Last backup              37 minutes ago
Last verification        6 hours ago
Last test restore        2 days ago
Recovery point           37 minutes
```

That becomes the center of the product.

## Features I would add

**Point-in-time file recovery** should be built in early. Don't make every restore an entire application restore. Let the administrator browse:

```text
Inventory
└── Sep 24, 2026 02:00
    └── uploads
        └── customers
            └── contract.pdf
```

and restore/download one file or directory. Kopia, DBR²'s repository engine, already supports browsing or mounting snapshots and selective restore, so DBR² builds this feature on Kopia rather than implementing it. ([Kopia][1])

**Configuration diffing** would be extremely useful. For each backup, show:

```diff
Backup: Sep 23 → Sep 24

+ image: inventory-api:2.8.1
- image: inventory-api:2.8.0

+ API_TIMEOUT=60
- API_TIMEOUT=30

+ volume:
    inventory-documents:/documents
```

That makes the platform useful even outside disaster recovery. It effectively becomes configuration history for Docker applications.

I would also track **configuration drift**. If the running stack differs from the last protected Compose definition:

```text
⚠ Configuration Drift

Running application changed since last backup.

2 environment variables changed
1 container image changed
1 volume added

[View Changes] [Back Up Now]
```

That's excellent operational functionality.

Another major feature should be **dependency awareness**. Docker Compose stacks aren't always fully self-contained. An application might rely on:

```text
External PostgreSQL server
NFS mount
SMB share
External Docker network
DNS name
Reverse proxy
SMTP relay
LDAP/AD
S3 bucket
Private image registry
```

Your backup scanner should identify those and warn:

```text
Recovery Dependencies

✓ Docker images available
✓ Internal PostgreSQL protected
⚠ External NFS server: 10.0.0.55
⚠ SMTP server: smtp.example.org
✕ External Docker network "proxy" not managed by stack
```

That prevents the classic problem of having a technically successful backup that cannot actually recreate the application.

Docker itself distinguishes named volumes, external volumes, drivers, driver options and Compose metadata, so your discovery model needs to preserve those distinctions rather than treating every volume equally. ([Docker Documentation][2])

## Recovery plans

I'd go beyond individual application restores and introduce **Recovery Plans**.

For example:

```text
Recovery Plan: Production Site

Priority 1
├── PostgreSQL
├── Dragonfly
└── MinIO

Priority 2
├── Inventory API
├── Inventory Workers
└── Authentication

Priority 3
├── Inventory Web
└── Reporting

Priority 4
└── Auxiliary Services
```

Then:

```text
[ Execute Recovery Plan ]
```

The platform understands dependencies and restores applications in the correct order.

Eventually this could include:

```text
Wait for PostgreSQL health check
        ↓
Start API
        ↓
Wait for /health = 200
        ↓
Start workers
        ↓
Start frontend
        ↓
Run synthetic test
```

That starts moving the product toward **Docker DR orchestration**, which is much more differentiated than just backup.

## Bare-host recovery

I would also capture enough information about the Docker host itself to recreate it.

Not a full operating-system image necessarily, but:

```text
Docker version
Docker daemon.json
Docker networks
Docker plugins
Storage driver
Host architecture
Kernel
Installed volume drivers
GPU/runtime information
Docker data-root
Firewall-related ports
Registry configuration
```

Then your platform could generate:

> **Host Recovery Report**

or eventually:

```text
bootstrap-host.sh
```

which gets a fresh Linux server into a state suitable for restoring its applications.

## Immutable / ransomware-resistant backups

This should absolutely be part of the architecture.

Support:

```text
Immutable repositories
S3 Object Lock
WORM retention
Separate repository credentials (per-agent Kopia identities via dbr2-reposerver; ADR-0002)
Backup deletion protection
MFA for destructive actions
Delayed deletion
```

Kopia, for example, currently supports S3-compatible destinations and can take advantage of object locking where the underlying service supports it. ([Kopia][3])

DBR² implements **dual authorization for destructive repository operations** as an optional policy. It uses the same approval mechanism as production restores (`restore.production`; see final_stack Authorization):

```text
Delete Backup
Delete Repository
Reduce Retention
Disable Immutability
Rotate Encryption Key
```

These are much more sensitive than ordinary backup operations.

## Air-gap/export support

Your DR package idea should extend into a real **offline recovery mode**.

Imagine:

```text
Export Application
```

produces:

```text
inventory-recovery.tar.zst.age
```

which contains:

```text
Manifest
Compose
Environment
Secrets
Volume data
Database dumps
Optional Docker images
Checksums
Restore utility
Documentation
```

The bundle is Zstandard-compressed and optionally age-encrypted (`inventory-recovery.tar.zst.age`), following the final stack.

Then, on a machine with no access to the DBR² server, the bundle is restored with the DBR² CLI:

```bash
dbr2 recover inventory-recovery.tar.zst.age
```

That is extremely attractive for isolated environments.

## Backup hooks

Give users a proper hook system:

```text
Pre-backup
Pre-volume
Post-volume
Post-backup

Pre-restore
Post-volume-restore
Post-restore
Post-healthcheck
```

Example:

```yaml
hooks:
  pre_backup:
    - docker exec nextcloud php occ maintenance:mode --on

  post_backup:
    - docker exec nextcloud php occ maintenance:mode --off
```

And you could supply built-in application profiles for popular software.

For example:

```text
PostgreSQL
MariaDB
Nextcloud
GitLab
Gitea
Vaultwarden
Immich
Paperless-ngx
Home Assistant
Qdrant
MinIO
```

The platform recognizes the workload and knows recommended backup behavior.

## Application templates/profiles

This could become a major differentiator.

Upon discovering:

```text
postgres:18
```

show:

```text
PostgreSQL detected.

Recommended protection:
✓ pg_dump
✓ Volume snapshot
✓ Stop writes during volume snapshot
✓ Verify database dump
```

Likewise:

```text
Qdrant detected.

Recommended:
✓ Snapshot API
✓ Configuration backup
✓ Collection inventory
```

So instead of being merely Docker-aware, your product becomes **workload-aware**.

## Backup quality score — but factual

Internally, I would calculate protection completeness rather than a subjective rating.

For example:

```text
Protection Coverage

Compose                Protected
Volumes                4 / 4
Bind mounts            3 / 3
Database               Protected
External dependencies  2 unresolved
Restore tested         Yes
Repository immutable   Yes
```

The dashboard could flag missing components without pretending a job is safe simply because the archive operation returned exit code 0.

## Backup window and resource controls

Backup systems can wreck production performance if you're not careful.

Per-host controls:

```text
Maximum concurrent jobs: 2

CPU limit:
25%

Disk IO:
Low priority

Network:
200 Mbps maximum

Allowed backup window:
22:00–06:00
```

Also support:

```text
ionice
nice
bandwidth throttling
concurrency control
repository upload limits
```

and pause backups if the host is overloaded.

## Changed-data estimation

Before a backup runs:

```text
Inventory

Last backup:
92 GB

Estimated changes:
3.7 GB

Expected upload:
1.2 GB after deduplication

Repository available:
4.8 TB
```

DBR² gets this efficiency from Kopia, which provides content-defined chunking, incremental snapshots, deduplication, compression and encrypted repositories. ([Kopia][1])

## Restore sandbox

This is one of my favorite additions.

Allow:

```text
Restore As Test Instance
```

Instead of restoring:

```text
inventory
```

restore:

```text
restore-test-inventory-20260924
```

with:

```text
isolated Docker network
random host ports
cloned volumes
disabled external integrations
optional environment overrides
```

Then an administrator can actually log into the restored application before declaring the backup healthy.

After testing:

```text
[ Destroy Test Environment ]
```

This could feed your automated restore-verification system.

## Secret handling

I'd strengthen what we discussed earlier.

The application should detect likely secrets automatically:

```text
PASSWORD
SECRET
TOKEN
API_KEY
PRIVATE_KEY
DATABASE_URL
AWS_SECRET_ACCESS_KEY
```

and prevent casual display.

The UI might show:

```text
DATABASE_PASSWORD     ••••••••••••
JWT_SECRET            ••••••••••••
SMTP_PASSWORD         ••••••••••••
```

with:

```text
Reveal
Rotate
Exclude
Protect
```

Secrets are protected in the repository by Kopia repository encryption and in exports by age. DBR² does not design its own cryptography. Displaying secrets requires the `secrets.read` permission.

## RBAC

RBAC follows the final stack. Roles are native to DBR².

```text
Administrator
Backup Administrator
Restore Operator
Application Operator
Auditor
Read Only
```

Granular permissions:

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

Authentication uses OIDC/OAuth2. DBR² acts as an OIDC client for Entra ID, Keycloak, Zitadel, Authentik, Okta, Google and generic OIDC providers, and keeps a local master admin account (username and password) for lockout protection. Entra ID is the v1.0 identity provider. SAML is not part of the final stack. If authorization becomes more complex, OPA or OpenFGA can be integrated later.

## Audit logging

Every meaningful action:

```text
2026-09-24 21:31:12
jdoe
Started manual backup
Application: Inventory

2026-09-24 21:44:51
Backup Worker
Backup verified

2026-09-24 22:02:18
admin@example
Restored file
/uploads/report.pdf
```

And importantly:

```text
Who
What
When
Source IP
Host
Application
Before state
After state
Result
```

Make audit logs append-oriented and difficult to tamper with.

## Notifications

Support:

```text
Email
Webhook
Microsoft Teams
Slack
Discord
Generic HTTP webhook
```

Events:

```text
Backup failed
Backup missed
Backup completed with warnings
Restore completed
Restore failed
Repository nearly full
Agent offline
Protection drift
No successful backup in X hours
Verification failed
Retention policy changed
```

## Maintenance mode

Your backup infrastructure itself needs maintenance tooling:

```text
Repository check
Index rebuild
Garbage collection
Integrity verification
Orphan cleanup
Retention enforcement
Storage reconciliation
Encryption key rotation
```

Kopia implements repository verification and maintenance. DBR² exposes and schedules these operations as Temporal workflows (repository verification, retention processing) rather than reimplementing them. ([Kopia][1])

## Multi-repository protection

An application should be able to write the same protection set to multiple targets:

```text
Inventory

Primary:
Local NAS

Secondary:
S3 Object Storage

Archive:
Offsite S3 Object Lock
```

Policy:

```text
Local
Hourly × 48
Daily × 14

Offsite
Daily × 30
Monthly × 12

Immutable
14 days
```

This makes implementing a **3-2-1-style backup strategy** straightforward.

## Repository health

Dashboard:

```text
Repository: Production-NAS

Status                Healthy
Total                  20 TB
Used                   8.3 TB
Backup data            5.1 TB
Dedup savings          43%
Objects                 14,821,993
Last integrity check    Sep 24 03:00
Last maintenance        Sep 24 04:15
Corrupt objects         0
```

## API-first design

Everything the UI can do is accessible through the REST API. Huma generates OpenAPI from the Go handlers, and that OpenAPI document is the formal external contract:

```text
POST /api/v1/backups
GET  /api/v1/backups/{id}
POST /api/v1/restores
GET  /api/v1/applications
POST /api/v1/policies
```

And then:

```text
CLI
Terraform provider
Ansible module
CI/CD integration
Webhooks
```

can all come later without redesigning the product.

The `dbr2` CLI exists from the start and is built on the same OpenAPI contract:

```bash
dbr2 host list
dbr2 app list

dbr2 backup inventory

dbr2 restore inventory \
  --recovery-point latest \
  --host docker02

dbr2 verify inventory

dbr2 export inventory \
  --recovery-point latest
```

## One more important concept: application ownership

You could tag applications:

```text
Application: Inventory

Owner:
Infrastructure

Environment:
Production

Criticality:
Tier 1

RPO:
1 hour

RTO:
30 minutes

Retention:
Production-Standard
```

Then your dashboard can tell you:

```text
RPO VIOLATIONS

Inventory
Required: 1 hour
Latest recovery point: 3h 12m
```

That is much more operationally meaningful than simply saying:

```text
Last Backup: Failed
```

---

The platform I'd ultimately aim for would sit at the intersection of **Docker application inventory + backup + disaster recovery + migration + recovery validation**:

```text
                                 DBR²

                                  │
          ┌───────────────────────┼────────────────────────┐
          │                       │                        │
      DISCOVER                PROTECT                  RECOVER
          │                       │                        │
    Hosts / Stacks          Recovery points           Full restore
    Containers              Databases                 File restore
    Volumes                 Config                    DB restore
    Dependencies            Secrets                   Bare-host DR
    Config drift            Images                    Cross-host
          │                       │                        │
          └───────────────────────┼────────────────────────┘
                                  │
                              VALIDATE
                                  │
                         Integrity checking
                         Automated restore
                         Health checks
                         RPO/RTO monitoring
```

Kopia already solves the raw storage problem: incremental content-addressable snapshots, deduplication, encryption, compression, repository maintenance and multiple storage backends. ([Kopia][1]) **DBR²'s value is the Docker intelligence above that layer:** understanding what constitutes an application, how to protect it consistently, what it depends on, and how to reliably reconstruct it somewhere else.

I would actually make **“Can I recover this application?”** the fundamental design question for every feature we add.

[1]: https://kopia.io/docs/features/ "Features | Kopia"
[2]: https://docs.docker.com/reference/compose-file/volumes/ "Define and manage volumes in Docker Compose | Docker Docs"
[3]: https://kopia.io/docs/repositories/ "Repositories | Kopia"

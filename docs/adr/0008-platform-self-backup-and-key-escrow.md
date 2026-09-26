# ADR-0008: Platform self-backup and key escrow

- **Status:** Accepted (mandatory; MVP scope)
- **Date:** 2026-09-25

## Context

If the DBR² control plane is lost, its policies, identities and keys go with it. If a Repository password is lost, **every backup in that Repository is permanently unrecoverable**. Backup software that cannot survive its own loss becomes a recovery dependency.

## Decision

### 1. Self-backup (mandatory)

A built-in **Platform Protection** workflow, scheduled daily and also triggered after security-relevant configuration changes, backs up:

| Item | Method |
|---|---|
| DBR² PostgreSQL database (platform state, policies, contracts, audit, RBAC) | `pg_dump` |
| Temporal database | `pg_dump` (kept for forensics; see recovery note below) |
| DBR² configuration (server, worker, reposerver configs, OIDC client configuration including the client secret) | Files |
| DBR² CA certificate and private key; reposerver TLS keys | Files (sensitive) |
| Repository passwords, storage backend credentials, maintenance-identity credentials | Secrets (sensitive) |
| Master admin account (Argon2id hash only, never a plaintext password) | Database |

It writes to:

- a dedicated **System Repository**, which must not be the only copy, **and**
- an **age-encrypted Platform Recovery Bundle** (`dbr2-platform-<date>.tar.zst.age`), exported to at least one location outside the System Repository. In v1.0 (single NAS) this is a separate NFS export or directory that is not part of any Repository. In addition, **Veeam's VM-level backup of the DBR² server** is an independent recovery layer.

### 2. Key escrow (mandatory)

- Every sensitive secret in the list above is **sealed with age to the escrow recipients**. In v1.0 this is **two recipients**, held by two people, offline, in a safe. Escrow recipients are age public keys held by designated administrators; hardware-backed keys (for example `age-plugin-yubikey`) or offline paper keys are recommended.
- **Creating a Repository cannot be completed until escrow is confirmed.** The escrow package for the new repository password is generated, and an administrator must acknowledge that it has been stored.
- DBR² runs an **escrow health check** and alerts when:
  - no escrow recipient is configured
  - a secret has changed since it was last escrowed
  - escrow has not been re-confirmed within a configured period (default 90 days)
- Rotating a repository password or the CA triggers re-escrow.
- **Opening the safe is only needed to decrypt.** Escrow packages are encrypted to the recipients' *public* keys, so creating or refreshing them needs no access to the safe. The packages can be stored anywhere (on the NAS, printed, or with the Platform Recovery Bundle). Only the private age identities live in the safe.
- **Annual escrow drill:** once a year, an escrow holder decrypts a test package to prove the private identities still work. The drill is recorded in the audit log.

### 3. Platform recovery procedure (documented runbook)

1. Install a fresh DBR² at the same version (or newer).
2. `dbr2 admin restore-platform <bundle>`, decrypted with an escrow identity. This restores the PostgreSQL platform state, configuration, CA and secrets.
3. `dbr2 admin reindex` for every Repository (ADR-0003).
4. **Temporal:** start with a **fresh Temporal database** by default. Schedules are recreated from the policies in PostgreSQL. Workflows that were in flight at the time of the disaster are not resumed; they are reconciled and reported. The Temporal backup is used for forensics only.
5. Agents reconnect using their existing certificates, because the CA was restored.

If DBR² itself cannot be restored, the escrowed repository passwords plus a stock `kopia` CLI still recover application data (ADR-0003, ADR-0007).

### 4. Verification

A **platform recovery test** exercises this runbook against a disposable environment. It is part of the release checklist and is recommended quarterly in production.

## Consequences

- Administrators must manage escrow keys. The product makes this unavoidable instead of optional.
- The self-backup contains the most sensitive material in the system. It is protected by age encryption and by RBAC (only Administrators can access it), and every access is audited.

## Amendment (2026-09-25): implementation decisions (Phase 9)

1. **Bundle format.** The Platform Recovery Bundle (`dbr2-platform-<UTC timestamp>.tar.zst.age`, e.g. `dbr2-platform-20260925T021500Z.tar.zst.age`) is a `tar` archive, compressed with zstd and encrypted with age (binary, not armored) to **every** active escrow recipient. Entries:
   - `db/<table>.copy`: every table of the dbr2 database's `public` schema except `goose_db_version`, as `COPY … (FORMAT binary)` with an explicit column list, all read in **one REPEATABLE READ, read-only transaction** (a consistent snapshot). This replaces `pg_dump`: it needs no external binary in the server image, and the restore can bypass foreign keys and the append-only audit triggers inside one transaction.
   - `secrets/dbr2_secret_key`, `secrets/dbr2_internal_token`, `secrets/dbr2_entra_client_secret` (if set): the dbr2-server secrets, in Compose secret-file format. `DBR2_SECRET_KEY` seals the CA private key, discovered secrets, TOTP seeds and notification secrets, so it must travel with the database.
   - `reposerver/<repository-id>.tar`: each Repository's reposerver state from its management API `GET /v1/state-export` (repository password, TLS certificate and key, control password, Kopia repository config). An unreachable reposerver is recorded as missing; the run is then **Partial** (alert) rather than failed.
   - `manifest.json` (last entry): format version, platform and component versions, goose schema version, per-table row counts and columns, secret names (never values), Repository states, escrow recipients (public keys) and the SHA-256 of every other entry. A copy is stored in `platform_backups.manifest`.
   The bundle is produced and encrypted **inside dbr2-server**; plaintext never leaves the process. dbr2-worker only handles ciphertext.
2. **Temporal is excluded.** Recovery already starts with a fresh Temporal database (§3.4), so the Temporal database is not in the bundle; the manifest states this. Temporal's forensic history is covered by the VM-level (Veeam) backup of the DBR² server. This replaces the "Temporal database — `pg_dump`" row of §1.
3. **Where it runs.** dbr2-server exports (control-plane RPC `PlatformService.ExportPlatform`, server-streaming 1 MiB chunks); the worker's `PlatformProtectionWorkflow` (daily schedule `platform-protection`, and on demand via `POST /api/v1/platform/backups`) tees the stream into the **System Repository** (a pinned Kopia snapshot of `maint@dbr2:/platform`, tag `dbr2-kind=platform`) and into the **bundle directory** (`DBR2_PLATFORM_BUNDLE_DIR`, a separate NFS export or directory outside every Repository; newest 14 kept). At least one target must succeed; one missing target makes the run Partial. The System Repository is designated with `PUT /api/v1/repositories/{id}/system` (at most one). Triggering after security-relevant configuration changes is not implemented yet.
4. **Restore command.** The runbook's `dbr2 admin restore-platform` is implemented as **`dbr2-server admin restore-platform`**, because it needs direct database access on a fresh installation and the `dbr2` CLI only talks to a running API (`dbr2 admin restore-platform` prints this pointer). It decrypts with an escrow identity, verifies every digest, migrates the target to the bundle's schema version, restores all tables in one transaction with `session_replication_role = replica` (so it connects as the PostgreSQL superuser; schema changes run as the database owner), resets identity and serial sequences, applies the migrations of the (same or newer) build, and writes the secrets and reposerver state (or imports the state with `POST /v1/state-import`). Runbook: `docs/operations/platform-recovery.md`.
5. **Verification.** The platform recovery test (`internal/platform/recovery_integration_test.go`, integration suite) seeds a platform through the real services, exports against a fake reposerver, restores into a fresh database and checks row counts, the audit trigger, the CA key (decrypted with the restored secret key), escrow package decryption and tamper rejection.

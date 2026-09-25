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

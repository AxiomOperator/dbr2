# Platform recovery runbook

<!-- SPDX-License-Identifier: Apache-2.0 -->

This runbook rebuilds a lost DBR² control plane from a **Platform Recovery Bundle** (ADR-0008). It restores the platform database (policies, identities, RBAC, audit, the recovery-point index, the agent CA), the platform secrets, and every reposerver's state (Repository password, TLS key pair). Application data is never in the bundle: it stays in the Repositories on the NAS.

> The runbook names `dbr2 admin restore-platform`. The command is **`dbr2-server admin restore-platform`**, because it needs direct database access on a fresh installation and the `dbr2` CLI only talks to a running API.

## Recovery layers

1. **Platform Recovery Bundle** (this runbook). Written daily by the Platform Protection workflow to **two** places:
   - the **System Repository**, as a pinned Kopia snapshot `maint@dbr2:/platform` (tag `dbr2-kind=platform`);
   - the **bundle directory** (`DBR2_PLATFORM_BUNDLE_HOST_PATH`), a separate NFS export or directory that is not part of any Repository (newest 14 kept).
2. **Veeam's VM-level backup of the DBR² server** — an additional, independent layer. Restoring the VM is often the fastest route when the host itself is lost; it also carries the Temporal history, which the bundle deliberately excludes. Use this runbook when the VM backup is unavailable, too old, or suspect.
3. **Escrow packages plus stock `kopia`** — if DBR² itself cannot be restored, the escrowed Repository passwords still recover application data (see the instructions inside each escrow package, ADR-0003).

## What is (and is not) in the bundle

| In the bundle | Not in the bundle |
|---|---|
| Every table of the `dbr2` database (`COPY … FORMAT binary`, one consistent snapshot) | The Temporal database (recovery starts a fresh Temporal; schedules are recreated from PostgreSQL) |
| `DBR2_SECRET_KEY`, `DBR2_INTERNAL_TOKEN`, the Entra client secret (if set) | `.env`, `dbr2_database_url`, PostgreSQL passwords (the new installation has its own) |
| Each Repository's reposerver state (`GET /v1/state-export`) | Application data (it is in the Repositories) |
| `manifest.json`: versions, schema version, row counts, SHA-256 of every entry | Caddy TLS certificates (re-issued or re-copied) |

The bundle is `tar` → `zstd` → **age**, encrypted to every escrow recipient. Only an escrow identity from the safe can open it. A run is **Partial** when one of the two copies could not be written or a reposerver's state was missing; check `GET /api/v1/platform/backups` (or the alerts) and fix partial runs promptly.

## Prerequisites

- **An escrow identity** from the safe: an age identity file (`AGE-SECRET-KEY-1…`, from `age-keygen`) or an unencrypted SSH private key whose public key is a registered escrow recipient. A passphrase-protected SSH key must be decrypted to a temporary file first (`ssh-keygen -p -N "" -f copy-of-key`), and that copy shredded afterwards.
- **The newest good bundle.** Take it from the bundle directory (`dbr2-platform-<UTC timestamp>.tar.zst.age`). If the bundle directory is lost too, restore it from the System Repository with the stock `kopia` CLI and the System Repository's escrowed password: `kopia repository connect filesystem --path <repository mount>`, `kopia snapshot list --all --tags dbr2-kind:platform`, `kopia restore <snapshot-id> <dir>`.
- **The NAS** with the Repository share(s), mounted read-write on the new host exactly as before (`DBR2_REPO_HOST_PATH`).
- **A fresh DBR² installation at the same version or newer** than the bundle (`manifest.json` → `platform_version`). Follow the Compose install (`deployments/docker-compose/README.md`) steps 1–2 only: mount the NAS, run `./init-secrets.sh`. Do **not** start the whole stack yet, and do not create Repositories.
- The same `DBR2_HOSTNAME`, gateway address and Repository `management_url`s as before (agents pin the CA and connect to the gateway address in their configuration; the reposerver certificate fingerprint is restored with its state).

## Procedure

Run everything from `deployments/docker-compose/` on the new host. Put the bundle and the identity in a root-only directory, e.g. `/root/restore` (the container runs as uid 65532: `chown -R 65532 /root/restore` for the duration of the restore, and shred the identity afterwards).

### 1. Verify the bundle (optional drill step)

```bash
docker compose run --rm --no-deps -v /root/restore:/restore:Z dbr2-server \
  admin restore-platform --verify-only \
  --bundle /restore/dbr2-platform-20260925T021500Z.tar.zst.age --identity /restore/escrow-a.txt
```

This decrypts the bundle, checks every SHA-256 against the manifest and prints what it contains, including Repositories whose reposerver state is **missing** from this bundle.

### 2. Start PostgreSQL only

```bash
docker compose up -d postgres
```

### 3. Restore the platform

The restore sets `session_replication_role = replica` for its transaction (foreign keys and the append-only audit triggers are bypassed for the restore only), which needs the PostgreSQL **superuser**:

```bash
PGURL="postgres://postgres:$(cat secrets/postgres_password)@postgres:5432/dbr2?sslmode=disable"
docker compose run --rm --no-deps -v /root/restore:/restore:Z dbr2-server \
  admin restore-platform \
  --bundle /restore/dbr2-platform-20260925T021500Z.tar.zst.age \
  --identity /restore/escrow-a.txt \
  --database-url "$PGURL" \
  --secrets-dir /restore/secrets \
  --reposerver-state-dir /restore/reposerver
```

What it does:

1. Decrypts and verifies the bundle (a tampered or truncated bundle is rejected).
2. Migrates the empty `dbr2` database to the bundle's schema version (as the database owner, so ownership stays with the `dbr2` role). A database already at a **newer** schema version is refused: restore into a database nothing has started against.
3. Refuses a database that already holds agents or Repositories unless `--force`.
4. In **one transaction**: `TRUNCATE` every table, `COPY … FROM STDIN (FORMAT binary)` each table, check the row counts, reset identity and serial sequences to the restored maxima.
5. Applies the migrations of this (same or newer) build to the restored data.
6. Writes `dbr2_secret_key`, `dbr2_internal_token` and `dbr2_entra_client_secret` (0600) to `--secrets-dir`, and each `<repository-id>.tar` to `--reposerver-state-dir`.

### 4. Install the restored secrets

```bash
for f in dbr2_secret_key dbr2_internal_token dbr2_entra_client_secret; do
  [ -f /root/restore/secrets/$f ] && install -m 644 /root/restore/secrets/$f secrets/$f
done
```

(Compose secret files are 0644 inside the 0700 `secrets/` directory so the non-root services can read them, as `init-secrets.sh` creates them.) Keep `dbr2_database_url` and `dbr2_db_password` of the new installation.

### 5. Restore each reposerver's state

Start the reposerver(s) with the Repository mount; they now use the restored internal token and are uninitialized (their state volume is empty):

```bash
docker compose up -d dbr2-reposerver
```

Import the state archives. restore-platform posts each archive to its reposerver's `POST /v1/state-import` with the restored internal token (`--no-database` leaves the database from step 3 alone):

```bash
docker compose run --rm --no-deps -v /root/restore:/restore:Z dbr2-server \
  admin restore-platform --bundle /restore/<bundle> --identity /restore/escrow-a.txt \
  --no-database --import-reposerver
```

Alternatively, post `/root/restore/reposerver/<repository-id>.tar` (`Content-Type: application/x-tar`) to each reposerver's management API yourself. A Repository whose state was missing from the bundle must be reconnected with its **escrowed Repository password** instead.

### 6. Start the stack

```bash
docker compose up -d
```

Temporal starts with a fresh database. dbr2-worker recreates its schedules (orphan GC, platform protection, …) and the policy schedules are rebuilt from PostgreSQL. Workflows that were running at the time of the disaster are not resumed; recovery points they left `pending` are reconciled by the reindex below.

### 7. Reindex every Repository (ADR-0003)

```bash
dbr2 admin reindex --repository <name>    # for each Repository
```

The Repository is authoritative: recovery points written after the bundle was taken reappear, and index rows without a manifest are marked missing.

### 8. Verify

- Sign in as the master admin (its password hash is restored — use the old password) and check the audit log is intact.
- **Hosts** reconnect on their own with their existing certificates (the agent CA was restored); check they are online.
- Repositories show `ready` with a healthy reposerver and the same certificate fingerprint.
- Run a backup of one application and a platform backup (`POST /api/v1/platform/backups`); both must succeed.
- Shred the escrow identity copy and `/root/restore` (`shred -u`), and return the identity to the safe.

## Drills

- **Quarterly (recommended) and before every release:** the platform recovery test runs in CI (`go test -tags integration ./internal/platform/...`). It seeds a platform, exports a bundle, restores it into a fresh database and checks row counts, the audit trigger, the CA key, escrow decryption and tamper rejection.
- **Production drill:** restore the newest bundle into a disposable VM (a fresh install on an isolated network, without the production NAS mounted read-write) with steps 1–3 and 8, then discard the VM. Record the drill and the bundle's timestamp.
- **Annual escrow drill (ADR-0008):** step 1 (`--verify-only`) with each escrow holder's identity proves both identities still open the bundle.

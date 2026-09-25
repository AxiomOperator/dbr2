# DBR² — Docker Compose deployment

Single-site deployment (v1.0). The architecture is described in `docs/stack_info/final_stack.md` → Deployment.

| Service | Role |
|---|---|
| `proxy` | Caddy: TLS and routing (`/api/*` → dbr2-server, the rest → dbr2-web). The only published port. |
| `dbr2-web` | Next.js console |
| `dbr2-server` | Control plane API, Swagger UI at `/api/docs` |
| `dbr2-worker` | Temporal worker |
| `dbr2-reposerver` | Repository server (Phase 1: storage guard and watchdog) |
| `postgres` | PostgreSQL 18.6: `dbr2`, `temporal` and `temporal_visibility` databases with separate roles |
| `valkey` | Disposable cache (no persistence, never used for locks) |
| `temporal`, `temporal-schema`, `temporal-namespace` | Temporal 1.32.0, plus one-shot schema and namespace jobs |
| `temporal-ui` | Administrators only; bound to `127.0.0.1:8233` (use an SSH tunnel) |

## Install

1. **Mount the NAS on the Docker host.** Use NFSv4, `hard`, restricted to this host, and ideally with NAS snapshots enabled:

   ```bash
   mount -t nfs4 -o hard,timeo=600,retrans=2,noatime nas.example.lan:/export/dbr2 /mnt/dbr2-repo
   chown 65532:65532 /mnt/dbr2-repo   # the reposerver runs as uid 65532
   ```

2. **Generate the secrets and settings, then review `.env`** (hostname, TLS, Entra ID):

   ```bash
   ./init-secrets.sh
   ```

3. **Initialize the Repository once.** This writes the sentinel file and refuses to run unless the path is an active, empty nfs4 mount:

   ```bash
   docker compose run --rm dbr2-reposerver init
   ```

4. **Start the stack:**

   ```bash
   docker compose up -d
   ```

5. **Sign in** at `https://<DBR2_HOSTNAME>/` as `dbr2-admin`. The initial password is inside the server's state volume:

   ```bash
   docker compose cp dbr2-server:/var/lib/dbr2/master-admin-initial-password - | tar -xO
   ```

   Change the password, enable TOTP, and delete the file.

## Entra ID

1. Register an application.
2. Set the redirect URI to `https://<DBR2_HOSTNAME>/api/v1/auth/oidc/entra/callback`.
3. Emit **group object IDs** in the ID token, limited to groups assigned to the application, to avoid the groups overage.
4. Set `DBR2_ENTRA_TENANT_ID` and `DBR2_ENTRA_CLIENT_ID` in `.env`, and put the client secret in `secrets/dbr2_entra_client_secret`.
5. Map groups to roles in the API (`POST /api/v1/oidc/group-mappings`). Users with no mapped role are denied.

## Lockout recovery

Run this on the Docker host:

```bash
docker compose exec dbr2-server /usr/local/bin/app admin reset-master-password [--disable-totp]
```

## Development (mocked NFS)

The dev box is not connected to the NAS. From the repository root:

```bash
make dev-up    # builds from source; console at https://localhost:9443
make dev-down  # stops the stack and deletes its volumes
```

`compose.dev.yaml` mocks the Repository with `.dev/repo`, a bind-mounted local directory. The functional NFS test (`tests/nfs/run.sh`) exercises a real nfs4 mount in containers. No throughput testing is done.

## Backups of this deployment

Protect `secrets/`, `.env` and the `pgdata` volume. Platform self-backup and key escrow arrive in Phase 9 (ADR-0008). Until then, Veeam's VM backup is the recovery layer.

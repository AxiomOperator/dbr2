# DBR² — Docker Compose deployment

Single-site deployment (v1.0). The architecture is described in `docs/stack_info/final_stack.md` → Deployment.

| Service | Role |
|---|---|
| `proxy` | Caddy: TLS and routing (`/api/*` → dbr2-server, the rest → dbr2-web). The only published port. |
| `dbr2-web` | Next.js console |
| `dbr2-server` | Control plane API, Swagger UI at `/api/docs` |
| `dbr2-worker` | Temporal worker |
| `dbr2-reposerver` | Kopia repository server (published on `51515` for agents; certificate pinned by fingerprint), management API on `:8091` (internal only), storage guard and stall watchdog |
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

   **Platform bundle directory (ADR-0008).** The daily Platform Recovery Bundle is also written outside every Repository: use a **separate NFS export or directory that is not part of any Repository** (never a subdirectory of the Repository mount), owned by the worker's uid:

   ```bash
   mount -t nfs4 -o hard,timeo=600,retrans=2,noatime nas.example.lan:/export/dbr2-platform /mnt/dbr2-platform
   chown 65532:65532 /mnt/dbr2-platform && chmod 700 /mnt/dbr2-platform
   echo DBR2_PLATFORM_BUNDLE_HOST_PATH=/mnt/dbr2-platform >> .env   # default ./platform-bundles
   ```

2. **Generate the secrets and settings, then review `.env`** (hostname, TLS, Entra ID):

   ```bash
   ./init-secrets.sh
   ```

3. **Prepare the Repository storage once.** This writes the sentinel file and refuses to run unless the path is an active, empty nfs4 mount (the Kopia repository itself is created in step 6):

   ```bash
   docker compose run --rm dbr2-reposerver init
   ```

4. **Start the stack:**

   ```bash
   docker compose up -d
   ```

5. **Sign in** at `https://<DBR2_HOSTNAME>/` as `dbr2-admin`. The initial password is inside the server's state volume. Run this from `deployments/docker-compose/` (no `sudo` needed if you are in the `docker` group):

   ```bash
   docker compose cp dbr2-server:/var/lib/dbr2/master-admin-initial-password - | tar -xO
   ```

   Change the password, enable TOTP, and delete the file.

6. **Create the Repository (with key escrow, ADR-0008).** The console does this under **Repositories**; the API equivalent is:
   1. On an offline machine, each of the two escrow holders runs `age-keygen -o escrow-<name>.txt`, keeps the identity file offline (in the safe), and gives you the public key (`age1…`).
   2. Register both keys: `POST /api/v1/escrow/recipients {"name","public_key"}`.
   3. Create the Repository:

      ```json
      POST /api/v1/repositories
      {"name": "primary", "backend": "nfs", "default": true,
       "management_url": "http://dbr2-reposerver:8091",
       "server_url": "https://<DBR2_HOSTNAME>:51515",
       "internal_server_url": "https://dbr2-reposerver:51515"}
      ```

      The response contains the **escrow package** (an age file). Store it offline (it is encrypted, so the NAS or a printout is fine as well).
   4. An escrow holder decrypts it once (`age -d -i escrow-<name>.txt dbr2-escrow-repository-primary.age`) and enters its `confirmation_code`: `POST /api/v1/repositories/{id}/escrow/confirm`. Only then can the Repository be used.

   Open TCP 51515 from the agent hosts to this host, and configure NAS snapshots on the share (`docs/operations/nas-snapshots.md`).

7. **Designate the System Repository** (ADR-0008): `PUT /api/v1/repositories/{id}/system` (v1.0: the only Repository). The Platform Protection workflow runs daily (`DBR2_PLATFORM_BACKUP_CRON`, default `15 2 * * *`) and writes the age-encrypted bundle (`dbr2-platform-<UTC timestamp>.tar.zst.age`, encrypted to the escrow recipients) as a pinned snapshot into the System Repository **and** into the bundle directory (newest `DBR2_PLATFORM_BUNDLE_KEEP`, default 14, kept). Run it once now with `POST /api/v1/platform/backups` and check `GET /api/v1/platform/backups`: a `partial` run means one copy is missing (alert raised). Recovery: `docs/operations/platform-recovery.md`.

8. **Back up.** Once a host is approved and discovered, run **Back up now** on an application (or `dbr2 backup --app <name> --wait`). Per-application settings (consistency mode, hooks, maximum quiesce, optional or excluded components) and per-host limits (concurrent jobs, backup window) are in the console.

## Install or update with `dbr2-deploy.sh` (recommended)

`./dbr2-deploy.sh` wraps the steps above and never destroys data: no `down -v`, no volume removal, a backup before every update, images pulled before anything changes, and an automatic rollback of image versions when an update does not become healthy. The full runbook is `docs/operations/upgrade.md`.

```bash
./dbr2-deploy.sh install                                            # first installation (after mounting the NAS)
./dbr2-deploy.sh update --release-manifest release-manifest.json    # update to a release
./dbr2-deploy.sh rollback                                           # previous image versions
./dbr2-deploy.sh status
```

State and backups live next to the Compose files in `.deploy/` and `backups/` (both git-ignored; `backups/` contains secrets, 0700).

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
make dev-up        # builds from source; console at https://localhost:9443
make dev-password  # prints the master admin's initial password (username dbr2-admin)
make dev-down      # stops the stack and DELETES its volumes (a new password is generated next time)
```

`compose.dev.yaml` mocks the Repository with `.dev/repo`, a bind-mounted local directory, and the platform bundle directory with `.dev/platform-bundles`. The functional NFS test (`tests/nfs/run.sh`) exercises a real nfs4 mount in containers. No throughput testing is done.

## Backups of this deployment

DBR² backs itself up (ADR-0008): the Platform Protection workflow exports the platform database, `DBR2_SECRET_KEY`, the internal token, the Entra client secret and every reposerver's state (repository password, TLS key pair) as an age-encrypted Platform Recovery Bundle, stored in the System Repository and in the bundle directory. The Temporal database is not included (recovery starts a fresh Temporal). Veeam's VM-level backup of the Docker host is an additional, independent layer; keep it. Protect `.env` (not in the bundle) and keep the escrow identities offline. Recovery runbook: `docs/operations/platform-recovery.md`.

## Adding a Docker host (agents)

1. In the console, go to **Hosts → Add host** (or call `POST /api/v1/agents/registration-tokens`). Copy the **join command**; it contains a single-use token and the CA fingerprint.
2. On the Docker host (Rocky or Fedora), install the RPM (`deployments/packaging/README.md`) and run the join command:

   ```bash
   sudo dbr2-agent enroll --server <DBR2_HOSTNAME>:8443 --token dbr2reg_… --ca-sha256 <fingerprint>
   sudo systemctl enable --now dbr2-agent
   ```

3. **Approve** the host in the console. The agent is refused until you do.
4. The agent reports its inventory every 5 minutes. **Run discovery** triggers it immediately.

The Agent Gateway port (`DBR2_GATEWAY_PORT`, default 8443) must be reachable from the Docker hosts. It carries mTLS only, and the HTTP console stays behind Caddy.

**Development:** the gateway is published on `localhost:18443`. You can run a native agent on this machine with `./bin/dbr2-agent enroll --server localhost:18443 … --config .dev/agent/agent.yaml --state-dir .dev/agent/state`, then `./bin/dbr2-agent run --config .dev/agent/agent.yaml`.

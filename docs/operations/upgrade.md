# Installing and updating DBR² (Docker Compose)

<!-- SPDX-License-Identifier: Apache-2.0 -->

`deployments/docker-compose/dbr2-deploy.sh` installs, updates and rolls back the control plane **without destroying data**. Run it from the deployment directory on the DBR² host, as a user in the `docker` group (or root).

## What it will never do

- **It never deletes anything.**
  - No `docker compose down -v`.
  - No volume, network, image or container removal.
  - No `--remove-orphans`, no prune.
- **It refuses to update when the database volume `dbr2_pgdata` is missing.** A wrong directory or Compose project name would otherwise start an empty platform next to the real one.
- **It refuses to install over an existing installation.**
- **It backs up before every update.** The backup holds the `dbr2`, `temporal` and `temporal_visibility` databases (`pg_dump -Fc`, each checked with `pg_restore -l`), `.env`, `secrets/`, the Compose files and the image versions, all checksummed (`SHA256SUMS`).
- **It pulls every image before changing anything.** A failed pull restores `.env`, and nothing changes.
- **It waits for running backups and restores to finish** (up to 15 minutes; `--wait-idle`) before it restarts services, unless `--force`.
- **It rolls back automatically when an update doesn't become healthy.** It restores the previous image versions and leaves the data in place.
- **It restores the database only with a typed confirmation.** `--yes` does not cover it.
- **It runs alone:** a lock prevents two runs at once.
- **It keeps a record:** every run is logged in `.deploy/deploy.log` and `.deploy/history.tsv`.

## First installation

1. Mount the NAS (NFSv4, `hard`) and create the platform bundle directory, as in `deployments/docker-compose/README.md` → Install, step 1.
2. Run:

   ```bash
   cd deployments/docker-compose
   ./dbr2-deploy.sh install
   ```

   This generates secrets and `.env` (never overwriting existing ones), checks the mount, pulls the images, prepares the Repository storage (writes its sentinel), starts the stack, and verifies:
   - every service is healthy
   - database migrations are applied
   - the API answers through the proxy

3. Review `.env` (hostname, TLS, Entra ID), then continue with the README: sign in, create the escrow recipients and the Repository, and enroll the agents.

## Updating

```bash
./dbr2-deploy.sh check                                # pre-flight only
./dbr2-deploy.sh update --dry-run --release-manifest release-manifest.json
./dbr2-deploy.sh update --release-manifest release-manifest.json
```

Choose the versions with one of these options:

| Option | Meaning |
|---|---|
| `--release-manifest FILE\|https://…` | The `release-manifest.json` attached to a DBR² GitHub Release. It sets the server, worker, reposerver and web versions exactly. **Recommended.** |
| `--version 0.2.0` | The same tag for every DBR² image (`0.2`, `0.2.0` or a full `0.2.0.61`). |
| `--channel edge` | The latest build of `main`. For test systems only. |
| `--set DBR2_SERVER_VERSION=0.2.0.61` | One component (repeatable). |
| *(none)* | Pull the current tags again (picks up a rebuilt moving tag such as `0.2.0`). |

What happens during an update:

1. Pre-flight checks: Docker, Compose 2.20 or later, secrets, the Repository mount, free disk space, the data volumes.
2. The plan is shown (current version → target version) and needs confirmation (`--yes` for automation).
3. The images are pulled. **Nothing has changed yet.**
4. It waits until no backup or restore is running.
5. A backup is written to `backups/<time>-pre-update/`. The newest 5 are kept (`DBR2_DEPLOY_KEEP_BACKUPS`).
6. `docker compose up -d`, which recreates only the services whose image or configuration changed. `dbr2-server` applies database migrations at start.
7. Verification: all services are healthy, `migrate status` shows nothing pending, and `/api/v1/health/ready` returns 200 through the proxy.
8. If verification fails, the previous image versions are restored and started again, and the run is recorded as `rolled_back`.

### Update order: control plane first, then agents

- Update the control plane first.
- Then update agents on each host with the RPM: `dnf upgrade dbr2-agent`, which restarts the service.
- The Agent Gateway accepts agents of the same protocol MAJOR version and marks older agents **outdated** in the console (ADR-0015). A protocol MAJOR change is called out in the release notes.

### Updating while backups run

An update restarts `dbr2-server`, `dbr2-worker` and `dbr2-reposerver`. Running operations survive this:

- workflows resume in Temporal
- agent commands continue and report back
- quiesced applications are resumed by the agent's lease if necessary

The script still waits for a quiet moment, because a restarted reposerver interrupts uploads, which are then retried. Use `--force` only when you have to.

## Rolling back

```bash
./dbr2-deploy.sh rollback                  # previous image versions; the database stays as it is
```

This is enough in almost every case. Migrations are additive, so the previous version runs against the newer schema.

If the database itself must go back to its state before the update:

```bash
./dbr2-deploy.sh rollback --restore-db     # asks you to type RESTORE DATABASE
```

What `--restore-db` does:

1. Verifies the backup's checksums.
2. Stops the DBR² services (PostgreSQL keeps running).
3. Takes a **safety backup** of the current databases (`backups/<time>-pre-restore`).
4. Restores the three databases from the pre-update dumps, in one transaction each, keeping table ownership.
5. Starts the stack and verifies it.

Anything recorded after that backup is lost from the **index**: recovery points, restores, audit events and settings. The recovery points themselves are still in the Repositories. Run `dbr2 admin reindex --repository <name>` for each Repository afterwards (ADR-0003).

For automation, set `DBR2_DEPLOY_CONFIRM_RESTORE="RESTORE DATABASE"` instead of typing it.

## Starting, stopping and restarting

```bash
./dbr2-deploy.sh stop                      # the whole stack
./dbr2-deploy.sh start
./dbr2-deploy.sh restart                   # in place
./dbr2-deploy.sh restart dbr2-worker       # one or more services
./dbr2-deploy.sh stop dbr2-web             # e.g. take only the console down
```

| Command | What it does | What it never does |
|---|---|---|
| `stop [SERVICE…]` | `docker compose stop`. Waits for running backups and restores first when a core service is involved (server, worker, reposerver, Temporal, PostgreSQL); `--force` skips the wait. | Remove containers, networks or volumes (`down`) |
| `start [SERVICE…]` | `up -d --no-build --no-recreate`, then verifies health (and migrations plus proxy readiness for the whole stack). Also recreates containers that a manual `docker compose down` removed, from the existing volumes. | Upgrade or recreate existing containers (that is `update`); start without the `dbr2_pgdata` volume |
| `restart [SERVICE…]` | `docker compose restart` in place, then verifies. Waits for operations like `stop`. Fails fast (pointing to `start`) when the containers no longer exist. | Apply new images or settings (use `update`); rerun the one-shot Temporal schema and namespace jobs |

- While the stack is stopped, scheduled backups don't run.
- Agents keep running and reconnect when the gateway returns.
- An application an agent quiesced is resumed by the agent's lease.
- Service names are validated against the Compose file. The one-shot jobs (`temporal-schema`, `temporal-namespace`) are started by the stack itself.

## Other commands

```bash
./dbr2-deploy.sh status    # running services and images, configured versions, data volumes, history, backups
./dbr2-deploy.sh backup    # the same backup as before an update, on demand
```

These backups protect against a failed **update**. They do not replace the daily age-encrypted Platform Recovery Bundle (ADR-0008, `docs/operations/platform-recovery.md`), which also covers loss of the whole host.

## Development stack

- `make dev-up` builds from source and starts the stack.
- `make dev-update` runs `dbr2-deploy.sh update --dev`: build, backup, apply, verify.
- `make dev-down` **stops** the stack and keeps all data.
- Deleting dev data is a separate, explicit step: `make dev-reset CONFIRM=delete-dev-data`. It removes the volumes and the mock Repository storage together, so the two can't get out of step.

# DBR² — Edge Cases and Backlog

> Aligned with `../stack_info/final_stack.md`, which wins on any conflict. Items marked *(stack)* are already covered by a final-stack technology choice.

These features address the edge cases administrators usually discover only when they actually need a restore.

* **Docker Swarm support** — stacks, configs, secrets, overlay networks, placement constraints, service replicas, update policies, node labels, and swarm-specific restore logic.
* **Podman compatibility** — not in v1.0. The final stack's `ContainerRuntime` abstraction ships with `DockerRuntime` first, leaving room for a future `PodmanRuntime` *(stack)*.
* **Compose normalization** — support `compose.yaml`, `docker-compose.yml`, multiple `-f` files, profiles, `extends`, anchors, interpolation, and environment-specific overrides.
* **Environment-variable provenance** — distinguish values coming from `.env`, shell environment, Compose `environment:`, `env_file`, secrets, and runtime overrides.
* **Docker Configs/Secrets** — back up metadata and optionally protected content where technically available, rather than only environment variables.
* **External volume-driver awareness** — NFS, CIFS, Ceph, Longhorn-like drivers, cloud volume plugins, etc. Don't blindly attempt to archive data owned by an external storage system.
* **Filesystem-type awareness** — XFS, ext4, ZFS, Btrfs. If the host supports native snapshots, use those for consistent and potentially much faster backups.
* **Snapshot-provider plugins** — ZFS snapshots, LVM snapshots, Btrfs snapshots, SAN/NAS snapshots, cloud block-volume snapshots. An optional accelerator only: many hosts will not have snapshots, and every mode must work without them (ADR-0005).
* **Guest-freeze/application-freeze hooks** — extensible quiescing rather than only `docker stop`. The quiesce safety guarantees (saga plus dead-man switch) are in ADR-0005.
* **Dependency graph visualization** — containers, networks, volumes, databases, reverse proxies, shared services and external dependencies displayed as an actual topology, rendered with React Flow *(stack)*.
* **Shared-resource detection** — alert when two unrelated Compose projects use the same volume, network, bind path or external database.
* **Orphan detection** — volumes, networks, containers and images that no longer belong to an active application.
* **Unprotected-data detection** — perhaps one of the most valuable features: “this container writes to `/data`, but `/data` is not backed by a volume or bind mount.”
* **Ephemeral-data classification** — recognize cache, temp and transient volumes so users don't waste storage backing them up.
* **Exclusion rules** — glob/path rules such as `/tmp`, cache directories, generated thumbnails or enormous expendable datasets.
* **Backup preview/dry run** — show exactly what would be captured before starting.
* **Backup size estimation** — by application, volume and repository.
* **Restore impact preview** — before restoring, clearly show which containers will stop, volumes overwritten, ports changed and paths replaced.
* **Restore collision detection** — catch existing container names, networks, volumes and bound ports before executing.
* **Automatic port remapping** — particularly valuable for test restores and migrations.
* **Architecture compatibility checks** — detect `amd64` → `arm64`, GPU runtime differences, kernel dependencies, unsupported CPU features, etc.
* **Docker-version compatibility** — warn if restoring to an older engine or incompatible Compose implementation.
* **Image digest enforcement** — restore the exact digest rather than merely `postgres:18`.
* **Registry availability testing** — verify that referenced images can still be pulled before claiming a backup is recoverable.
* **Private registry credentials** — encrypted registry authentication with per-host/per-application scoping.
* **Image escrow** — automatically archive images if the registry disappears or an image is locally built.
* **Build-context protection** — optionally capture `Dockerfile`, build context metadata and Git commit SHA for internally built applications.
* **Source-repository linkage** — associate an application with its GitHub/GitLab/Azure DevOps repository and record the deployed commit.
* **Git-aware Compose backups** — if Compose is managed through Git, save repository URL, branch, commit and dirty-state instead of treating the Compose file as an isolated artifact.
* **Deployment provenance** — “this running application came from commit `abc123`, deployed by pipeline 827.”
* **Restore-to-new-name** — clone `prod-app` into `prod-app-recovered` without manually rewriting Compose.
* **Restore mapping wizard** — map old paths, IPs, ports, hostnames, networks and storage locations to new equivalents.
* **Variable transformation rules** — useful during DR: automatically change `DB_HOST`, domain names, static IPs or storage paths.
* **DNS integration hooks** — after migration, optionally trigger external automation to update DNS.
* **Reverse-proxy integration** — Traefik, Nginx Proxy Manager, HAProxy, Caddy, etc., so restored applications can regain ingress configuration.
* **Certificate awareness** — identify mounted TLS certificates and ensure they are either protected or explicitly external.
* **Certificate-expiry warnings** — useful because a restored application with an expired cert is technically restored but operationally broken.
* **Backup dependency sequencing** — take PostgreSQL before application volumes, for example, rather than treating all resources independently.
* **Consistency groups** — several Compose projects can form one recovery unit.
* **Cross-application recovery points** — guarantee that several related services are recoverable from approximately the same timestamp.
* **Continuous log capture around failures** — retain agent/Docker logs from immediately before and after a failed backup (structured `slog` JSON with OpenTelemetry trace correlation).
* **Automatic retry policy** — exponential backoff, maintenance-window limits and maximum retry counts, implemented as Temporal retry policies and timeouts *(stack)*.
* **Missed-schedule handling** — after a host returns from being offline, optionally execute the missed backup, using Temporal scheduled workflows.
* **Agent store-and-forward** — let remote agents spool metadata or backup data temporarily if the central server is unreachable.
* **Resumable transfers** — extremely important for large backups and unreliable WAN links. Kopia's content-addressed uploads skip chunks already stored, and Temporal resumes interrupted workflows *(stack)*.
* **Multipart/object-store uploads** — especially for multi-hundred-GB backups; handled by Kopia's S3-compatible storage backend *(stack)*.
* **WAN optimization** — compression, dedupe and transfer throttling before data leaves the Docker host. Compression and dedup come from Kopia running on the agent *(stack)*; throttling is still needed.
* **Repository locality rules** — ensure a workload always has at least one copy outside the source host/site.
* **Replication policies** — local → remote NAS → object storage, rather than requiring every host to upload separately (the "Replicate Backup" workflow stage and replication workflows in Temporal).
* **Backup copy jobs** — copy existing recovery points between repositories without re-reading production data.
* **Legal hold** — mark particular backups so retention cleanup cannot remove them.
* **Retention simulation** — “If I apply this policy, 783 recovery points and approximately 2.1 TB will be removed.”
* **Deletion grace period** — deleted backups enter a recoverable state for X days.
* **Cryptographic signing** — sign manifests in addition to hashing backup contents.
* **Chain-of-custody records** — particularly useful in government/regulated environments.
* **Key escrow/recovery** — an encrypted backup nobody can decrypt after losing one key is not much of a backup. *Mandatory: ADR-0008.*
* **Key rotation without full re-backup** — rely on the repository engine's (Kopia's) key handling rather than a custom envelope-encryption scheme, because the platform avoids designing its own cryptography.
* **Repository key separation** — an agent that can write backup data should not automatically possess credentials that can destroy every historical backup. *Decided: ADR-0002 (Kopia Repository Server, per-agent append-only identities).*
* **Per-host credentials** — compromise of one Docker host should not expose all other hosts' backups. *Decided: ADR-0002.*
* **Agent certificate lifecycle** — registration tokens, mTLS certificates, renewal and revocation; each agent has its own certificate identity *(stack)*.
* **Host approval workflow** — newly installed agents remain pending until explicitly trusted.
* **Agent pinning** — certificate and host identity changes should be visible.
* **Tamper alerts** — agent disabled, backup schedule changed, exclusions modified, repository credentials changed, retention reduced.
* **Maintenance suppression** — temporarily mute expected backup failures during known infrastructure work.
* **Readiness reports** — printable/exportable DR reports by system or business service.
* **Recovery runbooks** — attach operational steps that cannot be automated.
* **Evidence capture** — after a test restore, store health-check output, screenshots/API responses, restore duration and validation results.
* **Recovery history** — not just backup history. Track every attempted restore, including failed ones.
* **Actual RTO measurement** — calculate observed restoration duration instead of merely storing a desired RTO value.
* **RPO compliance history** — report how often an application violated its protection objective.
* **Capacity forecasting** — estimate repository growth based on change rate and retention.
* **Failure forecasting** — e.g. “at current growth, repository reaches 90% in 37 days.”
* **Backup heatmap** — quickly identify hosts/applications with recurring failures or unusually long durations.
* **Baseline anomaly detection** — an application normally changes 300 MB/day and suddenly backs up 240 GB: flag it.
* **Possible ransomware indicators** — massive file churn, extensions changing, unexpectedly high entropy/change rates. This should be advisory, not treated as malware detection.
* **Data-loss estimator** — based on last verified recovery point rather than merely last attempted backup.
* **Disaster mode** — a simplified interface that suppresses normal administration and focuses entirely on recovery operations.
* **Emergency offline documentation** — generate human-readable restore instructions that can be printed or stored independently.
* **Bootstrap ISO/USB concept** — long-term, you could offer a minimal recovery environment that connects to the repository and restores Docker applications onto a fresh host.
* **Self-backup** — the platform must back up its own PostgreSQL database, Temporal state, configuration, keys (including Kopia repository credentials and the agent CA) and policy definitions.
* **Self-recovery export** — periodically generate enough configuration to rebuild the backup server itself.
* **Clustered control plane** — later, support multiple management servers or at least active/passive deployment so the backup controller is not a single point of failure.
* **Agent-only restore** — allow an authorized administrator to recover directly from an agent/repository even if the central web console is unavailable. *Enabled by ADR-0003 (the Repository is authoritative).*
* **Webhook/API event bus** — external systems should be able to react to `backup.completed`, `restore.failed`, `host.offline`, etc.
* **OpenTelemetry export** — logs, metrics and traces exported over OTLP, so external observability works alongside DBR²'s own dashboards; Prometheus via an OpenTelemetry Collector if needed *(stack)*.
* **Syslog support** — important for enterprise/SIEM environments.
* **SIEM-friendly audit format** — normalized JSON audit events with stable event IDs; consistent with the structured `slog` logging in the final stack *(stack)*.
* **Multi-tenancy** — even if you don't need it initially, model organizations/projects/tenants early enough that adding MSP functionality later doesn't require redesigning authorization.
* **Delegated administration** — application owners can restore their own app without having global Docker-host control (the Application Operator role).
* **Approval workflows** — production restore (`restore.production`) can optionally require approval from a second user who holds `restore.production` and is not the requester, with a mandatory reason and an audited emergency override. The same mechanism provides dual authorization for destructive operations. See final_stack → Authorization → *Production restores and approval workflows*.
* **Change tickets/reason fields** — “Restore requested under INC-48391.”
* **Maintenance-window-aware restores** — production restore restricted to authorized windows unless emergency override is used.

The one feature from that entire list that I would elevate to **core product functionality** is **unprotected-data detection**.

Your scanner should be able to inspect a container and say:

```text
Application: Finance-App

Persistent Writes Detected

✓ /var/lib/postgresql/data
  Named volume
  Protected

✓ /app/uploads
  Bind mount
  Protected

⚠ /app/reports
  Container filesystem
  NOT PERSISTENT

⚠ /var/lib/custom-data
  Container filesystem
  NOT PERSISTENT
```

Then:

```text
Recovery Risk

2 writable locations will be lost if this container is recreated.
```

That solves a problem most administrators don't even know they have until a container is destroyed.

Every application also has a **Recovery Contract**, a first-class entity stored in PostgreSQL and managed by the control plane per the final stack:

```yaml
application: inventory

protection:
  maximum_rpo: 1h
  target_rto: 30m

requirements:
  compose: required
  volumes: required
  database_dump: required
  secrets: required
  image_available: required
  restore_test_max_age: 7d
  offsite_copy: required
  immutable_copy: required
```

Then the system doesn't merely say:

```text
Backup succeeded.
```

It can say:

```text
Inventory Protection
────────────────────────────────

Backup                         PASS
Application consistency        PASS
Offsite copy                   PASS
Immutable copy                 PASS
Image recoverability           PASS
Restore verification           PASS

Current recovery point         41 min
Maximum allowed RPO            1 hr

Last restore test              3 days ago
Maximum allowed age            7 days

RECOVERY CONTRACT SATISFIED
```

And alternatively:

```text
RECOVERY CONTRACT VIOLATED

Reason:
Latest verified recovery point is 2h 17m old.

Required:
≤ 1 hour
```

That shifts the whole product from **“Did the backup job run?”** to **“Is this application currently recoverable to the standard we require?”**

That is the direction of the platform. It makes DBR² meaningfully different from a Docker GUI wrapped around `tar` or Kopia.

# DBR² — Roadmap, Checklists & Change Log

> **Mandatory update rule.** Update this document after **every** feature addition, enhancement, update, bug fix, deployment, design decision or documentation change:
>
> 1. Tick or adjust the affected checklist items (`[x]` done; add `(IN PROGRESS)` or `(BLOCKED: reason)` after an item's text as needed).
> 2. Add a dated entry to the **Change Log** at the bottom **with notes**: what changed, why, and which files, ADRs, PRs or deployments are involved.
> 3. If the change affects a decision, update the ADR and `stack_info/final_stack.md` first.
> 4. If the change touches a component, add an entry to that component's `CHANGELOG.md` under `[Unreleased]`, and bump its `VERSION` when releasing (`MAJOR.MINOR.BUGFIX.BUILD`, ADR-0015).
>
> A change is not "done" until this file reflects it.

Related documents: `stack_info/final_stack.md` (source of truth, including **Context & Constraints**), `adr/`, `domain_model.md`, `threat_model.md`, `platform/`.

**Release tiers:** **v1.0** = the MVP (first release; Phases 0–9) · **v2** = post-MVP · **Later** = unscheduled.

---

## Status overview

| Phase | Name | Tier | Status |
|---|---|---|---|
| 0 | Decisions & spikes | v1.0 | **Complete** (2026-09-25) |
| 1 | Foundations | v1.0 | **Complete** (2026-09-25) |
| 2 | Agent, enrollment & gateway | v1.0 | Not started |
| 3 | Discovery | v1.0 | Not started |
| 4 | Repositories & backup | v1.0 | Not started |
| 5 | Restore | v1.0 | Not started |
| 6 | Web console | v1.0 | Not started |
| 7 | Scheduling, retention & notifications | v1.0 | Not started |
| 8 | Database-aware backups (PostgreSQL, Redis) | v1.0 | Not started |
| 9 | Verification & platform self-protection | v1.0 | Not started |
| — | **v1.0 release gate** | v1.0 | Not started |
| v2 | Post-MVP feature set | v2 | Not started |
| L | Later | Later | Not started |

---

## v1.0 (MVP) definition

> **A web console that discovers hand-deployed Docker Compose applications on Rocky and Fedora hosts through DBR² Agents, lets a single operator click Back Up, captures the Compose definition, all persistent data, and PostgreSQL and Redis data into a Kopia-backed Repository on an NFS NAS as an atomic, manifest-committed recovery point with minimal downtime, and restores that application onto a clean Docker host in one workflow. The platform can recover itself from its own backups and from escrowed keys held by two people.**

**v1.0 target environment** (see final_stack → Context & Constraints):

- 6 Docker hosts, AMD64
- Rocky Linux (primary) and Fedora, with SELinux enforcing
- a single site
- a single NAS over NFS
- Entra ID plus a local master admin
- one operator
- Veeam protecting the VMs

---

## Phase 0 — Decisions & spikes

Goal: remove the architectural unknowns before building.

- [x] Reconcile the platform docs with `final_stack.md`
- [x] Replace Dragonfly with Valkey; remove cache locks (ADR-0010, ADR-0011)
- [x] Write ADR-0001 through ADR-0012, the domain model and the threat model
- [x] Owner confirms ADR-0006 (native agent and volume access). Accepted 2026-09-25
- [x] Owner confirms ADR-0007 (Kopia as an embedded library). Accepted 2026-09-25
- [x] Record the owner's context and constraints; ADR-0014 (single operator, team-ready)
- [x] Owner confirms ADR-0013 (Apache-2.0 plus `NOTICE`). Copyright DBR2 Team; `LICENSE` and `NOTICE` added
- [x] Per-component versioning and changelogs decided (ADR-0015)
- [x] Swagger-style interactive API docs required (final_stack → Control Plane)
- [x] Spike: Kopia used as a library. **Confirmed** with public packages only; three pitfalls recorded (ADR-0007). `spikes/kopia-library/`
- [x] Spike: Kopia repository-server ACLs. **Partial**: no-delete and own-only listing verified. Gaps: object-ID reads across agents and a content-existence oracle (documented and accepted; one Repository per host as the option), and agent-triggered retention (closed by pins). The server runs in-process through Kopia's `cli` package (ADR-0002, ADR-0007)
- [x] Spike: repository-server data path. **Confirmed**: the client splits, hashes and deduplicates (≈0 bytes re-sent for unchanged data); the server compresses and encrypts; the 1 MiB splitter was chosen (ADR-0002)
- [x] Spike: Kopia on **mocked NFS**. **Confirmed**: local-directory mock plus nfs-ganesha with a privileged client; an outage stalls then recovers with a clean verify; the export restriction works. Found that a repository created on an unmounted path silently lands on local disk, so a mount guard, sentinel and stall watchdog were added (ADR-0002). No throughput testing. `spikes/kopia-fidelity-nfs/`
- [x] Spike: Swagger UI API docs. **Confirmed**, served from embedded, pinned Swagger UI assets because Huma's renderers load from a CDN. Docs protection, four-part `info.version`, spec export, oasdiff v1.32.1 and spec lint all verified. `spikes/huma-swagger/`
- [x] Spike: Kopia metadata fidelity and SELinux (Fedora, non-root). **Partial**: extended attributes, ACLs, SELinux labels and hardlinks are lost, and directory mtimes are restored wrong, so a filesystem metadata record was added (ADR-0006, ADR-0004). The Docker daemon here has no `selinux-enabled`, so `:z`/`:Z` do nothing. Root-level tests are deferred (below)
- [x] Spike: Temporal on PG18 in Compose, workflow-ID exclusivity, schedule-trigger pattern, saga. **Confirmed**. Found an SDK default that silently returns the running workflow instead of an error (fixed by a mandatory start helper); termination skips compensation, so the UI offers Cancel only and the dead-man switch is mandatory (ADR-0005, 0009, 0011). `spikes/temporal/`
- [x] Choose the CI/CD platform: **GitHub Actions**; source repository `github.com/AxiomOperator/dbr2`

**Deferred until the NAS is connected:** real-NFS throughput testing; seed and incremental timing for the ~500 GB volume against the 60-minute quiesce default (ADR-0005); reposerver throughput with several agents; confirming the splitter choice before creating the production Repository.

**Deferred until a real Entra ID tenant is configured:** end-to-end sign-in against Entra ID (Phase 1 verified the flow against a standards-compliant fake OIDC provider), including groups-overage behavior. **Deferred until an OTLP collector is available:** confirming trace and metric export.

**Deferred to a root-capable Rocky and Fedora test host** (procedures in `spikes/kopia-fidelity-nfs/RESULTS.md`): the agent reading data as `unconfined_service_t` under systemd; a Docker daemon with `selinux-enabled`; restoring `trusted.*` extended attributes and SELinux labels as root. Also still to observe: Kopia's scheduled full GC in the reposerver (more than 24 hours; inferred from source).

**Exit criteria:** every ADR is Accepted; the spike results are recorded in the ADRs and in this change log.

## Phase 1 — Foundations

- [x] Monorepo scaffold per the final stack codebase structure (`cmd/`, `internal/`, `workflows/`, `proto/`, `db/`, `web/`, `deployments/`, `tests/`)
- [x] `LICENSE` (Apache-2.0) and `NOTICE` crediting the original repository (ADR-0013)
- [x] SPDX headers (CI check, 147 files), generated `THIRD_PARTY_NOTICES` with a license allow-list, DCO sign-off check (ADR-0013)
- [x] Per-component `VERSION` (starting at `0.1.0`) and `CHANGELOG.md` for api, server, worker, agent, reposerver, cli, web, agent-protocol, manifest-schema, db-schema and deployment; root `CHANGELOG.md` (ADR-0015)
- [x] Version stamping: `-ldflags` for Go binaries, `version` on every binary, `GET /api/v1/version`, version in the web footer and About page; agent / agent-protocol version fields defined in the `Connect` handshake (`Hello`). Enforcing them at `Connect` moved to Phase 2 with the Agent Gateway (ADR-0015)
- [x] Docker Compose deployment: Caddy edge proxy, `dbr2-web`, `dbr2-server`, `dbr2-worker`, `dbr2-reposerver`, `postgres`, `valkey`, `temporal` (+ schema and namespace jobs, UI on loopback); secrets generator; verified end to end in a real browser
- [x] PostgreSQL schema and migrations (goose, embedded); sqlc store (`internal/store`)
- [x] Data model leaves room for future multi-tenancy (`organizations` + `org_id` on tenant-scoped tables)
- [x] Go control plane: Chi + Huma, OpenAPI generation, `/api/v1` (22 paths, 24 operations)
- [x] **Swagger UI API docs** from embedded, pinned assets at `/api/docs` (Huma's docs route disabled), plus `/api/openapi.json` and `/api/openapi.yaml`. Login required by default (`DBR2_API_DOCS_PUBLIC`; browsers redirected to login); `x-dbr2-permission` documented and enforced from one source; CSRF same-origin check; `dbr2-server openapi -o api/openapi.yaml` (deterministic); spec lint test. Huma's `$schema`/`Link` additions **disabled**
- [x] Temporal worker skeleton on the pinned images (server/admin-tools 1.32.0, UI 2.54.1; schema jobs; `dbr2` namespace); `StartApplicationOperation()` helper plus a lint rule; the schedule → trigger → child (ABANDON) pattern; Cancel-only (lint forbids `TerminateWorkflow`; no terminate API); `saga` compensation helper; `docs/dev/temporal-guidelines.md`; verified against a real Temporal 1.32.0 server (ADR-0005, 0009, 0011)
- [x] Authentication: **Entra ID** through OIDC (authorization code + PKCE, nonce, browser-bound state, lazy discovery), with group-to-role mapping synced at sign-in; users with no role denied. Verified against a fake OIDC provider (validation against a real Entra tenant is listed under Deferred)
- [x] **Master admin**: local username and password (Argon2id), per-IP rate limiting and progressive lockout, optional replay-safe TOTP, a notification-outbox entry on every login, bootstrap password in a root-only file, `dbr2-server admin reset-master-password` on the server host (a server subcommand, not the CLI, because it needs database access)
- [x] RBAC: roles and permissions per the final stack (including `restore.production`) plus `user.read` / `user.manage`; manual roles, enable/disable, group mappings; master admin immutable; self-lockout prevented (ADR-0014)
- [x] Audit log: append-only (database triggers reject UPDATE/DELETE/TRUNCATE), structured JSON log mirror, 19 stable event types (`docs/dev/audit-events.md`), before/after state
- [x] Observability: `slog` JSON logging with trace correlation, OpenTelemetry traces and metrics, OTLP export (`DBR2_OTEL_ENABLED`), secret redaction in logs and audit details
- [x] CI on **GitHub Actions**: Go and web builds, unit and integration tests, NFS functional test, govulncheck, npm audit, gitleaks, license checks, SBOM, cosign container signing; actions pinned by SHA; Dependabot
- [x] CI versioning gates: per-component changelog check (`no-changelog` label only for test, CI or docs PRs), OpenAPI breaking-change diff (oasdiff), `buf breaking` (pre-1.0: MINOR bump permits breaking changes), release workflow (`VERSION` + `run_number`, tags `<component>/vX.Y.Z.B`, `release-manifest.json`) (ADR-0015)
- [x] Dev storage profile: mocked NFS (`.dev/repo` bind-mounted at `/mnt/dbr2-repo`) and a containerized NFS server for functional tests (`tests/nfs/run.sh`: 12 checks on a real nfs4 mount, including stall detection)

## Phase 2 — Agent, enrollment & gateway

- [ ] `dbr2-agent` native static binary (AMD64) and systemd unit (ADR-0006); **RPM packages for Rocky and Fedora**
- [ ] DBR² CA; agent certificate issuance, renewal, suspension and revocation
- [ ] Enrollment: single-use, expiring registration tokens; **host approval workflow** (Pending → Active)
- [ ] Agent Gateway in `dbr2-server`: `AgentService.Connect` stream, session leases in PostgreSQL (ADR-0001); enforce agent / agent-protocol versions from `Hello` (reject a mismatched protocol MAJOR, flag outdated agents; moved from Phase 1)
- [ ] Command protocol: idempotent `command_id`, deadlines, local command journal, status report on reconnect
- [ ] Temporal activity dispatch through the gateway, with heartbeats
- [ ] Agent health reporting, and `agent.connection_state` / `agent.latency` metrics
- [ ] Integration test: agent disconnect mid-command, then reconnect and resume

## Phase 3 — Discovery

- [ ] `ContainerRuntime` interface; `DockerRuntime` (rootful) using the Docker/Moby Go SDK
- [ ] Discover hosts, containers, volumes, networks and bind mounts; paths resolved from Docker, never hard-coded
- [ ] Compose Project detection through `com.docker.compose.*` labels, for **hand-deployed** projects; collect the original Compose files, multiple `-f` files and `.env`
- [ ] Group resources into **Applications**; manual Applications for non-Compose containers
- [ ] Reconstructed Compose definition for applications without Compose files (flagged **Reconstructed**)
- [ ] Volume classification: Local, External (driver-backed or network-backed; not protected by default) or Ephemeral
- [ ] External dependency flags: external networks and external volumes
- [ ] **Unprotected-data detection**: writable container paths that no volume or bind mount backs (core feature)
- [ ] Image references with digest and platform recorded
- [ ] Secret detection in environment variables and `.env` files; masked in the UI and API; `secrets.read` required to reveal
- [ ] Application ownership metadata: owner, environment, criticality

## Phase 4 — Repositories & backup

- [ ] `BackupEngine` interface plus `internal/engine/kopia` (pinned Kopia **v0.23.1**), covering the spike pitfalls: deep restore depth, cancellation bridged to `Uploader.Cancel()`, no saving incomplete manifests, hyphenated tag keys (ADR-0007)
- [ ] `dbr2-reposerver` deployable: Kopia server in-process through the `cli` package; TLS from the DBR² CA; maintenance and GC owner; **mount guard, sentinel and stall watchdog**; `DYNAMIC-1M-BUZHASH` splitter for new repositories (ADR-0002)
- [ ] Per-agent Kopia users with the spike-verified ACL set (APPEND own snapshots, READ own policies); `maint@dbr2` for the worker; **every snapshot pinned**, Kopia retention disabled; per-agent storage monitoring; temporary per-source READ ACL for cross-host restore, revoked by compensation
- [ ] Storage backend: **NFS** (production; mounted only on the reposerver host, `hard` mount, export restricted) plus local filesystem (testing)
- [ ] NAS guidance documented: scheduled read-only NAS snapshots on the Repository share (the v1.0 immutability substitute)
- [ ] **Key escrow at Repository creation** to the two escrow recipients. Creation is blocked until escrow is confirmed (ADR-0008)
- [ ] Recovery manifest schema v1 (`schema_version`), with a JSON schema published in the codebase (ADR-0004)
- [ ] Backup workflow per the final stack: claim the application → hooks → dumps → quiesce → protect → resume → commit
- [ ] Components: config, volumes, bind mounts, plus a **`fsmeta` record** for each filesystem component; tagged (`dbr2-*` keys) and pinned snapshots; manifest written last, **by `maint@dbr2` only**, with component sources validated (ADR-0004)
- [ ] Required and optional component rules; recovery point status Complete or Partial; failure means no recovery point
- [ ] Orphan-component garbage collection after a grace period
- [ ] Consistency modes: Live, Quiesced (pre/post hooks), Offline; minimal-downtime defaults (ADR-0005)
- [ ] **Quiesce safety:** saga compensation, **60-minute** default maximum quiesce, agent dead-man switch, alerts (ADR-0005)
- [ ] **Seed pass** for large first-time volumes (~500 GB) so the consistent pass only uploads the delta (ADR-0005)
- [ ] SELinux context captured for volume and bind-mount roots
- [ ] Per-host concurrency limit (maximum concurrent jobs); configurable backup window, to avoid Veeam job windows
- [ ] Manual backup through the API and the `dbr2` CLI
- [ ] `dbr2 admin reindex`: rebuild the recovery-point index from a Repository (ADR-0003)

## Phase 5 — Restore

- [ ] Restore workflow: to the original host or an alternate host
- [ ] **Restore impact preview**: containers stopped, volumes overwritten, ports and paths changed
- [ ] **Restore collision detection**: names, networks, volumes and bound ports
- [ ] Bind-mount path remapping
- [ ] Create missing networks and volumes; pull images by digest (image digest enforcement)
- [ ] Database restore from the logical dump or RDB file (PostgreSQL, Redis)
- [ ] Restore into staging, then apply the `fsmeta` record (hardlinks, ACLs, extended attributes, full SELinux contexts, directory mtimes), verify, and swap in; `IgnorePermissionErrors = false`; sparse writing on (ADR-0006)
- [ ] Start the application; health-check validation
- [ ] `restore.production` enforcement plus **single-operator safeguards**: typed confirmation, mandatory reason (ADR-0014)
- [ ] Recovery history: every restore attempt recorded, including failures
- [ ] Restores claim the same `application/{id}` workflow ID, so they cannot overlap with backups

## Phase 6 — Web console

- [ ] Next.js, ShadCN, Tailwind, TanStack Query and Table; OpenAPI-generated client and Zod schemas
- [ ] Navigation: Dashboard, Docker (Hosts, Applications, Containers, Volumes), Protection (Policies, Jobs, Recovery Points), Recovery (Restore, Restore Testing), Storage (Repositories, Usage), System (Agents, Users, Notifications, Audit Log, Settings)
- [ ] Application inventory with **protection status** and **protection coverage** (components protected, unresolved dependencies)
- [ ] Manual Back Up and Restore flows with live progress over SSE
- [ ] Recovery point browser (the manifest view; Complete or Partial status)
- [ ] Agent approval UI
- [ ] Playwright end-to-end tests for the core flows (Phase 1 ran an ad-hoc headless Chromium check against the real stack)
- [ ] Content-Security-Policy with nonces for console pages (Next.js inline scripts); other security headers already set

## Phase 7 — Scheduling, retention & notifications

- [ ] Protection Policies: schedule (cron, hourly, daily, weekly, monthly), consistency mode, retention, target Repositories
- [ ] Temporal schedules; overlapping runs skipped and recorded (ADR-0011)
- [ ] Retention at recovery-point level: manifest deleted first, then components; runs under the maintenance identity
- [ ] **Deletion grace period** (default 7 days) for manual deletions of recovery points and Repositories (ADR-0014)
- [ ] **Recovery Contract** (v1.0 subset): maximum RPO, required components; Satisfied or Violated, with RPO-violation reporting
- [ ] Notifications: email and generic webhook. Events: backup failed, missed or warning; restore completed or failed; agent offline; RPO violated; auto-resume; escrow unhealthy; verification failed; master admin login

## Phase 8 — Database-aware backups (PostgreSQL, Redis)

- [ ] Database detection for PostgreSQL and Redis
- [ ] PostgreSQL: `pg_dump` streamed into a `database` component (Zstandard-compressed stream); online, no quiesce
- [ ] Redis: `BGSAVE`, wait for completion, then capture the RDB file (plus the AOF when enabled); online, no quiesce
- [ ] Strategy options: logical, volume, or both (default **both** for databases, with the volume copy allowed to be crash-consistent)
- [ ] Dump and RDB validation

## Phase 9 — Verification & platform self-protection

- [ ] Repository integrity verification workflow (`repository/{id}/verify`)
- [ ] Recovery point verification state (Unverified, Verified, Verification Failed)
- [ ] **Platform self-backup** to the System Repository, plus an age-encrypted Platform Recovery Bundle in a separate NFS export or directory (ADR-0008)
- [ ] Escrow health checks, re-escrow on secret rotation, annual escrow drill reminder
- [ ] `dbr2 admin restore-platform` plus the platform recovery runbook (fresh Temporal, reindex); document Veeam's VM backup as an additional layer
- [ ] Platform recovery test passing in CI

## v1.0 release gate

- [ ] Real-Docker integration suite green on **Rocky and Fedora** (SELinux enforcing), covering every scenario in the final stack's testing section
- [ ] Engine compatibility suite and manifest schema tests in place
- [ ] Platform recovery test green
- [ ] Threat model reviewed against the build; no open critical items
- [ ] Documentation: install, agent enrollment, backup and restore, platform recovery, escrow runbooks
- [ ] Signed release artifacts: `dbr2-server`, `dbr2-worker`, `dbr2-agent` (RPM), `dbr2-reposerver`, `dbr2`, web container, Compose manifests. Every component at `1.0.0.x` with its changelog finalized; platform `release-manifest.json`
- [ ] Real-NAS validation once it is connected: NFS throughput, and seed and incremental timing for the ~500 GB volume
- [ ] Open-source release hygiene: `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES`, `CONTRIBUTING`, `SECURITY.md`

---

## v2 — Post-MVP

**Platform & storage**
- [ ] Debian and Ubuntu support (DEB packages, AppArmor considerations)
- [ ] SMB storage backend; S3-compatible storage (MinIO, AWS S3, R2, Wasabi, B2)
- [ ] Immutability: S3 Object Lock / WORM retention
- [ ] Multi-repository protection; replication policies; backup copy jobs; repository locality rules (offsite copy)
- [ ] Legal hold; retention simulation
- [ ] Repository health dashboard; bandwidth throttling, `ionice`/`nice`
- [ ] Key rotation (repository and CA) with re-escrow
- [ ] Additional OIDC identity providers (Keycloak, Zitadel, Authentik, Okta, Google, generic)

**Recovery & restore**
- [ ] Restore sandbox (Restore As Test Instance): isolated network, automatic port remapping, disabled integrations
- [ ] Automatic scheduled restore tests, with evidence capture (health output, duration)
- [ ] Actual RTO measurement; RPO compliance history
- [ ] Point-in-time file and directory recovery (browse a recovery point)
- [ ] Restore-to-new-name; restore mapping wizard (paths, ports, hostnames, networks)
- [ ] Cross-host migration workflow
- [ ] Disaster-recovery package / offline export (`.tar.zst.age`) plus `dbr2 recover`
- [ ] Agent-only restore (control plane unavailable)
- [ ] Emergency offline documentation / readiness reports; recovery runbooks attached to Applications
- [ ] Data-loss estimator based on the last *verified* recovery point

**Protection depth**
- [ ] **LVM snapshot provider** first (common on Rocky and Fedora), then ZFS and Btrfs. An optional accelerator for minimal downtime (ADR-0005)
- [ ] Full hook system (pre and post volume, restore hooks, post-healthcheck)
- [ ] Rootless Docker support (ADR-0006)
- [ ] Opt-in backup of External (driver-backed) volumes through a helper container
- [ ] Ephemeral-data classification and exclusion rules
- [ ] Backup preview / dry run; backup size estimation
- [ ] Environment-variable provenance
- [ ] Image archive option (`docker save`), image escrow for locally built images, registry availability testing, private registry credentials
- [ ] Architecture and Docker-version compatibility checks
- [ ] Backup dependency sequencing
- [ ] Certificate awareness and expiry warnings
- [ ] Full Recovery Contract: target RTO, restore-test age, offsite and immutable copy requirements

**Security & governance (team operation)**
- [ ] **Production restore approval workflow** (second eligible user with `restore.production`; enabled only when at least 2 eligible users exist; audited master-admin override) (ADR-0014)
- [ ] Dual authorization for destructive operations (delete recovery point or Repository, reduce retention, disable immutability, rotate keys)
- [ ] Maintenance-window-aware restores
- [ ] Delegated administration (Application Operator scope)
- [ ] Tamper alerts; agent pinning (identity-change visibility)
- [ ] Maintenance suppression (muting expected failures)
- [ ] Syslog output

**Operations & insight**
- [ ] Configuration diffing between recovery points; configuration drift detection
- [ ] Dependency awareness (external DB, NFS, SMTP, DNS, identity provider, registry) and dependency graph visualization (React Flow)
- [ ] Shared-resource detection; orphan detection
- [ ] Missed-schedule handling
- [ ] Log capture around failures
- [ ] Webhook and API event bus; Teams, Slack and Discord notifications
- [ ] Built-in application profiles (PostgreSQL, Redis, Qdrant, MinIO, …)

## Later

- [ ] Multi-site: offsite replication targets, WAN optimization, agent store-and-forward, resumable-transfer tuning across a WAN
- [ ] ARM64 support
- [ ] Deployment tools: Portainer, Dockge and Komodo stack discovery; Git-aware Compose backups; build-context protection; source-repository linkage; deployment provenance
- [ ] Bare-host recovery report and `bootstrap-host.sh`; bootstrap ISO or USB recovery environment (Veeam covers VM-level recovery today)
- [ ] Recovery Plans (staged, health-gated multi-application recovery)
- [ ] Consistency Groups and cross-application recovery points
- [ ] Docker Swarm support (stacks, configs, secrets, overlay networks)
- [ ] `PodmanRuntime`
- [ ] Disaster mode UI
- [ ] Clustered or active-passive control plane; multiple Agent Gateway instances with lease forwarding
- [ ] Variable transformation rules; DNS integration hooks; reverse-proxy integration
- [ ] Cryptographic manifest signing; chain-of-custody records (no compliance requirement today)
- [ ] Changed-data estimation; capacity and failure forecasting; backup heatmap
- [ ] Baseline anomaly detection; possible ransomware indicators (advisory only)
- [ ] Multi-tenancy / MSP mode
- [ ] Additional application profiles (Nextcloud, GitLab, Gitea, Vaultwarden, Immich, Paperless-ngx, Home Assistant, …)
- [ ] MySQL/MariaDB, MongoDB, SQL Server, InfluxDB and Elasticsearch database plugins
- [ ] Additional storage backends (SFTP, Azure Blob); external KMS integrations
- [ ] Terraform provider; Ansible module
- [ ] Confined SELinux policy module for the agent
- [ ] Kubernetes deployment of the control plane

---

## Change Log

Newest first. Each entry lists the date, the type (Feature / Enhancement / Fix / Deployment / Decision / Docs), a summary and **notes**.

### 2026-09-25 — Feature / Deployment — Phase 1 (Foundations) complete
- **Notes:**
  - **Control plane:**
    - `dbr2-server` with Chi + Huma: 22 API paths.
    - OpenAPI 3.1 export, committed and deterministic.
    - Swagger UI from embedded, pinned assets, behind login by default.
    - problem+json errors with stable codes.
  - **Authentication:**
    - Master admin: Argon2id, per-IP rate limit, progressive lockout, replay-safe TOTP, bootstrap password in a 0600 file, offline reset through `dbr2-server admin reset-master-password`.
    - Entra ID OIDC: PKCE, nonce, browser-bound state, group → role sync; users with no role are denied.
    - Server-side sessions and personal API tokens.
  - **Access control and audit:**
    - RBAC with `user.read` / `user.manage` added.
    - CSRF same-origin check.
    - Audit log that is append-only, enforced by database triggers, with 19 stable event types.
    - Notification outbox entry for every master admin login.
  - **Temporal:**
    - `dbr2-worker`, and the `StartApplicationOperation` helper that fixes the SDK's silent-attach default.
    - Schedule → trigger → child pattern, `saga` compensation helper.
    - Lint rules: no `TerminateWorkflow`; `ExecuteWorkflow` only in `temporalx`.
    - Verified on a real Temporal 1.32.0 server.
  - **Storage and components:**
    - `dbr2-reposerver` storage guard: mount check, sentinel, stall watchdog. Verified on a real nfs4 mount (12/12 checks, including stall detection and recovery).
    - `dbr2-agent` / `dbr2` CLI version-level binaries.
    - Agent protocol v1 handshake (`buf` STANDARD lint).
  - **Web console** (Next.js 16): login with TOTP and Entra ID, dashboard, audit log, security settings, About page, version footer.
  - **Deployment:**
    - Docker Compose with a **Caddy edge proxy**. Decision: the Next.js proxy cannot sanitize a client-supplied `X-Forwarded-For`; Caddy discards it and the server trusts only Caddy, which prevents audit IP and rate-limit spoofing. Verified: a spoofed `6.6.6.6` was recorded as the real peer. Recorded as threat T21.
    - Distroless Go images, Compose secrets, PostgreSQL with separate roles.
    - Dev override with mocked NFS.
    - The full stack was verified in headless Chromium: login, dashboard, audit page and Swagger UI with 24 operations.
  - **CI (GitHub Actions):**
    - Go, web, integration and NFS functional jobs.
    - api-contract (oasdiff), proto (`buf breaking`), changelog, DCO, license/SPDX/`THIRD_PARTY_NOTICES`, security (govulncheck, npm audit, gitleaks), actionlint and CI-script tests.
    - Image build with SBOM and cosign; release workflow; Dependabot.
  - **Decisions:**
    - Huma `$schema`/`Link` additions disabled.
    - Pre-1.0 breaking changes are allowed with a MINOR bump (ADR-0015 amended).
    - sharp/libvips LGPL-3.0 exception approved for the optional Next.js image libraries. **Pending owner review**; remove sharp to drop it.
    - Wrong current password on a password change returns 400, so the console does not treat it as an expired session.
  - **Verification:** `make lint`, `make test` (race), `make test-integration`, `make generate` (no drift), web lint, typecheck, 55 tests and build, CI script tests (71), actionlint and `tests/nfs/run.sh` all green locally.
  - **Moved to Phase 2:** enforcing agent versions at `Connect`, because `Connect` is a Phase 2 deliverable.
  - **Deferred:** a real Entra ID tenant, OTLP collector validation, and a console CSP with nonces (Phase 6).
- **Files:** `cmd/**`, `internal/**`, `workflows/**`, `db/**`, `proto/**`, `api/**`, `web/**`, `deployments/**`, `tests/nfs/**`, `.github/**`, `scripts/**`, `Makefile`, `buf*.yaml`, `sqlc.yaml`, `go.mod`, `README.md`, `CHANGELOG.md`, `THIRD_PARTY_NOTICES`, all component `VERSION`/`CHANGELOG.md`, `docs/stack_info/final_stack.md`, `docs/adr/0015`, `docs/threat_model.md` (T21), `docs/dev/*`, `docs/roadmap.md`

### 2026-09-25 — Decision / Docs — Phase 0 complete: spike results folded into the ADRs
- **Notes:**
  - Ran four time-boxed spikes (code and `RESULTS.md` under `spikes/`; see `spikes/README.md`). Spikes are reference material, not versioned components.
  - **Kopia as a library** (`spikes/kopia-library/`): confirmed with public packages only.
    - Pitfalls: shallow restores by default, the uploader ignoring context cancellation, and colon tag keys breaking CLI filtering. Tag keys renamed to `dbr2-*`.
    - The Kopia server is in `internal/`, so `dbr2-reposerver` runs it in-process through Kopia's public `cli` package, and owns maintenance and GC (ADR-0007, ADR-0002).
  - **Repository-server ACLs:** no-delete and own-only listing verified with an explicit ACL set. Two gaps were found:
    1. Object-ID reads across agents plus a content-existence oracle. Documented honestly and accepted for a single-owner deployment; one Repository per host is the mitigation (T1).
    2. Agent-triggered Kopia retention. Closed by pinning every snapshot and disabling Kopia retention (T20).
  - **Recovery manifests** are now written only by `maint@dbr2`, and reindexing trusts only that source (ADR-0004).
  - **Data path:** the client deduplicates (≈0 bytes re-sent when unchanged); the server compresses and encrypts; TLS mandatory; the `DYNAMIC-1M-BUZHASH` splitter chosen for new repositories (to be revisited at real-NAS validation).
  - **Metadata fidelity** (`spikes/kopia-fidelity-nfs/`): Kopia loses extended attributes, ACLs, SELinux labels and hardlinks, and restores directory mtimes wrong. ACL loss can widen access (T19). Added a required `fsmeta` component per filesystem component, and restore into staging → apply metadata → verify → swap in (ADR-0006, ADR-0004). This Docker daemon lacks `selinux-enabled`, so `:z`/`:Z` are not relied on.
  - **Mocked NFS:** a local directory plus nfs-ganesha with a privileged client work; an outage stalls, then recovers with a clean verify. A repository created on an unmounted path silently lands on local disk, so a mount guard, sentinel and stall watchdog were added to `dbr2-reposerver` (ADR-0002). No throughput testing.
  - **Temporal** (`spikes/temporal/`): PG18 works with pinned server/admin-tools 1.32.0 and UI 2.54.1, schema jobs and separate owner roles (ADR-0009).
    - Workflow-ID exclusivity confirmed, but the Go SDK silently returns the already-running workflow unless `WorkflowExecutionErrorWhenAlreadyStarted` is set, so a mandatory start helper was added (ADR-0011).
    - The schedule → trigger → child (ABANDON) pattern was adopted.
    - Saga compensation was verified for failure, cancel and a worker crash. **Termination skips compensation**, so the UI offers Cancel only, quiescing workflows have no workflow timeouts, new operations check for a lease first, and the dead-man switch is mandatory (ADR-0005).
  - **Swagger UI** (`spikes/huma-swagger/`): confirmed. Huma's renderers load from a CDN, so embedded, pinned Swagger UI 5.31.1 is used. Documented and enforced permissions share one source; spec export, oasdiff v1.32.1 and the lint test are verified.
  - **Deferred:** real-NAS throughput and 500 GB seed timing; multi-agent reposerver throughput; root-level SELinux and `trusted.*` tests on Rocky and Fedora; observing Kopia's scheduled full GC.
  - **Host side effects on the dev box:** NFS client kernel modules were auto-loaded (unloading needs root); Docker build cache remains; `fedora-minimal:44` may be in the rootless Podman store. All `dbr2spike-*` containers, networks and volumes were removed.
- **Files:** `spikes/**` (new), `adr/0001`, `0002`, `0003`, `0004`, `0005`, `0006`, `0007`, `0009`, `0011`, `stack_info/final_stack.md`, `threat_model.md` (T1 revised; T19 and T20 added), `roadmap.md`

### 2026-09-25 — Decision / Docs — License accepted; GitHub Actions; NFS mocked; per-component versioning; Swagger docs
- **Notes:**
  - **ADR-0013 accepted:** Apache-2.0, copyright **DBR2 Team**, canonical repository `https://github.com/AxiomOperator/dbr2`. Added root `LICENSE` (the canonical Apache-2.0 text, copied from a system copy with the standard checksum) and `NOTICE`.
  - **CI/CD:** GitHub Actions chosen.
  - **NFS mocked for development:** the dev box is not connected to the NAS. Development uses a local directory at the production mount path. Functional NFS tests use a containerized NFS server (mount options, permissions, outage). **No throughput testing** until the NAS is connected; the ~500 GB seed timing is deferred.
  - **ADR-0015 (new):** every component (api, server, worker, agent, reposerver, cli, web, agent-protocol, manifest-schema, db-schema, deployment) gets a `VERSION` and a `CHANGELOG.md`.
    - Versions are `MAJOR.MINOR.BUGFIX.BUILD`, where BUILD is the GitHub Actions `run_number` and never resets.
    - Where tools can't use four parts: npm uses three parts; RPM uses `Version`/`Release`.
    - Runtime exposure: `--version`, `/api/v1/version`, and the web footer.
    - CI gates: changelog check, oasdiff and `buf breaking`.
  - **Swagger-style API docs required:** Swagger UI at `/api/docs`, with the spec at `/api/openapi.json` and `/api/openapi.yaml`, via Huma. Login required by default; CI fails on undocumented operations.
  - Update rule step 4 added: component changelog and version.
- **Files:** `LICENSE` (new), `NOTICE` (new), `adr/0013`, `adr/0015` (new), `adr/README.md`, `stack_info/final_stack.md`, `roadmap.md`

### 2026-09-25 — Decision / Docs — Owner context and constraints applied; ADR-0013 and ADR-0014 added
- **Notes:**
  - Recorded the owner's answers in a new final_stack section, **Context & Constraints**:
    - internal tool, published as open source with attribution
    - a single operator, but team-ready
    - 6 hosts; largest volume about 500 GB
    - AMD64; Rocky and Fedora (Debian nice to have)
    - hand-run Compose; a single site; a single NAS over NFS
    - Entra ID plus a username-and-password master admin
    - 2 offline escrow holders
    - PostgreSQL and Redis
    - minimal downtime preferred
    - Veeam already in use
  - **Tier rename:** the owner's "v1" means the first release, so the tiers are now **v1.0 = MVP**, **v2** = post-MVP (previously called "V1"), and **Later**.
  - **Default maximum quiesce raised from 15 to 60 minutes** (ADR-0005). Added a seed pass for large first-time volumes and minimal-downtime defaults (online database dumps, Redis `BGSAVE`).
  - **Storage for v1.0 is NFS only** (plus local filesystem for testing). SMB and S3 moved to v2. NAS snapshots and a restricted NFS export are the v1.0 immutability substitute.
  - **Master admin:** a required local username-and-password account (Argon2id, lockout, optional TOTP, a notification on every login, root-only reset). It replaces the "break-glass with mandatory MFA" wording, per the owner's requirement.
  - **ADR-0014:** operable by one person, ready for teams. Approvals and dual authorization are enabled only when at least 2 eligible users exist. Single-operator safeguards (typed confirmation, mandatory reason, 7-day deletion grace period) moved into v1.0.
  - **ADR-0013 (Proposed):** Apache-2.0 plus a `NOTICE` crediting the original repository, recommended as the best fit for "free to use and commercialize, credit the original repo".
  - **Escrow:** two recipients; escrow packages can be stored anywhere because only the private keys live in the safe; annual decrypt drill.
  - **Scope moves:**
    - database plugins for v1.0 are PostgreSQL and Redis; MySQL/MariaDB moved to Later
    - DEB and additional identity providers moved to v2
    - LVM became the first snapshot provider (v2)
    - bare-host recovery, multi-site and non-hand-run Compose tooling moved to Later (Veeam covers VM recovery)
    - cross-host migration stays v2 (the owner said not to plan around the launch)
  - **Threat model:** T8 updated for the master admin. Added T17 (single NAS) and T18 (single operator).
- **Files:** `stack_info/final_stack.md`, `adr/0005`, `adr/0006`, `adr/0008`, `adr/0013` (new), `adr/0014` (new), `adr/README.md`, `threat_model.md`, `platform/primary.md`, `platform/secondary.md`, `platform/oneoffs.md`, `roadmap.md`

### 2026-09-25 — Decision — ADR-0006 (native agent) and ADR-0007 (embedded Kopia) accepted
- **Notes:**
  - The owner confirmed both recommendations. `dbr2-agent` is a native, statically linked Go binary running as a systemd service (no containerized agent initially).
  - Kopia is embedded as a Go library, pinned to an exact version, and imported only by `internal/engine/kopia`. Kopia upgrades are gated by the engine compatibility suite.
  - All ADRs are now Accepted.
  - Phase 0 now only waits on the spikes (library, ACLs, repository-server data path, xattr/SELinux, Temporal). Their results may add implementation detail to ADRs 0002, 0006 and 0007 but do not reopen these decisions.
- **Files:** `adr/0006-native-agent-and-volume-access.md`, `adr/0007-kopia-embedded-library.md`, `adr/README.md`, `stack_info/final_stack.md`, `roadmap.md`

### 2026-09-25 — Decision / Docs — Design gaps resolved; ADRs, domain model, threat model and roadmap created
- **Notes:**
  - Resolved the review's design gaps with the owner's decisions:
    - Agent Gateway execution model (ADR-0001)
    - Kopia Repository Server with per-agent identities (ADR-0002)
    - the Repository is authoritative and PostgreSQL is a rebuildable index (ADR-0003)
    - recovery point two-phase commit and Partial/Failed semantics (ADR-0004)
    - quiesce saga plus agent dead-man switch, with snapshots optional because not all hosts have them (ADR-0005)
    - mandatory self-backup and key escrow (ADR-0008)
    - Temporal kept (ADR-0009)
    - terminology fixed (ADR-0012)
  - ADR-0006 (native agent) and ADR-0007 (Kopia as a library) are written as recommendations with status **Proposed**, pending owner confirmation.
  - Linked `restore.production` to the approval workflow and dual authorization (final_stack → Authorization; `platform/oneoffs.md`).
  - Scrubbed internal names from the examples: Planix → Inventory, FBCAD-NAS → Primary-NAS, a personal name → jdoe, Xlynk → Wiki, and a public IP → 10.0.0.55.
  - Replaced the phase list in `platform/primary.md` with a pointer to this roadmap.
- **Files:** `adr/*` (new), `domain_model.md` (new), `threat_model.md` (new), `roadmap.md` (new), `stack_info/final_stack.md`, `system_name.md`, `platform/primary.md`, `platform/secondary.md`, `platform/oneoffs.md`

### 2026-09-25 — Decision — Valkey replaces Dragonfly; no locks in the cache
- **Notes:**
  - BSD-3 licensing, and the cache workload does not need Dragonfly's throughput.
  - "Acquire Backup Lock" became "Claim Application (exclusive workflow ID)".
  - Application exclusivity comes from the Temporal workflow ID `application/{id}`; PostgreSQL covers short critical sections.
  - Recorded retroactively as ADR-0010 and ADR-0011.
- **Files:** `stack_info/final_stack.md`, `platform/primary.md`

### 2026-09-25 — Docs — Platform docs reconciled with the final stack
- **Notes:**
  - `final_stack.md` declared the source of truth.
  - Removed contradictions: queue, cache, backup format, encryption, engine choice, host connectivity, storage list, names, RBAC and phases.
  - Retired the names `dockerbackup-*`, `dbk` and `docker-recover`.
- **Files:** `system_name.md`, `platform/primary.md`, `platform/secondary.md`, `platform/oneoffs.md`

### 2026-09-25 — Docs — Initial documentation review
- **Notes:** reviewed all docs. Found cross-document contradictions and design gaps A–H, which led to the three entries above.

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
| 2 | Agent, enrollment & gateway | v1.0 | **Complete** (2026-09-25) |
| 3 | Discovery | v1.0 | **Complete** (2026-09-25) |
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

- [x] `dbr2-agent` native static binary (AMD64) and hardened systemd unit (ADR-0006); **RPM packages for Rocky and Fedora** (nfpm; `tests/packaging/rpm-test.sh`: 53 checks on Rocky 9.8 and Fedora 44 — install, upgrade, remove keeps state)
- [x] DBR² CA (ECDSA P-256, key sealed in PostgreSQL); agent certificate issuance (CSR, CA-set identity), renewal at 2/3 lifetime with a fresh key, suspension and revocation checked on every connection (ADR-0016)
- [x] Enrollment: single-use, expiring registration tokens with a join command and CA fingerprint pinning; **host approval workflow** (Pending → Active; suspend, resume, revoke)
- [x] Agent Gateway in `dbr2-server`: `AgentService.Connect` stream (mTLS, TLS 1.3), session leases in PostgreSQL (ADR-0001); agent-protocol MAJOR enforced from `Hello`, outdated agents flagged
- [x] Command protocol: idempotent `command_id` (workflow ID + run ID + activity ID — ADR-0001 amended), deadlines, fsynced local command journal, in-flight report and result replay on reconnect
- [x] Temporal activity dispatch through the gateway (internal control channel, internal token), with heartbeats; `DiscoverHost` workflow
- [x] Agent health reporting (Docker reachability and version, uptime), heartbeat latency, last seen; `agent.connection_state` / `agent.latency` OpenTelemetry metrics (export not yet validated against a collector — see Deferred)
- [x] Integration test: agent disconnect mid-command, then reconnect and resume — exactly one execution (`internal/gateway`, and through Temporal in `workflows/hosts`)

## Phase 3 — Discovery

- [x] `ContainerRuntime` interface; `DockerRuntime` (rootful) using the Moby Go SDK (`moby/moby/client`)
- [x] Discover hosts, containers, volumes, networks and bind mounts; paths resolved from Docker, never hard-coded
- [x] Compose Project detection through `com.docker.compose.*` labels, for **hand-deployed** projects; original Compose files (multiple `-f`) and `.env` collected
- [x] Group resources into **Applications** (compose, standalone container); manual Applications grouping non-Compose containers
- [x] Reconstructed Compose definition for applications without (readable) Compose files, flagged **Reconstructed**, secrets as `${VAR}` placeholders
- [x] Volume classification: Local, External (driver-backed or network-backed; not protected by default) or Ephemeral
- [x] External dependency flags: external networks and volumes, network storage, shared network namespaces
- [x] **Unprotected-data detection**: writable container paths that no volume or bind mount backs, grouped per data directory with severity (core feature)
- [x] Image references with digest and platform recorded (locally built images shown without a digest)
- [x] Secret detection in environment variables, `.env` and Compose files; sealed at rest; masked in the UI and API; `secrets.read` required to reveal (audited)
- [x] Application ownership metadata: owner, environment, criticality, display name (API and console)

## Phase 4 — Repositories & backup

- [x] `BackupEngine` interface plus `internal/engine/kopia` (pinned Kopia **v0.23.1**), covering the spike pitfalls: deep restore depth, cancellation bridged to `Uploader.Cancel()`, no saving incomplete manifests, hyphenated tag keys (ADR-0007)
- [x] `dbr2-reposerver` deployable: Kopia server in-process through the `cli` package; TLS (self-signed, **pinned by fingerprint**: the only mode Kopia clients support; ADR-0002 Phase 4 amendment); maintenance and GC owner; **mount guard, sentinel and stall watchdog**; `DYNAMIC-1M-BUZHASH` splitter for new repositories (ADR-0002)
- [x] Per-agent Kopia users with the spike-verified ACL set (APPEND own snapshots, READ own policies); `maint@dbr2` for the worker; **every snapshot pinned**, Kopia retention disabled; per-agent storage monitoring (per-host usage on each Repository); temporary per-source READ ACL grants in the management API (the restore-side compensation that revokes them ships with Phase 5)
- [x] Storage backend: **NFS** (production; mounted only on the reposerver host, `hard` mount, export restricted) plus local filesystem (testing)
- [x] NAS guidance documented: scheduled read-only NAS snapshots on the Repository share (the v1.0 immutability substitute)
- [x] **Key escrow at Repository creation** to the two escrow recipients. Creation is blocked until escrow is confirmed (ADR-0008)
- [x] Recovery manifest schema v1 (`schema_version`), with a JSON schema published in the codebase (ADR-0004)
- [x] Backup workflow per the final stack: claim the application → hooks → dumps → quiesce → protect → resume → commit (the database-dump slot exists; dump plugins arrive in Phase 8)
- [x] Components: config, volumes, bind mounts, plus a **`fsmeta` record** for each filesystem component; tagged (`dbr2-*` keys) and pinned snapshots; manifest written last, **by `maint@dbr2` only**, with component sources validated (ADR-0004)
- [x] Required and optional component rules; recovery point status Complete or Partial; failure means no recovery point
- [x] Orphan-component garbage collection after a grace period
- [x] Consistency modes: Live, Quiesced (pre/post hooks), Offline; minimal-downtime defaults (ADR-0005)
- [x] **Quiesce safety:** saga compensation, **60-minute** default maximum quiesce, agent dead-man switch, alerts (ADR-0005)
- [x] **Seed pass** for large first-time volumes (~500 GB) so the consistent pass only uploads the delta (ADR-0005)
- [x] SELinux context captured for volume and bind-mount roots
- [x] Per-host concurrency limit (maximum concurrent jobs); configurable backup window, to avoid Veeam job windows
- [x] Manual backup through the API and the `dbr2` CLI
- [x] `dbr2 admin reindex`: rebuild the recovery-point index from a Repository (ADR-0003)

**Status: complete (2026-09-25).** Verified end to end on the dev stack: Live, Quiesced (pause and hooks) and Offline (stop) backups, key escrow, reindex, and an escrow drill with the stock Kopia CLI.

## Phase 5 — Restore

- [x] Restore workflow: to the original host or an alternate host
- [x] **Restore impact preview**: containers stopped, volumes overwritten, ports and paths changed
- [x] **Restore collision detection**: names, networks, volumes and bound ports
- [x] Bind-mount path remapping
- [x] Create missing networks and volumes; pull images by digest (image digest enforcement)
- [x] Database restore from the logical dump or RDB file (PostgreSQL, Redis). The restore side is implemented and integration-tested against real `postgres:18-alpine` and `redis:8-alpine`; dump **capture** arrives with Phase 8, in the formats fixed by ADR-0017
- [x] Restore into staging, then apply the `fsmeta` record (hardlinks, ACLs, extended attributes, full SELinux contexts, directory mtimes), verify, and swap in; `IgnorePermissionErrors = false`; sparse writing on (ADR-0006)
- [x] Start the application; health-check validation
- [x] `restore.production` enforcement plus **single-operator safeguards**: typed confirmation, mandatory reason (ADR-0014)
- [x] Recovery history: every restore attempt recorded, including failures
- [x] Restores claim the same `application/{id}` workflow ID, so they cannot overlap with backups

**Status: complete (2026-09-25).** ADR-0017. Verified end to end on the dev stack: in-place restore, restore of a deleted application with re-creation, automatic rollback on an unhealthy restore, cancellation, and re-creation from an Offline-mode recovery point.

## Phase 6 — Web console

- [x] Next.js, ShadCN, Tailwind, TanStack Query and Table; OpenAPI-generated client and Zod schemas
- [x] Navigation: Dashboard, Docker (Hosts, Applications, Containers, Volumes), Protection (Policies, Jobs, Recovery Points), Recovery (Restore, Restore Testing), Storage (Repositories, Usage), System (Agents, Users, Notifications, Audit Log, Settings)
- [x] Application inventory with **protection status** and **protection coverage** (components protected, unresolved dependencies). *Early delivery in Phase 3:* Applications list and detail pages (unprotected data, volume classes, dependencies, Original/Reconstructed Compose with audited reveal, metadata editing, manual grouping); protection status arrives with backups (Phase 4)
- [x] Manual Back Up and Restore flows with live progress over SSE
- [x] Recovery point browser (the manifest view; Complete or Partial status)
- [x] Agent approval UI — delivered early with Phase 2 (Hosts page: approve/suspend/resume/revoke with reasons, typed-hostname revoke, registration tokens with join command, run discovery)
- [x] Playwright end-to-end tests for the core flows (Phase 1 ran an ad-hoc headless Chromium check against the real stack)
- [x] Content-Security-Policy with nonces for console pages (Next.js inline scripts); other security headers already set

**Status: complete (2026-09-25).** Verified against the real dev stack as well as the mock: every page loads in headless Chromium with the nonce CSP and no violations, and live `job.progress` events streamed during a real backup.

## Phase 7 — Scheduling, retention & notifications

- [x] Protection Policies: schedule (cron, hourly, daily, weekly, monthly), consistency mode, retention, target Repositories
- [x] Temporal schedules; overlapping runs skipped and recorded (ADR-0011)
- [x] Retention at recovery-point level: manifest deleted first, then components; runs under the maintenance identity
- [x] **Deletion grace period** (default 7 days) for manual deletions of recovery points and Repositories (ADR-0014)
- [x] **Recovery Contract** (v1.0 subset): maximum RPO, required components; Satisfied or Violated, with RPO-violation reporting
- [x] Notifications: email and generic webhook. Events: backup failed, missed or warning; restore completed or failed; agent offline; RPO violated; auto-resume; escrow unhealthy; verification failed; master admin login

**Status: complete (2026-09-25).** ADR-0018. Verified on the dev stack: scheduled backups every minute from a policy, retention deleting outside `keep_last`, a contract RPO violation raising an alert, and deletion grace with undo (API integration test).

## Phase 8 — Database-aware backups (PostgreSQL, Redis)

- [x] Database detection for PostgreSQL and Redis
- [x] PostgreSQL: `pg_dumpall` (all databases and roles) streamed into a `database` component (Zstandard-compressed stream); online, no quiesce
- [x] Redis: `BGSAVE`, wait for completion, then capture the RDB file (plus the AOF when enabled); online, no quiesce
- [x] Strategy options: logical, volume, or both (default **both** for databases, with the volume copy allowed to be crash-consistent)
- [x] Dump and RDB validation

**Status: complete (2026-09-25).** Verified on the dev stack against real `postgres:18-alpine` and `redis:8-alpine`: dumps were validated (pg_dumpall trailer; RDB magic plus AOF), and a database-only restore brought back 1000 rows while the database kept running.

## Phase 9 — Verification & platform self-protection

- [x] Repository integrity verification workflow (`repository/{id}/verify`)
- [x] Recovery point verification state (Unverified, Verified, Verification Failed)
- [x] **Platform self-backup** to the System Repository, plus an age-encrypted Platform Recovery Bundle in a separate NFS export or directory (ADR-0008)
- [x] Escrow health checks, re-escrow on secret rotation, annual escrow drill reminder
- [x] `dbr2 admin restore-platform` plus the platform recovery runbook (fresh Temporal, reindex); document Veeam's VM backup as an additional layer (implemented as `dbr2-server admin restore-platform`; `docs/operations/platform-recovery.md`)
- [x] Platform recovery test passing in CI (`internal/platform/recovery_integration_test.go`, part of `make test-integration`)

**Status: complete (2026-09-25).** Verified on the dev stack: Repository verification with 100 % reads marked recovery points Verified, an escrow drill decrypted with a key from the "safe" made escrow healthy, and a platform bundle was written to the System Repository and the bundle directory and verified with an escrow identity (`restore-platform --verify-only`).

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

### 2026-09-26 — Feature / Fix — Non-destructive deploy and update script; `make dev-down` no longer wipes data
- **Notes:**
  - **Owner request:** "a deploy/update script that is non-destructive".
  - **`deployments/docker-compose/dbr2-deploy.sh`** (runbook `docs/operations/upgrade.md`) has these commands: `check`, `install`, `update`, `backup`, `rollback`, `status`.
    - **Never** runs `down -v`, removes volumes, networks, images or containers, uses `--remove-orphans`, or prunes.
    - Refuses to update when `dbr2_pgdata` is missing (a wrong directory or project would start an empty platform). Refuses to install over an existing installation.
    - **Update sequence:** pre-flight checks → plan and confirmation → **pull every image before changing anything** (a failed pull restores `.env`) → wait for running backups and restores (`--wait-idle`, or `--force`) → **pre-update backup** → `up -d` → verification. The backup covers the three databases (`pg_dump -Fc`, each checked with `pg_restore -l`), `.env`, secrets, the Compose files, the versions and checksums.
    - **Verification:** every service healthy, no pending migrations, and readiness through the proxy. If it fails, the image versions are **rolled back automatically**.
    - `rollback` restores the previous image versions. `--restore-db` restores the pre-update databases, but only after a typed `RESTORE DATABASE` (`--yes` does not cover it) and after taking a safety backup of the current databases; it keeps table ownership and says to reindex.
    - A lock prevents concurrent runs, and every run is recorded in `.deploy/history.tsv`.
    - Versions come from a release manifest, `--version`, `--channel edge` or `--set`.
  - **Root cause of the repeated dev-data wipes:** `make dev-down` ran `docker compose down -v`. That deleted the dev database, CA and Caddy data, while the bind-mounted mock Repository and reposerver state survived, leaving an initialized reposerver the platform no longer knew.
    - `dev-down` now only stops the stack.
    - `make dev-reset CONFIRM=delete-dev-data` deletes both together.
    - `make dev-update` runs the script against the dev stack.
  - **Verification on the dev stack:**
    - A dry run, then a real update: all checks passed, the marker data survived, and the backup checksums verified.
    - `rollback --restore-db` refused without confirmation. With it, it took a safety backup, and a marker added after the backup disappeared while the earlier one remained.
    - After the restore, all 33 tables were still owned by `dbr2`, the append-only audit triggers were still enforced, and the stack was healthy.
    - CI: 18 tests (`scripts/ci/test/test_dbr2_deploy.sh`, with Docker stubbed), including one that fails if the automatic rollback is removed (checked by mutation). shellcheck now also covers `deployments/docker-compose/*.sh`.
    - **Follow-up fix:** the CI runner's shellcheck flagged SC2015 (`A && B || C`) in `log()`, which the local 0.11.0 did not. It is now an explicit `if`.
- **Files:** `deployments/docker-compose/{dbr2-deploy.sh,README.md,.gitignore}`, `docs/operations/upgrade.md` (new), `Makefile`, `.github/workflows/ci.yml`, `scripts/ci/test/test_dbr2_deploy.sh`, `scripts/ci/README.md`, `deployments/CHANGELOG.md`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phases 7, 8 and 9 complete (scheduling, retention, contracts, notifications; database-aware backups; verification and self-protection)
- **Notes:**
  - **ADR-0018 (new):** scheduling, retention, deletion grace, recovery contracts, notifications, verification, escrow health and the database strategy.
  - **Phase 7:**
    - Protection Policies: cron or preset schedule plus timezone, mode and Repository defaults, grandfather-father-son retention.
    - One Temporal schedule per application, kept in sync; overlapping runs are skipped and recorded (`backup.skipped`).
    - `RetentionWorkflow` runs daily as `maint@dbr2`: manifest first, then components; an application's latest recovery point is never deleted.
    - Deletion grace: 7 days for recovery points and Repositories, with typed confirmation, a reason and undo.
    - Recovery Contracts (max RPO, required components): evaluated every 5 minutes with violation and recovery alerts, and recorded in each manifest at capture.
    - Notifications (`internal/notify`): email and HMAC-signed webhooks with event and severity filters, retries, multi-instance safety (`SKIP LOCKED`), sealed write-only secrets, and agent offline/online alerts.
  - **Phase 8:**
    - PostgreSQL and Redis detection, with tooling images excluded.
    - Online dumps before the quiesce window:
      - `pg_dumpall --clean --if-exists` streamed through zstd, validated by exit code and trailer.
      - Redis `BGSAVE` completion, then the RDB (plus AOF), validated by the RDB magic.
    - Strategy per application: logical, volume or both (the default).
    - Manifests record `database` and `validation`.
  - **Phase 9:**
    - Engine `Verify`: every object checked and a sampled share of files fully read. `repository/<id>/verify` runs on demand and weekly (`platform-verify`); recovery points become Verified or Verification Failed (critical alert).
    - Escrow health checked hourly: recipients, confirmation, recipient drift, the 90-day re-confirmation and the annual drill. Packages can be regenerated from the reposerver's password; drills run through the console.
    - Platform self-backup and `dbr2-server admin restore-platform`: see the entry below, and the reposerver `state-export`/`state-import` and `repository-password` endpoints.
  - **Found by the end-to-end run and fixed:**
    1. A **database-only restore** sent an empty component list to the agent, and it also stopped the whole application first. Restores now stop the application only when files are swapped, and skip the data step when there are none. Dumps load into the running database.
    2. A new policy's `next_run` was missing from the create response.
  - **Dev environment:** the dev database and Caddy volumes were recreated again during the subagents' parallel Docker work. The dev agent, escrow recipients and Repository were re-bootstrapped; the dev Repository storage, which is test data only, was reset.
  - **Verification on the dev stack:**
    - A PostgreSQL + Redis Compose app backed up with 7 components; the dumps were validated.
    - Database-only restore of 1000 rows.
    - Policy schedule firing every minute.
    - Retention deleting the out-of-policy recovery point.
    - Verification at 100 % reads: all recovery points Verified.
    - Contract RPO violation.
    - Escrow drill making escrow healthy.
    - Platform bundle (82 KiB) written and verified with an escrow identity.
  - **Deferred:**
    - Redis AUTH/ACL.
    - A pack-blob-level verification from the worker (the maint session has no blob list; the stock `kopia snapshot verify` on the reposerver host covers it).
    - Retention of pinned platform snapshots.
    - Platform backup triggered by security-relevant changes (daily only for now).
    - Two-person approval and maintenance windows ("Later").
- **Files:** `internal/{protection,notify,platform,agent,runtime,engine,reposerver,repoclient,api,audit,manifest,config,temporalx}`, `workflows/{backup,platform,ops,restore,register.go}`, `cmd/{server,worker,dbr2}`, `proto/{agent,control}/v1`, `db/migrations/0000{5,6,7}_*`, `db/queries/{policies,notifications,platform,backups}.sql`, `web/**`, `api/openapi.yaml`, component changelogs, `docs/adr/0018` (new), `docs/adr/0008` (amendment), `docs/operations/platform-recovery.md` (new), `docs/stack_info/final_stack.md`, `docs/threat_model.md` (T36–T39), `docs/dev/audit-events.md`, `README.md`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phase 9: platform self-backup and platform recovery (ADR-0008)
- **Notes:**
  - **Platform Recovery Bundle** (`internal/platform`): `tar` → `zstd` → age (binary) to every escrow recipient, produced and encrypted **inside dbr2-server** (plaintext never leaves the process). Entries: `db/<table>.copy` for every `public` table except `goose_db_version` (`COPY … FORMAT binary`, one REPEATABLE READ read-only snapshot), `secrets/` (`dbr2_secret_key`, `dbr2_internal_token`, `dbr2_entra_client_secret` if set), `reposerver/<id>.tar` (each reposerver's `GET /v1/state-export`; an unreachable reposerver is recorded as missing → Partial + alert), and `manifest.json` last (versions, goose schema version, row counts, columns, SHA-256 of every entry, no secret values). Tables are buffered one at a time in memory.
  - **Temporal excluded** (recovery uses a fresh Temporal; forensic history is covered by the Veeam VM backup) — recorded as an ADR-0008 amendment together with the bundle format and where each part runs.
  - **Control plane:** `PlatformService` `BeginPlatformBackup`, `ExportPlatform` (server-streaming, 1 MiB chunks, final summary message) and `RecordPlatformBackup` (audit `platform.backup.succeeded|partial|failed`, warning/critical alert). The response message is `ExportPlatformResponse` (buf lint requires the `<Rpc>Response` name).
  - **Worker:** `PlatformProtectionWorkflow` (`workflows/platform`; ID `platform/protection`, daily schedule `platform-protection` at `DBR2_PLATFORM_BACKUP_CRON` = `15 2 * * *`, overlap skip; the server refuses a second concurrent run) tees the ciphertext into the System Repository (pinned snapshot `maint@dbr2:/platform`, `dbr2-kind=platform`, shared `MaintSessions`) and `DBR2_PLATFORM_BUNDLE_DIR` (temp + fsync + rename; newest 14 kept). One target missing → Partial; both → failed.
  - **API:** `GET/POST /api/v1/platform/backups`, `PUT /api/v1/repositories/{id}/system` (`repository.manage`); audit `platform.backup.requested`, `repository.system.designated`.
  - **DB:** migration `00007_platform_backups` (`platform_backups`, `repositories.is_system` with a partial unique index).
  - **Restore:** `dbr2-server admin restore-platform` (the roadmap's `dbr2 admin restore-platform` needs direct database access on a fresh install; the CLI prints a pointer). Verifies every digest, migrates to the bundle's schema version as the database owner, restores every table in one transaction with `session_replication_role = replica` (superuser connection), resets sequences, applies newer migrations, writes secrets / reposerver state or imports the state (`POST /v1/state-import`), prints the next steps. `--verify-only` for drills, `--no-database` for the reposerver import after the reposervers use the restored token.
  - **Platform recovery test:** two throwaway PostgreSQL databases; seed through the real services (master admin, OIDC user, CA, agent, escrow recipient, Repository with escrow package, recovery points, audit events, outbox), export against a fake reposerver, restore, and assert row counts, audit trigger enforcement, sequence reset, CA key decryption with the restored secret key, escrow package decryption, reposerver state import and tamper rejection.
  - **Deployment:** worker bind mount `${DBR2_PLATFORM_BUNDLE_HOST_PATH:-./platform-bundles}` (a separate NFS export or directory outside every Repository); dev uses `.dev/platform-bundles` (`make dev-up` creates it).
  - **Open:** triggering a platform backup after security-relevant configuration changes; retention of platform snapshots in the System Repository (pinned, kept indefinitely); console UI; the reposerver `state-export`/`state-import` endpoints are coded against the agreed contract (tested with fakes).
- **Files:** `internal/platform/*`, `internal/protection/platform_backup_rpc.go`, `internal/protection/protection.go` (one field), `workflows/platform/*`, `workflows/register.go`, `internal/api/ops_platform.go`, `internal/audit/events_platform.go`, `internal/repoclient/client.go`, `internal/temporalx/temporalx.go`, `internal/config/config.go`, `proto/control/v1/control.proto`, `db/migrations/00007_platform_backups.sql`, `db/queries/platform.sql`, `cmd/server/{main.go,serve.go,restore_platform.go}`, `cmd/worker/{main.go,schedules.go}`, `cmd/dbr2/{backup.go,main.go,platform.go}`, compose files, `Makefile`, `docs/operations/platform-recovery.md`, ADR-0008, `docs/dev/audit-events.md`, changelogs (server, worker, cli, api, agent-protocol, db-schema, deployment)

### 2026-09-25 — Fix — `make dev-up` build failure (DNS inside build containers)
- **Notes:**
  - **Symptom (reported by the owner):** `make dev-up` failed in `go mod download` (and `npm ci`) with `lookup proxy.golang.org on [2600:…::1]:53: … network is unreachable`.
  - **Cause:**
    - The host resolves through the systemd-resolved stub (`127.0.0.53`), so Docker's build containers fall back to the real upstream resolvers.
    - The first upstream is the router's IPv6 address, and the default build network has no IPv6 route.
    - Earlier builds had worked from the module cache. New modules (Phase 5–6 dependencies) exposed the problem.
  - **Fix:** the development Compose file builds with `network: host`, so builds use the host's working resolver. Production images are built in CI and are unaffected.
  - **Alternative for other machines:** set `"dns": [...]` in `/etc/docker/daemon.json`, or enable IPv6 on Docker networks.
  - Verified: `make dev-up` builds all four images and every service is healthy.
- **Files:** `deployments/docker-compose/compose.dev.yaml`, `deployments/CHANGELOG.md`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phase 6 (Web console) complete
- **Notes:**
  - **Live updates:** `GET /api/v1/events` (Server-Sent Events).
    - Event types: `job.progress` (agent command progress relayed by the gateway and attributed to its workflow through the command ID), `backup.updated`, `restore.updated`, `agent.status`, `alert.created`, `inventory.updated`.
    - Filtered per subscriber by the permission each event carries. Restore progress needs `restore.read`; backup progress needs `backup.read`.
    - 15 s heartbeats. The per-message write deadline is extended, so streams outlive the server's 120 s write timeout; verified through Caddy for 150 s.
    - Valkey pub/sub fan-out when configured.
    - The console keeps a single `EventSource` that invalidates the affected queries, reconnects with backoff, re-reads everything after a reconnect, and shows a Live / Not live indicator.
  - **Protection status and coverage:** each application reports protected, at risk, failed or unprotected, with reasons; the latest recovery point and attempt; a running backup or restore; per-component coverage against the latest manifest; and unresolved dependencies. The server computes this, so the console no longer duplicates the backup-plan rules.
  - **New APIs:** jobs (backups and restores together), fleet-wide containers and volumes.
  - **Console:**
    - Grouped, permission-gated navigation: Docker, Protection, Recovery, Storage, System.
    - Hosts (inventory) split from Agents (lifecycle).
    - New pages: Containers, Volumes, Jobs, Usage, Users (roles, enable/disable, Entra group mappings), Notifications (replaces Alerts; `/alerts` redirects), and "Start a restore".
    - Honest placeholders for Policies (Phase 7) and Restore Testing (Phase 9).
    - Protection card and column; live progress on "Back up now", Jobs and restores; cancel restore.
    - Topology graph (React Flow, `@xyflow/react` 12.12.0) coloured by protection.
    - Recovery point browser: Complete/Partial, fsmeta nested under its parent, topology and databases.
  - **Generated contract:**
    - `@hey-api/openapi-ts` 0.99.0 generates TypeScript types and Zod 4 schemas from `api/openapi.yaml`, and the client validates against them.
    - A unit test fails if the generated code is stale, and the mock API is validated against the generated schemas.
    - Zod runs in `jitless` mode, because its `eval` probe was a CSP violation.
  - **CSP with nonces:** the Next.js proxy (`src/proxy.ts`) sets a per-request nonce, with `script-src 'self' 'nonce-…' 'strict-dynamic'`, `object-src 'none'`, `frame-ancestors 'none'` and `base-uri 'self'`. Pages render dynamically. Playwright fails on any CSP violation.
  - **Playwright** (1.63.0, Chromium): 10 end-to-end tests covering sign-in, navigation and permission gating, protection and topology, "Back up now" with live progress, the recovery point browser, the restore wizard through to succeeded, and the Repository wizard. They run against the mock API with a production build, in the new CI job "Web end-to-end (Playwright)".
  - **Tests:** web unit tests went from 186 to 261; Go gained SSE integration, protection status and bus tests.
  - **Threat model:** T34 (live-update stream), T35 (script injection and CSP).
  - **Contract gaps noted, deferred to API polishing:**
    - Nullable struct and enum fields are not marked nullable in the spec (the console generator patches this).
    - Untyped progress documents.
    - Readiness 503 body type.
    - No inventory timestamp on fleet lists.
- **Files:** `internal/{events,gateway,protection,api}`, `cmd/server`, `web/**` (incl. `e2e/`, `playwright.config.ts`, `openapi-ts.config.ts`, `src/proxy.ts`), `.github/workflows/ci.yml`, `Makefile`, `THIRD_PARTY_NOTICES`, component changelogs, `docs/stack_info/final_stack.md`, `docs/threat_model.md`, `scripts/ci/README.md`, `README.md`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phase 5 (Restore) complete
- **Notes:**
  - **ADR-0017 (new):** restores claim `application/<id>`; impact preview with blocking collisions; production safeguards; staged restore with fsmeta verification, swap with the previous content kept, commit only after a healthy start, and rollback on any failure; formats for database dumps.
  - **Server:**
    - Impact preview and collision detection from the manifest topology and the target host's inventory: container names, published ports, networks, volumes, bind paths, missing external networks; path remapping; in-place vs alternate host.
    - Production restores (target tagged production, or a running application overwritten in place) require `restore.production`, a typed confirmation and a reason.
    - Recovery history in `restore_runs`, including failures and rollbacks.
    - Cancel endpoint: cancellation runs the compensation.
    - `PlatformService`: `PrepareRestore` re-validates before anything changes; per-source READ grants for cross-host restores; `UpdateRestore` records audit and alerts.
  - **Worker:** `RestoreWorkflow`: grant → agent access → images by digest → stop (dead-man lease, 24 h) → staged restore → re-create containers → start → database dumps → health check → commit. A rollback saga covers any failure, and the grant is always revoked.
  - **Agent:**
    - `EnsureImages` (pull by digest, verify, tag).
    - `RestoreComponents`: staging next to the target, Kopia with `IgnorePermissionErrors = false` and sparse writing, fsmeta Apply and Verify (hardlinks, xattrs/ACLs/SELinux, directory mtimes deepest first), root owner/mode/SELinux, free-space pre-check, and a journaled swap that keeps the previous content. It refuses to swap if the stop lease has already fired.
    - `RecreateContainers` from the captured inspect documents, with remapped binds and networks created.
    - `StartContainers`.
    - `CheckHealth`: running, and healthy when a healthcheck exists, for 15 s. It fails early on exit, restart loops and sustained `unhealthy`, and returns log tails.
    - `FinalizeRestore`: COMMIT or ROLLBACK, idempotent and crash-safe through the journal.
    - `RestoreDatabase`: PostgreSQL SQL stream into `psql`; Redis RDB, refused when AOF is on.
  - **Manifest schema 1, additive:** `topology`, component `file_name`, `database`.
  - **API, CLI and console:**
    - API: preview, start, list, get, cancel.
    - CLI: `dbr2 restore` (prints the preview; `--confirm` and `--reason`) and `dbr2 restores`.
    - Console: restore wizard (target, components, path remaps, impact preview, confirmation), Restores page and detail with a step indicator, the application's Restores tab, dashboard card. Web tests went from 146 to 186.
  - **Found by the end-to-end run and fixed:**
    1. **Workflow type name clash.** `backup.Workflow` and `restore.Workflow` both registered as "Workflow", so the worker panicked at start. They are renamed `BackupWorkflow` and `RestoreWorkflow`. A new test registers every workflow and activity on one registry, which the Temporal test environment enforces.
       - A dev backup started under the old type name could not run and held the application's workflow ID. It was terminated: 4 history events, no activity had run, so no compensation was skipped. Dev only.
    2. **Path parameters not bound.** Handlers embedding a path-parameter struct did not get `{id}` bound by Huma, which caused a 404. The fields are now explicit, and the spec lint fails when any `{param}` is undeclared (proven by reintroducing the bug).
    3. **Offline-mode backups.** Their inspect documents say "exited", so re-created containers would never start. The worker now starts containers that were running according to the manifest topology, which is recorded before quiesce (`running_at_capture`).
    4. **Containerised agent collided with itself.** The dev agent, running in a container that mounts host paths, collided with every bind path. Containers labelled `dbr2.role=agent` are now ignored for bind-path collisions.
    5. **Unhealthy detected slowly.** The health check waited the whole 5-minute timeout on an `unhealthy` container; it now fails after 30 s of sustained `unhealthy`.
  - **Verification on the dev stack (real Docker, real Kopia):**
    1. **In place:** a deleted 20 MB blob and a tampered bind-mounted config file were restored with identical checksums; fsmeta applied with 0 verification mismatches; 26 s; no leftovers.
    2. **Deleted application** (`compose down -v`): volume, network and container were re-created with their Compose labels and real environment, and the data matched.
    3. **Rollback:** a recovery point whose data fails the app's healthcheck was restored; the restore ended `rolled_back`, and the app is healthy with its previous data.
    4. **Cancel** during the stop step: the app was resumed and the restore recorded as `failed`.
    5. **Offline-mode recovery point of a deleted app:** re-created and started.
    - The safeguards were exercised: a missing confirmation was rejected with 400, and collisions blocked the restore.
  - **Local dev note:** Docker builds on this machine can't resolve DNS (an unreachable IPv6 resolver inside build containers), so the dev images were built with `docker build --network host`. The Compose file is unchanged.
  - **Deferred:**
    - The two-person approval workflow and maintenance-window-aware restores ("Later").
    - Restore to a new name or project.
    - Registry credentials for image pulls; database credentials (Redis `requirepass`).
    - Preserving the mtime of single-file bind mounts.
    - `restorecon` for unlabelled paths.
    - Pagination of `/restores`.
- **Files:** `internal/{protection,agent,fsmeta,runtime,engine,manifest,api,audit}`, `workflows/{restore,backup,register.go,workflows_test.go}`, `cmd/{worker,dbr2}`, `proto/{agent,control}/v1`, `db/migrations/00004_restores.sql`, `db/queries/restores.sql`, `web/**`, `api/openapi.yaml`, component changelogs, `docs/adr/0017` (new), `docs/stack_info/final_stack.md`, `docs/threat_model.md` (T31–T33), `docs/dev/audit-events.md`, `docs/domain_model.md`, `README.md`, `docs/roadmap.md`

### 2026-09-25 — Fix — CI integration failure after the Phase 4 push
- **Notes:**
  - **Symptom:** `TestDockerControl` failed on GitHub Actions with "page not found" from the Docker daemon. Every other job passed, including images.
  - **Cause:** the test fixture captured `docker run -d` with `CombinedOutput()`. On the runner's cold image cache, the pull progress written to stderr ended up in the "container ID", so the inspect URL was invalid. Locally the image was cached, which hid the bug.
  - **Fix:** use stdout only. Reproduced and verified locally after removing the image from the cache. The product code was not affected.
- **Files:** `internal/runtime/control_integration_test.go`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phase 4 (Repositories & backup) complete
- **Notes:**
  - **Backup engine:** `internal/engine` interface plus `internal/engine/kopia`, pinned to Kopia v0.23.1 and the only package that imports Kopia. It covers the spike pitfalls: deep restore, `Uploader.Cancel()` bridged to cancellation, incomplete snapshots never saved, hyphenated tag keys, and incremental uploads based on the previous snapshot of the same source.
  - **dbr2-reposerver:** the Kopia server runs as a supervised child of the same binary through Kopia's public `cli` package; secrets are kept off `argv` and scrubbed from logs, and the storage guard is checked before each start.
    - Stable self-signed TLS certificate pinned by fingerprint. Kopia clients cannot trust a custom CA, so the roadmap's "TLS from the DBR² CA" was replaced (ADR-0002 Phase 4 amendment).
    - Management API on `:8091` (internal only, token): status, initialize, users, read grants.
    - Global policy: `zstd-fastest`, retention never deletes. The spike ACL set replaces Kopia's defaults.
    - Integration test with real Kopia clients: agents cannot delete or see other agents' data, `maint@dbr2` can, read grants work, fingerprint pinning works, restart works.
  - **Key escrow (ADR-0008):**
    - Recipients are age or SSH public keys; private keys are rejected.
    - The Repository password is generated by `dbr2-server`, sent once to the reposerver, sealed into an age package for every recipient, and **never stored**.
    - A Repository stays `awaiting_escrow`, so backups are refused, until the one-time confirmation code from the decrypted package is entered.
    - Verified: 409 before confirmation, 400 for a wrong code, and the code accepted in any case and spacing.
  - **Recovery manifest schema v1** (`internal/manifest`, JSON Schema `schema/v1.json`): invariants that the schema cannot express are validated in Go; `rp_<ULID>` IDs.
  - **Backup workflow** (`application/<id>`): prepare → agent access → seed pass → pre hooks → quiesce → protect → resume → post hooks → commit, with a saga plus the agent's dead-man lease.
    - Commit runs as `maint@dbr2`, verifies every component's source and tags, and writes the manifest last, pinned.
    - Required component failed: no manifest and a critical alert. Optional component failed: Partial.
  - **Agent:**
    - `ConfigureRepository` (credentials stored 0600, never logged).
    - `Quiesce`/`Resume` with a journaled dead-man lease that is re-armed after an agent restart; events buffered while disconnected.
    - `RunHooks` via docker exec.
    - `SnapshotComponents`: config staging with the raw inspect documents, volumes, bind mounts including single files, the `fsmeta` record (extended attributes, ACLs, SELinux, hardlinks, directory mtimes as zstd JSON Lines), SELinux and ownership of roots, and a per-host concurrency limit.
  - **Server:** `PlatformService` for the worker; API for Repositories, escrow, backups, backup settings, host limits, recovery points and alerts; CLI `dbr2 backup`, `dbr2 recovery-points` and `dbr2 admin reindex`; orphan GC schedule (7-day grace) and the reindex workflow.
  - **Found by the end-to-end run and fixed:**
    1. The worker cannot reach the Kopia server through the agents' public URL, so Repositories gained `internal_server_url`.
    2. A manual backup against an unconfirmed Repository returned 202 and then failed silently; it now returns 409 with the reason.
    3. There was no `.dockerignore`: local state and Compose secrets were sent into the image build context (not into the final images). The build failed on root-owned dev state.
    4. The Temporal usage lint only matched a few variable names. It now flags any `.ExecuteWorkflow(` outside `internal/temporalx`, which caught a direct start of the reindex workflow; `temporalx.StartRepositoryOperation` was added for `repository/<id>/<op>` IDs.
  - **Verification:**
    - Unit tests (race) and every integration suite pass. That includes Temporal workflow tests for ordering, resume on failure and on cancel inside the quiesce window, the seed pass, and "required failed means no manifest", plus commit, orphan GC and reindex against real filesystem Kopia repositories.
    - The dev stack backed up a Compose app in Live, Quiesced and Offline modes:
      - 5 components each: config, volume, bind mount, and 2 `fsmeta` records.
      - The container was paused for about 1 s; hooks ran in order.
      - The app returned to `running` after each backup.
      - SELinux contexts were captured (`container_file_t`, `user_tmp_t`).
    - Reindex found 2 manifests and marked 0 missing. The escrowed password plus the stock Kopia CLI listed the pinned manifests without DBR².
    - License policy passes with no new exceptions; govulncheck finds no reachable vulnerabilities.
  - **Dev environment:** the dev agent was moved into a privileged `debian:trixie-slim` container (`dbr2-dev-agent`, host network) because reading Docker volume data requires root. The dev database had been recreated during the session, so the agent was re-enrolled.
  - **Deferred or noted:**
    - Lease renewal (not needed while the lease exceeds the activity bound).
    - Agent events have no acknowledgement (they are logged and buffered, but lost if the agent restarts while disconnected).
    - Per-host storage quotas.
    - Database dump plugins (Phase 8).
    - Filesystem-snapshot providers such as LVM (v2).
    - A gracefully stopped agent keeps an application paused until it starts again (threat model T28).
    - Console and API gaps noted by the web work, for Phase 6:
      - No running-backup status (only a `workflow_id`).
      - No pagination or Repository filter for recovery points; no severity filter for alerts.
      - No endpoints to retire a Repository, change the default or edit its URLs.
      - The console derives backup components from the analysis, duplicating the server's skip rules.
  - **Console:** Repositories (list, detail with per-host usage, three-step create wizard with the escrow download and confirmation code), escrow recipients (private keys cleared, never sent), Recovery points (filters, detail with component table and manifest viewer), Alerts (dashboard card, list, acknowledge), application "Back up now" with Backups and Backup settings tabs (hooks editor), host Limits card. Web tests went from 78 to 146. `RepositoryDTO.live` is null when the reposerver is unreachable (Huma cannot mark a struct reference nullable; documented in the field description).
- **Files:** `internal/{engine,manifest,escrow,repoclient,protection,reposerver,fsmeta,agent,runtime,gateway,api,audit,config}`, `workflows/{backup,agentcmd,hosts}`, `cmd/{server,worker,reposerver,agent,dbr2}`, `proto/{agent,control}/v1`, `db/migrations/00003_*`, `db/queries/{repositories,backups}.sql`, `deployments/docker-compose`, `.dockerignore`, `web/**`, `api/openapi.yaml`, `THIRD_PARTY_NOTICES`, component changelogs, `docs/adr/0002`, `docs/stack_info/final_stack.md`, `docs/threat_model.md` (T26–T30), `docs/dev/audit-events.md`, `docs/operations/nas-snapshots.md` (new), `README.md`, `docs/roadmap.md`

### 2026-09-25 — Fix — CI failures after the Phase 2 & 3 push
- **Notes:**
  - **gofmt:** five new Go files were unformatted. `make fmt-check` only checked *tracked* files, so files created before the commit were never checked locally. `fmt-check` and `fmt` now include untracked files.
  - **Flaky exactly-once assertion** in `workflows/hosts`. On the slower CI runner the Temporal dev server took about 15 s to start, so the agent's periodic inventory push (first fired 10 s after start) also called the fake runtime and was counted as a second execution. The command itself still ran once; the test was measuring the wrong thing.
  - **Fix:** `Agent.FirstInventoryDelay` is now configurable, and the integration tests disable the periodic push.
- **Files:** `Makefile`, `internal/agent/agent.go`, `workflows/hosts/integration_test.go`, `internal/gateway/integration_test.go`, `cmd/agent/CHANGELOG.md`, `docs/roadmap.md`

### 2026-09-25 — Feature — Phase 2 (agents, enrollment, gateway) and Phase 3 (discovery) complete
- **Notes:**
  - **Agent:** `dbr2-agent` supports `enroll`, `run`, `status` and `version`.
    - CA pinned by fingerprint; the key stays on the host (CSR).
    - Outbound mTLS session with backoff that honours `retry_after`; heartbeat echo, health reports and an inventory push every 5 minutes.
    - Certificate renewal at two-thirds of its lifetime with a fresh key.
    - Durable fsynced command journal: exactly-once per `command_id`, with results replayed until acknowledged.
    - Keeps running when Docker is unavailable.
  - **Packaging:** RPM (nfpm) plus a hardened systemd unit. `RestrictSUIDSGID`, `PrivateTmp` and `ProtectSystem=full` were deliberately **not** used because they would break future restores (documented in the unit). Tested on Rocky 9.8 and Fedora 44.
  - **Gateway** (in dbr2-server, ADR-0016):
    - CA in PostgreSQL with a sealed key; single-use expiring registration tokens with a join command.
    - Pending → approve / suspend / resume / revoke, with status, revocation and protocol-MAJOR checks on every connection.
    - PostgreSQL session leases and heartbeat latency.
    - Idempotent dispatcher that re-sends on reconnect; internal control channel for the worker.
    - Metrics `agent.connection_state` and `agent.latency`.
  - **ADR-0001 amended:** `command_id` = workflow ID + run ID + activity ID. The original "+ attempt" would have broken idempotency across retries.
  - **Discovery:**
    - The agent collects raw facts through the Moby Go SDK: host, containers, writable-layer changes, volumes, networks, images with digests, and original Compose/`.env` files.
    - The server does the analysis: application grouping (Compose, container, manual), Original vs Reconstructed Compose, volume classes (Local, External, Ephemeral), external dependencies and unprotected-data detection.
    - Secrets are sealed at rest, masked in the API and UI, and revealed only with `secrets.read`, audited.
    - A `DiscoverHost` Temporal workflow runs on demand.
  - **Real-data fixes found by the end-to-end run:**
    - `*_FILE` / `*_PATH` variables and empty values are no longer treated as secrets.
    - Parents of mount points are no longer reported as unprotected.
    - Severity is judged per file, so written CA certificates count as low.
  - **Console** (delivered early from Phase 6): Hosts (approval actions, add host with join command, registration tokens, discovery, host detail with inventory) and Applications (list and filters, detail with unprotected data first, volume classes, dependencies, images, masked environment, Compose tab with audited reveal, metadata editing, manual grouping). Dashboard protection overview.
  - **Verification:**
    - **Unit and integration:** unit tests (race); integration suites for the gateway lifecycle, Temporal dispatch through the gateway with a mid-command drop (exactly once), the hosts and applications API, and discovery against a real Compose project on Docker. The Phase 1 suites also pass.
    - **Web:** 78 tests.
    - **Packaging and CI:** RPM 53/53, CI script tests, actionlint, `buf breaking` against `main` (additive changes only), no generated-code drift.
    - **Real dev stack:** a native agent was enrolled, approved and discovered this machine's 11 applications, including DBR² itself. It reconnected on its own after a server restart. Masking, reveal auditing and metadata all verified.
  - **Deferred:** OTLP export of the agent metrics (needs a collector); multi-instance gateway forwarding (Later); rootless Docker (v2).
- **Files:** `cmd/agent`, `cmd/server`, `cmd/worker`, `internal/{agent,gateway,fleet,inventory,runtime,secrets,pki,config,api,audit,temporalx}`, `workflows/hosts`, `proto/agent/v1`, `proto/control/v1`, `db/migrations/00002_*`, `db/queries/{agents,inventory}.sql`, `deployments/{docker-compose,packaging}`, `tests/packaging`, `.github/workflows`, `web/**`, `api/openapi.yaml`, `THIRD_PARTY_NOTICES`, component changelogs, `docs/adr/0001`, `docs/adr/0016` (new), `docs/stack_info/final_stack.md`, `docs/threat_model.md` (T22–T25), `docs/domain_model.md`, `docs/dev/audit-events.md`, `README.md`, `docs/roadmap.md`

### 2026-09-25 — Decision / Maintenance — Dependabot PRs merged; automatic PRs stopped; push directly to `main`
- **Notes:**
  - **Owner instruction:** stop creating pull requests and push directly to `main`, and merge the open pull requests.
  - **Merged onto `main`:**
    - #3 `golang.org/x/oauth2` 0.37.0
    - #4 `react` / `react-dom` 19.3.0
    - #7 `jsdom` 30.1.1
    - #8 `@types/node` 26.6.2
  - Each Dependabot PR failed the changelog gate (and #3/#4 the stale `THIRD_PARTY_NOTICES` check). Added the changelog entries and regenerated the notices here.
  - **Not merged, blocked upstream:**
    - #5 ESLint 10: `eslint-config-next`'s bundled `eslint-plugin-react` uses the removed `context.getFilename`.
    - #6 TypeScript 7: `typescript-eslint` does not support TS 7. It builds and tests fine, but linting fails.
    - Both were closed with an explanation; retry when upstream support lands.
  - **Removed `.github/dependabot.yml`,** so no more automatic version-update PRs. Dependabot security-fix PRs were already disabled in the repository settings.
  - **Dependency updates are now manual:** CI still runs govulncheck, npm audit and gitleaks on every push.
- **Files:** `go.mod`, `go.sum`, `web/package.json`, `web/package-lock.json`, `THIRD_PARTY_NOTICES`, `.github/dependabot.yml` (removed), `web/CHANGELOG.md`, `cmd/server/CHANGELOG.md`, `docs/roadmap.md`

### 2026-09-25 — Enhancement — Platform status card shows every platform service
- **Notes:**
  - Owner request: the Caddy edge proxy is a system the platform relies on, so it must appear on the dashboard's "Platform status" card. Applied the same rule to dbr2-worker and dbr2-reposerver, which were missing too.
  - Added `DBR2_READY_HTTP_CHECKS` (non-critical HTTP readiness probes), a Caddy internal health endpoint (`:8090`, not published) and a proxy container HEALTHCHECK. Compose wires proxy, worker and reposerver. The reposerver check reflects storage health, so a stalled NFS mount shows up on the card.
  - Verified on the dev stack:
    - all six checks `ok`;
    - stopping the worker → `worker: unavailable`, overall `degraded` (HTTP 200);
    - restart → `ok`.
  - Unit tests cover config parsing and degraded readiness.
- **Files:** `internal/config`, `internal/api` (readycheck, ops_system), `cmd/server`, `api/openapi.yaml`, `deployments/docker-compose/{Caddyfile,compose.yaml}`, the server/api/deployment changelogs, `docs/stack_info/final_stack.md`, `docs/roadmap.md`

### 2026-09-25 — Fix — Easier retrieval of the dev master admin password
- **Notes:**
  - The owner ran `sudo docker compose cp dbr2-server:… | tar -xO` from the repo root and got "no configuration file provided". Two causes:
    - The dev stack needs both Compose files, from `deployments/docker-compose/`.
    - The stack had been stopped with `make dev-down`, which deletes volumes, so a new password is generated on the next start.
  - Added `make dev-password`, and clarified the deployment README (where to run the command; `sudo` not needed in the `docker` group; `dev-down` resets the password).
- **Files:** `Makefile`, `deployments/docker-compose/README.md`, `deployments/CHANGELOG.md`, `docs/roadmap.md`

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
  - **First GitHub Actions run:** every job passed except govulncheck, which flagged standard-library vulnerabilities because CI resolved `go 1.26` to go1.26.0. **Fix:** pinned `toolchain go1.26.8` in `go.mod` (local builds and images already used 1.26.8). The next run (36161769664) passed every job.
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

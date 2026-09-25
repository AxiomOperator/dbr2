# DBR² — Threat Model

> Aligned with `stack_info/final_stack.md` and `adr/`. Review this document whenever an ADR changes a trust boundary, and log the review in `roadmap.md`.

## Assets (most to least critical)

1. **Repository passwords and storage backend credentials**: full access to all backup data (held only by `dbr2-reposerver` and in escrow).
2. **Backup data**: application data, database dumps and captured secrets.
3. **DBR² CA private key**: can mint agent and reposerver identities.
4. **Maintenance identity**: can delete and restore any recovery point.
5. **Root access on protected hosts**: the agent runs as root and holds the Docker socket.
6. **Master admin account** (local username and password): bypasses the external identity provider (Entra ID).
7. **OIDC client secret and user sessions.**
8. **Platform state** (PostgreSQL): policies, RBAC and the audit log.
9. **Escrow private keys**: held offline by administrators.
10. **Self-backup / Platform Recovery Bundles**: contain items 1, 3, 4 and 7.

## Trust boundaries

```text
[Browser/CLI] ──HTTPS+OIDC──► [dbr2-web / dbr2-server] ──► [PostgreSQL, Temporal, Valkey]
                                      │ Agent Gateway (gRPC+mTLS, agent-initiated)
                                      ▼
                       [dbr2-agent: root on Docker host] ──TLS + Kopia user──► [dbr2-reposerver] ──► [Storage backend]
[dbr2-worker] ──maintenance identity──► [dbr2-reposerver]
```

## Threats and mitigations

| # | Threat | Mitigations | Residual risk |
|---|---|---|---|
| T1 | **Compromised Docker host or agent** reads or destroys other hosts' backups | Per-agent Kopia users with APPEND on their own snapshots, READ on their own policies, no delete (spike-verified, ADR-0002). All snapshots pinned; Kopia retention disabled. Agents never hold repository passwords or storage credentials. DBR² never sends an agent another host's object IDs. S3 Object Lock where available (v2). Optionally one Repository per host. | The agent can read its own host's history. **Spike-found:** object IDs act as read keys across agents, and the HMAC secret gives a **content-existence oracle** (confirming guessed content). Accepted for a single-owner deployment; use one Repository per host to eliminate it. |
| T2 | Compromised host **poisons** backups (ransomware writes encrypted data that later gets backed up) | Immutable retention windows; changed-data anomaly detection (advisory); multiple retained RPs; restore tests | Detection is heuristic |
| T3 | **Rogue agent enrollment** | Registration tokens that are single-use and expire; host approval workflow (Pending until approved); visible identity pinning; audit | An administrator approves a malicious host |
| T4 | **Stolen agent certificate** or Kopia user credential | Per-agent revocation and rotation; certificates bound to agent identity; anomaly alerts (new source IP or identity changes); credentials root-only (`0600`) | Valid until revoked |
| T5 | **Compromised `dbr2-reposerver`** | It is the only holder of repository secrets, so it must be hardened, minimal and isolated; S3 Object Lock limits destruction; offsite or secondary Repositories use separate credentials | Full read access to its Repository |
| T6 | **Compromised control plane** (`dbr2-server` or `dbr2-worker`) issues malicious restores or deletes | RBAC; `restore.production` plus approval workflows; dual authorization for destructive operations; deletion grace period; Object Lock; tamper alerts; audit log shipped externally (SIEM/syslog) | The worker holds the maintenance identity: deletion is possible outside the lock window |
| T7 | **Malicious or careless administrator** | Dual authorization for destructive operations (delete, retention reduction, immutability changes, key rotation); approval for production restore; mandatory reason or change-ticket fields; append-oriented audit log | Two colluding administrators |
| T8 | **Master admin abuse or password guessing** | Username and password required for lockout protection (owner requirement). Argon2id hashing, rate limiting and progressive lockout, optional TOTP (recommended), a notification on every login, audit, and rotation after emergency use. Password reset only as root on the server host (`dbr2 admin reset-master-password`) | Password-only authentication when TOTP is not enabled; mitigated by keeping the console off the public internet |
| T9 | **Secrets exposed** through the UI, API, logs or manifests | Secret detection and masking; `secrets.read` required to reveal; never logged (structured-logging redaction); protected in the Repository by Kopia encryption and in exports by age | Captured `.env` files remain in the backup data by design |
| T10 | **Docker socket misuse** through DBR² | Only the agent holds the socket; no API passes arbitrary Docker commands through; hooks are defined by administrators with `policy.manage` and audited; hook execution is scoped to the Application's containers | Hooks are code execution by design |
| T11 | **Loss of keys** makes backups unrecoverable (availability) | Mandatory key escrow and escrow health checks (ADR-0008) | Every escrow recipient's key lost |
| T12 | **Loss of the control plane** | Self-backup plus Platform Recovery Bundle; the Repository is authoritative (ADR-0003); agent-only and stock-`kopia` recovery paths | — |
| T13 | **Self-backup bundle theft** | age encryption to escrow recipients; stored outside the System Repository with access controls; access audited | — |
| T14 | **Supply chain** (dependencies, images, agent binaries) | Dependency scanning, SBOMs, signed containers and agent releases, pinned Kopia version with a compatibility suite | — |
| T15 | **Network attacker between components** | mTLS for agents; TLS to the reposerver using certificates from the DBR² CA; HTTPS for the web and API; internal services on a private network in the Compose deployment | — |
| T16 | **Temporal UI or API exposure** reveals workflow inputs | Not exposed publicly; administrator-only access; no secrets in workflow inputs (pass references, not values) | — |
| T17 | **Single NAS** (v1.0) is a single point of failure, and ransomware can reach it over NFS | NFS export restricted to the reposerver host; agents never mount it (ADR-0002); scheduled read-only NAS snapshots; Platform Recovery Bundle in a separate export; Veeam as an independent layer | No offsite copy and no object lock in v1.0. The owner accepts this for a single site; S3 and offsite replication are v2 |
| T18 | **Single operator** (ADR-0014): one compromised account can issue destructive operations | Deletion grace period; typed confirmation plus a reason; audit notifications; NAS snapshots; Entra ID conditional access and MFA on the operator's account | Accepted until a second operator exists and approvals are enabled |
| T19 | **Restore silently widens file access** (Kopia drops POSIX ACLs; a 0600 file with an ACL restored with group read/write) or silently skips ownership | Filesystem metadata record reapplied in staging before swap-in; restore with `IgnorePermissionErrors = false`; post-restore fidelity verification (ADR-0006) | — |
| T20 | **Agent abuses repository-server features**: triggering Kopia retention on its own snapshots, forging manifest-tagged snapshots, or filling storage | Pins on every snapshot (retention can't delete them); manifests trusted only from the `maint@dbr2` source, with component sources validated; per-host usage view (logical size of the latest recovery points, Phase 4); quotas later | A host can fill storage until quotas exist |
| T21 | **Spoofed client IP** via a client-supplied `X-Forwarded-For` (evades the login rate limit, falsifies audit source IPs) | The Caddy edge proxy discards client `X-Forwarded-*`; `dbr2-server` trusts `X-Forwarded-For` only from Caddy's fixed address and uses the rightmost untrusted hop; verified end to end (a spoofed `6.6.6.6` was recorded as the real peer). The Next.js `/api` proxy is development-only | Deployments that bypass the edge proxy lose this guarantee |
| T22 | **Leaked registration token** used to enroll a rogue host | Tokens are single-use, expire (1 h to 7 d), hashed at rest, and revocable; every use and failure is audited. The enrolled host stays **pending** until an administrator approves it | An administrator approves the wrong host |
| T23 | **Agent CA private key theft** (can mint agent identities) | Key sealed in PostgreSQL with `DBR2_SECRET_KEY` (both needed); certificates bind identity through a CA-set URI SAN, not the CSR; revocation and status checked on every connection; escrow per ADR-0008 | Compromise of both the database and the secret key |
| T24 | **Discovered secrets** (Compose `.env`, environment variables) exposed through inventory storage, the API or logs | Sealed before storage and bound to the agent; masked in the API and UI; reveal requires `secrets.read` and is audited; never logged | Secrets are revealed by design to holders of `secrets.read` |
| T25 | **Gateway port exposure** (published for agents on other hosts) | TLS 1.3; sessions require a CA-issued client certificate; enrollment requires a valid token; malformed or anonymous sessions are rejected; nothing is served in plain text | No rate limiting on enrollment attempts (tokens have 256 bits of entropy) |
| T26 | **Escrow package theft or loss** (the package holds the repository password) | age-encrypted to the escrow recipients' **public** keys, so a copy is useless without an offline identity; storing it anywhere is safe. Private keys pasted as recipients are rejected. The package is generated, and only stored encrypted, by `dbr2-server`, which never keeps the plaintext password. A Repository stays unusable until an administrator proves decryption with the one-time confirmation code. Downloads are audited | Losing **both** escrow identities *and* the reposerver state makes the Repository unrecoverable (the annual escrow drill, ADR-0008) |
| T27 | **Reposerver management API abuse** (creating Kopia users, granting cross-source READ) | Bound to the deployment network only (not published); requires the internal token (at least 32 characters); never returns passwords; every call made by `dbr2-server` is audited on its side | A holder of the internal token on the deployment network could grant itself read access |
| T28 | **The backup system causes an outage** (application left paused or stopped) | Saga compensation (Resume + post hooks) on failure and cancellation; the agent's journaled **dead-man lease** resumes the application on its own; the application is claimed again only after the lease is checked; critical alerts for auto-resume, failed resume and "not resumed"; Cancel only (never Terminate); Live is the default without hooks (minimal downtime) | A gracefully *stopped* agent keeps the lease and resumes the application when it starts again, not before |
| T29 | **Secrets leaking into workflow history or logs** (Temporal UI exposes inputs and results) | The capture plan carries **redacted** metadata only. Agent and `maint@dbr2` passwords never pass through Temporal (the server hands them to the agent over mTLS; the worker generates its own in memory). The real environment reaches only the encrypted Repository (`containers/<id>.json`, staged by the agent). The reposerver scrubs secrets from Kopia output | Hook output (truncated to 8 KiB) is the hook author's responsibility |
| T30 | **Forged or cross-agent recovery point contents** (an agent points a manifest at another agent's snapshots, or tags its own snapshots as manifests) | Commit (as `maint@dbr2`) verifies that every component snapshot exists, was written by the recovery point's own agent (`agent@<agent ID>`), carries the matching `dbr2-rp` and `dbr2-component` tags and is complete. Reindex trusts only manifests from the `maint@dbr2` source and re-validates component sources | — |

## Security requirements derived from this model (tracked in `roadmap.md`)

- No secrets in Temporal workflow inputs, logs or traces.
- Every privileged action is audited with who, what, when, source IP, target, reason and result.
- Destructive operations support dual authorization, and production restores support approval.
- The agent is hardened: minimal listening ports (none inbound), a systemd sandboxing profile compatible with restore, and signed updates.
- Periodic review: update this document whenever an ADR is added or changed.

# Audit event catalog

The stable audit event types are the SIEM contract (source: `internal/audit/audit.go`).

**Never rename or reuse an event type.** Add a new one and document it here.

Every event records:

- `event_id` (UUID), `occurred_at`, `event_type`
- actor: `actor_user_id`, `actor_display`, `actor_kind` (`user`, `system` or `anonymous`)
- `source_ip`
- target: `target_type`, `target_id`
- `reason`
- `result` (`success`, `failure` or `denied`)
- `details` (JSON; secret-looking keys are redacted)
- optional `before_state` / `after_state`
- `request_id` and `trace_id`

Events are stored in the append-only `audit_events` table, where UPDATE, DELETE and TRUNCATE are rejected by triggers for every role. They are also emitted as structured JSON log lines (`"event":"audit"`) for syslog and SIEM shipping.

| Event type | Meaning |
|---|---|
| `auth.master_admin.bootstrapped` | Master admin created on first start (actor: system). |
| `auth.master_admin.password_reset_offline` | Master admin password reset on the server host (`dbr2-server admin reset-master-password`). |
| `auth.login.succeeded` | Master admin signed in (also enqueues a notification). |
| `auth.login.failed` | Failed master admin sign-in (unknown user, wrong password or wrong/replayed TOTP code). |
| `auth.login.locked` | Sign-in attempted while the account is locked. |
| `auth.login.rate_limited` | Sign-in rejected by the per-IP rate limit. |
| `auth.oidc.login.succeeded` | Entra ID (OIDC) sign-in succeeded. |
| `auth.oidc.login.failed` | OIDC sign-in failed or was denied (state/nonce/token failure, disabled user, no role). |
| `auth.logout` | Session revoked by the user. |
| `auth.password.changed` | Master admin changed the password (result failure when the current password was wrong). |
| `auth.totp.enabled` | TOTP enrolled for the master admin. |
| `auth.totp.disabled` | TOTP disabled for the master admin. |
| `auth.api_token.created` | Personal API token created. |
| `auth.api_token.revoked` | Personal API token revoked. |
| `user.roles.changed` | Manual roles replaced (before/after state recorded). |
| `user.disabled.changed` | User enabled or disabled (before/after state recorded). |
| `rbac.group_mapping.added` | Entra ID group → role mapping added. |
| `rbac.group_mapping.removed` | Entra ID group → role mapping removed. |
| `authz.denied` | Authenticated request refused for a missing permission. |
| `agent.enrolled` | Host enrolled with a registration token (starts pending). |
| `agent.enrollment.failed` | Enrollment refused (invalid, used, revoked or expired token). |
| `agent.approved` | Pending agent approved (before/after status). |
| `agent.suspended` | Agent suspended; its session is ended. |
| `agent.resumed` | Suspended agent resumed. |
| `agent.revoked` | Agent permanently revoked; all certificates revoked. |
| `agent.certificate_renewed` | Agent renewed its certificate (older ones revoked). |
| `agent.registration_token.created` | Registration token created (secret shown once). |
| `agent.registration_token.revoked` | Unused registration token revoked. |
| `agent.discovery.requested` | On-demand discovery workflow started. |
| `application.created` | Manual application created (grouped containers). |
| `application.updated` | Application ownership metadata changed (before/after). |
| `application.deleted` | Manual application deleted. |
| `secrets.revealed` | Secret values revealed (requires secrets.read). |
| `escrow.recipient.added` | Escrow recipient (age public key) registered. |
| `escrow.recipient.removed` | Escrow recipient removed (existing packages unchanged). |
| `repository.created` | Repository initialized; escrow package generated (status `awaiting_escrow`). |
| `repository.escrow.downloaded` | Encrypted escrow package downloaded. |
| `repository.escrow.confirmed` | Confirmation code checked; `failure` for a wrong code, `success` makes the Repository ready. |
| `repository.reindex.requested` | Reindex workflow started. |
| `repository.reindexed` | Index rebuilt from the Repository's manifests (upserted, marked missing). System actor. |
| `backup.requested` | Manual backup workflow started. |
| `backup.completed` | Recovery point committed (Complete or Partial). System actor. |
| `backup.failed` | Backup failed; no recovery point (critical alert). System actor. |
| `backup.settings.updated` | Application backup settings changed (before/after). |
| `host.settings.updated` | Host limits changed (before/after). |
| `application.not_resumed` | Resume failed after its retry budget (critical alert). System actor. |
| `quiesce.auto_resumed` | The agent's dead-man switch resumed an application (critical alert). Agent actor. |
| `quiesce.resume_failed` | The agent could not resume an application (critical alert). Agent actor. |
| `quiesce.lease_warning` | Quiesced longer than 80% of its lease (warning alert). Agent actor. |
| `alert.acknowledged` | An alert was acknowledged. |
| `restore.requested` | Restore requested (reason, target, mode, production flag, components, remaps). |
| `restore.succeeded` | Restore committed after a healthy start (info alert). System actor. |
| `restore.rolled_back` | Restore failed and the previous data and containers were put back (warning alert). System actor. |
| `restore.cancel_requested` | Cancellation of a running restore requested (compensation rolls it back). |
| `policy.created` / `policy.updated` / `policy.deleted` | Protection Policy changed (before/after). |
| `application.policy.assigned` | Application assigned to a policy (or unassigned). |
| `contract.updated` | Recovery Contract set or removed. |
| `contract.violated` / `contract.satisfied` | Contract state changed (critical / info alert). System actor. |
| `backup.skipped` | Scheduled backup skipped because another operation held the application (warning alert). System actor. |
| `recovery_point.delete.scheduled` / `recovery_point.delete.canceled` | Manual deletion scheduled after the grace period, or undone. |
| `recovery_point.deleted` | Recovery point deleted (retention or grace period expired; manifest first, then components). System actor. |
| `repository.delete.scheduled` / `repository.delete.canceled` / `repository.retired` | Repository deletion with grace period; retired when it ends. |
| `verification.requested` | Verification of a Repository or recovery point started. |
| `verification.succeeded` / `verification.failed` | Recovery point verification result (failure: critical alert). System actor. |
| `escrow.unhealthy` | Escrow problems found (recipients, unconfirmed or outdated packages, re-confirmation or drill due). System actor. |
| `repository.escrow.regenerated` | Escrow package re-sealed to the current recipients. |
| `escrow.drill.started` / `escrow.drill.completed` | Escrow drill package created / decrypted and confirmed (`failure` for a wrong code). |
| `restore.failed` | Restore failed before changing anything, or its rollback failed (critical alert). System actor. |
| `notification.channel.created` | Notification channel (email or webhook) created. The webhook secret is never recorded. |
| `notification.channel.updated` | Notification channel changed (before/after; whether the signing secret changed). |
| `notification.channel.deleted` | Notification channel and its delivery history deleted. |
| `notification.channel.tested` | Test notification sent to a channel; `failure` with the error when delivery failed. |
| `settings.smtp.updated` | SMTP relay settings changed (before/after; the password is never recorded). |
| `agent.offline` | An active agent has had no gateway session and no contact for 10 minutes (warning alert, once per outage). System actor, result `failure`. |
| `agent.online` | An agent reported offline is back (info alert). System actor. |
| `platform.backup.requested` | Platform Protection run started manually (ADR-0008). |
| `platform.backup.succeeded` | Platform Recovery Bundle written to the System Repository and the bundle directory (file name, SHA-256, size, snapshot, path). System actor. |
| `platform.backup.partial` | Platform backup completed with a gap: one target failed, no System Repository is designated, or a reposerver's state was missing (warning alert). System actor. |
| `platform.backup.failed` | Platform backup failed; no bundle was stored (critical alert). System actor, result `failure`. |
| `repository.system.designated` | A Repository was designated the System Repository (previous one recorded). |

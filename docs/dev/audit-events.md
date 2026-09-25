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

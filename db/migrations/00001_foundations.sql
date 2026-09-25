-- SPDX-License-Identifier: Apache-2.0
-- Phase 1 foundations: organizations (multi-tenancy seam), identities,
-- credentials, RBAC assignments, sessions, API tokens, OIDC state,
-- append-only audit log and notification outbox.

-- +goose Up

CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- Single-tenant today: every row belongs to the default organization. The
-- org_id columns keep MSP / multi-tenant mode a data change, not a redesign.
INSERT INTO organizations (id, slug, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'default', 'Default');

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id),
    kind          text NOT NULL CHECK (kind IN ('master_admin', 'oidc')),
    username      text NOT NULL,
    display_name  text NOT NULL,
    email         text,
    oidc_provider text,
    oidc_issuer   text,
    oidc_subject  text,
    disabled      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT users_oidc_identity_present CHECK (
        (kind = 'oidc') = (oidc_provider IS NOT NULL AND oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL)
    )
);
CREATE UNIQUE INDEX users_one_master_admin ON users (org_id) WHERE kind = 'master_admin';
CREATE UNIQUE INDEX users_oidc_identity ON users (org_id, oidc_issuer, oidc_subject) WHERE kind = 'oidc';

CREATE TABLE local_credentials (
    user_id                 uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash           text NOT NULL,
    password_changed_at     timestamptz NOT NULL DEFAULT now(),
    must_change_password    boolean NOT NULL DEFAULT false,
    failed_attempts         integer NOT NULL DEFAULT 0,
    locked_until            timestamptz,
    totp_secret_enc         bytea,
    totp_pending_secret_enc bytea,
    totp_enabled            boolean NOT NULL DEFAULT false,
    totp_last_step          bigint NOT NULL DEFAULT 0
);

CREATE TABLE user_roles (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN (
                   'administrator', 'backup_administrator', 'restore_operator',
                   'application_operator', 'auditor', 'read_only')),
    source     text NOT NULL CHECK (source IN ('manual', 'oidc_group')),
    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role, source)
);

CREATE TABLE oidc_group_mappings (
    org_id     uuid NOT NULL REFERENCES organizations (id),
    provider   text NOT NULL,
    group_id   text NOT NULL,
    role       text NOT NULL CHECK (role IN (
                   'administrator', 'backup_administrator', 'restore_operator',
                   'application_operator', 'auditor', 'read_only')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, provider, group_id, role)
);

CREATE TABLE sessions (
    id           bytea PRIMARY KEY, -- SHA-256 of the session token; the token itself is never stored
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    auth_method  text NOT NULL CHECK (auth_method IN ('password', 'oidc')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    source_ip    inet,
    user_agent   text,
    revoked_at   timestamptz
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE, -- SHA-256 of the token
    prefix       text NOT NULL,         -- first characters, for display
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX api_tokens_user ON api_tokens (user_id);

CREATE TABLE oidc_auth_requests (
    state         text PRIMARY KEY,
    provider      text NOT NULL,
    nonce         text NOT NULL,
    code_verifier text NOT NULL,
    return_to     text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL
);

CREATE TABLE audit_events (
    seq           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id      uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id),
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    event_type    text NOT NULL,
    actor_user_id uuid, -- deliberately no FK: audit rows outlive users
    actor_display text NOT NULL,
    actor_kind    text NOT NULL CHECK (actor_kind IN ('user', 'system', 'anonymous')),
    source_ip     inet,
    target_type   text,
    target_id     text,
    reason        text,
    result        text NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    before_state  jsonb,
    after_state   jsonb,
    request_id    text,
    trace_id      text
);
CREATE INDEX audit_events_org_seq ON audit_events (org_id, seq DESC);
CREATE INDEX audit_events_type ON audit_events (org_id, event_type, seq DESC);

-- The audit log is append-only (threat model T6/T7): updates, deletes and
-- truncation are rejected for every role, including the table owner.
-- +goose StatementBegin
CREATE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% rejected)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER audit_events_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
CREATE TRIGGER audit_events_no_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_append_only();

CREATE TABLE notification_outbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organizations (id),
    event_type   text NOT NULL,
    severity     text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz
);
CREATE INDEX notification_outbox_pending ON notification_outbox (created_at) WHERE delivered_at IS NULL;

-- +goose Down
DROP TABLE notification_outbox;
DROP TRIGGER audit_events_no_truncate ON audit_events;
DROP TRIGGER audit_events_no_update_delete ON audit_events;
DROP TABLE audit_events;
DROP FUNCTION audit_events_append_only();
DROP TABLE oidc_auth_requests;
DROP TABLE api_tokens;
DROP TABLE sessions;
DROP TABLE oidc_group_mappings;
DROP TABLE user_roles;
DROP TABLE local_credentials;
DROP TABLE users;
DROP TABLE organizations;

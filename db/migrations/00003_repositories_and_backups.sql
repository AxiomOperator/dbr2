-- SPDX-License-Identifier: Apache-2.0
-- Phase 4: Repositories, key escrow, agent repository access, recovery-point
-- index, backup settings, host limits and the notification outbox.

-- +goose Up

-- age recipients that every escrowed secret is encrypted to (ADR-0008).
CREATE TABLE escrow_recipients (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations (id),
    name        text NOT NULL,
    public_key  text NOT NULL, -- age1… (or an age plugin recipient)
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    removed_at  timestamptz
);
CREATE UNIQUE INDEX escrow_recipients_key ON escrow_recipients (org_id, public_key) WHERE removed_at IS NULL;

-- A Repository is served by one dbr2-reposerver (ADR-0002). The repository
-- password is never stored here: it lives in the reposerver's state and in
-- the age-encrypted escrow package.
CREATE TABLE repositories (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                uuid NOT NULL REFERENCES organizations (id),
    name                  text NOT NULL,
    description           text NOT NULL DEFAULT '',
    backend               text NOT NULL CHECK (backend IN ('nfs', 'filesystem')),
    management_url        text NOT NULL,
    server_url            text NOT NULL,  -- what agents connect to
    internal_server_url   text NOT NULL DEFAULT '', -- what dbr2-worker connects to ('' = server_url)
    cert_sha256           text NOT NULL DEFAULT '',
    kopia_repository_id   text,
    splitter              text,
    status                text NOT NULL DEFAULT 'awaiting_escrow'
                          CHECK (status IN ('awaiting_escrow', 'ready', 'unavailable', 'retired')),
    is_default            boolean NOT NULL DEFAULT false,
    escrow_package        bytea,          -- age ciphertext, safe to store
    escrow_recipient_ids  uuid[] NOT NULL DEFAULT '{}',
    escrow_confirm_hash   bytea,          -- SHA-256 of the confirmation code inside the package
    escrow_generated_at   timestamptz,
    escrow_confirmed_at   timestamptz,
    escrow_confirmed_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    last_reindex_at       timestamptz,
    created_by            uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE UNIQUE INDEX repositories_default ON repositories (org_id) WHERE is_default AND status <> 'retired';

-- Kopia user of an agent on a Repository (agent@<agent_id>). The password is
-- handed to the reposerver and the agent once and not stored.
CREATE TABLE agent_repository_access (
    agent_id        uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    repository_id   uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    username        text NOT NULL,
    hostname        text NOT NULL,
    configured_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, repository_id)
);

-- Recovery-point index (ADR-0003): a rebuildable cache of the manifests in
-- the Repositories. application_id and agent_id are not foreign keys because
-- reindexing may find recovery points of deleted applications.
CREATE TABLE recovery_points (
    id                    text PRIMARY KEY, -- rp_<ULID>
    org_id                uuid NOT NULL REFERENCES organizations (id),
    repository_id         uuid NOT NULL REFERENCES repositories (id),
    application_id        uuid NOT NULL,
    application_name      text NOT NULL,
    agent_id              uuid NOT NULL,
    hostname              text NOT NULL,
    state                 text NOT NULL DEFAULT 'pending'
                          CHECK (state IN ('pending', 'committed', 'failed', 'missing', 'deleting')),
    status                text CHECK (status IN ('complete', 'partial')),
    verification          text NOT NULL DEFAULT 'unverified'
                          CHECK (verification IN ('unverified', 'verified', 'verification_failed')),
    consistency_mode      text NOT NULL CHECK (consistency_mode IN ('live', 'quiesced', 'offline')),
    consistency_point     timestamptz,
    crash_consistent_only boolean NOT NULL DEFAULT false,
    trigger               text NOT NULL DEFAULT 'manual',
    requested_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    workflow_id           text NOT NULL,
    run_id                text NOT NULL,
    size_bytes            bigint NOT NULL DEFAULT 0,
    component_count       integer NOT NULL DEFAULT 0,
    manifest              jsonb,
    manifest_snapshot_id  text,
    error                 text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    committed_at          timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recovery_points_app ON recovery_points (application_id, created_at DESC);
CREATE INDEX recovery_points_repo ON recovery_points (repository_id, state);

-- Per-application backup settings (ADR-0005 defaults when absent).
CREATE TABLE application_backup_settings (
    application_id      uuid PRIMARY KEY REFERENCES applications (id) ON DELETE CASCADE,
    repository_id       uuid REFERENCES repositories (id) ON DELETE SET NULL,
    consistency_mode    text CHECK (consistency_mode IN ('live', 'quiesced', 'offline')), -- NULL = automatic
    max_quiesce_seconds integer NOT NULL DEFAULT 3600 CHECK (max_quiesce_seconds BETWEEN 60 AND 86400),
    pre_hooks           jsonb NOT NULL DEFAULT '[]',
    post_hooks          jsonb NOT NULL DEFAULT '[]',
    optional_components text[] NOT NULL DEFAULT '{}', -- best-effort (e.g. caches)
    excluded_components text[] NOT NULL DEFAULT '{}',
    updated_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Per-host limits: concurrency and the backup window (scheduled backups
-- wait for it, e.g. to stay clear of Veeam job windows).
CREATE TABLE host_settings (
    agent_id               uuid PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    max_concurrent_jobs    integer NOT NULL DEFAULT 2 CHECK (max_concurrent_jobs BETWEEN 1 AND 16),
    backup_window_start    integer CHECK (backup_window_start BETWEEN 0 AND 1439), -- minute of day
    backup_window_end      integer CHECK (backup_window_end BETWEEN 0 AND 1439),
    backup_window_timezone text NOT NULL DEFAULT 'UTC',
    updated_by             uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CHECK ((backup_window_start IS NULL) = (backup_window_end IS NULL))
);

-- Alerts: the Phase 1 outbox gains a target and acknowledgement (channels
-- arrive with notifications; the console lists open alerts).
ALTER TABLE notification_outbox
    ADD COLUMN target_type     text,
    ADD COLUMN target_id       text,
    ADD COLUMN message         text NOT NULL DEFAULT '',
    ADD COLUMN acknowledged_at timestamptz,
    ADD COLUMN acknowledged_by uuid REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX notification_outbox_open ON notification_outbox (org_id, created_at DESC) WHERE acknowledged_at IS NULL;

-- +goose Down
DROP INDEX notification_outbox_open;
ALTER TABLE notification_outbox DROP COLUMN target_type, DROP COLUMN target_id, DROP COLUMN message,
    DROP COLUMN acknowledged_at, DROP COLUMN acknowledged_by;
DROP TABLE host_settings;
DROP TABLE application_backup_settings;
DROP TABLE recovery_points;
DROP TABLE agent_repository_access;
DROP TABLE repositories;
DROP TABLE escrow_recipients;

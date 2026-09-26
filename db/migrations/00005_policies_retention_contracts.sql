-- SPDX-License-Identifier: Apache-2.0
-- Phase 7: Protection Policies (schedules, retention), recovery contracts,
-- deletion grace periods. Phase 8: database backup strategy. Phase 9:
-- verification results and escrow drills.

-- +goose Up

CREATE TABLE protection_policies (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations (id),
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    -- 5-field cron expression evaluated in timezone (presets are stored as cron).
    schedule         text NOT NULL,
    timezone         text NOT NULL DEFAULT 'UTC',
    enabled          boolean NOT NULL DEFAULT true,
    consistency_mode text CHECK (consistency_mode IN ('live', 'quiesced', 'offline')), -- NULL = application setting
    repository_id    uuid REFERENCES repositories (id) ON DELETE SET NULL,              -- NULL = application/default
    keep_last        integer NOT NULL DEFAULT 7  CHECK (keep_last >= 1),
    keep_hourly      integer NOT NULL DEFAULT 0  CHECK (keep_hourly >= 0),
    keep_daily       integer NOT NULL DEFAULT 14 CHECK (keep_daily >= 0),
    keep_weekly      integer NOT NULL DEFAULT 8  CHECK (keep_weekly >= 0),
    keep_monthly     integer NOT NULL DEFAULT 12 CHECK (keep_monthly >= 0),
    keep_yearly      integer NOT NULL DEFAULT 0  CHECK (keep_yearly >= 0),
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

ALTER TABLE applications ADD COLUMN policy_id uuid REFERENCES protection_policies (id) ON DELETE SET NULL;

-- Recovery Contract, v1.0 subset (maximum RPO, required components).
CREATE TABLE recovery_contracts (
    application_id      uuid PRIMARY KEY REFERENCES applications (id) ON DELETE CASCADE,
    max_rpo_minutes     integer CHECK (max_rpo_minutes > 0),
    required_components text[] NOT NULL DEFAULT '{}',
    state               text NOT NULL DEFAULT 'unknown' CHECK (state IN ('satisfied', 'violated', 'unknown')),
    state_reasons       text[] NOT NULL DEFAULT '{}',
    evaluated_at        timestamptz,
    violated_since      timestamptz,
    updated_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Deletion grace (ADR-0014) and retention.
ALTER TABLE recovery_points DROP CONSTRAINT recovery_points_state_check;
ALTER TABLE recovery_points ADD CONSTRAINT recovery_points_state_check
    CHECK (state IN ('pending', 'committed', 'failed', 'missing', 'deleting', 'deleted'));
ALTER TABLE recovery_points
    ADD COLUMN delete_after        timestamptz,
    ADD COLUMN delete_reason       text,
    ADD COLUMN delete_requested_by uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN deleted_at          timestamptz,
    ADD COLUMN verified_at         timestamptz,
    ADD COLUMN verification_details jsonb;
CREATE INDEX recovery_points_delete_after ON recovery_points (delete_after) WHERE delete_after IS NOT NULL;

ALTER TABLE repositories DROP CONSTRAINT repositories_status_check;
ALTER TABLE repositories ADD CONSTRAINT repositories_status_check
    CHECK (status IN ('awaiting_escrow', 'ready', 'unavailable', 'pending_deletion', 'retired'));
ALTER TABLE repositories
    ADD COLUMN delete_after      timestamptz,
    ADD COLUMN delete_reason     text,
    ADD COLUMN last_verified_at  timestamptz;

-- Phase 8: database strategy per application.
ALTER TABLE application_backup_settings
    ADD COLUMN database_strategy text NOT NULL DEFAULT 'both' CHECK (database_strategy IN ('logical', 'volume', 'both'));

-- Phase 9: escrow drills (ADR-0008: annual drill proves the identities work).
CREATE TABLE escrow_drills (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id),
    package       bytea NOT NULL,           -- age ciphertext of a random drill secret
    code_hash     bytea NOT NULL,
    recipient_ids uuid[] NOT NULL,
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz,
    completed_by  uuid REFERENCES users (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE escrow_drills;
ALTER TABLE application_backup_settings DROP COLUMN database_strategy;
ALTER TABLE repositories DROP COLUMN delete_after, DROP COLUMN delete_reason, DROP COLUMN last_verified_at;
ALTER TABLE repositories DROP CONSTRAINT repositories_status_check;
ALTER TABLE repositories ADD CONSTRAINT repositories_status_check
    CHECK (status IN ('awaiting_escrow', 'ready', 'unavailable', 'retired'));
DROP INDEX recovery_points_delete_after;
ALTER TABLE recovery_points DROP COLUMN delete_after, DROP COLUMN delete_reason, DROP COLUMN delete_requested_by,
    DROP COLUMN deleted_at, DROP COLUMN verified_at, DROP COLUMN verification_details;
ALTER TABLE recovery_points DROP CONSTRAINT recovery_points_state_check;
ALTER TABLE recovery_points ADD CONSTRAINT recovery_points_state_check
    CHECK (state IN ('pending', 'committed', 'failed', 'missing', 'deleting'));
DROP TABLE recovery_contracts;
ALTER TABLE applications DROP COLUMN policy_id;
DROP TABLE protection_policies;

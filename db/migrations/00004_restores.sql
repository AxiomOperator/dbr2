-- SPDX-License-Identifier: Apache-2.0
-- Phase 5: restore history (every attempt is recorded, including failures).

-- +goose Up

CREATE TABLE restore_runs (
    id                    text PRIMARY KEY, -- rs_<ULID>
    org_id                uuid NOT NULL REFERENCES organizations (id),
    recovery_point_id     text NOT NULL REFERENCES recovery_points (id),
    repository_id         uuid NOT NULL REFERENCES repositories (id),
    source_application_id uuid NOT NULL,
    application_name      text NOT NULL,
    source_agent_id       uuid NOT NULL,
    source_hostname       text NOT NULL,
    target_agent_id       uuid NOT NULL REFERENCES agents (id),
    target_hostname       text NOT NULL,
    -- The application overwritten in place (NULL = none existed on the target).
    target_application_id uuid,
    mode                  text NOT NULL CHECK (mode IN ('in_place', 'alternate_host')),
    production            boolean NOT NULL DEFAULT false,
    components            text[] NOT NULL,
    path_remaps           jsonb NOT NULL DEFAULT '[]',
    preview               jsonb NOT NULL,
    reason                text,
    requested_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    requested_by_display  text NOT NULL,
    state                 text NOT NULL DEFAULT 'requested'
                          CHECK (state IN ('requested', 'running', 'succeeded', 'failed', 'rolled_back')),
    step                  text,
    result                jsonb,
    error                 text,
    workflow_id           text,
    run_id                text,
    grant_id              text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    started_at            timestamptz,
    finished_at           timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX restore_runs_app ON restore_runs (source_application_id, created_at DESC);
CREATE INDEX restore_runs_rp ON restore_runs (recovery_point_id);

-- +goose Down
DROP TABLE restore_runs;

-- SPDX-License-Identifier: Apache-2.0
-- Phase 7: notification channels (email, webhook), per-channel deliveries fed
-- from notification_outbox, platform settings (SMTP) and agent offline alerts.

-- +goose Up

CREATE TABLE notification_channels (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations (id),
    name             text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('email', 'webhook')),
    enabled          boolean NOT NULL DEFAULT true,
    -- email: {"to": ["ops@example.com"]}; webhook: {"url": "https://…"}
    config           jsonb NOT NULL DEFAULT '{}',
    -- Webhook HMAC secret, sealed with DBR2_SECRET_KEY (AD dbr2:notify:<id>).
    secret_enc       bytea,
    -- Subscribed event types (exact names or prefix wildcards like backup.*);
    -- empty = every event.
    events           text[] NOT NULL DEFAULT '{}',
    min_severity     text NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info', 'warning', 'critical')),
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_delivery_at timestamptz,
    last_error       text,
    UNIQUE (org_id, name)
);

-- One row per (outbox notification, channel). Claimed with
-- FOR UPDATE SKIP LOCKED, so several dbr2-server instances can dispatch.
CREATE TABLE notification_deliveries (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    notification_id bigint NOT NULL REFERENCES notification_outbox (id),
    channel_id      uuid NOT NULL REFERENCES notification_channels (id) ON DELETE CASCADE,
    state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'failed')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (notification_id, channel_id)
);
CREATE INDEX notification_deliveries_due ON notification_deliveries (next_attempt_at) WHERE state = 'pending';
CREATE INDEX notification_deliveries_channel ON notification_deliveries (channel_id, created_at DESC);

-- Platform-wide settings by key (e.g. smtp). Secrets are sealed separately.
CREATE TABLE platform_settings (
    org_id     uuid NOT NULL REFERENCES organizations (id),
    key        text NOT NULL,
    value      jsonb NOT NULL DEFAULT '{}',
    secret_enc bytea,
    updated_by uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key)
);

-- Set when agent.offline was raised for the current outage; cleared with
-- agent.online (once per outage).
ALTER TABLE agents ADD COLUMN offline_alerted_at timestamptz;

-- +goose Down
ALTER TABLE agents DROP COLUMN offline_alerted_at;
DROP TABLE platform_settings;
DROP TABLE notification_deliveries;
DROP TABLE notification_channels;

-- SPDX-License-Identifier: Apache-2.0
-- Phase 2 (agents, enrollment, gateway) and Phase 3 (discovery inventory,
-- applications).

-- +goose Up

-- Certificate authorities. The private key is sealed with DBR2_SECRET_KEY
-- (AES-256-GCM); escrow arrives with ADR-0008.
CREATE TABLE pki_authorities (
    name        text PRIMARY KEY,
    org_id      uuid NOT NULL REFERENCES organizations (id),
    cert_der    bytea NOT NULL,
    key_enc     bytea NOT NULL,
    fingerprint text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                uuid NOT NULL REFERENCES organizations (id),
    hostname              text NOT NULL,
    status                text NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending', 'active', 'suspended', 'revoked')),
    status_reason         text,
    status_changed_at     timestamptz NOT NULL DEFAULT now(),
    status_changed_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    enrolled_at           timestamptz NOT NULL DEFAULT now(),
    approved_at           timestamptz,
    agent_version         text NOT NULL,
    protocol_version      text NOT NULL,
    os_release            text,
    architecture          text,
    cert_serial           text,
    cert_fingerprint      text,
    cert_not_after        timestamptz,
    last_seen_at          timestamptz,
    last_latency_ms       integer,
    docker_reachable      boolean,
    docker_version        text,
    health_error          text,
    registration_token_id uuid
);
CREATE INDEX agents_org ON agents (org_id, hostname);

CREATE TABLE agent_registration_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id),
    token_hash    bytea NOT NULL UNIQUE, -- SHA-256; the token is shown once
    prefix        text NOT NULL,
    description   text NOT NULL,
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    used_at       timestamptz,
    used_by_agent uuid REFERENCES agents (id) ON DELETE SET NULL,
    revoked_at    timestamptz
);

-- Every issued agent certificate (revocation and renewal history).
CREATE TABLE agent_certificates (
    serial      text PRIMARY KEY,
    agent_id    uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    fingerprint text NOT NULL,
    not_before  timestamptz NOT NULL,
    not_after   timestamptz NOT NULL,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX agent_certificates_agent ON agent_certificates (agent_id);

-- Session lease (ADR-0001): which gateway instance holds the agent's stream.
CREATE TABLE agent_sessions (
    agent_id          uuid PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    session_id        text NOT NULL,
    gateway_instance  text NOT NULL,
    remote_addr       text,
    connected_at      timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at timestamptz NOT NULL DEFAULT now()
);

-- Server-side record of dispatched commands (visibility and audit; the
-- agent's local journal is authoritative for execution).
CREATE TABLE agent_commands (
    command_id  text PRIMARY KEY,
    agent_id    uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    kind        text NOT NULL,
    state       text NOT NULL,
    error       text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_commands_agent ON agent_commands (agent_id, created_at DESC);

-- Latest discovery inventory per agent (internal/inventory schema). Secret
-- values are sealed before storage.
CREATE TABLE inventory_snapshots (
    agent_id       uuid PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    org_id         uuid NOT NULL REFERENCES organizations (id),
    schema_version integer NOT NULL,
    collected_at   timestamptz NOT NULL,
    received_at    timestamptz NOT NULL DEFAULT now(),
    data           jsonb NOT NULL
);

-- Applications: the unit of protection (docs/domain_model.md).
CREATE TABLE applications (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid NOT NULL REFERENCES organizations (id),
    agent_id          uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    key               text NOT NULL, -- compose:<project> | container:<name> | manual:<id>
    kind              text NOT NULL CHECK (kind IN ('compose', 'container', 'manual')),
    name              text NOT NULL,
    display_name      text,
    owner             text,
    environment       text CHECK (environment IN ('production', 'staging', 'development', 'test', 'other')),
    criticality       text CHECK (criticality IN ('critical', 'high', 'medium', 'low')),
    manual_containers text[] NOT NULL DEFAULT '{}',
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at      timestamptz NOT NULL DEFAULT now(),
    missing_since     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, key)
);
CREATE INDEX applications_org ON applications (org_id, name);

-- +goose Down
DROP TABLE applications;
DROP TABLE inventory_snapshots;
DROP TABLE agent_commands;
DROP TABLE agent_sessions;
DROP TABLE agent_certificates;
DROP TABLE agent_registration_tokens;
DROP TABLE agents;
DROP TABLE pki_authorities;

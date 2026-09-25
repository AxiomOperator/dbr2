-- SPDX-License-Identifier: Apache-2.0

-- name: GetAuthority :one
SELECT * FROM pki_authorities WHERE name = $1;

-- name: CreateAuthority :exec
INSERT INTO pki_authorities (name, org_id, cert_der, key_enc, fingerprint)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO NOTHING;

-- name: CreateRegistrationToken :one
INSERT INTO agent_registration_tokens (org_id, token_hash, prefix, description, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, prefix, description, created_at, expires_at;

-- name: ListRegistrationTokens :many
SELECT id, prefix, description, created_by, created_at, expires_at, used_at, used_by_agent, revoked_at
FROM agent_registration_tokens WHERE org_id = $1 ORDER BY created_at DESC LIMIT 200;

-- name: RevokeRegistrationToken :execrows
UPDATE agent_registration_tokens SET revoked_at = now()
WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL AND used_at IS NULL;

-- name: ClaimRegistrationToken :one
-- Single use: the conditional update makes concurrent claims race-safe.
UPDATE agent_registration_tokens SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
RETURNING id, org_id;

-- name: SetRegistrationTokenAgent :exec
UPDATE agent_registration_tokens SET used_by_agent = $2 WHERE id = $1;

-- name: CreateAgent :one
INSERT INTO agents (org_id, hostname, agent_version, protocol_version, os_release, architecture, registration_token_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents WHERE id = $1;

-- name: ListAgents :many
SELECT * FROM agents WHERE org_id = $1 ORDER BY hostname, enrolled_at;

-- name: SetAgentCertificate :exec
UPDATE agents SET cert_serial = $2, cert_fingerprint = $3, cert_not_after = $4 WHERE id = $1;

-- name: InsertAgentCertificate :exec
INSERT INTO agent_certificates (serial, agent_id, fingerprint, not_before, not_after)
VALUES ($1, $2, $3, $4, $5);

-- name: GetAgentCertificate :one
SELECT * FROM agent_certificates WHERE serial = $1;

-- name: RevokeAgentCertificates :exec
UPDATE agent_certificates SET revoked_at = now() WHERE agent_id = $1 AND revoked_at IS NULL;

-- name: RevokeAgentCertificatesExcept :exec
UPDATE agent_certificates SET revoked_at = now() WHERE agent_id = $1 AND serial <> $2 AND revoked_at IS NULL;

-- name: SetAgentStatus :one
UPDATE agents SET status = $2, status_reason = $3, status_changed_at = now(), status_changed_by = $4,
    approved_at = CASE WHEN $2 = 'active' AND approved_at IS NULL THEN now() ELSE approved_at END
WHERE id = $1 AND org_id = $5
RETURNING *;

-- name: UpdateAgentHello :exec
UPDATE agents SET agent_version = $2, protocol_version = $3, os_release = $4, architecture = $5,
    hostname = $6, last_seen_at = now()
WHERE id = $1;

-- name: UpdateAgentHealth :exec
UPDATE agents SET docker_reachable = $2, docker_version = $3, health_error = $4, last_seen_at = now()
WHERE id = $1;

-- name: UpdateAgentLatency :exec
UPDATE agents SET last_latency_ms = $2, last_seen_at = now() WHERE id = $1;

-- name: UpsertAgentSession :exec
INSERT INTO agent_sessions (agent_id, session_id, gateway_instance, remote_addr)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_id) DO UPDATE SET session_id = EXCLUDED.session_id, gateway_instance = EXCLUDED.gateway_instance,
    remote_addr = EXCLUDED.remote_addr, connected_at = now(), last_heartbeat_at = now();

-- name: TouchAgentSession :exec
UPDATE agent_sessions SET last_heartbeat_at = now() WHERE agent_id = $1 AND session_id = $2;

-- name: DeleteAgentSession :exec
DELETE FROM agent_sessions WHERE agent_id = $1 AND session_id = $2;

-- name: ListAgentSessions :many
SELECT * FROM agent_sessions;

-- name: UpsertAgentCommand :exec
INSERT INTO agent_commands (command_id, agent_id, kind, state, error)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (command_id) DO UPDATE SET state = EXCLUDED.state, error = EXCLUDED.error, updated_at = now();

-- name: ListAgentCommands :many
SELECT * FROM agent_commands WHERE agent_id = $1 ORDER BY created_at DESC LIMIT $2;

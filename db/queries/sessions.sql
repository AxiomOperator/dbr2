-- SPDX-License-Identifier: Apache-2.0

-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, auth_method, expires_at, source_ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetActiveSession :one
SELECT s.id, s.user_id, s.auth_method, s.expires_at, s.last_seen_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.id = $1 AND s.revoked_at IS NULL AND s.expires_at > now() AND NOT u.disabled;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() - interval '7 days';

-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, prefix, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, name, prefix, created_at, expires_at, last_used_at, revoked_at;

-- name: GetActiveAPIToken :one
SELECT t.id, t.user_id FROM api_tokens t JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND t.revoked_at IS NULL
  AND (t.expires_at IS NULL OR t.expires_at > now()) AND NOT u.disabled;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListUserAPITokens :many
SELECT id, user_id, name, prefix, created_at, expires_at, last_used_at, revoked_at
FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIToken :execrows
UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: CreateOIDCAuthRequest :exec
INSERT INTO oidc_auth_requests (state, provider, nonce, code_verifier, return_to, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ConsumeOIDCAuthRequest :one
DELETE FROM oidc_auth_requests WHERE state = $1 AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredOIDCAuthRequests :execrows
DELETE FROM oidc_auth_requests WHERE expires_at < now();

-- name: RevokeUserSessionsExcept :exec
UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL;

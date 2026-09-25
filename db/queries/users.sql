-- SPDX-License-Identifier: Apache-2.0

-- name: GetMasterAdmin :one
SELECT * FROM users WHERE org_id = $1 AND kind = 'master_admin';

-- name: CreateMasterAdmin :one
INSERT INTO users (org_id, kind, username, display_name)
VALUES ($1, 'master_admin', $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByOIDC :one
SELECT * FROM users
WHERE org_id = $1 AND kind = 'oidc' AND oidc_issuer = $2 AND oidc_subject = $3;

-- name: CreateOIDCUser :one
INSERT INTO users (org_id, kind, username, display_name, email, oidc_provider, oidc_issuer, oidc_subject)
VALUES ($1, 'oidc', $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateOIDCUserProfile :exec
UPDATE users SET username = $2, display_name = $3, email = $4, updated_at = now()
WHERE id = $1;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users WHERE org_id = $1 ORDER BY kind, lower(display_name);

-- name: SetUserDisabled :exec
UPDATE users SET disabled = $2, updated_at = now() WHERE id = $1 AND kind <> 'master_admin';

-- SPDX-License-Identifier: Apache-2.0

-- name: ListEscrowRecipients :many
SELECT * FROM escrow_recipients WHERE org_id = $1 AND removed_at IS NULL ORDER BY created_at;

-- name: CreateEscrowRecipient :one
INSERT INTO escrow_recipients (org_id, name, public_key, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RemoveEscrowRecipient :execrows
UPDATE escrow_recipients SET removed_at = now() WHERE id = $1 AND org_id = $2 AND removed_at IS NULL;

-- name: CreateRepository :one
INSERT INTO repositories (org_id, name, description, backend, management_url, server_url, internal_server_url, cert_sha256,
    kopia_repository_id, splitter, status, is_default, escrow_package, escrow_recipient_ids,
    escrow_confirm_hash, escrow_generated_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'awaiting_escrow', $11, $12, $13, $14, now(), $15)
RETURNING *;

-- name: ListRepositories :many
SELECT * FROM repositories WHERE org_id = $1 ORDER BY name;

-- name: GetRepository :one
SELECT * FROM repositories WHERE id = $1 AND org_id = $2;

-- name: GetRepositoryByID :one
SELECT * FROM repositories WHERE id = $1;

-- name: GetDefaultRepository :one
SELECT * FROM repositories WHERE org_id = $1 AND is_default AND status <> 'retired';

-- name: ConfirmRepositoryEscrow :one
UPDATE repositories SET escrow_confirmed_at = now(), escrow_confirmed_by = $3,
    status = CASE WHEN status = 'awaiting_escrow' THEN 'ready' ELSE status END, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: UpdateRepositoryConnection :exec
UPDATE repositories SET cert_sha256 = $2, kopia_repository_id = coalesce($3, kopia_repository_id), updated_at = now()
WHERE id = $1;

-- name: SetRepositoryReindexed :exec
UPDATE repositories SET last_reindex_at = now(), updated_at = now() WHERE id = $1;

-- name: GetAgentRepositoryAccess :one
SELECT * FROM agent_repository_access WHERE agent_id = $1 AND repository_id = $2;

-- name: UpsertAgentRepositoryAccess :exec
INSERT INTO agent_repository_access (agent_id, repository_id, username, hostname)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_id, repository_id) DO UPDATE SET username = EXCLUDED.username,
    hostname = EXCLUDED.hostname, configured_at = now();

-- name: DeleteAgentRepositoryAccessForAgent :exec
DELETE FROM agent_repository_access WHERE agent_id = $1;

-- name: ListAgentRepositoryAccessForAgent :many
SELECT * FROM agent_repository_access WHERE agent_id = $1;

-- name: RepositoryUsageByHost :many
-- Logical size of the latest committed recovery point of every application,
-- summed per host (physical usage is not attributable under deduplication).
SELECT agent_id, hostname, count(*)::int AS applications, sum(size_bytes)::bigint AS latest_bytes
FROM (
    SELECT DISTINCT ON (application_id) application_id, agent_id, hostname, size_bytes
    FROM recovery_points
    WHERE repository_id = $1 AND state = 'committed'
    ORDER BY application_id, created_at DESC
) latest
GROUP BY agent_id, hostname
ORDER BY hostname;

-- SPDX-License-Identifier: Apache-2.0

-- name: CreatePlatformBackup :one
INSERT INTO platform_backups (id, org_id, trigger, requested_by, workflow_id, run_id, repository_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPlatformBackup :one
SELECT * FROM platform_backups WHERE id = $1;

-- name: ListPlatformBackups :many
SELECT * FROM platform_backups WHERE org_id = $1 ORDER BY started_at DESC LIMIT $2;

-- name: GetRunningPlatformBackup :one
SELECT * FROM platform_backups WHERE org_id = $1 AND state = 'running' ORDER BY started_at DESC LIMIT 1;

-- name: AbandonRunningPlatformBackups :execrows
UPDATE platform_backups SET state = 'failed', finished_at = now(),
    error = 'abandoned: the run did not finish (worker or server restarted)'
WHERE org_id = $1 AND state = 'running' AND started_at < $2;

-- name: SetPlatformBackupManifest :exec
UPDATE platform_backups SET manifest = $2 WHERE id = $1;

-- name: FinishPlatformBackup :one
UPDATE platform_backups SET state = $2, finished_at = now(), size_bytes = $3, sha256 = $4, file_name = $5,
    snapshot_id = $6, repository_id = coalesce($7, repository_id), bundle_path = $8, error = $9
WHERE id = $1 AND state = 'running'
RETURNING *;

-- name: GetSystemRepository :one
SELECT * FROM repositories WHERE org_id = $1 AND is_system AND status <> 'retired';

-- name: ClearSystemRepository :exec
UPDATE repositories SET is_system = false, updated_at = now() WHERE org_id = $1 AND is_system;

-- name: SetSystemRepository :one
UPDATE repositories SET is_system = true, updated_at = now() WHERE id = $1 AND org_id = $2
RETURNING *;

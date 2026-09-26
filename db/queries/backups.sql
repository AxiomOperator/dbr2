-- SPDX-License-Identifier: Apache-2.0

-- name: CreateRecoveryPoint :one
INSERT INTO recovery_points (id, org_id, repository_id, application_id, application_name, agent_id, hostname,
    consistency_mode, trigger, requested_by, workflow_id, run_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (id) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: GetPendingRecoveryPointForRun :one
SELECT * FROM recovery_points WHERE workflow_id = $1 AND run_id = $2 AND state = 'pending'
ORDER BY created_at DESC LIMIT 1;

-- name: GetRecoveryPoint :one
SELECT * FROM recovery_points WHERE id = $1 AND org_id = $2;

-- name: GetRecoveryPointByID :one
SELECT * FROM recovery_points WHERE id = $1;

-- name: CommitRecoveryPoint :one
UPDATE recovery_points SET state = 'committed', status = $2, consistency_mode = $3, consistency_point = $4,
    crash_consistent_only = $5, size_bytes = $6, component_count = $7, manifest = $8,
    manifest_snapshot_id = $9, error = NULL, committed_at = coalesce(committed_at, now()), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FailRecoveryPoint :one
UPDATE recovery_points SET state = 'failed', error = $2, updated_at = now()
WHERE id = $1 AND state = 'pending'
RETURNING *;

-- name: UpsertIndexedRecoveryPoint :exec
-- Reindexing (ADR-0003): the Repository wins.
INSERT INTO recovery_points (id, org_id, repository_id, application_id, application_name, agent_id, hostname,
    state, status, consistency_mode, consistency_point, crash_consistent_only, trigger, workflow_id, run_id,
    size_bytes, component_count, manifest, manifest_snapshot_id, created_at, committed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'committed', $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19)
ON CONFLICT (id) DO UPDATE SET state = 'committed', status = EXCLUDED.status,
    repository_id = EXCLUDED.repository_id, consistency_mode = EXCLUDED.consistency_mode,
    consistency_point = EXCLUDED.consistency_point, crash_consistent_only = EXCLUDED.crash_consistent_only,
    size_bytes = EXCLUDED.size_bytes, component_count = EXCLUDED.component_count, manifest = EXCLUDED.manifest,
    manifest_snapshot_id = EXCLUDED.manifest_snapshot_id, error = NULL,
    committed_at = coalesce(recovery_points.committed_at, EXCLUDED.committed_at), updated_at = now();

-- name: MarkMissingRecoveryPoints :execrows
UPDATE recovery_points SET state = 'missing', updated_at = now()
WHERE repository_id = $1 AND state = 'committed' AND NOT (id = ANY(@present_ids::text[]));

-- name: ListRecoveryPoints :many
SELECT * FROM recovery_points
WHERE org_id = $1
  AND (sqlc.narg(application_id)::uuid IS NULL OR application_id = sqlc.narg(application_id))
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state))
ORDER BY created_at DESC
LIMIT $2;

-- name: LastCommittedRecoveryPoint :one
SELECT * FROM recovery_points WHERE application_id = $1 AND state = 'committed'
ORDER BY created_at DESC LIMIT 1;

-- name: GetApplicationBackupSettings :one
SELECT * FROM application_backup_settings WHERE application_id = $1;

-- name: UpsertApplicationBackupSettings :one
INSERT INTO application_backup_settings (application_id, repository_id, consistency_mode, max_quiesce_seconds,
    pre_hooks, post_hooks, optional_components, excluded_components, updated_by, database_strategy)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (application_id) DO UPDATE SET repository_id = EXCLUDED.repository_id,
    consistency_mode = EXCLUDED.consistency_mode, max_quiesce_seconds = EXCLUDED.max_quiesce_seconds,
    pre_hooks = EXCLUDED.pre_hooks, post_hooks = EXCLUDED.post_hooks,
    optional_components = EXCLUDED.optional_components, excluded_components = EXCLUDED.excluded_components,
    database_strategy = EXCLUDED.database_strategy, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: GetHostSettings :one
SELECT * FROM host_settings WHERE agent_id = $1;

-- name: UpsertHostSettings :one
INSERT INTO host_settings (agent_id, max_concurrent_jobs, backup_window_start, backup_window_end,
    backup_window_timezone, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (agent_id) DO UPDATE SET max_concurrent_jobs = EXCLUDED.max_concurrent_jobs,
    backup_window_start = EXCLUDED.backup_window_start, backup_window_end = EXCLUDED.backup_window_end,
    backup_window_timezone = EXCLUDED.backup_window_timezone, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: InsertNotification :exec
INSERT INTO notification_outbox (org_id, severity, event_type, target_type, target_id, message, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListNotifications :many
SELECT * FROM notification_outbox WHERE org_id = $1
  AND (NOT @open_only::boolean OR acknowledged_at IS NULL)
ORDER BY created_at DESC LIMIT $2;

-- name: AcknowledgeNotification :execrows
UPDATE notification_outbox SET acknowledged_at = now(), acknowledged_by = $3
WHERE id = $1 AND org_id = $2 AND acknowledged_at IS NULL;

-- name: LatestCommittedPerApplication :many
SELECT DISTINCT ON (application_id) * FROM recovery_points
WHERE org_id = $1 AND state = 'committed'
ORDER BY application_id, created_at DESC;

-- name: LatestAttemptPerApplication :many
SELECT DISTINCT ON (application_id) * FROM recovery_points
WHERE org_id = $1
ORDER BY application_id, created_at DESC;

-- name: ListApplicationBackupSettings :many
SELECT s.* FROM application_backup_settings s JOIN applications a ON a.id = s.application_id
WHERE a.org_id = $1;

-- SPDX-License-Identifier: Apache-2.0

-- name: CreateRestoreRun :one
INSERT INTO restore_runs (id, org_id, recovery_point_id, repository_id, source_application_id, application_name,
    source_agent_id, source_hostname, target_agent_id, target_hostname, target_application_id, mode, production, components,
    path_remaps, preview, reason, requested_by, requested_by_display)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
RETURNING *;

-- name: GetRestoreRun :one
SELECT * FROM restore_runs WHERE id = $1 AND org_id = $2;

-- name: GetRestoreRunByID :one
SELECT * FROM restore_runs WHERE id = $1;

-- name: ListRestoreRuns :many
SELECT * FROM restore_runs
WHERE org_id = $1
  AND (sqlc.narg(application_id)::uuid IS NULL OR source_application_id = sqlc.narg(application_id)
       OR target_application_id = sqlc.narg(application_id))
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state))
ORDER BY created_at DESC
LIMIT $2;

-- name: SetRestoreWorkflow :exec
UPDATE restore_runs SET workflow_id = $2, run_id = $3, updated_at = now() WHERE id = $1;

-- name: StartRestoreRun :one
UPDATE restore_runs SET state = 'running', started_at = coalesce(started_at, now()), workflow_id = $2, run_id = $3,
    updated_at = now()
WHERE id = $1 AND state IN ('requested', 'running')
RETURNING *;

-- name: SetRestoreStep :exec
UPDATE restore_runs SET step = $2, updated_at = now() WHERE id = $1 AND state = 'running';

-- name: SetRestoreGrant :exec
UPDATE restore_runs SET grant_id = $2, updated_at = now() WHERE id = $1;

-- name: FinishRestoreRun :one
UPDATE restore_runs SET state = $2, result = coalesce($3, result), error = $4, finished_at = now(), updated_at = now()
WHERE id = $1 AND state IN ('requested', 'running')
RETURNING *;

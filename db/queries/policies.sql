-- SPDX-License-Identifier: Apache-2.0

-- name: ListPolicies :many
SELECT p.*, (SELECT count(*) FROM applications a WHERE a.policy_id = p.id)::int AS application_count
FROM protection_policies p WHERE p.org_id = $1 ORDER BY p.name;

-- name: GetPolicy :one
SELECT * FROM protection_policies WHERE id = $1 AND org_id = $2;

-- name: CreatePolicy :one
INSERT INTO protection_policies (org_id, name, description, schedule, timezone, enabled, consistency_mode, repository_id,
    keep_last, keep_hourly, keep_daily, keep_weekly, keep_monthly, keep_yearly, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: UpdatePolicy :one
UPDATE protection_policies SET name = $3, description = $4, schedule = $5, timezone = $6, enabled = $7,
    consistency_mode = $8, repository_id = $9, keep_last = $10, keep_hourly = $11, keep_daily = $12,
    keep_weekly = $13, keep_monthly = $14, keep_yearly = $15, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeletePolicy :execrows
DELETE FROM protection_policies WHERE id = $1 AND org_id = $2;

-- name: SetApplicationPolicy :execrows
UPDATE applications SET policy_id = $3, updated_at = now() WHERE id = $1 AND org_id = $2;

-- name: ListPolicyAssignments :many
SELECT a.id AS application_id, a.name, a.agent_id, a.policy_id, a.missing_since, p.schedule, p.timezone, p.enabled
FROM applications a JOIN protection_policies p ON p.id = a.policy_id
WHERE a.org_id = $1;

-- name: ListApplicationsForPolicy :many
SELECT a.id, a.name, a.agent_id FROM applications a WHERE a.policy_id = $1 ORDER BY a.name;

-- name: GetContract :one
SELECT * FROM recovery_contracts WHERE application_id = $1;

-- name: UpsertContract :one
INSERT INTO recovery_contracts (application_id, max_rpo_minutes, required_components, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (application_id) DO UPDATE SET max_rpo_minutes = EXCLUDED.max_rpo_minutes,
    required_components = EXCLUDED.required_components, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: DeleteContract :execrows
DELETE FROM recovery_contracts WHERE application_id = $1;

-- name: ListContracts :many
SELECT c.*, a.name AS application_name, a.agent_id FROM recovery_contracts c
JOIN applications a ON a.id = c.application_id WHERE a.org_id = $1 ORDER BY a.name;

-- name: SetContractState :exec
UPDATE recovery_contracts SET state = $2, state_reasons = $3, evaluated_at = now(),
    violated_since = CASE WHEN $2 = 'violated' THEN coalesce(violated_since, now()) ELSE NULL END
WHERE application_id = $1;

-- name: ListCommittedForRetention :many
SELECT id, application_id, created_at, state, delete_after FROM recovery_points
WHERE org_id = $1 AND application_id = $2 AND state = 'committed'
ORDER BY created_at DESC;

-- name: ListDueDeletions :many
SELECT * FROM recovery_points WHERE org_id = $1 AND delete_after IS NOT NULL AND delete_after <= now()
  AND state IN ('committed', 'missing', 'deleting')
ORDER BY delete_after LIMIT $2;

-- name: ScheduleRecoveryPointDeletion :one
UPDATE recovery_points SET delete_after = $3, delete_reason = $4, delete_requested_by = $5, updated_at = now()
WHERE id = $1 AND org_id = $2 AND state IN ('committed', 'missing') AND delete_after IS NULL
RETURNING *;

-- name: CancelRecoveryPointDeletion :one
UPDATE recovery_points SET delete_after = NULL, delete_reason = NULL, delete_requested_by = NULL, updated_at = now()
WHERE id = $1 AND org_id = $2 AND state IN ('committed', 'missing') AND delete_after IS NOT NULL
RETURNING *;

-- name: MarkRecoveryPointDeleting :exec
UPDATE recovery_points SET state = 'deleting', updated_at = now() WHERE id = $1 AND state IN ('committed', 'missing', 'deleting');

-- name: MarkRecoveryPointDeleted :exec
UPDATE recovery_points SET state = 'deleted', deleted_at = now(), updated_at = now() WHERE id = $1;

-- name: SetRecoveryPointVerification :exec
UPDATE recovery_points SET verification = $2, verified_at = now(), verification_details = $3, updated_at = now() WHERE id = $1;

-- name: SetRepositoryVerified :exec
UPDATE repositories SET last_verified_at = now(), updated_at = now() WHERE id = $1;

-- name: ScheduleRepositoryDeletion :one
UPDATE repositories SET status = 'pending_deletion', delete_after = $3, delete_reason = $4, is_default = false, updated_at = now()
WHERE id = $1 AND org_id = $2 AND status IN ('ready', 'awaiting_escrow', 'unavailable')
RETURNING *;

-- name: CancelRepositoryDeletion :one
UPDATE repositories SET status = CASE WHEN escrow_confirmed_at IS NULL THEN 'awaiting_escrow' ELSE 'ready' END,
    delete_after = NULL, delete_reason = NULL, updated_at = now()
WHERE id = $1 AND org_id = $2 AND status = 'pending_deletion'
RETURNING *;

-- name: RetireDueRepositories :many
UPDATE repositories SET status = 'retired', updated_at = now()
WHERE org_id = $1 AND status = 'pending_deletion' AND delete_after <= now()
RETURNING *;

-- name: CreateEscrowDrill :one
INSERT INTO escrow_drills (org_id, package, code_hash, recipient_ids, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEscrowDrill :one
SELECT * FROM escrow_drills WHERE id = $1 AND org_id = $2;

-- name: CompleteEscrowDrill :one
UPDATE escrow_drills SET completed_at = now(), completed_by = $3 WHERE id = $1 AND org_id = $2 AND completed_at IS NULL
RETURNING *;

-- name: LastCompletedEscrowDrill :one
SELECT * FROM escrow_drills WHERE org_id = $1 AND completed_at IS NOT NULL ORDER BY completed_at DESC LIMIT 1;

-- name: ListEscrowDrills :many
SELECT * FROM escrow_drills WHERE org_id = $1 ORDER BY created_at DESC LIMIT 20;

-- name: RegenerateRepositoryEscrow :one
UPDATE repositories SET escrow_package = $3, escrow_recipient_ids = $4, escrow_confirm_hash = $5,
    escrow_generated_at = now(), escrow_confirmed_at = NULL, escrow_confirmed_by = NULL, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: ListVerificationCandidates :many
SELECT id, manifest FROM recovery_points
WHERE repository_id = $1 AND state = 'committed' AND manifest IS NOT NULL
ORDER BY verified_at NULLS FIRST, created_at DESC
LIMIT $2;

-- SPDX-License-Identifier: Apache-2.0

-- name: InsertAuditEvent :one
INSERT INTO audit_events (
    org_id, event_type, actor_user_id, actor_display, actor_kind, source_ip,
    target_type, target_id, reason, result, details, before_state, after_state,
    request_id, trace_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
RETURNING seq, event_id, occurred_at;

-- name: ListAuditEvents :many
SELECT * FROM audit_events
WHERE org_id = @org_id
  AND (sqlc.narg('before_seq')::bigint IS NULL OR seq < sqlc.narg('before_seq'))
  AND (sqlc.narg('event_type')::text IS NULL OR event_type = sqlc.narg('event_type'))
ORDER BY seq DESC
LIMIT @max_rows;

-- name: EnqueueNotification :exec
INSERT INTO notification_outbox (org_id, event_type, severity, payload)
VALUES ($1, $2, $3, $4);

-- SPDX-License-Identifier: Apache-2.0

-- ---- Channels ----------------------------------------------------------------

-- name: CreateNotificationChannel :one
INSERT INTO notification_channels (id, org_id, name, kind, enabled, config, secret_enc, events, min_severity, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels WHERE org_id = $1 ORDER BY name;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = $1 AND org_id = $2;

-- name: GetNotificationChannelByID :one
SELECT * FROM notification_channels WHERE id = $1;

-- name: UpdateNotificationChannel :one
UPDATE notification_channels SET name = @name, enabled = @enabled, config = @config, events = @events,
    min_severity = @min_severity,
    secret_enc = CASE WHEN @set_secret::boolean THEN sqlc.narg(secret_enc)::bytea ELSE secret_enc END,
    updated_at = now()
WHERE id = @id AND org_id = @org_id
RETURNING *;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE id = $1 AND org_id = $2;

-- name: ListEnabledNotificationChannels :many
SELECT * FROM notification_channels WHERE enabled ORDER BY org_id, id;

-- name: RecordNotificationChannelResult :exec
UPDATE notification_channels
SET last_delivery_at = CASE WHEN @ok::boolean THEN now() ELSE last_delivery_at END,
    last_error = sqlc.narg(last_error)
WHERE id = @id;

-- ---- Fan-out -------------------------------------------------------------------

-- name: ClaimUndeliveredNotifications :many
-- Run inside a transaction: the claimed rows stay locked until it commits, and
-- other dispatchers skip them.
SELECT * FROM notification_outbox WHERE delivered_at IS NULL
ORDER BY id LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: InsertNotificationDelivery :exec
INSERT INTO notification_deliveries (notification_id, channel_id) VALUES ($1, $2)
ON CONFLICT (notification_id, channel_id) DO NOTHING;

-- name: MarkNotificationsFannedOut :exec
UPDATE notification_outbox SET delivered_at = now() WHERE id = ANY(@ids::bigint[]) AND delivered_at IS NULL;

-- name: GetOutboxNotification :one
SELECT * FROM notification_outbox WHERE id = $1;

-- ---- Deliveries ----------------------------------------------------------------

-- name: ClaimDueDeliveries :many
-- Claims due deliveries with a lease: attempts is incremented and the next
-- attempt pushed past the lease, so a crashed sender's work is retried and
-- concurrent dispatchers never claim the same row.
WITH due AS (
    SELECT dd.id FROM notification_deliveries dd
    WHERE dd.state = 'pending' AND dd.next_attempt_at <= now()
    ORDER BY dd.next_attempt_at, dd.id
    LIMIT @batch
    FOR UPDATE SKIP LOCKED
)
UPDATE notification_deliveries d
SET attempts = d.attempts + 1, next_attempt_at = now() + make_interval(secs => @lease_seconds::double precision)
FROM due WHERE d.id = due.id
RETURNING d.id, d.notification_id, d.channel_id, d.attempts;

-- name: MarkDeliverySent :exec
UPDATE notification_deliveries SET state = 'sent', sent_at = now(), last_error = NULL WHERE id = $1;

-- name: MarkDeliveryRetry :exec
UPDATE notification_deliveries SET last_error = $2, next_attempt_at = $3 WHERE id = $1 AND state = 'pending';

-- name: MarkDeliveryFailed :exec
UPDATE notification_deliveries SET state = 'failed', last_error = $2 WHERE id = $1;

-- name: ListChannelDeliveries :many
SELECT d.id, d.notification_id, d.channel_id, d.state, d.attempts, d.next_attempt_at, d.last_error, d.sent_at, d.created_at,
       o.event_type, o.severity, o.message
FROM notification_deliveries d
JOIN notification_outbox o ON o.id = d.notification_id
WHERE d.channel_id = $1
ORDER BY d.created_at DESC, d.id DESC
LIMIT $2;

-- ---- Platform settings --------------------------------------------------------

-- name: GetPlatformSetting :one
SELECT * FROM platform_settings WHERE org_id = $1 AND key = $2;

-- name: UpsertPlatformSetting :one
INSERT INTO platform_settings (org_id, key, value, secret_enc, updated_by)
VALUES (@org_id, @key, @value, sqlc.narg(secret_enc)::bytea, @updated_by)
ON CONFLICT (org_id, key) DO UPDATE SET value = EXCLUDED.value,
    secret_enc = CASE WHEN @set_secret::boolean THEN EXCLUDED.secret_enc ELSE platform_settings.secret_enc END,
    updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- ---- Agent presence -----------------------------------------------------------

-- name: ListActiveAgentPresence :many
-- last_activity: the latest sign of life (heartbeat, health report) or the
-- last status change (an agent just approved or resumed is not "offline").
SELECT id, org_id, hostname, last_seen_at, offline_alerted_at,
       GREATEST(COALESCE(last_seen_at, status_changed_at), status_changed_at)::timestamptz AS last_activity
FROM agents WHERE status = 'active';

-- name: MarkAgentOfflineAlerted :execrows
-- Conditional: exactly one dispatcher raises agent.offline per outage.
UPDATE agents SET offline_alerted_at = now() WHERE id = $1 AND offline_alerted_at IS NULL;

-- name: ClearAgentOfflineAlerted :execrows
UPDATE agents SET offline_alerted_at = NULL WHERE id = $1 AND offline_alerted_at IS NOT NULL;

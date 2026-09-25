-- SPDX-License-Identifier: Apache-2.0

-- name: UpsertInventorySnapshot :exec
INSERT INTO inventory_snapshots (agent_id, org_id, schema_version, collected_at, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id) DO UPDATE SET schema_version = EXCLUDED.schema_version,
    collected_at = EXCLUDED.collected_at, data = EXCLUDED.data, received_at = now()
WHERE inventory_snapshots.collected_at <= EXCLUDED.collected_at;

-- name: GetInventorySnapshot :one
SELECT * FROM inventory_snapshots WHERE agent_id = $1;

-- name: ListApplications :many
SELECT a.*, g.hostname FROM applications a JOIN agents g ON g.id = a.agent_id
WHERE a.org_id = $1 ORDER BY lower(coalesce(a.display_name, a.name)), g.hostname;

-- name: ListAgentApplications :many
SELECT * FROM applications WHERE agent_id = $1;

-- name: GetApplication :one
SELECT a.*, g.hostname FROM applications a JOIN agents g ON g.id = a.agent_id
WHERE a.id = $1 AND a.org_id = $2;

-- name: UpsertDiscoveredApplication :exec
INSERT INTO applications (org_id, agent_id, key, kind, name, last_seen_at, missing_since)
VALUES ($1, $2, $3, $4, $5, $6, NULL)
ON CONFLICT (agent_id, key) DO UPDATE SET name = EXCLUDED.name, last_seen_at = EXCLUDED.last_seen_at,
    missing_since = NULL, updated_at = now();

-- name: MarkApplicationsMissing :exec
UPDATE applications SET missing_since = coalesce(missing_since, $3), updated_at = now()
WHERE agent_id = $1 AND kind <> 'manual' AND NOT (key = ANY(@present_keys::text[])) AND missing_since IS NULL
  AND $2::boolean;

-- name: CreateManualApplication :one
INSERT INTO applications (org_id, agent_id, key, kind, name, manual_containers)
VALUES ($1, $2, $3, 'manual', $4, $5)
RETURNING *;

-- name: UpdateApplicationMetadata :one
UPDATE applications SET display_name = $3, owner = $4, environment = $5, criticality = $6, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: UpdateManualApplication :one
UPDATE applications SET name = $3, manual_containers = $4, updated_at = now()
WHERE id = $1 AND org_id = $2 AND kind = 'manual'
RETURNING *;

-- name: DeleteManualApplication :execrows
DELETE FROM applications WHERE id = $1 AND org_id = $2 AND kind = 'manual';

-- name: DeleteContainerApplications :exec
-- Standalone container applications claimed by a manual application.
DELETE FROM applications WHERE agent_id = $1 AND kind = 'container' AND key = ANY(@keys::text[]);

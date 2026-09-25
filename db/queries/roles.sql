-- SPDX-License-Identifier: Apache-2.0

-- name: ListUserRoles :many
SELECT role, source FROM user_roles WHERE user_id = $1 ORDER BY role, source;

-- name: AddUserRole :exec
INSERT INTO user_roles (user_id, role, source, granted_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: DeleteUserRolesBySource :exec
DELETE FROM user_roles WHERE user_id = $1 AND source = $2;

-- name: DeleteUserRole :exec
DELETE FROM user_roles WHERE user_id = $1 AND role = $2 AND source = $3;

-- name: ListGroupMappings :many
SELECT * FROM oidc_group_mappings WHERE org_id = $1 ORDER BY provider, group_id, role;

-- name: ListGroupMappingsForGroups :many
SELECT role FROM oidc_group_mappings
WHERE org_id = $1 AND provider = $2 AND group_id = ANY(@group_ids::text[]);

-- name: AddGroupMapping :exec
INSERT INTO oidc_group_mappings (org_id, provider, group_id, role)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: DeleteGroupMapping :execrows
DELETE FROM oidc_group_mappings WHERE org_id = $1 AND provider = $2 AND group_id = $3 AND role = $4;

// SPDX-License-Identifier: Apache-2.0

// Package rbac defines DBR² roles and permissions (final_stack → Authorization).
// Roles are fixed in code; assignments live in PostgreSQL (user_roles) and
// can come from Entra ID group mappings. The master admin always holds the
// Administrator role and cannot be demoted (final_stack → Authentication).
package rbac

import "sort"

// Permission is a granular capability, e.g. "backup.execute".
type Permission string

// Permissions (final_stack → Authorization). user.read / user.manage were
// added in Phase 1 for user, role and group-mapping administration.
const (
	HostRead          Permission = "host.read"
	HostManage        Permission = "host.manage"
	ApplicationRead   Permission = "application.read"
	ApplicationManage Permission = "application.manage"
	BackupRead        Permission = "backup.read"
	BackupExecute     Permission = "backup.execute"
	BackupDelete      Permission = "backup.delete"
	RestoreRead       Permission = "restore.read"
	RestoreExecute    Permission = "restore.execute"
	RestoreProduction Permission = "restore.production"
	RepositoryRead    Permission = "repository.read"
	RepositoryManage  Permission = "repository.manage"
	PolicyRead        Permission = "policy.read"
	PolicyManage      Permission = "policy.manage"
	AuditRead         Permission = "audit.read"
	SecretsRead       Permission = "secrets.read"
	UserRead          Permission = "user.read"
	UserManage        Permission = "user.manage"
)

// All lists every permission.
var All = []Permission{
	HostRead, HostManage, ApplicationRead, ApplicationManage,
	BackupRead, BackupExecute, BackupDelete,
	RestoreRead, RestoreExecute, RestoreProduction,
	RepositoryRead, RepositoryManage, PolicyRead, PolicyManage,
	AuditRead, SecretsRead, UserRead, UserManage,
}

// Role is a named permission set.
type Role string

// Roles (final_stack → Authorization). The string values match the
// user_roles.role CHECK constraint in db/migrations.
const (
	Administrator       Role = "administrator"
	BackupAdministrator Role = "backup_administrator"
	RestoreOperator     Role = "restore_operator"
	ApplicationOperator Role = "application_operator"
	Auditor             Role = "auditor"
	ReadOnly            Role = "read_only"
)

// RoleInfo describes a role for the API and UI.
type RoleInfo struct {
	Role        Role
	DisplayName string
	Description string
	Permissions []Permission
}

var readAll = []Permission{HostRead, ApplicationRead, BackupRead, RestoreRead, RepositoryRead, PolicyRead}

var roles = map[Role]RoleInfo{
	Administrator: {Administrator, "Administrator",
		"Full control of DBR², including users, secrets and production restores.", All},
	BackupAdministrator: {BackupAdministrator, "Backup Administrator",
		"Manages hosts, applications, policies, repositories and backups; may run production restores.",
		[]Permission{HostRead, HostManage, ApplicationRead, ApplicationManage,
			BackupRead, BackupExecute, BackupDelete, RestoreRead, RestoreExecute, RestoreProduction,
			RepositoryRead, RepositoryManage, PolicyRead, PolicyManage, AuditRead}},
	RestoreOperator: {RestoreOperator, "Restore Operator",
		"Runs non-production restores and reviews recovery points.",
		append(append([]Permission{}, readAll...), RestoreExecute)},
	ApplicationOperator: {ApplicationOperator, "Application Operator",
		"Backs up and restores applications (delegated per application in v2).",
		[]Permission{ApplicationRead, BackupRead, BackupExecute, RestoreRead, RestoreExecute}},
	Auditor: {Auditor, "Auditor",
		"Read-only access to everything including the audit log and users.",
		append(append([]Permission{}, readAll...), AuditRead, UserRead)},
	ReadOnly: {ReadOnly, "Read Only", "Read-only access to inventory and protection state.", readAll},
}

// Lookup returns a role definition.
func Lookup(r Role) (RoleInfo, bool) {
	info, ok := roles[r]
	return info, ok
}

// Roles returns every role, sorted by name.
func Roles() []RoleInfo {
	out := make([]RoleInfo, 0, len(roles))
	for _, info := range roles {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

// Valid reports whether r is a defined role.
func Valid(r Role) bool { _, ok := roles[r]; return ok }

// PermissionSet is an effective set of permissions.
type PermissionSet map[Permission]struct{}

// Resolve computes the effective permissions of a set of roles.
func Resolve(rs []Role) PermissionSet {
	set := PermissionSet{}
	for _, r := range rs {
		for _, p := range roles[r].Permissions {
			set[p] = struct{}{}
		}
	}
	return set
}

// Has reports whether the set contains p.
func (s PermissionSet) Has(p Permission) bool { _, ok := s[p]; return ok }

// Sorted returns the permissions as a sorted string slice.
func (s PermissionSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for p := range s {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

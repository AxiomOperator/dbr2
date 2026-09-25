// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// RoleAssignment is one role held by a user and where it comes from.
type RoleAssignment struct {
	Role   string `json:"role" example:"backup_administrator"`
	Source string `json:"source" enum:"manual,oidc_group,master_admin" doc:"manual grants, Entra ID group mapping, or the implicit master admin role."`
}

// User is a DBR² user.
type User struct {
	ID          string           `json:"id" format:"uuid"`
	Kind        string           `json:"kind" enum:"master_admin,oidc"`
	Username    string           `json:"username"`
	DisplayName string           `json:"display_name"`
	Email       *string          `json:"email"`
	Disabled    bool             `json:"disabled"`
	LastLoginAt *time.Time       `json:"last_login_at"`
	Roles       []RoleAssignment `json:"roles"`
}

// Role describes a role and its permissions.
type Role struct {
	Role        string   `json:"role"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// GroupMapping maps an identity-provider group to a DBR² role.
type GroupMapping struct {
	Provider string `json:"provider" example:"entra"`
	GroupID  string `json:"group_id" minLength:"1" maxLength:"256" doc:"Entra ID group object ID."`
	Role     string `json:"role" example:"auditor"`
}

type usersOutput struct {
	Body struct {
		Items []User `json:"items"`
	}
}

type rolesOutput struct {
	Body struct {
		Items []Role `json:"items"`
	}
}

type setRolesInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Roles  []string `json:"roles" doc:"Complete list of manually granted roles (group-mapped roles are managed by Entra ID)."`
		Reason string   `json:"reason" minLength:"1" maxLength:"500" doc:"Why the change is made (stored in the audit log)."`
	}
}

type setStatusInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason" minLength:"1" maxLength:"500"`
	}
}

type mappingsOutput struct {
	Body struct {
		Items []GroupMapping `json:"items"`
	}
}

type mappingInput struct{ Body GroupMapping }

func registerUsers(a huma.API, d *Deps) {
	huma.Register(a, op("list-users", http.MethodGet, "/api/v1/users", "Users",
		"List users", "Lists every user with role assignments.", rbac.UserRead),
		func(ctx context.Context, _ *struct{}) (*usersOutput, error) {
			rows, err := d.Auth.ListUsers(ctx)
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &usersOutput{}
			out.Body.Items = make([]User, 0, len(rows))
			for _, u := range rows {
				roles := make([]RoleAssignment, 0, len(u.Roles))
				for _, r := range u.Roles {
					roles = append(roles, RoleAssignment{Role: r.Role, Source: r.Source})
				}
				out.Body.Items = append(out.Body.Items, User{ID: u.ID.String(), Kind: u.Kind, Username: u.Username,
					DisplayName: u.DisplayName, Email: u.Email, Disabled: u.Disabled, LastLoginAt: u.LastLoginAt, Roles: roles})
			}
			return out, nil
		})

	huma.Register(a, op("list-roles", http.MethodGet, "/api/v1/roles", "Users",
		"List roles", "Lists the built-in roles and their permissions.", rbac.UserRead),
		func(context.Context, *struct{}) (*rolesOutput, error) {
			out := &rolesOutput{}
			for _, r := range rbac.Roles() {
				perms := make([]string, len(r.Permissions))
				for i, p := range r.Permissions {
					perms[i] = string(p)
				}
				out.Body.Items = append(out.Body.Items, Role{Role: string(r.Role), DisplayName: r.DisplayName, Description: r.Description, Permissions: perms})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("set-user-roles", http.MethodPut, "/api/v1/users/{id}/roles", "Users",
		"Set a user's manual roles",
		"Replaces the user's manually granted roles. The master admin cannot be changed, and you cannot remove your own administrator role.",
		rbac.UserManage, http.StatusBadRequest, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *setRolesInput) (*struct{}, error) {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid id")
			}
			roles := make([]rbac.Role, len(in.Body.Roles))
			for i, r := range in.Body.Roles {
				roles[i] = rbac.Role(r)
			}
			return nil, d.fail(ctx, d.Auth.SetManualRoles(ctx, principal(ctx), id, roles, in.Body.Reason, metaFrom(ctx)))
		})

	huma.Register(a, withStatus(op("set-user-status", http.MethodPut, "/api/v1/users/{id}/status", "Users",
		"Enable or disable a user", "Disabling a user revokes all of their sessions. The master admin cannot be disabled.",
		rbac.UserManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *setStatusInput) (*struct{}, error) {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid id")
			}
			return nil, d.fail(ctx, d.Auth.SetUserDisabled(ctx, principal(ctx), id, in.Body.Disabled, in.Body.Reason, metaFrom(ctx)))
		})

	huma.Register(a, op("list-group-mappings", http.MethodGet, "/api/v1/oidc/group-mappings", "Users",
		"List Entra ID group mappings", "Lists identity-provider group → role mappings. Roles are synced at each sign-in.", rbac.UserRead),
		func(ctx context.Context, _ *struct{}) (*mappingsOutput, error) {
			rows, err := d.Auth.ListGroupMappings(ctx)
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &mappingsOutput{}
			out.Body.Items = make([]GroupMapping, 0, len(rows))
			for _, m := range rows {
				out.Body.Items = append(out.Body.Items, GroupMapping{Provider: m.Provider, GroupID: m.GroupID, Role: m.Role})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("add-group-mapping", http.MethodPost, "/api/v1/oidc/group-mappings", "Users",
		"Add a group mapping", "Maps an Entra ID group (object ID) to a DBR² role.", rbac.UserManage, http.StatusBadRequest), http.StatusNoContent),
		func(ctx context.Context, in *mappingInput) (*struct{}, error) {
			return nil, d.fail(ctx, d.Auth.AddGroupMapping(ctx, principal(ctx), in.Body.Provider, in.Body.GroupID, rbac.Role(in.Body.Role), metaFrom(ctx)))
		})

	huma.Register(a, withStatus(op("remove-group-mapping", http.MethodPost, "/api/v1/oidc/group-mappings/remove", "Users",
		"Remove a group mapping", "Removes a group → role mapping; affected users lose the role at their next sign-in.",
		rbac.UserManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *mappingInput) (*struct{}, error) {
			return nil, d.fail(ctx, d.Auth.RemoveGroupMapping(ctx, principal(ctx), in.Body.Provider, in.Body.GroupID, rbac.Role(in.Body.Role), metaFrom(ctx)))
		})
}

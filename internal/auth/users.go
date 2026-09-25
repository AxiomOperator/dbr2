// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Errors for user administration.
var (
	ErrMasterAdminImmutable = errors.New("the master admin's roles and status cannot be changed")
	ErrInvalidRole          = errors.New("unknown role")
	ErrSelfLockout          = errors.New("you cannot remove your own administrator access or disable yourself")
)

// UserWithRoles is a user and its role assignments.
type UserWithRoles struct {
	store.User
	Roles []store.ListUserRolesRow
}

// ListUsers returns every user in the organization with role assignments.
func (s *Service) ListUsers(ctx context.Context) ([]UserWithRoles, error) {
	users, err := s.q.ListUsers(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]UserWithRoles, 0, len(users))
	for _, u := range users {
		roles, err := s.q.ListUserRoles(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		if u.Kind == KindMasterAdmin {
			roles = append([]store.ListUserRolesRow{{Role: string(rbac.Administrator), Source: "master_admin"}}, roles...)
		}
		out = append(out, UserWithRoles{User: u, Roles: roles})
	}
	return out, nil
}

func (s *Service) targetUser(ctx context.Context, q *store.Queries, id uuid.UUID) (store.User, error) {
	u, err := q.GetUserByID(ctx, id)
	if isNoRows(err) || (err == nil && u.OrgID != s.opts.OrgID) {
		return store.User{}, ErrNotFound
	}
	if err != nil {
		return store.User{}, err
	}
	if u.Kind == KindMasterAdmin {
		return store.User{}, ErrMasterAdminImmutable
	}
	return u, nil
}

// SetManualRoles replaces a user's manually granted roles. Group-mapped roles
// are managed by Entra ID group membership and are not affected.
func (s *Service) SetManualRoles(ctx context.Context, actor *Principal, userID uuid.UUID, roles []rbac.Role, reason string, m RequestMeta) error {
	for _, r := range roles {
		if !rbac.Valid(r) {
			return ErrInvalidRole
		}
	}
	if userID == actor.UserID && !slices.Contains(roles, rbac.Administrator) && actor.Can(rbac.UserManage) {
		return ErrSelfLockout
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if _, err := s.targetUser(ctx, q, userID); err != nil {
			return err
		}
		before, err := q.ListUserRoles(ctx, userID)
		if err != nil {
			return err
		}
		if err := q.DeleteUserRolesBySource(ctx, store.DeleteUserRolesBySourceParams{UserID: userID, Source: "manual"}); err != nil {
			return err
		}
		for _, r := range roles {
			if err := q.AddUserRole(ctx, store.AddUserRoleParams{UserID: userID, Role: string(r), Source: "manual", GrantedBy: &actor.UserID}); err != nil {
				return err
			}
		}
		after, err := q.ListUserRoles(ctx, userID)
		if err != nil {
			return err
		}
		ev := s.userEvent(actor, m, audit.UserRolesChanged, audit.Success)
		ev.TargetID, ev.Reason, ev.Before, ev.After = userID.String(), reason, before, after
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// SetUserDisabled enables or disables a user and revokes their sessions when disabling.
func (s *Service) SetUserDisabled(ctx context.Context, actor *Principal, userID uuid.UUID, disabled bool, reason string, m RequestMeta) error {
	if userID == actor.UserID && disabled {
		return ErrSelfLockout
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		u, err := s.targetUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if err := q.SetUserDisabled(ctx, store.SetUserDisabledParams{ID: userID, Disabled: disabled}); err != nil {
			return err
		}
		if disabled {
			if err := q.RevokeUserSessions(ctx, userID); err != nil {
				return err
			}
		}
		ev := s.userEvent(actor, m, audit.UserDisabledChanged, audit.Success)
		ev.TargetID, ev.Reason = userID.String(), reason
		ev.Before, ev.After = map[string]any{"disabled": u.Disabled}, map[string]any{"disabled": disabled}
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ListGroupMappings returns every Entra ID group → role mapping.
func (s *Service) ListGroupMappings(ctx context.Context) ([]store.OidcGroupMapping, error) {
	return s.q.ListGroupMappings(ctx, s.opts.OrgID)
}

// AddGroupMapping maps an IdP group (object ID for Entra) to a role.
func (s *Service) AddGroupMapping(ctx context.Context, actor *Principal, provider, groupID string, role rbac.Role, m RequestMeta) error {
	if !rbac.Valid(role) {
		return ErrInvalidRole
	}
	if _, ok := s.oidc[provider]; !ok {
		return ErrOIDCUnknown
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if err := q.AddGroupMapping(ctx, store.AddGroupMappingParams{OrgID: s.opts.OrgID, Provider: provider, GroupID: groupID, Role: string(role)}); err != nil {
			return err
		}
		ev := s.userEvent(actor, m, audit.GroupMappingAdded, audit.Success)
		ev.TargetType, ev.TargetID = "oidc_group", provider+":"+groupID
		ev.Details = map[string]any{"role": role}
		_, err := rec.Record(ctx, ev)
		return err
	})
}

// RemoveGroupMapping deletes a group → role mapping. Affected users lose the
// role at their next sign-in (roles are synced at login).
func (s *Service) RemoveGroupMapping(ctx context.Context, actor *Principal, provider, groupID string, role rbac.Role, m RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.DeleteGroupMapping(ctx, store.DeleteGroupMappingParams{OrgID: s.opts.OrgID, Provider: provider, GroupID: groupID, Role: string(role)})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.userEvent(actor, m, audit.GroupMappingRemoved, audit.Success)
		ev.TargetType, ev.TargetID = "oidc_group", provider+":"+groupID
		ev.Details = map[string]any{"role": role}
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// RecordDenied audits an authorization failure.
func (s *Service) RecordDenied(ctx context.Context, p *Principal, m RequestMeta, operation, permission string) {
	ev := s.userEvent(p, m, audit.AccessDenied, audit.Denied)
	ev.TargetType, ev.TargetID = "operation", operation
	ev.Details = map[string]any{"permission": permission}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		s.log.ErrorContext(ctx, "audit access denied", "err", err)
	}
}

// ListAuditEvents pages through the audit log, newest first.
func (s *Service) ListAuditEvents(ctx context.Context, beforeSeq *int64, eventType *string, limit int32) ([]store.AuditEvent, error) {
	return s.q.ListAuditEvents(ctx, store.ListAuditEventsParams{OrgID: s.opts.OrgID, BeforeSeq: beforeSeq, EventType: eventType, MaxRows: limit})
}

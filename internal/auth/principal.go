// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// Principal is an authenticated caller. Every request — including Swagger
// UI's "Try it out" — is authorized against the principal's own permissions.
type Principal struct {
	UserID      uuid.UUID
	OrgID       uuid.UUID
	Username    string
	DisplayName string
	Email       string
	Kind        string // master_admin | oidc
	Roles       []rbac.Role
	Permissions rbac.PermissionSet
	TOTPEnabled bool

	// Credential describes how this request authenticated.
	Credential Credential
	// SessionID is set for session-authenticated requests.
	SessionID []byte
	// APITokenID is set for API-token-authenticated requests.
	APITokenID uuid.UUID
}

// Credential is the authentication mechanism of a request.
type Credential int

const (
	// CredentialSessionCookie is the browser session cookie. Unsafe requests
	// with it must pass the same-origin (CSRF) check.
	CredentialSessionCookie Credential = iota + 1
	// CredentialBearerSession is a session token sent as a bearer token.
	CredentialBearerSession
	// CredentialAPIToken is a personal API token (CLI, automation, Swagger UI).
	CredentialAPIToken
)

// Can reports whether the principal holds permission p.
func (p *Principal) Can(perm rbac.Permission) bool { return p.Permissions.Has(perm) }

// IsMasterAdmin reports whether the principal is the local master admin.
func (p *Principal) IsMasterAdmin() bool { return p.Kind == KindMasterAdmin }

// User kinds.
const (
	KindMasterAdmin = "master_admin"
	KindOIDC        = "oidc"
)

type principalKey struct{}

// WithPrincipal stores p on the context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal, if any.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

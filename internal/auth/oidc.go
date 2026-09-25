// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// OIDC errors (mapped to /login?error=<code>).
var (
	ErrOIDCState    = errors.New("oidc: invalid or expired login state")
	ErrOIDCExchange = errors.New("oidc: code exchange or token validation failed")
	ErrOIDCNoAccess = errors.New("oidc: account has no DBR² role")
	ErrOIDCDisabled = errors.New("oidc: account disabled")
	ErrOIDCUnknown  = errors.New("oidc: unknown provider")
)

// OIDCConfig configures one OIDC provider (Entra ID in v1.0).
type OIDCConfig struct {
	ID           string // URL-safe identifier, e.g. "entra"
	DisplayName  string // "Microsoft Entra ID"
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string // <public URL>/api/v1/auth/oidc/<id>/callback
	GroupsClaim  string // "groups"
}

// OIDCProvider performs the authorization-code + PKCE flow. Discovery is
// lazy and retried, so an unreachable IdP never blocks server start-up (the
// master admin keeps working — lockout protection).
type OIDCProvider struct {
	cfg OIDCConfig

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// RegisterOIDC adds a provider to the service.
func (s *Service) RegisterOIDC(cfg OIDCConfig) {
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	s.oidc[cfg.ID] = &OIDCProvider{cfg: cfg}
}

// OIDCProviders lists configured providers.
func (s *Service) OIDCProviders() []OIDCConfig {
	out := make([]OIDCConfig, 0, len(s.oidc))
	for _, p := range s.oidc {
		out = append(out, p.cfg)
	}
	return out
}

func (p *OIDCProvider) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provider != nil {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(dctx, p.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("oidc discovery for %s: %w", p.cfg.ID, err)
	}
	p.provider = prov
	p.verifier = prov.Verifier(&oidc.Config{ClientID: p.cfg.ClientID})
	p.oauth = &oauth2.Config{
		ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, RedirectURL: p.cfg.RedirectURL,
		Endpoint: prov.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return nil
}

// OIDCStart is the result of starting a login: redirect the browser to URL
// and set the state cookie to State (binds the flow to this browser).
type OIDCStart struct {
	URL   string
	State string
}

const oidcRequestTTL = 10 * time.Minute

// BeginOIDC starts an authorization-code + PKCE login. returnTo must already
// be sanitised to a same-origin relative path.
func (s *Service) BeginOIDC(ctx context.Context, providerID, returnTo string) (*OIDCStart, error) {
	p, ok := s.oidc[providerID]
	if !ok {
		return nil, ErrOIDCUnknown
	}
	if err := p.ensure(ctx); err != nil {
		return nil, err
	}
	state, err := NewToken("")
	if err != nil {
		return nil, err
	}
	nonce, err := NewToken("")
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	if err := s.q.CreateOIDCAuthRequest(ctx, store.CreateOIDCAuthRequestParams{
		State: state, Provider: providerID, Nonce: nonce, CodeVerifier: verifier,
		ReturnTo: returnTo, ExpiresAt: s.now().Add(oidcRequestTTL),
	}); err != nil {
		return nil, err
	}
	url := p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return &OIDCStart{URL: url, State: state}, nil
}

// OIDCResult is a completed OIDC login.
type OIDCResult struct {
	Session  *Session
	ReturnTo string
}

type idClaims struct {
	Name              string            `json:"name"`
	PreferredUsername string            `json:"preferred_username"`
	Email             string            `json:"email"`
	Groups            []string          `json:"-"`
	ClaimNames        map[string]string `json:"_claim_names"`
}

// CompleteOIDC finishes the login: validates state (against the browser's
// state cookie), exchanges the code with the PKCE verifier, verifies the ID
// token and nonce, provisions/updates the user, syncs group-mapped roles and
// issues a session. Users with no role are recorded but denied.
func (s *Service) CompleteOIDC(ctx context.Context, providerID, code, state, stateCookie string, m RequestMeta) (*OIDCResult, error) {
	p, ok := s.oidc[providerID]
	if !ok {
		return nil, ErrOIDCUnknown
	}
	failed := func(reason string, e error, actor string) (*OIDCResult, error) {
		_, aerr := s.audit.Record(ctx, audit.Event{
			OrgID: s.opts.OrgID, Type: audit.OIDCLoginFailed, ActorDisplay: actor, ActorKind: audit.ActorAnonymous,
			SourceIP: m.IP, RequestID: m.RequestID, Result: audit.Failure, Reason: reason,
			Details: map[string]any{"provider": providerID},
		})
		return nil, errors.Join(e, aerr)
	}
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(stateCookie)) != 1 {
		return failed("state mismatch", ErrOIDCState, "unknown")
	}
	req, err := s.q.ConsumeOIDCAuthRequest(ctx, state)
	if err != nil || req.Provider != providerID {
		return failed("unknown or expired state", ErrOIDCState, "unknown")
	}
	if err := p.ensure(ctx); err != nil {
		return nil, err
	}
	tok, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(req.CodeVerifier))
	if err != nil {
		return failed("code exchange failed", ErrOIDCExchange, "unknown")
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return failed("id token verification failed", ErrOIDCExchange, "unknown")
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(req.Nonce)) != 1 {
		return failed("nonce mismatch", ErrOIDCExchange, "unknown")
	}
	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return failed("claims decode failed", ErrOIDCExchange, idt.Subject)
	}
	var all map[string]any
	_ = idt.Claims(&all)
	c.Groups = stringSlice(all[p.cfg.GroupsClaim])
	if _, overage := c.ClaimNames[p.cfg.GroupsClaim]; overage {
		// Entra "groups overage" (>200 groups): groups are not in the token.
		// Configure the app registration to emit only groups assigned to the
		// application. Treated as no groups (fail closed).
		s.log.WarnContext(ctx, "oidc groups overage: no group claims in token", "provider", providerID, "subject", idt.Subject)
	}

	username := firstNonEmpty(c.PreferredUsername, c.Email, idt.Subject)
	display := firstNonEmpty(c.Name, username)
	var result *OIDCResult
	var denial error
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		u, err := q.GetUserByOIDC(ctx, store.GetUserByOIDCParams{OrgID: s.opts.OrgID, OidcIssuer: &idt.Issuer, OidcSubject: &idt.Subject})
		switch {
		case err == nil:
			if err := q.UpdateOIDCUserProfile(ctx, store.UpdateOIDCUserProfileParams{
				ID: u.ID, Username: username, DisplayName: display, Email: strPtr(c.Email),
			}); err != nil {
				return err
			}
		case isNoRows(err):
			provider := providerID
			u, err = q.CreateOIDCUser(ctx, store.CreateOIDCUserParams{
				OrgID: s.opts.OrgID, Username: username, DisplayName: display, Email: strPtr(c.Email),
				OidcProvider: &provider, OidcIssuer: &idt.Issuer, OidcSubject: &idt.Subject,
			})
			if err != nil {
				return err
			}
		default:
			return err
		}
		ev := audit.Event{
			OrgID: s.opts.OrgID, ActorUserID: &u.ID, ActorDisplay: username, ActorKind: audit.ActorUser,
			SourceIP: m.IP, RequestID: m.RequestID, TargetType: "user", TargetID: u.ID.String(),
			Details: map[string]any{"provider": providerID, "groups": len(c.Groups)},
		}
		if u.Disabled {
			ev.Type, ev.Result, ev.Reason = audit.OIDCLoginFailed, audit.Denied, "user disabled"
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			denial = ErrOIDCDisabled
			return nil
		}
		// Sync group-mapped roles (manual grants are untouched).
		mapped, err := q.ListGroupMappingsForGroups(ctx, store.ListGroupMappingsForGroupsParams{
			OrgID: s.opts.OrgID, Provider: providerID, GroupIds: c.Groups,
		})
		if err != nil {
			return err
		}
		if err := q.DeleteUserRolesBySource(ctx, store.DeleteUserRolesBySourceParams{UserID: u.ID, Source: "oidc_group"}); err != nil {
			return err
		}
		for _, r := range mapped {
			if !rbac.Valid(rbac.Role(r)) {
				continue
			}
			if err := q.AddUserRole(ctx, store.AddUserRoleParams{UserID: u.ID, Role: r, Source: "oidc_group"}); err != nil {
				return err
			}
		}
		roles, err := q.ListUserRoles(ctx, u.ID)
		if err != nil {
			return err
		}
		if len(roles) == 0 {
			ev.Type, ev.Result, ev.Reason = audit.OIDCLoginFailed, audit.Denied, "no DBR² role (no mapped group and no manual grant)"
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			denial = ErrOIDCNoAccess
			return nil
		}
		if err := q.TouchUserLogin(ctx, u.ID); err != nil {
			return err
		}
		sess, err := s.createSession(ctx, q, u.ID, "oidc", m)
		if err != nil {
			return err
		}
		ev.Type, ev.Result = audit.OIDCLoginSucceeded, audit.Success
		if _, err := rec.Record(ctx, ev); err != nil {
			return err
		}
		result = &OIDCResult{Session: sess, ReturnTo: req.ReturnTo}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if denial != nil {
		return nil, denial
	}
	return result, nil
}

func stringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

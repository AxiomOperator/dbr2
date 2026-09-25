// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/auth"
)

// Me describes the signed-in user.
type Me struct {
	ID          string   `json:"id" format:"uuid"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       *string  `json:"email" doc:"Email address, if known."`
	Kind        string   `json:"kind" enum:"master_admin,oidc"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	TOTPEnabled bool     `json:"totp_enabled"`
}

func meFrom(p *auth.Principal) Me {
	roles := make([]string, len(p.Roles))
	for i, r := range p.Roles {
		roles[i] = string(r)
	}
	var email *string
	if p.Email != "" {
		email = &p.Email
	}
	return Me{ID: p.UserID.String(), Username: p.Username, DisplayName: p.DisplayName, Email: email,
		Kind: p.Kind, Roles: roles, Permissions: p.Permissions.Sorted(), TOTPEnabled: p.TOTPEnabled}
}

type meOutput struct {
	Body struct {
		User Me `json:"user"`
	}
}

type meDirectOutput struct{ Body Me }

// ProviderInfo describes a sign-in option.
type ProviderInfo struct {
	ID          string `json:"id" example:"entra"`
	DisplayName string `json:"display_name" example:"Microsoft Entra ID"`
	LoginURL    string `json:"login_url" example:"/api/v1/auth/oidc/entra/login"`
}

type providersOutput struct {
	Body struct {
		MasterAdmin bool           `json:"master_admin" doc:"The local master admin sign-in is always available."`
		OIDC        []ProviderInfo `json:"oidc"`
	}
}

type loginInput struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"256"`
		Password string `json:"password" minLength:"1" maxLength:"1024"`
		TOTPCode string `json:"totp_code,omitempty" pattern:"^[0-9]{6}$" doc:"Required when TOTP is enabled (the first attempt returns code totp_required)."`
	}
}

type loginOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      struct {
		User Me `json:"user"`
	}
}

type noContentOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type passwordInput struct {
	Body struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
		NewPassword     string `json:"new_password" minLength:"1" maxLength:"1024"`
	}
}

type codeInput struct {
	Body struct {
		Code string `json:"code" pattern:"^[0-9]{6}$"`
	}
}

type totpEnrollOutput struct {
	Body struct {
		Secret     string `json:"secret" doc:"Base32 TOTP secret (shown once)."`
		OTPAuthURL string `json:"otpauth_url" doc:"otpauth:// URL for authenticator apps (render as a QR code)."`
	}
}

type redirectOutput struct {
	Status    int
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type oidcLoginInput struct {
	Provider string `path:"provider" example:"entra"`
	ReturnTo string `query:"return_to" doc:"Console path to return to after sign-in (same-origin relative paths only)."`
}

type oidcCallbackInput struct {
	Provider    string      `path:"provider"`
	Code        string      `query:"code"`
	State       string      `query:"state"`
	Error       string      `query:"error"`
	StateCookie http.Cookie `cookie:"dbr2_oidc_state"`
}

// SafeReturnTo accepts only same-origin relative paths ("/x", not "//x" or
// "/\x"), preventing open redirects after sign-in.
func SafeReturnTo(s string) string {
	if s == "" || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") ||
		strings.ContainsAny(s, "\r\n") {
		return "/"
	}
	if u, err := url.Parse(s); err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return s
}

func (d *Deps) sessionCookie(value string, expires time.Time) http.Cookie {
	c := http.Cookie{Name: SessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: d.CookieSecure, SameSite: http.SameSiteLaxMode}
	if value == "" {
		c.MaxAge = -1
	} else {
		c.Expires = expires
	}
	return c
}

func principal(ctx context.Context) *auth.Principal {
	p, _ := auth.PrincipalFrom(ctx)
	return p
}

func registerAuth(a huma.API, d *Deps) {
	huma.Register(a, public(op("list-auth-providers", http.MethodGet, "/api/v1/auth/providers", "Authentication",
		"List sign-in options", "Returns the available sign-in methods: the local master admin and configured OIDC providers (Entra ID).", "")),
		func(ctx context.Context, _ *struct{}) (*providersOutput, error) {
			out := &providersOutput{}
			out.Body.MasterAdmin = true
			out.Body.OIDC = []ProviderInfo{}
			for _, p := range d.Auth.OIDCProviders() {
				out.Body.OIDC = append(out.Body.OIDC, ProviderInfo{ID: p.ID, DisplayName: p.DisplayName, LoginURL: "/api/v1/auth/oidc/" + p.ID + "/login"})
			}
			return out, nil
		})

	loginOp := public(op("login", http.MethodPost, "/api/v1/auth/login", "Authentication",
		"Sign in as the master admin",
		"Authenticates the local master admin (username + password, plus a TOTP code when enabled) and sets the "+
			"`dbr2_session` cookie. Error codes: `invalid_credentials`, `totp_required`, `invalid_totp`, "+
			"`account_locked` (423 + Retry-After), `rate_limited` (429 + Retry-After). Every master admin sign-in is audited and raises a notification.",
		"", http.StatusUnauthorized, http.StatusLocked, http.StatusTooManyRequests))
	huma.Register(a, loginOp, func(ctx context.Context, in *loginInput) (*loginOutput, error) {
		s, err := d.Auth.LoginPassword(ctx, auth.LoginInput{
			Username: in.Body.Username, Password: in.Body.Password, TOTPCode: in.Body.TOTPCode, Meta: metaFrom(ctx),
		})
		if err != nil {
			return nil, d.fail(ctx, err)
		}
		out := &loginOutput{SetCookie: []http.Cookie{d.sessionCookie(s.Token, s.ExpiresAt)}}
		out.Body.User = meFrom(s.Principal)
		return out, nil
	})

	huma.Register(a, withStatus(op("logout", http.MethodPost, "/api/v1/auth/logout", "Authentication",
		"Sign out", "Revokes the current session and clears the session cookie.", ""), http.StatusNoContent),
		func(ctx context.Context, _ *struct{}) (*noContentOutput, error) {
			if err := d.Auth.Logout(ctx, principal(ctx), metaFrom(ctx)); err != nil {
				return nil, d.fail(ctx, err)
			}
			return &noContentOutput{SetCookie: []http.Cookie{d.sessionCookie("", time.Time{})}}, nil
		})

	huma.Register(a, op("get-me", http.MethodGet, "/api/v1/auth/me", "Authentication",
		"Get the signed-in user", "Returns the caller's identity, roles and effective permissions.", ""),
		func(ctx context.Context, _ *struct{}) (*meDirectOutput, error) {
			return &meDirectOutput{Body: meFrom(principal(ctx))}, nil
		})

	huma.Register(a, withStatus(op("change-password", http.MethodPost, "/api/v1/auth/password", "Authentication",
		"Change the master admin password",
		"Changes the master admin password (minimum 12 characters) and revokes every other session. Not available to Entra ID users.",
		"", http.StatusBadRequest), http.StatusNoContent),
		func(ctx context.Context, in *passwordInput) (*struct{}, error) {
			return nil, d.fail(ctx, d.Auth.ChangePassword(ctx, principal(ctx), in.Body.CurrentPassword, in.Body.NewPassword, metaFrom(ctx)))
		})

	huma.Register(a, op("enroll-totp", http.MethodPost, "/api/v1/auth/totp/enroll", "Authentication",
		"Start TOTP enrollment",
		"Generates a new TOTP secret for the master admin. TOTP becomes active only after /auth/totp/confirm. "+
			"Entra ID users use Entra ID MFA instead.", ""),
		func(ctx context.Context, _ *struct{}) (*totpEnrollOutput, error) {
			e, err := d.Auth.BeginTOTP(ctx, principal(ctx))
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &totpEnrollOutput{}
			out.Body.Secret, out.Body.OTPAuthURL = e.Secret, e.OTPAuthURL
			return out, nil
		})

	huma.Register(a, withStatus(op("confirm-totp", http.MethodPost, "/api/v1/auth/totp/confirm", "Authentication",
		"Confirm TOTP enrollment", "Activates TOTP after verifying a code from the authenticator app.", "", http.StatusBadRequest), http.StatusNoContent),
		func(ctx context.Context, in *codeInput) (*struct{}, error) {
			return nil, d.fail(ctx, d.Auth.ConfirmTOTP(ctx, principal(ctx), in.Body.Code, metaFrom(ctx)))
		})

	huma.Register(a, withStatus(op("disable-totp", http.MethodPost, "/api/v1/auth/totp/disable", "Authentication",
		"Disable TOTP", "Disables TOTP after verifying a current code.", ""), http.StatusNoContent),
		func(ctx context.Context, in *codeInput) (*struct{}, error) {
			return nil, d.fail(ctx, d.Auth.DisableTOTP(ctx, principal(ctx), in.Body.Code, metaFrom(ctx)))
		})

	huma.Register(a, withStatus(public(op("oidc-login", http.MethodGet, "/api/v1/auth/oidc/{provider}/login", "Authentication",
		"Start an OIDC sign-in",
		"Redirects the browser to the identity provider (authorization code + PKCE) and sets a short-lived state cookie. "+
			"Navigate to this URL; do not call it with fetch.", "", http.StatusNotFound)), http.StatusFound),
		func(ctx context.Context, in *oidcLoginInput) (*redirectOutput, error) {
			start, err := d.Auth.BeginOIDC(ctx, in.Provider, SafeReturnTo(in.ReturnTo))
			if err != nil {
				if errors.Is(err, auth.ErrOIDCUnknown) {
					return nil, d.fail(ctx, auth.ErrNotFound)
				}
				d.Log.ErrorContext(ctx, "oidc login start failed", "provider", in.Provider, "err", err)
				return d.loginRedirect("provider_unavailable"), nil
			}
			return &redirectOutput{Status: http.StatusFound, Location: start.URL, SetCookie: []http.Cookie{{
				Name: OIDCStateCookie, Value: start.State, Path: "/api/v1/auth/oidc", MaxAge: 600,
				HttpOnly: true, Secure: d.CookieSecure, SameSite: http.SameSiteLaxMode,
			}}}, nil
		})

	huma.Register(a, withStatus(public(op("oidc-callback", http.MethodGet, "/api/v1/auth/oidc/{provider}/callback", "Authentication",
		"Complete an OIDC sign-in",
		"Redirect target registered with the identity provider. Validates state, PKCE and the ID token, maps Entra ID groups "+
			"to roles, sets the session cookie and redirects to the console. Failures redirect to the login page with ?error=<code>.", "")), http.StatusFound),
		func(ctx context.Context, in *oidcCallbackInput) (*redirectOutput, error) {
			clear := http.Cookie{Name: OIDCStateCookie, Path: "/api/v1/auth/oidc", MaxAge: -1, HttpOnly: true, Secure: d.CookieSecure, SameSite: http.SameSiteLaxMode}
			if in.Error != "" {
				c := "provider_error"
				if in.Error == "access_denied" {
					c = "access_denied" // user cancelled or was refused at the IdP
				}
				out := d.loginRedirect(c)
				out.SetCookie = append(out.SetCookie, clear)
				return out, nil
			}
			res, err := d.Auth.CompleteOIDC(ctx, in.Provider, in.Code, in.State, in.StateCookie.Value, metaFrom(ctx))
			if err != nil {
				code := "login_failed"
				switch {
				case errors.Is(err, auth.ErrOIDCNoAccess):
					code = "no_roles"
				case errors.Is(err, auth.ErrOIDCDisabled):
					code = "forbidden"
				case errors.Is(err, auth.ErrOIDCState):
					code = "invalid_state"
				default:
					d.Log.ErrorContext(ctx, "oidc callback failed", "provider", in.Provider, "err", err)
				}
				out := d.loginRedirect(code)
				out.SetCookie = append(out.SetCookie, clear)
				return out, nil
			}
			return &redirectOutput{Status: http.StatusFound, Location: SafeReturnTo(res.ReturnTo),
				SetCookie: []http.Cookie{d.sessionCookie(res.Session.Token, res.Session.ExpiresAt), clear}}, nil
		})
}

func (d *Deps) loginRedirect(code string) *redirectOutput {
	return &redirectOutput{Status: http.StatusFound, Location: d.WebLoginPath + "?error=" + url.QueryEscape(code)}
}

func withStatus(o huma.Operation, status int) huma.Operation {
	o.DefaultStatus = status
	return o
}

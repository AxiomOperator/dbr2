package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// SessionCookie is the browser session cookie. In Phase 1 it is issued by the
// OIDC login flow (Entra ID); in this spike it simply carries a static token.
const SessionCookie = "dbr2_session"

// Principal is the authenticated caller. Every request, including those sent
// from Swagger UI's "Try it out", is authorized against the caller's own
// permissions: the docs UI never gets elevated rights.
type Principal struct {
	Subject     string
	Permissions []string
	// ViaCookie is true when the caller authenticated with the session cookie
	// rather than an Authorization header. Cookie-authenticated unsafe
	// requests get an additional same-origin (CSRF) check.
	ViaCookie bool
}

// Can reports whether the principal holds the given permission.
func (p Principal) Can(perm string) bool { return slices.Contains(p.Permissions, perm) }

// Authenticator resolves a credential (bearer token or session value) to a
// principal. Phase 1 replaces the static implementation with JWT/OIDC
// validation and a server-side session store.
type Authenticator interface {
	Authenticate(ctx context.Context, credential string) (Principal, bool)
}

// StaticTokens is a toy Authenticator for the spike: token -> principal.
type StaticTokens map[string]Principal

func (s StaticTokens) Authenticate(_ context.Context, credential string) (Principal, bool) {
	for tok, p := range s {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(credential)) == 1 {
			return p, true
		}
	}
	return Principal{}, false
}

type principalKey struct{}

// PrincipalFrom returns the authenticated principal stored on the context.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// authenticate is a router-level middleware that resolves the caller from the
// Authorization header (preferred) or the session cookie. It never rejects a
// request: enforcement happens in requireAuth (API operations) and
// protectDocs (docs + spec), so the policy stays per-route.
func authenticate(authn Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var (
				p  Principal
				ok bool
			)
			if h := r.Header.Get("Authorization"); h != "" {
				if tok, found := strings.CutPrefix(h, "Bearer "); found {
					p, ok = authn.Authenticate(r.Context(), strings.TrimSpace(tok))
				}
			} else if c, err := r.Cookie(SessionCookie); err == nil {
				p, ok = authn.Authenticate(r.Context(), c.Value)
				p.ViaCookie = true
			}
			if ok {
				r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAuth is a Huma middleware enforcing the operation's OpenAPI security
// requirement and its permission (op.Metadata[MetaPermission]). Because it
// reads the same data the spec is generated from, the documented security and
// the enforced security cannot drift apart.
func requireAuth(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if !requiresAuth(api, op) {
			next(ctx)
			return
		}
		p, ok := PrincipalFrom(ctx.Context())
		if !ok {
			ctx.SetHeader("WWW-Authenticate", `Bearer realm="dbr2"`)
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}
		if p.ViaCookie && !isSafeMethod(ctx.Method()) && !sameOrigin(ctx) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "cross-site request with session cookie rejected")
			return
		}
		if perm, _ := op.Metadata[MetaPermission].(string); perm != "" && !p.Can(perm) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "missing permission "+perm)
			return
		}
		next(ctx)
	}
}

// requiresAuth: an operation inherits the global security requirement unless
// it declares its own; an explicitly empty list marks it public.
func requiresAuth(api huma.API, op *huma.Operation) bool {
	sec := op.Security
	if sec == nil {
		sec = api.OpenAPI().Security
	}
	return len(sec) > 0
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin is the CSRF guard for cookie-authenticated unsafe requests.
// Swagger UI runs on the same origin, so its fetches pass; a cross-site form
// or fetch does not. Browsers always send Sec-Fetch-Site and/or Origin on
// unsafe requests.
func sameOrigin(ctx huma.Context) bool {
	if s := ctx.Header("Sec-Fetch-Site"); s != "" {
		return s == "same-origin"
	}
	origin := ctx.Header("Origin")
	if origin == "" {
		return false
	}
	scheme := "http"
	if ctx.TLS() != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+ctx.Host()
}

// protectDocs guards the docs UI, the OpenAPI documents and the JSON schemas.
// They require an authenticated caller unless api.docs.public is true.
func protectDocs(public bool, paths ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public || !hasAnyPrefix(r.URL.Path, paths) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := PrincipalFrom(r.Context()); !ok {
				// Phase 1: for Accept: text/html, redirect to the login page
				// with ?next=/api/docs instead of returning a bare 401.
				w.Header().Set("WWW-Authenticate", `Bearer realm="dbr2"`)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"title":"Unauthorized","status":401,"detail":"authentication required to view the API documentation"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasAnyPrefix(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") || strings.HasPrefix(p, pre+".") || strings.HasPrefix(p, pre+"-") {
			return true
		}
	}
	return false
}

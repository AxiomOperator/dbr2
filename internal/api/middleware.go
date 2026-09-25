// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/rbac"
)

type metaKey struct{}

func metaFrom(ctx context.Context) auth.RequestMeta {
	m, _ := ctx.Value(metaKey{}).(auth.RequestMeta)
	return m
}

// requestContext assigns a request ID (echoed as X-Request-ID) and resolves
// the client IP (trusting X-Forwarded-For only from configured proxies).
func requestContext(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 64 {
				b := make([]byte, 8)
				_, _ = rand.Read(b)
				id = hex.EncodeToString(b)
			}
			w.Header().Set("X-Request-ID", id)
			m := auth.RequestMeta{IP: obs.ClientIP(r, d.TrustedProxies), UserAgent: r.UserAgent(), RequestID: id}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), metaKey{}, m)))
		})
	}
}

func recoverer(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil && v != http.ErrAbortHandler {
					d.Log.ErrorContext(r.Context(), "panic", "panic", v, "request_id", metaFrom(r.Context()).RequestID)
					writeProblem(w, problem(http.StatusInternalServerError, CodeInternal, "internal error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func accessLog(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if r.URL.Path == "/api/v1/health/live" || r.URL.Path == "/api/v1/health/ready" {
				return
			}
			m := metaFrom(r.Context())
			d.Log.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()), slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("client_ip", m.IP.String()), slog.String("request_id", m.RequestID))
		})
	}
}

// authenticate resolves the caller from `Authorization: Bearer` (API token
// or session token) or the session cookie. It never rejects: enforcement is
// per route (requireAuth for operations, protectDocs for docs).
func authenticate(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Auth == nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			var p *auth.Principal
			if h := r.Header.Get("Authorization"); h != "" {
				tok, ok := strings.CutPrefix(h, "Bearer ")
				tok = strings.TrimSpace(tok)
				switch {
				case ok && auth.HasPrefix(tok, auth.APITokenPrefix):
					p, _ = d.Auth.ResolveAPIToken(ctx, tok)
				case ok && auth.HasPrefix(tok, auth.SessionTokenPrefix):
					if p, _ = d.Auth.ResolveSession(ctx, tok); p != nil {
						p.Credential = auth.CredentialBearerSession
					}
				}
			} else if c, err := r.Cookie(SessionCookie); err == nil {
				if p, _ = d.Auth.ResolveSession(ctx, c.Value); p != nil {
					p.Credential = auth.CredentialSessionCookie
				}
			}
			if p != nil {
				r = r.WithContext(auth.WithPrincipal(ctx, p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAuth enforces each operation's security requirement, CSRF for
// cookie-authenticated unsafe requests, and the operation's permission. It
// reads the same metadata the OpenAPI document is generated from, so the
// documented and enforced security cannot drift apart.
func requireAuth(a huma.API, d *Deps) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if !requiresAuth(a, op) {
			next(ctx)
			return
		}
		p, ok := auth.PrincipalFrom(ctx.Context())
		if !ok {
			ctx.SetHeader("WWW-Authenticate", `Bearer realm="dbr2"`)
			_ = huma.WriteErr(a, ctx, http.StatusUnauthorized, "authentication required")
			return
		}
		if p.Credential == auth.CredentialSessionCookie && !isSafeMethod(ctx.Method()) && !sameOrigin(ctx, d.AllowedOrigins) {
			writeHumaProblem(a, ctx, problem(http.StatusForbidden, CodeCSRF, "cross-site request with session cookie rejected"))
			return
		}
		if perm, _ := op.Metadata[MetaPermission].(string); perm != "" && !p.Can(rbac.Permission(perm)) {
			d.Auth.RecordDenied(ctx.Context(), p, metaFrom(ctx.Context()), op.OperationID, perm)
			writeHumaProblem(a, ctx, problem(http.StatusForbidden, CodeForbidden, "missing permission "+perm))
			return
		}
		next(ctx)
	}
}

func requiresAuth(a huma.API, op *huma.Operation) bool {
	sec := op.Security
	if sec == nil {
		sec = a.OpenAPI().Security
	}
	return len(sec) > 0
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin is the CSRF guard. Browsers send Sec-Fetch-Site and/or Origin
// on unsafe requests; requests without either are rejected when they carry
// the session cookie.
func sameOrigin(ctx huma.Context, allowed []string) bool {
	if s := ctx.Header("Sec-Fetch-Site"); s != "" {
		return s == "same-origin"
	}
	o := strings.TrimRight(ctx.Header("Origin"), "/")
	return o != "" && slices.Contains(allowed, o)
}

// protectDocs guards the docs UI and the OpenAPI documents unless
// api.docs.public is set. Browsers without a session are redirected to the
// console login with a return_to back to the docs.
func protectDocs(d *Deps, paths ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.DocsPublic || !hasAnyPrefix(r.URL.Path, paths) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := auth.PrincipalFrom(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, d.WebLoginPath+"?return_to="+url.QueryEscape(DocsPath), http.StatusFound)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="dbr2"`)
			writeProblem(w, problem(http.StatusUnauthorized, CodeUnauthorized, "authentication required to view the API documentation"))
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

func writeProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_, _ = w.Write(mustJSON(p))
}

func writeHumaProblem(a huma.API, ctx huma.Context, p *Problem) {
	ctx.SetHeader("Content-Type", "application/problem+json")
	ctx.SetStatus(p.Status)
	_, _ = ctx.BodyWriter().Write(mustJSON(p))
	_ = a
}

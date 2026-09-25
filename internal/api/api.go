// SPDX-License-Identifier: Apache-2.0

// Package api wires the DBR² HTTP API (component `api`): Chi + Huma v2
// operations, the OpenAPI document, authentication/authorization middleware,
// and the embedded Swagger UI (final_stack → Control Plane).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/docsui"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// Fixed public paths.
const (
	OpenAPIPath = "/api/openapi" // .json and .yaml
	DocsPath    = "/api/docs"    // embedded Swagger UI

	BearerScheme = "bearerAuth"
	CookieScheme = "sessionCookie"

	// SessionCookie holds the browser session token.
	SessionCookie = "dbr2_session"
	// OIDCStateCookie binds an OIDC login to the browser that started it.
	OIDCStateCookie = "dbr2_oidc_state"

	// MetaPermission is the operation metadata key holding the required
	// permission; it is also published as the x-dbr2-permission extension.
	MetaPermission = "permission"
	// ExtPermission is the OpenAPI extension documenting the permission.
	ExtPermission = "x-dbr2-permission"
)

// ReadyCheck is one readiness dependency (PostgreSQL, Temporal, Valkey).
type ReadyCheck struct {
	Name string
	// Critical checks make the service not-ready; others only degrade it.
	Critical bool
	Check    func(ctx context.Context) error
}

// Deps are the runtime dependencies of the API. NewAPI accepts nil Deps so
// the OpenAPI document can be exported without a database.
type Deps struct {
	Auth           *auth.Service
	Fleet          *fleet.Service
	Log            *slog.Logger
	Ready          []ReadyCheck
	DocsPublic     bool
	CookieSecure   bool
	WebLoginPath   string
	PublicURL      string
	AllowedOrigins []string
	TrustedProxies []netip.Prefix
	// Now is overridable in tests.
	Now func() time.Time
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// HumaConfig returns the configuration shared by the server and the offline
// spec exporter, so both produce the identical document.
func HumaConfig() huma.Config {
	cfg := huma.DefaultConfig("DBR² API", version.Of(version.API))
	cfg.Info.Description = "Docker Backup, Recovery & Restore (DBR²) control-plane API.\n\n" +
		"Authenticate with a personal API token (`dbr2pat_…`, created under /api/v1/tokens) in " +
		"**Authorize**, or use your console session. Requests made from this page run with **your own** " +
		"permissions; each operation lists the permission it needs in `x-dbr2-permission`."
	cfg.Info.License = &huma.License{Name: "Apache-2.0", URL: "https://www.apache.org/licenses/LICENSE-2.0"}
	cfg.OpenAPIPath = OpenAPIPath
	cfg.DocsPath = ""    // Huma's renderers load from a CDN; docsui serves embedded assets.
	cfg.SchemasPath = "" // no $schema links (decision: Phase 1, see api/CHANGELOG.md)
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		BearerScheme: {
			Type: "http", Scheme: "bearer",
			Description: "Personal API token (`dbr2pat_…`). Paste it into Authorize without the `Bearer ` prefix.",
		},
		CookieScheme: {
			Type: "apiKey", In: "cookie", Name: SessionCookie,
			Description: "Browser session issued by the console login (master admin or Entra ID).",
		},
	}
	cfg.Security = []map[string][]string{{BearerScheme: {}}, {CookieScheme: {}}}
	cfg.Tags = []*huma.Tag{
		{Name: "System", Description: "Version, health and readiness."},
		{Name: "Authentication", Description: "Sign-in (master admin and Entra ID), sessions, TOTP and API tokens."},
		{Name: "Users", Description: "Users, roles and Entra ID group mappings."},
		{Name: "Audit", Description: "Append-only audit log."},
		{Name: "Hosts", Description: "Agents, enrollment (registration tokens), approval, suspension, revocation and discovery."},
		{Name: "Applications", Description: "Discovered applications, ownership metadata, manual grouping and Compose definitions."},
	}
	return cfg
}

// NewAPI registers every operation on r. It does not start a server.
func NewAPI(r chi.Router, d *Deps) huma.API {
	a := humachi.New(r, HumaConfig())
	a.UseMiddleware(requireAuth(a, d))
	registerSystem(a, d)
	registerAuth(a, d)
	registerTokens(a, d)
	registerUsers(a, d)
	registerAudit(a, d)
	registerFleet(a, d)
	return a
}

// NewHandler builds the complete HTTP handler.
func NewHandler(d *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(requestContext(d), recoverer(d), accessLog(d), authenticate(d),
		protectDocs(d, DocsPath, OpenAPIPath))
	NewAPI(r, d)
	docsui.Mount(r, docsui.Options{Path: DocsPath, SpecURL: OpenAPIPath + ".json", Title: "DBR² API Reference"})
	return r
}

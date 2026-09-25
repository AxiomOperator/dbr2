// Package api wires the DBR² HTTP API: Chi router + Huma v2 operations, the
// OpenAPI document, authentication, and the embedded Swagger UI.
package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/docsui"
	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version"
)

// Fixed public paths (final_stack.md, "Interactive API documentation").
const (
	OpenAPIPath = "/api/openapi" // Huma serves .json, .yaml, -3.0.json, -3.0.yaml
	DocsPath    = "/api/docs"    // embedded Swagger UI (docsui)
	SchemasPath = "/api/schemas" // JSON Schemas referenced by $schema links

	// BearerScheme is the OpenAPI security scheme name used by every
	// operation (and by Swagger UI's "Authorize" dialog).
	BearerScheme = "bearerAuth"

	// MetaPermission is the huma.Operation.Metadata key holding the RBAC
	// permission an operation needs. It is also published in the spec as
	// the x-dbr2-permission extension.
	MetaPermission = "permission"
)

// Config is the subset of the DBR² server configuration the API needs.
type Config struct {
	// DocsPublic maps to `api.docs.public` (default false): when true the
	// docs UI and the OpenAPI documents are served without authentication.
	// API operations are always protected regardless of this flag.
	DocsPublic bool
	// Authenticator resolves bearer tokens and session cookies.
	Authenticator Authenticator
}

// HumaConfig returns the Huma configuration shared by the server and the
// offline spec exporter, so both produce the identical document.
func HumaConfig() huma.Config {
	cfg := huma.DefaultConfig("DBR² API", version.API)
	cfg.Info.Description = "Docker Backup, Recovery & Restore (DBR²) control-plane API.\n\n" +
		"All operations require a bearer token (or a browser session). " +
		"Requests made from this page run with your own permissions."
	cfg.OpenAPIPath = OpenAPIPath
	cfg.SchemasPath = SchemasPath
	// Huma's built-in renderers (Stoplight default, Scalar, Swagger UI) all
	// load their assets from unpkg.com, which breaks air-gapped installs, so
	// the built-in docs route is disabled and docsui serves embedded assets.
	cfg.DocsPath = ""

	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		BearerScheme: {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "DBR² access token. Paste it into Authorize (without the `Bearer ` prefix).",
		},
		// Phase 1 (Entra ID): add an OIDC / OAuth2 scheme next to bearerAuth,
		// e.g.
		//
		//  "entraId": {
		//    Type: "oauth2",
		//    Flows: &huma.OAuthFlows{AuthorizationCode: &huma.OAuthFlow{
		//      AuthorizationURL: "https://login.microsoftonline.com/<tenant>/oauth2/v2.0/authorize",
		//      TokenURL:         "https://login.microsoftonline.com/<tenant>/oauth2/v2.0/token",
		//      Scopes: map[string]string{"api://<app-id>/dbr2.access": "Access DBR²"},
		//    }},
		//  },
		//
		// (or Type "openIdConnect" + OpenIDConnectURL pointing at
		// .../v2.0/.well-known/openid-configuration) and list it as an
		// alternative in cfg.Security: [{bearerAuth: []}, {entraId: [...]}].
		// Swagger UI then needs docsui.Options.OAuth2 (client ID + PKCE) and
		// the redirect page at /api/docs/oauth2-redirect.html (already embedded).
	}
	cfg.Security = []map[string][]string{{BearerScheme: {}}}
	cfg.Tags = []*huma.Tag{
		{Name: "System", Description: "Platform and component metadata."},
		{Name: "Applications", Description: "Discovered Docker applications."},
		{Name: "Backups", Description: "Backup runs."},
	}
	return cfg
}

// NewAPI registers every operation on r. It does not start a server, which
// lets `dbr2-api openapi` export the spec in CI.
func NewAPI(r chi.Router) huma.API {
	api := humachi.New(r, HumaConfig())
	api.UseMiddleware(requireAuth(api))
	registerSystem(api)
	registerApplications(api)
	registerBackups(api)
	return api
}

// NewHandler builds the complete HTTP handler.
func NewHandler(cfg Config) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(authenticate(cfg.Authenticator))
	r.Use(protectDocs(cfg.DocsPublic, DocsPath, OpenAPIPath, SchemasPath))

	NewAPI(r)
	docsui.Mount(r, docsui.Options{
		Path:    DocsPath,
		SpecURL: OpenAPIPath + ".json",
		Title:   "DBR² API Reference",
	})
	return r
}

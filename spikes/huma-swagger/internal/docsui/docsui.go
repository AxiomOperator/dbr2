// Package docsui serves Swagger UI from assets embedded in the binary, so the
// API documentation works on air-gapped installs (no CDN).
//
// Vendored: swagger-ui-dist 5.31.1 (Apache-2.0, see assets/swagger-ui/LICENSE),
// the same version Huma v2.39.1's built-in Swagger UI renderer pins. Update by
// copying swagger-ui-bundle.js, swagger-ui.css, favicon-32x32.png and
// oauth2-redirect.{html,js} from `npm pack swagger-ui-dist@<version>`.
package docsui

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// SwaggerUIVersion is the vendored swagger-ui-dist version.
const SwaggerUIVersion = "5.31.1"

//go:embed assets
var assets embed.FS

// csp: no inline scripts, no third-party origins. Swagger UI sets inline
// style attributes, hence 'unsafe-inline' for styles only.
var csp = strings.Join([]string{
	"default-src 'none'",
	"base-uri 'none'",
	"script-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data:",
	"font-src 'self' data:",
	"connect-src 'self'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

// OAuth2 configures Swagger UI's initOAuth (Phase 1: Entra ID, PKCE).
type OAuth2 struct {
	ClientID                          string   `json:"clientId"`
	Scopes                            []string `json:"scopes,omitempty"`
	UsePkceWithAuthorizationCodeGrant bool     `json:"usePkceWithAuthorizationCodeGrant"`
}

// Options configures the docs page.
type Options struct {
	Path    string  // mount path, e.g. /api/docs
	SpecURL string  // e.g. /api/openapi.json
	Title   string  // page title
	OAuth2  *OAuth2 // nil until an OIDC scheme is declared
}

// Mount registers GET {Path} (the page) and GET {Path}/* (static assets).
func Mount(r chi.Router, o Options) {
	page := render(o)
	static, err := fs.Sub(assets, "assets/swagger-ui")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix(o.Path+"/", http.FileServerFS(static))
	initJS, _ := assets.ReadFile("assets/dbr2-init.js")

	// gzip: swagger-ui-bundle.js is 1.5 MB raw, ~410 KB compressed.
	r = r.With(middleware.Compress(5, "text/html", "text/css", "text/javascript"))

	r.Get(o.Path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	r.Get(o.Path+"/dbr2-init.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(initJS)
	})
	r.Get(o.Path+"/*", func(w http.ResponseWriter, req *http.Request) {
		// Assets are versioned with the binary; private because the docs may
		// be behind authentication.
		w.Header().Set("Cache-Control", "private, max-age=86400")
		files.ServeHTTP(w, req)
	})
}

func render(o Options) []byte {
	cfg := map[string]any{
		"url":                      o.SpecURL,
		"deepLinking":              true,
		"displayOperationId":       false,
		"displayRequestDuration":   true,
		"persistAuthorization":     false, // never store tokens in localStorage
		"validatorUrl":             nil,   // do not call validator.swagger.io (air-gapped, privacy)
		"defaultModelsExpandDepth": 1,
	}
	if o.OAuth2 != nil {
		cfg["oauth2"] = o.OAuth2 // dbr2-init.js also sets oauth2RedirectUrl
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	tmpl := template.Must(template.ParseFS(assets, "assets/index.html"))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"Title": o.Title, "Path": o.Path, "ConfigJSON": string(cfgJSON),
	}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

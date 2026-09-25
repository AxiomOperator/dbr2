# Spike: Huma v2 + Swagger UI interactive API docs

Date: 2026-09-25. Branch `docs/architecture-foundation`. Time spent: about 50 minutes.

Validates the "Interactive API documentation (required)" section of `docs/stack_info/final_stack.md` and the
API-contract parts of ADR-0015 (4-part `info.version`, CI contract check).

**Versions:** Go 1.26.8, `github.com/danielgtaylor/huma/v2` **v2.39.1** (+ `adapters/humachi`),
`github.com/go-chi/chi/v5` **v5.3.2**, `swagger-ui-dist` **5.31.1** (vendored), `oasdiff` **v1.32.1**,
Chromium 153 (system) driven by `playwright-core` (installed only under `.work/`).

**Verdict: all requirements are met.** One design correction is needed. Huma's built-in Swagger UI renderer
exists, but it loads assets from `unpkg.com`. For air-gapped installs, disable it and serve an **embedded**
Swagger UI from our own handler (about 100 lines of Go).

## Layout

```
cmd/dbr2-api/main.go          serve | openapi (export, no server) | version
internal/version/version.go   ldflags-injected 4-part versions + validation
internal/api/api.go           Huma config (paths, security schemes, tags), NewAPI (no server), NewHandler
internal/api/auth.go          authenticate (bearer|cookie -> Principal), requireAuth (Huma mw), protectDocs
internal/api/operations.go    GET /api/v1/version, GET /api/v1/applications, POST /api/v1/backups
internal/api/spec_lint_test.go  CI lint gate (summary/description/tags/operationId/401, info.version)
internal/docsui/              embedded Swagger UI (go:embed) + index.html + dbr2-init.js
scripts/docs-e2e.mjs          headless browser check (Try it out, Authorize) + screenshots
Makefile                      build/run/openapi/lint/tools/breaking
api/openapi.yaml              exported contract (what Phase 1 commits)
```

---

## 1. Representative operations

**Method:** Operations are registered with `huma.Register` through a small `op(...)` helper. The helper
forces operationId, tag, summary, description, the documented error codes and the RBAC permission
(`op.Metadata["permission"]`, also published as `x-dbr2-permission`). Examples and validation come from
struct tags (`format:"uuid"`, `maxLength`, `minimum/maximum`, `enum`, `default`, `example`, `doc`).
Errors use Huma's RFC 9457 `ErrorModel` (`application/problem+json`).

**Evidence** (server with `admin-token`/`viewer-token` static tokens):

```
GET  /api/v1/version (admin)  -> {"platform":"0.1.0.42","components":{"api":"0.1.0.0","server":"0.1.0.42"}}
POST /api/v1/backups valid    -> 202 {"id":"7d1e…","application_id":"0b8f…","status":"queued",…}
POST /api/v1/backups {"application_id":"nope","extra":1}
  -> 422 {"title":"Unprocessable Entity","detail":"validation failed","errors":[
        {"message":"expected string to be RFC 4122 uuid…","location":"body.application_id"},
        {"message":"unexpected property","location":"body.extra"}]}
GET  /api/v1/applications?limit=500 -> 422 "expected number <= 100" at query.limit
```

**Conclusion:** Works as expected. Notes:

- `huma.Register` does **not** auto-generate `operationId` or `summary`. Only the `huma.Get/Post` convenience
  helpers do that. The lint gate (task 8) catches missing values.
- Huma adds a `default`/500 `ErrorModel` response automatically.
- Huma middleware runs **before** input validation, so an unauthorized caller gets 401/403 and never sees
  422 details.
- `DefaultConfig` installs a link transformer that adds `$schema` to every response body and a `Link`
  header. Its example URL is `https://example.com/...`. This is harmless, but Phase 1 should decide whether
  to keep it. Drop it with `cfg.CreateHooks = nil`.
- Use `omitzero`, not `omitempty`, for optional `time.Time` fields.

## 2. Bearer security scheme, future Entra ID scheme, toy auth

**Method:** `internal/api/api.go` declares the following:

```go
cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
    "bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT", Description: "..."},
}
cfg.Security = []map[string][]string{{"bearerAuth": {}}}   // global: every operation is secured
```

Enforcement is split into three pieces:

- `authenticate`: a Chi middleware that resolves `Authorization: Bearer` (preferred) or the `dbr2_session`
  cookie into a `Principal` on the context. It never rejects a request.
- `requireAuth`: a Huma middleware (`api.UseMiddleware`) that reads the **same** `op.Security` and
  `op.Metadata` the spec is generated from. It returns 401 with `WWW-Authenticate` when there is no
  principal and 403 when the permission is missing. An operation can opt out with `Security: []` (an
  explicit empty list).
- A CSRF guard for **cookie-authenticated unsafe** requests. It requires `Sec-Fetch-Site: same-origin`, or
  an `Origin` that equals the host.

**Evidence:**

```
anon GET /api/v1/version                   -> 401 (WWW-Authenticate: Bearer realm="dbr2")
viewer POST /api/v1/backups                -> 403 "missing permission backups:create"
cookie(admin) POST + Origin: evil.example  -> 403 "cross-site request with session cookie rejected"
cookie(admin) POST + Sec-Fetch-Site: same-origin -> 202
```

**Entra ID later:** add a second scheme and list it as an **alternative** requirement. The commented code
is in `api.go`:

```go
"entraId": {Type: "oauth2", Flows: &huma.OAuthFlows{AuthorizationCode: &huma.OAuthFlow{
    AuthorizationURL: "https://login.microsoftonline.com/<tenant>/oauth2/v2.0/authorize",
    TokenURL:         "https://login.microsoftonline.com/<tenant>/oauth2/v2.0/token",
    Scopes: map[string]string{"api://<app-id>/dbr2.access": "Access DBR²"}}}},
// or {Type: "openIdConnect", OpenIDConnectURL: ".../v2.0/.well-known/openid-configuration"}
cfg.Security = []map[string][]string{{"bearerAuth": {}}, {"entraId": {"api://<app-id>/dbr2.access"}}}
```

On the Swagger UI side, set `docsui.Options.OAuth2 = &docsui.OAuth2{ClientID: ..., UsePkceWithAuthorizationCodeGrant: true}`.
`dbr2-init.js` then calls `ui.initOAuth` and sets `oauth2RedirectUrl` to `/api/docs/oauth2-redirect.html`.
That page is already embedded and served (verified 200). The redirect URI must be registered in the Entra
app registration as an SPA redirect.

## 3. Swagger UI at `/api/docs`: renderer selection and embedded assets

**How Huma v2.39.1 selects the renderer**
(`~/go/pkg/mod/github.com/danielgtaylor/huma/v2@v2.39.1/`):

- `api.go:183-185`: the constants `DocsRendererScalar = "scalar"`, `DocsRendererStoplightElements = "stoplight"`
  and `DocsRendererSwaggerUI = "swagger-ui"`.
- `api.go:198-227`: the `Config` fields `DocsPath`, `DocsRenderer` and `DocsRendererConfig`. The last one is
  JSON-merged into the `SwaggerUIBundle({...})` call. Huma owns `url` and `dom_id`.
- `defaults.go:75`: the default is `DocsRendererStoplightElements`.
- `api.go:630-789` (`registerDocsRoute`): a `switch` over the renderer. The Swagger UI branch
  (`api.go:722-771`) emits HTML that loads
  `https://unpkg.com/swagger-ui-dist@5.31.1/swagger-ui-bundle.js` and `swagger-ui.css` with SRI. It sets a
  CSP that allows only those unpkg URLs plus a hash of its inline script. The spec URL is
  `OpenAPIPath + ".json"`, prefixed with the path from `servers[0]`.
- `api.go:559-605`: `OpenAPIPath` makes Huma serve `.json`, `.yaml`, `-3.0.json` and `-3.0.yaml`.

**Native Swagger UI: yes.** `DocsRenderer: huma.DocsRendererSwaggerUI` works. **Air-gapped: no.** All
three renderers hard-code CDN URLs, and the CSP forbids anything else, so the CSS and JS cannot be
redirected to local copies.

**Method:** Set `cfg.DocsPath = ""` to disable Huma's docs route. `internal/docsui` then serves Swagger UI
from `//go:embed assets`:

- The page HTML (`index.html`, `html/template`) references `/api/docs/swagger-ui.css`,
  `/api/docs/swagger-ui-bundle.js` and `/api/docs/dbr2-init.js`. The config goes in a `data-config`
  attribute, so there is **no inline script**.
- Strict CSP: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;`
  `connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`. Swagger UI needs
  inline styles only.
- Swagger UI config:
  - `url: /api/openapi.json`
  - `persistAuthorization: false`, so tokens are never written to localStorage.
  - `validatorUrl: null`, so the page never calls validator.swagger.io. The default would call it, which
    breaks air-gapped installs and leaks the spec URL.
  - `deepLinking` and `displayRequestDuration`.
- gzip (`chi/middleware.Compress`) applies to docs assets only.

**Embedded size** (`swagger-ui-dist@5.31.1`: bundle, css, favicon, oauth2-redirect, LICENSE, NOTICE):

| file | raw | gzip -9 |
|---|---|---|
| swagger-ui-bundle.js | 1,514,087 B | 410,559 B (served gz: 416,927 B) |
| swagger-ui.css | 177,183 B | 25,652 B |
| **total embedded** | **1,704,742 B (≈1.7 MB)** | |

The binary is 16.4 MB in total. Pre-gzipping the embedded files would save about 1.25 MB of binary if that
ever matters. It is not needed.

**Evidence:** The page points at the right spec, and "Authorize" works (see task 9 for the browser run):

```
<script src="/api/docs/dbr2-init.js" data-config="{…&#34;url&#34;:&#34;/api/openapi.json&#34;,&#34;validatorUrl&#34;:null}">
```

**Conclusion:** Use an embedded Swagger UI through our own `docsui` package, pinned to the same
swagger-ui-dist version that Huma's native renderer pins (5.31.1 today; 5.33.0 is the latest on npm).

## 4. Docs protection (`api.docs.public`)

**Method:** `protectDocs(public, "/api/docs", "/api/openapi", "/api/schemas")` is a Chi middleware placed
after `authenticate`. When `public=false` it returns 401 `application/problem+json` if no principal is
present. API operations stay protected **regardless** of the flag. In the spike the flag is `-docs-public`;
in Phase 1 it maps to `api.docs.public`.

**Evidence** (`viewer-token`; status codes):

```
protected (default)              anon  bearer  cookie
/api/docs                        401   200     200
/api/openapi.json                401   200     200
/api/openapi.yaml                401   200     200
/api/openapi-3.0.json            401   200     200
/api/schemas/Backup.json         401   200     200
/api/docs/swagger-ui-bundle.js   401   200     200
/api/v1/version                  401   200     200
bad bearer on /api/docs          401

-docs-public                     anon
/api/docs, /api/openapi.{json,yaml}, bundle.js   200
/api/v1/version, /api/v1/applications            401   (still protected)
```

**Browser sessions:** A user opens `/api/docs` in a browser, so the page load itself cannot carry a
bearer header. The browser session cookie (`dbr2_session`, HttpOnly, Secure, SameSite=Lax) set by the web
UI's OIDC login authenticates these requests:

- The page and its assets.
- Swagger UI's `fetch('/api/openapi.json')`. The fetch is same-origin, so the cookie is sent by default.
  `dbr2-init.js` also sets `requestInterceptor: req.credentials = "same-origin"` explicitly.
- Every "Try it out" call, which also runs under that session's own permissions.

If the user clicks "Authorize" and pastes a token, Swagger UI adds `Authorization: Bearer …`. The server
prefers the header over the cookie. Cookie-authenticated POST/PUT/DELETE requests pass the CSRF guard
because the browser sends `Sec-Fetch-Site: same-origin`. In Phase 1, an unauthenticated
`Accept: text/html` request should get a 302 to the login page with `?next=/api/docs` instead of a bare 401.

## 5. `info.version` from `-ldflags -X`

**Method:** `internal/version` holds `Platform`, `API` and `Server` (default `0.0.0.0`).
`version.Check()` runs at startup and **fails fast** on a malformed value. `HumaConfig()` uses
`huma.DefaultConfig("DBR² API", version.API)`.

```
$ make build SERVER_VERSION=0.1.0.42 PLATFORM_VERSION=0.1.0.42
go build -ldflags "-X github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version.API=0.1.0.0 \
  -X …/internal/version.Platform=0.1.0.42 -X …/internal/version.Server=0.1.0.42" -o bin/dbr2-api ./cmd/dbr2-api
$ bin/dbr2-api version
dbr2-api 0.1.0.42 (platform 0.1.0.42, api 0.1.0.0)
$ bin/dbr2-api openapi | grep -E '^  (title|version)'
  title: DBR² API
  version: 0.1.0.0
$ curl -s -H 'Authorization: Bearer viewer-token' :18888/api/openapi.json | jq .info.version   -> "0.1.0.0"
$ go build -ldflags "-X …/version.API=0.1" … && ./bad openapi
version: api version "0.1" is not MAJOR.MINOR.BUGFIX.BUILD   (exit 1)
```

Swagger UI shows the `0.1.0.0` badge (screenshot). In line with ADR-0015, the `api` contract version keeps
BUILD=0. CI passes `API=$(cat api/VERSION).0`, and the server binary gets `.${{ github.run_number }}`.

## 6. Export the spec without a server

**Method:** `dbr2-api openapi [-format yaml|json] [-o file]` calls `api.NewAPI(chi.NewMux()).OpenAPI().YAML()`.
It uses the same `HumaConfig()` and operation registration as the server, and it never listens on a port.

```
$ make openapi        # = bin/dbr2-api openapi -format yaml -o api/openapi.yaml
$ wc -c api/openapi.yaml  -> 11523
```

The output is **deterministic**: 5 YAML exports and 3 JSON exports had identical sha256. That makes a
`git diff --exit-code` check viable. Huma emits OpenAPI **3.1.0**. `-3.0` downgrades are also served at
runtime if a client generator needs them.

## 7. Breaking-change detection with oasdiff

**Method:** Install locally with `GOBIN=$PWD/bin go install github.com/oasdiff/oasdiff@latest` (v1.32.1,
25 MB, about 6 s). oasdiff reads Huma's 3.1 output without problems. The steps: export the v1 spec, make
temporary code edits, re-export, compare, then revert (after the revert, `diff` against v1 was empty).

```
$ bin/oasdiff breaking .work/v1.yaml api/openapi.yaml --fail-on ERR          # identical
No changes detected                                                           exit=0

# Breaking: removed Application.host; Application.containers int -> string
$ bin/oasdiff breaking .work/v1.yaml .work/v2-breaking.yaml --fail-on ERR
3 changes: 3 error, 0 warning, 0 info
error [response-property-min-unset]        GET /api/v1/applications … containers min was unset from 0.00
error [response-property-type-changed]     GET /api/v1/applications … containers integer/int64 -> string
error [response-required-property-removed] GET /api/v1/applications … removed required property items/items/host
                                                                              exit=1

# Non-breaking: optional request field `labels` + optional response field `image`
$ bin/oasdiff breaking .work/v1.yaml .work/v2-additive.yaml --fail-on ERR
No breaking changes to report, but the specs are different.                  exit=0
$ bin/oasdiff changelog .work/v1.yaml .work/v2-additive.yaml
info [response-optional-property-added]  … added the optional property items/items/image
info [new-optional-request-property]     POST /api/v1/backups … added the new optional request property labels

# Same field made required -> breaking
error [new-required-request-property] POST /api/v1/backups … added the new required request property labels   exit=1
```

`-f githubactions` prints `::error title=…,file=…,line=…::` annotations for the PR.

**CI commands (PR job):**

```bash
go build -ldflags "$LDFLAGS" -o bin/dbr2-api ./cmd/dbr2-api
bin/dbr2-api openapi -format yaml -o api/openapi.yaml
git diff --exit-code api/openapi.yaml            # committed contract must match the code
git show origin/main:api/openapi.yaml > /tmp/base.yaml
oasdiff breaking /tmp/base.yaml api/openapi.yaml --fail-on ERR -f githubactions \
  || [ "$(cut -d. -f1 api/VERSION)" -gt "$(git show origin/main:api/VERSION | cut -d. -f1)" ]   # allowed only with an api MAJOR bump
oasdiff changelog /tmp/base.yaml api/openapi.yaml -f markdown   # input for the api CHANGELOG
```

Pin oasdiff to a specific version in CI (`@v1.32.1`), not `@latest`.

## 8. Lint: operations must be documented

**Method:** `internal/api/spec_lint_test.go` builds the spec in-process and reports these problems:

- missing `operationId`, `summary`, `description` or `tags`
- a secured operation that does not document `401`
- an `info.version` that is not 4-part

A second test registers a bare operation and asserts that the gate catches it.

```
$ go test ./internal/api -v
--- PASS: TestSpecLint
--- PASS: TestSpecLintCatchesUndocumented
# with the description of list-applications blanked out:
spec_lint_test.go:65: GET /api/v1/applications: missing description
FAIL   (go test exit 1)
```

**Conclusion:** A Go test is simpler than an external Spectral setup. It needs no extra tool, and it sees
Huma's operation structs directly. If a Spectral ruleset is ever wanted, it can run against
`api/openapi.yaml`.

## 9. Swagger UI actually loads (headless browser)

**Method:** First, curl the page and every asset it references. Then run `scripts/docs-e2e.mjs`, which
uses `playwright-core` installed in `.work/node_modules` (no system install) and drives the system
`/usr/bin/chromium-browser`.

```
200 628B     image/png  /api/docs/favicon-32x32.png
200 177183B  text/css   /api/docs/swagger-ui.css
200 1514087B text/javascript /api/docs/swagger-ui-bundle.js
200 968B     text/javascript /api/docs/dbr2-init.js
200          text/html  /api/docs/oauth2-redirect.html

$ node scripts/docs-e2e.mjs
anonymous GET /api/docs -> 401
session GET /api/docs -> 200
title: DBR² API Reference | operations rendered: 3 | info.version: 0.1.0.0
Try it out POST /api/v1/backups as viewer session: { status: 403, authorization: '(none)', cookie: 'sent' }
Try it out POST /api/v1/backups after Authorize(admin-token): { status: 202, authorization: 'Bearer admin-token', cookie: 'sent' }
```

The browser console showed no CSP violations and no JS errors. The only errors were the expected
401/403 responses and a 404 for `/favicon.ico`, which the web UI will serve in the product.

Screenshots: `.work/docs.png` (full page with the version badge, the Authorize button, tags and schemas)
and `.work/docs-tryitout.png` (expanded POST with the example body).

**Conclusion:** "Try it out" runs with the caller's own permissions: the viewer session got 403, and a
pasted admin token got 202. "Authorize" sends the bearer header as declared in the scheme.

---

## Proposed ADR / final_stack impact

1. **Renderer.** Change the final_stack wording from "rendered by Huma … (Swagger UI renderer)" to
   **"Swagger UI served by the API from embedded assets (`go:embed`), pinned `swagger-ui-dist` version.
   Huma's built-in docs route is disabled (`DocsPath: ""`) because all Huma renderers load from unpkg.com,
   which breaks air-gapped installs."** Record the Swagger UI version in the `api` component's changelog
   when it is bumped.
2. **Huma config pattern** (Phase 1, `internal/api`):
   ```go
   cfg := huma.DefaultConfig("DBR² API", version.API)
   cfg.OpenAPIPath = "/api/openapi"; cfg.SchemasPath = "/api/schemas"; cfg.DocsPath = ""
   cfg.Components.SecuritySchemes = {"bearerAuth": {Type:"http", Scheme:"bearer", BearerFormat:"JWT"}} // + "entraId" oauth2 later
   cfg.Security = [{"bearerAuth": []}]
   api := humachi.New(r, cfg); api.UseMiddleware(requireAuth(api))
   docsui.Mount(r, docsui.Options{Path:"/api/docs", SpecURL:"/api/openapi.json"})
   ```
   Keep one `NewAPI(router)` function shared by `serve` and `openapi` export. Set RBAC permissions per
   operation in `Metadata` (published as `x-dbr2-permission`) and enforce them in the same Huma middleware.
3. **Docs protection.** Use `api.docs.public` (default `false`) and apply it with a router middleware over
   `/api/docs`, `/api/openapi*` and `/api/schemas*`. Accept both the session cookie and a bearer token.
   Operations are always protected. Cookie-authenticated unsafe methods need a same-origin (CSRF) check.
   Unauthenticated HTML requests get a redirect to login. Swagger UI settings: `persistAuthorization:false`
   and `validatorUrl:null`, with a strict CSP (no inline script).
4. **Versioning.** Use `-ldflags -X <module>/internal/version.API=<api/VERSION>.0` (and `.Platform`/`.Server`
   with `run_number`), validate at startup, and add the check to the lint test.
5. **CI API contract check** (ADR-0015): `dbr2-api openapi -o api/openapi.yaml && git diff --exit-code`,
   then `oasdiff@v1.32.1 breaking base head --fail-on ERR -f githubactions`. A failure is allowed only with an
   `api` MAJOR bump. Also run `oasdiff changelog -f markdown` for the changelog, and
   `go test ./internal/api -run TestSpecLint`.
6. Small Huma notes worth recording: `huma.Register` does not auto-fill operationId or summary (the lint
   covers this); the link transformer adds `$schema` and `Link` (decide keep or drop); use `omitzero` for
   optional times; the spec is OpenAPI 3.1 with `-3.0` variants for older generators.

## How to rerun

```bash
cd spikes/huma-swagger
make build SERVER_VERSION=0.1.0.42 PLATFORM_VERSION=0.1.0.42   # API_VERSION defaults to 0.1.0.0
bin/dbr2-api serve &                     # protected docs on 127.0.0.1:18888 (add -docs-public for public)
curl -i localhost:18888/api/docs                                  # 401
curl -H 'Authorization: Bearer viewer-token' localhost:18888/api/openapi.yaml
curl -b dbr2_session=viewer-token localhost:18888/api/docs        # 200
(cd .work && npm i --no-save playwright-core) && node scripts/docs-e2e.mjs   # screenshots in .work/
kill %1
make openapi && make lint
make tools && cp api/openapi.yaml .work/base.yaml   # edit an operation, then:
make breaking BASE=.work/base.yaml
```

Tokens: `admin-token` (applications:read, backups:create) and `viewer-token` (applications:read).

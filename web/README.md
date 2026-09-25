# dbr2-web — DBR² web console

The Next.js administrative console for DBR² (component `web`, see
[ADR-0015](../docs/adr/0015-component-versioning-and-changelogs.md)). Phase 1
provides the foundation: sign-in (master admin with optional TOTP, and Entra ID
through OIDC), a dashboard, security settings, the audit log and the About page.
Phases 2 and 3 add Hosts (enrollment, approval, health, inventory) and
Applications (discovery results, unprotected data, Compose definitions). Phase 4 adds
Repositories (key escrow, storage health), backups (settings, "Back up now",
recovery points and manifests), alerts and host limits. The full console arrives in Phase 6 (see
[`docs/roadmap.md`](../docs/roadmap.md)).

**Stack:** Next.js 16 (App Router, `output: "standalone"`), React 19,
TypeScript (strict), Tailwind CSS 4, shadcn/ui (Radix), TanStack Query 5,
TanStack Table 9, Zod 4, Vitest + Testing Library. Node 22.

## How the console talks to the API

The browser only ever talks to the console's own origin. Every `/api/*` request
is forwarded by a route handler (`src/app/api/[...path]/route.ts`, logic in
`src/lib/proxy.ts`) to:

```text
${DBR2_API_INTERNAL_URL}/api/*        default http://127.0.0.1:8080
```

- `DBR2_API_INTERNAL_URL` is read **at request time**, so one image works in any
  deployment (the container image defaults it to `http://dbr2-server:8080`).
- Request and response bodies are streamed; `cookie`, `origin`, `sec-fetch-*`,
  `authorization`, `content-type` and `accept` are forwarded; hop-by-hop
  headers are dropped; `X-Forwarded-For/-Proto/-Host` are set (existing values
  from a front proxy are kept).
- Status, headers and **every** `Set-Cookie` are returned unmodified, and
  redirects (such as the 302 to Entra ID) are passed to the browser, not followed.
- The backend's Swagger UI and OpenAPI document are therefore available on the
  console origin at `/api/docs` and `/api/openapi.json`.

Because the session cookie is same-origin, unsafe requests from the console
carry `Sec-Fetch-Site: same-origin`, which the backend's CSRF check requires.
Behind the proxy, the backend sees `Host` as its own address; it must compare
`Origin` against `X-Forwarded-Host` and trust `X-Forwarded-*` only from dbr2-web.

## Development

```bash
cd web
npm ci

# Option A: against the Go backend (dbr2-server on :8080)
DBR2_API_INTERNAL_URL=http://127.0.0.1:8080 npm run dev

# Option B: against the bundled mock API (no Go backend needed)
npm run mock-api                                   # 127.0.0.1:8099
DBR2_API_INTERNAL_URL=http://127.0.0.1:8099 npm run dev
```

Open http://localhost:3000. The mock API accepts `admin` / `correct-horse-battery`
(TOTP code `123456` once enabled); "Sign in with Microsoft" logs in as a
read-only OIDC user (can view hosts, applications, Repositories, recovery points
and backup settings; cannot manage them, back up or reveal secrets). The
awaiting-escrow mock Repository confirms with `K7QX-M2DA-PL4W-ZT6R`; Repositories
created in the mock print their code to the mock's log. Five wrong passwords lock the account for 60 seconds. See
the header of `scripts/mock-api.mjs` for details.

The console's own version comes from `NEXT_PUBLIC_DBR2_VERSION`, inlined at
build time (`$(cat VERSION).$BUILD`, for example `0.1.0.57`). It defaults to
`0.1.0.0` for local work.

## Checks

```bash
npm run lint        # ESLint (next/core-web-vitals + TypeScript)
npm run typecheck   # next typegen && tsc --noEmit
npm test            # Vitest unit tests
NEXT_PUBLIC_DBR2_VERSION=0.1.0.0 npm run build
```

From the repository root, `make web-install`, `make web-test` and `make web-build`
run the same steps.

## Container image

```bash
# from the repository root
docker build -f web/Dockerfile --build-arg BUILD=0 -t dbr2-web:0.1.0.0 web
docker run --rm -p 3000:3000 -e DBR2_API_INTERNAL_URL=http://dbr2-server:8080 dbr2-web:0.1.0.0
```

The image is a multi-stage build on a digest-pinned `node:22` Alpine image. It
runs the standalone server as the unprivileged `node` user on port 3000
(`HOSTNAME=0.0.0.0`) and writes the full four-part version to `/app/VERSION`.
`BUILD` must be a non-negative integer; CI passes the GitHub Actions `run_number`.

## Layout

```text
src/app/                   routes (App Router)
  (console)/               signed-in pages: dashboard, hosts, applications, repositories,
                           recovery-points, alerts, audit, settings (client-side AuthGuard)
  login/  about/           public pages
  api/[...path]/route.ts   same-origin API proxy
src/components/            app shell, footer, feature components; ui/ = shadcn/ui
src/lib/api/               Zod schemas, typed client (problem+json → ApiError), endpoints, hooks
src/lib/return-to.ts       open-redirect-safe ?return_to= handling
scripts/mock-api.mjs       local mock of the API contract (Hosts / Applications in mock-fleet.mjs,
                           Repositories / backups in mock-protection.mjs)
```

Add shadcn/ui components with `npx shadcn@4.21.0 add <name>` and put the SPDX
header (`// SPDX-License-Identifier: Apache-2.0`) at the top of the generated file.

## Changes

Record every change under `## [Unreleased]` in [`CHANGELOG.md`](CHANGELOG.md).

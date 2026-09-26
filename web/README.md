# dbr2-web — DBR² web console

The Next.js administrative console for DBR² (component `web`, see
[ADR-0015](../docs/adr/0015-component-versioning-and-changelogs.md)). Phase 1
provides the foundation: sign-in (master admin with optional TOTP, and Entra ID
through OIDC), a dashboard, security settings, the audit log and the About page.
Phases 2 and 3 add Hosts (enrollment, approval, health, inventory) and
Applications (discovery results, unprotected data, Compose definitions). Phase 4 adds
Repositories (key escrow, storage health), backups (settings, "Back up now",
recovery points and manifests), alerts and host limits; Phase 5 adds restores. Phase 6 completes the
console: grouped navigation (Docker, Protection, Recovery, Storage, System), protection status and
coverage from the API, fleet-wide Containers / Volumes, Jobs, Usage and Users pages, live progress over
Server-Sent Events, the application topology graph (React Flow), OpenAPI-generated types and Zod
schemas, Playwright end-to-end tests and a nonce-based Content-Security-Policy (see
[`docs/roadmap.md`](../docs/roadmap.md)).

**Stack:** Next.js 16 (App Router, `output: "standalone"`), React 19,
TypeScript (strict), Tailwind CSS 4, shadcn/ui (Radix), TanStack Query 5,
TanStack Table 9, Zod 4, React Flow (`@xyflow/react`), Vitest + Testing Library,
Playwright (Chromium), `@hey-api/openapi-ts` (code generation). Node 22.

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

In production the Caddy edge proxy sends `/api/*` straight to dbr2-server (see
`deployments/docker-compose/Caddyfile`); the route handler above serves
development and deployments without Caddy.

## API contract: generated types and Zod schemas

`src/lib/api/generated/` (`types.gen.ts`, `zod.gen.ts`) is generated from
`../api/openapi.yaml` by [`@hey-api/openapi-ts`](openapi-ts.config.ts) and
committed. Regenerate after the contract changes, and commit the result:

```bash
npm run generate:api        # or, from the repository root: make web-generate
```

`src/lib/api/generated.test.ts` regenerates into a temporary directory and fails
while the committed files are stale. The configuration patches the input (never
the file): int64 stays a JavaScript number, `format: uuid` is not enforced (IDs are
opaque to the console), and nullable enums / `$ref`s documented as "null when …"
accept `null` as the server sends it.

Every response is validated with the generated schemas through `contract()`
(`src/lib/api/contract.ts`), which only normalises nullable / optional arrays to
`[]` and strips unknown keys (additive API changes keep working). Request bodies
are validated twice: with the form schema (user-facing messages, in
`src/lib/api/*-schemas.ts`, typed against the generated request types) and with the
generated schema. The recovery manifest and a restore's result document are
untyped in the contract and keep lenient hand-written schemas. The mock API is
held to the same contract by `src/test/mock-api-contract.test.ts`.

## Live updates (Server-Sent Events)

One shared `EventSource` on `GET /api/v1/events` (`LiveEventsProvider`, mounted in
the console layout) receives `job.progress`, `backup.updated`, `restore.updated`,
`agent.status`, `alert.created` and `inventory.updated`. Events are hints: each one
invalidates the TanStack Query caches it affects (coalesced), `job.progress` feeds
the per-application progress shown by "Back up now", the Protection card, restore
detail and Jobs, `agent.status` patches connection badges in place and
`alert.created` raises a toast. After every (re)connect the console re-reads all
live data, because missed events are never replayed. When the server closes the
stream (for example 401) the provider reconnects with exponential backoff (1 s … 60 s);
while the tab is hidden for 30 s the stream is paused. Polling stays as a slower
fallback. The dev proxy route streams `text/event-stream` responses unbuffered
(`Cache-Control: no-cache, no-transform`); in production Caddy proxies the stream
to dbr2-server directly.

## Content-Security-Policy

`src/proxy.ts` (Next.js 16 Proxy, formerly middleware) sets a per-request nonce
and, on every console page (not `/api/*`, static assets or prefetches):

```text
default-src 'self'; script-src 'self' 'nonce-…' 'strict-dynamic'; style-src 'self' 'unsafe-inline';
img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none';
base-uri 'self'; form-action 'self'; object-src 'none'
```

`next dev` adds `'unsafe-eval'` to `script-src`. The root layout reads the nonce
(which renders every page dynamically, as nonces require) and passes it to
next-themes' inline script; Next.js applies it to its own scripts. Zod runs
`jitless` so it never probes `eval`. The other security headers
(`X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`,
`Cross-Origin-Opener-Policy`, `Permissions-Policy`) stay in `next.config.ts`; the
Caddyfile sets no CSP.

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

Open http://localhost:3000. The mock API also serves the Phase 6 endpoints (jobs,
containers, volumes, users, protection status) and streams Server-Sent Events while
its backups and restores run ("Back up now" takes 15 s; `MOCK_BACKUP_MS` /
`MOCK_RESTORE_MS` change that). The mock API accepts `admin` / `correct-horse-battery`
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
npm test            # Vitest unit tests (incl. generated-code staleness and mock contract)
NEXT_PUBLIC_DBR2_VERSION=0.1.0.0 npm run build
npx playwright install chromium   # once
npm run test:e2e    # Playwright: mock API + production build (E2E_NEXT=dev: next dev)
```

From the repository root, `make web-install`, `make web-test`, `make web-build`,
`make web-generate` and `make web-e2e` run the same steps. The Playwright tests
(`e2e/`) sign in, walk the navigation (including the read-only OIDC user's
permission gating), open an application's Protection card and Topology tab, run
"Back up now" with live progress, restore a recovery point through the wizard to a
succeeded restore, create a Repository, and fail on any CSP violation; CI uploads
the report on failure.

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
  (console)/               signed-in pages (client-side AuthGuard + live events):
                           Docker: hosts, applications, containers, volumes
                           Protection: policies (Phase 7 placeholder), jobs, recovery-points
                           Recovery: restores (+ restores/new), restore-testing (Phase 9 placeholder)
                           Storage: repositories, usage
                           System: agents, users, notifications (/alerts redirects), audit, settings
  login/  about/           public pages
  api/[...path]/route.ts   same-origin API proxy (streams SSE)
src/proxy.ts               per-request CSP nonce (src/lib/csp.ts)
src/components/            app shell (grouped sidebar), feature components; ui/ = shadcn/ui
  live/                    LiveEventsProvider (SSE), live progress
  protection/              protection status badges and card
src/lib/api/generated/     GENERATED types and Zod schemas (npm run generate:api)
src/lib/api/               contract() wrapper, form schemas, typed client, endpoints, hooks
src/lib/live/              SSE event parsing and cache invalidation rules
src/lib/topology.ts        topology graph layout (Topology tab)
e2e/                       Playwright tests (playwright.config.ts)
scripts/mock-api.mjs       local mock of the API contract (Hosts / Applications in mock-fleet.mjs,
                           Repositories / backups in mock-protection.mjs, restores in
                           mock-restore.mjs, jobs / containers / volumes / users / protection in
                           mock-live.mjs, SSE in mock-events.mjs)
```

Add shadcn/ui components with `npx shadcn@4.21.0 add <name>` and put the SPDX
header (`// SPDX-License-Identifier: Apache-2.0`) at the top of the generated file.

## Changes

Record every change under `## [Unreleased]` in [`CHANGELOG.md`](CHANGELOG.md).

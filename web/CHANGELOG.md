# Changelog — web

All notable changes to the `web` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Changed
- Dependencies: `react` / `react-dom` 19.2.8 → 19.3.0, `jsdom` 29.1.1 → 30.1.1 (tests), `@types/node` 22.20.4 → 26.6.2 (merged from Dependabot PRs #4, #7, #8).

### Notes
- Not merged, blocked upstream: ESLint 10 (#5; the `eslint-plugin-react` bundled with `eslint-config-next` calls `context.getFilename`, which ESLint 10 removed) and TypeScript 7 (#6; `typescript-eslint` does not support TS 7 yet). Revisit when upstream support lands.
- `@types/node` 26 is ahead of the Node 22 runtime; avoid Node APIs newer than 22.

### Added
- Hosts page (`/hosts`, `host.read`): TanStack Table of agents with status, live connection, agent version (+ outdated), OS/arch, Docker reachability, latency, last seen and certificate expiry; refreshes every 15 s. For `host.manage`: Approve / Suspend / Resume / Revoke with a required audit reason (Revoke also requires typing the hostname, ADR-0014), "Run discovery" (toast with the workflow id), "Add host" (registration token; join command, CA fingerprint and gateway shown once, with install steps) and a collapsible registration-token list with revoke.
- Host detail (`/hosts/[id]`): every agent field, the last status reason, and the inventory summary (engine, root dir, storage driver, SELinux, rootless, resource counts, discovery warnings); link to the host's applications.
- Applications page (`/applications`, `application.read`): kind, **Original / Reconstructed** definition badge, resource counts, high-severity unprotected-data warning, dependencies, secrets, ownership, last seen / missing; filters for host, kind and "only with unprotected data" (kept in the URL). For `application.manage`: "Group containers" (manual application from standalone containers) and delete for manual applications.
- Application detail (`/applications/[id]`): **Unprotected data** first (high before low), services and containers, volumes with class (Local / External / Ephemeral) and reasons, bind mounts, tmpfs, networks, images (digests, platform, "no registry digest" for local builds), dependencies, per-container env (secrets arrive masked) / ports / mounts, and an edit-metadata dialog (display name, owner, environment, criticality; only changed fields are sent).
- Compose tab: original Compose and `.env` files (masked) or the reconstructed YAML; "Reveal secrets" for `secrets.read` behind an audited-action confirmation. Revealed content uses its own query key with `gcTime: 0`, is never refetched automatically and is dropped when hidden or on unmount.
- Dashboard "Protection overview" card (applications, with unprotected high-severity data, reconstructed definitions, hosts pending approval).
- Navigation entries for Hosts and Applications, shown only with `host.read` / `application.read`.
- Zod schemas and typed client functions for the Hosts and Applications operations of `api/openapi.yaml` (nullable arrays normalised to `[]`); `actionErrorMessage` for 409 `conflict`, 400 `validation_failed` (with field errors) and 403 `forbidden`.
- In-house toast notifications (no new dependency); shadcn/ui `dialog`, `select`, `tabs`, `collapsible`, `checkbox` and `textarea`.
- Mock API: Hosts and Applications endpoints (`scripts/mock-fleet.mjs`) with hosts in every state, an application with unprotected data, a reconstructed one, a manual one and a missing one, masked and revealed Compose responses; the mock OIDC user is now read-only for hosts and applications.
- Tests: schema fixtures, unprotected-data sorting, the Reconstructed badge, the reveal flow and its permission, the revoke hostname confirmation and the show-once join command.
- Component scaffold (Phase 1).
- Next.js 16.3 console (App Router, TypeScript strict, `output: "standalone"`) with React 19.2, Tailwind CSS 4.3, shadcn/ui (Radix, light and dark themes), TanStack Query 5, TanStack Table 9 and Zod 4.
- Same-origin API proxy (`/api/*` → `DBR2_API_INTERNAL_URL`, resolved per request): streams bodies, forwards cookies and the CSRF-relevant headers, sets `X-Forwarded-*`, returns every `Set-Cookie` unchanged and passes redirects through. Serves the backend's Swagger UI at `/api/docs`.
- Typed API client and Zod schemas for the Phase 1 contract; `application/problem+json` errors become a typed `ApiError` (with `code` and `Retry-After`).
- Sign-in page: master admin username and password, TOTP step on `totp_required`, locked and rate-limited messages using `Retry-After`, one button per OIDC provider ("Sign in with Microsoft" for Entra ID), open-redirect-safe `?return_to=` and `?error=` handling.
- Client-side auth guard on `/auth/me`, and an app shell with navigation (Dashboard, Audit, Settings), theme toggle and user menu with sign-out.
- Dashboard placeholder with roles, a link to the API docs and a platform status card (`/health/ready`).
- Security settings: change the master admin password; enroll TOTP (QR code rendered locally), confirm and disable.
- Audit log table with cursor-based "Load more" (requires `audit.read`).
- Version footer on every page (console `NEXT_PUBLIC_DBR2_VERSION` and platform version) and an About page listing every component version (ADR-0015).
- Multi-stage `Dockerfile` (digest-pinned `node:22.23.3-alpine3.24`, non-root, `ARG BUILD`, version `$(cat VERSION).$BUILD`) and `.dockerignore`.
- Mock of the Phase 1 API (`npm run mock-api`) for development without the Go backend.
- Vitest unit tests for the `return_to` sanitizer, API error handling, the version footer, the TOTP login step and the proxy's header and cookie forwarding.

### Notes
- In the Compose deployment, browsers reach `/api/*` through the Caddy edge proxy, which discards client-supplied `X-Forwarded-*`. The console's own `/api` proxy is for development: a Next.js route handler cannot see the socket address, so it cannot sanitize a client-supplied `X-Forwarded-For`.

### Security
- Security headers on console pages (`X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin`, COOP, `Permissions-Policy`); `X-Powered-By` disabled.
- System font stacks only, so nothing is fetched from third-party hosts at build or run time.

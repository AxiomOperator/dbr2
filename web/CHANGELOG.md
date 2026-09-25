# Changelog — web

All notable changes to the `web` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Changed
- Dependencies: `react` / `react-dom` 19.2.8 → 19.3.0, `jsdom` 29.1.1 → 30.1.1 (tests), `@types/node` 22.20.4 → 26.6.2 (merged from Dependabot PRs #4, #7, #8).

### Notes
- Not merged, blocked upstream: ESLint 10 (#5; the `eslint-plugin-react` bundled with `eslint-config-next` calls `context.getFilename`, which ESLint 10 removed) and TypeScript 7 (#6; `typescript-eslint` does not support TS 7 yet). Revisit when upstream support lands.
- `@types/node` 26 is ahead of the Node 22 runtime; avoid Node APIs newer than 22.

### Added
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

#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
//
// Tiny in-memory mock of the Phase 1 DBR² API contract, for developing the
// console without the Go backend. NOT a security reference implementation.
//
//   node scripts/mock-api.mjs            # listens on 127.0.0.1:8099
//   MOCK_API_PORT=8098 node scripts/mock-api.mjs
//   DBR2_API_INTERNAL_URL=http://127.0.0.1:8099 npm run dev
//
// Accounts:
//   master admin   admin / correct-horse-battery   (TOTP code in mock: 123456)
//   OIDC (entra)   "Sign in with Microsoft" logs in as a read-only operator
// Five wrong passwords lock the master admin for 60 s (423 + Retry-After).

import { randomBytes, randomUUID } from "node:crypto";
import { createServer } from "node:http";

const HOST = process.env.MOCK_API_HOST ?? "127.0.0.1";
const PORT = Number(process.env.MOCK_API_PORT ?? 8099);
const COOKIE = "dbr2_session";
const MOCK_TOTP_CODE = "123456";

const state = {
  password: "correct-horse-battery",
  totpEnabled: false,
  pendingTotpSecret: null,
  failures: 0,
  lockedUntil: 0,
  /** @type {Map<string, object>} session id -> user */
  sessions: new Map(),
  audit: [],
};

const masterAdmin = () => ({
  id: "00000000-0000-0000-0000-000000000001",
  username: "admin",
  display_name: "Master Admin",
  email: null,
  kind: "master_admin",
  roles: ["administrator"],
  permissions: ["audit.read", "users.manage", "settings.manage", "restore.production"],
  totp_enabled: state.totpEnabled,
});

const oidcUser = {
  id: "7d1f6c1e-5b8a-4c35-9d0e-6a3b2b8f1a10",
  username: "ada@example.com",
  display_name: "Ada Lovelace",
  email: "ada@example.com",
  kind: "oidc",
  roles: ["operator"],
  permissions: ["hosts.read"],
  totp_enabled: false,
};

function audit(event_type, result, req, extra = {}) {
  state.audit.unshift({
    event_id: randomUUID(),
    occurred_at: new Date().toISOString(),
    event_type,
    actor_display: extra.actor ?? null,
    source_ip: String(req.headers["x-forwarded-for"] ?? req.socket.remoteAddress ?? "").split(",")[0].trim() || null,
    target_type: extra.target_type ?? "user",
    target_id: extra.target_id ?? null,
    result,
    reason: extra.reason ?? null,
    details: extra.details ?? {},
  });
}

// Seed enough events to exercise "load more" paging.
for (let i = 0; i < 120; i++) {
  state.audit.push({
    event_id: randomUUID(),
    occurred_at: new Date(Date.now() - (i + 1) * 3_600_000).toISOString(),
    event_type: i % 3 === 0 ? "auth.login" : i % 3 === 1 ? "auth.logout" : "settings.changed",
    actor_display: i % 2 ? "Master Admin" : "Ada Lovelace",
    source_ip: `10.0.0.${(i % 250) + 1}`,
    target_type: "user",
    target_id: i % 2 ? "admin" : "ada@example.com",
    result: i % 7 === 0 ? "failure" : "success",
    reason: i % 7 === 0 ? "invalid_credentials" : null,
    details: { seq: i },
  });
}

function send(res, status, body, headers = {}) {
  const payload = body === undefined ? "" : JSON.stringify(body);
  res.writeHead(status, {
    ...(body === undefined ? {} : { "content-type": "application/json" }),
    "cache-control": "no-store",
    ...headers,
  });
  res.end(payload);
}

function problem(res, status, code, title, detail, headers = {}) {
  const payload = JSON.stringify({ title, status, detail, code });
  res.writeHead(status, {
    "content-type": "application/problem+json",
    "cache-control": "no-store",
    ...headers,
  });
  res.end(payload);
}

function cookies(req) {
  const out = {};
  for (const part of (req.headers.cookie ?? "").split(";")) {
    const i = part.indexOf("=");
    if (i > 0) out[part.slice(0, i).trim()] = decodeURIComponent(part.slice(i + 1).trim());
  }
  return out;
}

function sessionUser(req) {
  const sid = cookies(req)[COOKIE];
  if (!sid || !state.sessions.has(sid)) return null;
  const user = state.sessions.get(sid);
  return user.kind === "master_admin" ? masterAdmin() : user;
}

function newSession(user) {
  const sid = randomBytes(24).toString("base64url");
  state.sessions.set(sid, user);
  return `${COOKIE}=${sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age=43200`;
}

async function readJson(req) {
  let raw = "";
  for await (const chunk of req) raw += chunk;
  if (!raw) return {};
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

/** Same-origin CSRF check as described in the final stack (cookie-authenticated unsafe requests). */
function csrfOk(req) {
  if (["GET", "HEAD", "OPTIONS"].includes(req.method)) return true;
  const site = req.headers["sec-fetch-site"];
  if (site && site !== "same-origin" && site !== "none") return false;
  const origin = req.headers.origin;
  if (origin) {
    const host = req.headers["x-forwarded-host"] ?? req.headers.host;
    try {
      if (new URL(origin).host !== host) return false;
    } catch {
      return false;
    }
  }
  return true;
}

const routes = {
  "GET /api/v1/version": (req, res) =>
    send(res, 200, {
      platform: "0.1.0.0",
      components: {
        api: "0.1.0.0",
        server: "0.1.0.0",
        worker: "0.1.0.0",
        "db-schema": "0.1.0.0",
        "agent-protocol": "0.1.0.0",
        "manifest-schema": "0.1.0.0",
      },
    }),

  "GET /api/v1/health/live": (req, res) => send(res, 200, { status: "ok" }),

  "GET /api/v1/health/ready": (req, res) =>
    send(res, 200, { status: "ok", checks: { postgres: "ok", temporal: "ok", valkey: "ok" } }),

  "GET /api/v1/auth/providers": (req, res) =>
    send(res, 200, {
      master_admin: true,
      oidc: [
        {
          id: "entra",
          display_name: "Microsoft Entra ID",
          login_url: "/api/v1/auth/oidc/entra/login",
        },
      ],
    }),

  "GET /api/v1/auth/oidc/entra/login": (req, res, url) => {
    // Real backend: redirect to Entra, then callback -> return_to. Mock: skip Entra.
    const rt = url.searchParams.get("return_to") ?? "/";
    const safe = rt.startsWith("/") && !rt.startsWith("//") ? rt : "/";
    if (url.searchParams.get("fail")) {
      res.writeHead(302, { location: "/login?error=access_denied" });
      return res.end();
    }
    audit("auth.login", "success", req, { actor: oidcUser.display_name, target_id: oidcUser.username });
    res.writeHead(302, { location: safe, "set-cookie": newSession(oidcUser) });
    res.end();
  },

  "POST /api/v1/auth/login": async (req, res) => {
    const body = await readJson(req);
    if (!body || typeof body.username !== "string" || typeof body.password !== "string") {
      return problem(res, 400, "invalid_request", "Bad Request", "username and password are required.");
    }
    const now = Date.now();
    if (state.lockedUntil > now) {
      const retry = Math.ceil((state.lockedUntil - now) / 1000);
      return problem(res, 423, "account_locked", "Locked", "The account is temporarily locked.", {
        "retry-after": String(retry),
      });
    }
    if (body.username !== "admin" || body.password !== state.password) {
      state.failures++;
      audit("auth.login", "failure", req, { target_id: body.username, reason: "invalid_credentials" });
      if (state.failures >= 5) {
        state.failures = 0;
        state.lockedUntil = now + 60_000;
        return problem(res, 423, "account_locked", "Locked", "The account is temporarily locked.", {
          "retry-after": "60",
        });
      }
      return problem(res, 401, "invalid_credentials", "Unauthorized", "Invalid username or password.");
    }
    if (state.totpEnabled) {
      if (!body.totp_code) {
        return problem(res, 401, "totp_required", "Unauthorized", "A TOTP code is required.");
      }
      if (body.totp_code !== MOCK_TOTP_CODE) {
        audit("auth.login", "failure", req, { target_id: "admin", reason: "invalid_totp" });
        return problem(res, 401, "invalid_totp", "Unauthorized", "The TOTP code is invalid.");
      }
    }
    state.failures = 0;
    audit("auth.login", "success", req, { actor: "Master Admin", target_id: "admin" });
    // Two Set-Cookie headers on purpose: exercises multi-cookie forwarding in the proxy.
    send(res, 200, { user: masterAdmin() }, {
      "set-cookie": [newSession(masterAdmin()), "dbr2_mock=1; Path=/; SameSite=Lax"],
    });
  },

  "POST /api/v1/auth/logout": (req, res) => {
    const sid = cookies(req)[COOKIE];
    if (sid) state.sessions.delete(sid);
    send(res, 204, undefined, { "set-cookie": `${COOKIE}=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0` });
  },

  "GET /api/v1/auth/me": (req, res, _url, user) => send(res, 200, user),

  "POST /api/v1/auth/password": async (req, res, _url, user) => {
    if (user.kind !== "master_admin") return problem(res, 403, "forbidden", "Forbidden", "Master admin only.");
    const body = await readJson(req);
    if (!body || body.current_password !== state.password) {
      return problem(res, 400, "invalid_credentials", "Bad Request", "The current password is incorrect.");
    }
    if (typeof body.new_password !== "string" || body.new_password.length < 12) {
      return problem(res, 400, "weak_password", "Bad Request", "The password must be at least 12 characters.");
    }
    state.password = body.new_password;
    audit("auth.password_changed", "success", req, { actor: "Master Admin", target_id: "admin" });
    send(res, 204);
  },

  "POST /api/v1/auth/totp/enroll": (req, res, _url, user) => {
    if (user.kind !== "master_admin") return problem(res, 403, "forbidden", "Forbidden", "Master admin only.");
    if (state.totpEnabled) return problem(res, 409, "totp_already_enabled", "Conflict", "TOTP is already enabled.");
    const secret = "JBSWY3DPEHPK3PXP";
    state.pendingTotpSecret = secret;
    send(res, 200, {
      secret,
      otpauth_url: `otpauth://totp/DBR2:admin?secret=${secret}&issuer=DBR2&algorithm=SHA1&digits=6&period=30`,
    });
  },

  "POST /api/v1/auth/totp/confirm": async (req, res) => {
    const body = await readJson(req);
    if (!state.pendingTotpSecret) return problem(res, 409, "totp_not_enrolling", "Conflict", "Start enrollment first.");
    if (body?.code !== MOCK_TOTP_CODE) return problem(res, 400, "invalid_totp", "Bad Request", "The TOTP code is invalid.");
    state.totpEnabled = true;
    state.pendingTotpSecret = null;
    audit("auth.totp_enabled", "success", req, { actor: "Master Admin", target_id: "admin" });
    send(res, 204);
  },

  "POST /api/v1/auth/totp/disable": async (req, res) => {
    const body = await readJson(req);
    if (body?.code !== MOCK_TOTP_CODE) return problem(res, 400, "invalid_totp", "Bad Request", "The TOTP code is invalid.");
    state.totpEnabled = false;
    audit("auth.totp_disabled", "success", req, { actor: "Master Admin", target_id: "admin" });
    send(res, 204);
  },

  "GET /api/v1/audit-events": (req, res, url, user) => {
    if (!user.permissions.includes("audit.read")) {
      return problem(res, 403, "forbidden", "Forbidden", "Missing permission audit.read.");
    }
    const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 50) || 50, 1), 200);
    const start = Number(url.searchParams.get("cursor") ?? 0) || 0;
    const items = state.audit.slice(start, start + limit);
    const next = start + limit < state.audit.length ? String(start + limit) : null;
    send(res, 200, { items, next_cursor: next });
  },

  // Mock-only helper: shows what the proxy forwarded.
  "GET /api/v1/_mock/echo": (req, res, url) =>
    send(res, 200, { method: req.method, url: url.pathname + url.search, headers: req.headers }),

  "GET /api/docs": (req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(
      "<!doctype html><title>DBR² API docs (mock)</title><h1>DBR² API docs (mock)</h1>" +
        '<p>The real backend serves Swagger UI here. <a href="/api/openapi.json">openapi.json</a></p>',
    );
  },

  "GET /api/openapi.json": (req, res) =>
    send(res, 200, { openapi: "3.1.0", info: { title: "DBR² API (mock)", version: "0.1.0.0" }, paths: {} }),
};

const PUBLIC = new Set([
  "GET /api/v1/version",
  "GET /api/v1/health/live",
  "GET /api/v1/health/ready",
  "GET /api/v1/auth/providers",
  "GET /api/v1/auth/oidc/entra/login",
  "POST /api/v1/auth/login",
  "POST /api/v1/auth/logout",
  "GET /api/v1/_mock/echo",
  "GET /api/docs",
  "GET /api/openapi.json",
]);

const server = createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", `http://${req.headers.host ?? "localhost"}`);
  const key = `${req.method} ${url.pathname}`;
  const handler = routes[key];
  try {
    if (!handler) {
      return problem(res, 404, "not_found", "Not Found", `No route for ${key}.`);
    }
    if (!csrfOk(req)) {
      return problem(res, 403, "csrf_failed", "Forbidden", "Cross-site request rejected.");
    }
    let user = null;
    if (!PUBLIC.has(key)) {
      user = sessionUser(req);
      if (!user) return problem(res, 401, "unauthenticated", "Unauthorized", "Sign in required.");
    }
    await handler(req, res, url, user);
  } catch (err) {
    console.error(err);
    if (!res.headersSent) problem(res, 500, "internal", "Internal Server Error", "Mock failure.");
  } finally {
    console.log(`${new Date().toISOString()} ${key} -> ${res.statusCode}`);
  }
});

server.listen(PORT, HOST, () => {
  console.log(`DBR² mock API listening on http://${HOST}:${PORT}`);
});

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => server.close(() => process.exit(0)));
}

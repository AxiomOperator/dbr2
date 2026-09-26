// SPDX-License-Identifier: Apache-2.0
//
// Phase 6 part of the mock API, imported by scripts/mock-api.mjs: protection
// status and coverage of every application (the `protection` object of
// ApplicationSummary / ApplicationDetail, computed like
// internal/protection/status.go), the fleet-wide jobs, containers and volumes
// lists, and users / roles / Entra ID group mappings. In-memory only.
//
// Seed data (users):
//   admin            master admin (administrator, implicit)
//   ada@example.com  OIDC, restore_operator (the mock "Sign in with Microsoft"
//                    user)
//   linus@example.com  OIDC, backup_administrator via the group mapping
//                    "DBR2-Backup-Admins" + auditor granted manually
//   mallory@example.com  OIDC, disabled

import { fleetData } from "./mock-fleet.mjs";
import { protectionData } from "./mock-protection.mjs";
import { restoreData } from "./mock-restore.mjs";

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();

// ---------------------------------------------------------------------------
// Protection (internal/protection/status.go: computeProtection)
// ---------------------------------------------------------------------------

function protectionFor(app) {
  const rps = protectionData.recoveryPoints
    .filter((r) => r.application_id === app.id)
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
  const ok = rps.find((r) => r.state === "committed");
  const attempt = rps.find((r) => r.state !== "missing" && r.state !== "deleting");
  const restoring = restoreData.runs.some(
    (r) => (r.application_id === app.id || r.target_application_id === app.id) && (r.state === "requested" || r.state === "running"),
  );
  const p = {
    status: "unprotected",
    reasons: [],
    last_backup_at: null,
    last_attempt_at: null,
    components: [],
    components_total: 0,
    components_protected: 0,
    unresolved_dependencies: 0,
  };
  if (ok) {
    p.last_backup_at = ok.committed_at;
    p.last_recovery_point_id = ok.id;
    p.last_mode = ok.consistency_mode;
    if (ok.status) p.last_status = ok.status;
  }
  if (attempt) {
    p.last_attempt_at = attempt.created_at;
    p.last_attempt_state = attempt.state;
    if (attempt.error) p.last_error = attempt.error;
    if (attempt.state === "pending") p.running = "backup";
  }
  if (restoring) p.running = "restore";
  const captured = new Map();
  for (const c of ok?._manifest?.components ?? []) if (c.status === "succeeded") captured.set(c.name, c);
  if (app.analysis) {
    p.unresolved_dependencies = app.analysis.dependencies.length;
    for (const c of protectionData.planFor(app)) {
      const got = captured.get(c.name);
      p.components.push({ name: c.name, kind: c.kind, required: c.required, protected: Boolean(got), last_size_bytes: got?.size_bytes ?? 0 });
      if (got) p.components_protected++;
    }
    p.components_total = p.components.length;
  } else {
    p.reasons.push("the application is missing from its host's latest inventory");
  }
  if (!ok && attempt?.state === "failed") {
    p.status = "failed";
    p.reasons.push("no backup has succeeded; the last attempt failed");
  } else if (!ok) {
    p.status = "unprotected";
    p.reasons.push("no recovery point exists");
  } else if (attempt?.state === "failed" && attempt.created_at > ok.created_at) {
    p.status = "failed";
    p.reasons.push("the last backup attempt failed");
  } else {
    p.status = "protected";
    if (p.last_status === "partial") {
      p.status = "at_risk";
      p.reasons.push("the latest recovery point is Partial");
    }
    if (p.components_protected < p.components_total) {
      p.status = "at_risk";
      p.reasons.push(`${p.components_total - p.components_protected} of ${p.components_total} components are not in the latest recovery point`);
    }
  }
  if (p.unresolved_dependencies > 0) {
    p.reasons.push(`${p.unresolved_dependencies} external dependencies are not protected by DBR²`);
  }
  return p;
}
fleetData.setProtection(protectionFor);

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

const BACKUP_JOB_STATE = { pending: "running", failed: "failed", missing: "missing" };
const RESTORE_JOB_STATE = { requested: "running", running: "running", succeeded: "succeeded", failed: "failed", rolled_back: "rolled_back" };

function backupJob(r) {
  const state = r.state === "committed" ? (r.status === "partial" ? "partial" : "succeeded") : (BACKUP_JOB_STATE[r.state] ?? "failed");
  return {
    id: r.id,
    type: "backup",
    application_id: r.application_id,
    application_name: r.application_name,
    hostname: r.hostname,
    state,
    detail: r.consistency_mode,
    trigger: r.trigger,
    ...(r.error ? { error: r.error } : {}),
    ...(r.size_bytes ? { size_bytes: r.size_bytes } : {}),
    started_at: r.created_at,
    finished_at: r.committed_at ?? (r.state === "failed" ? r.created_at : null),
  };
}

function restoreJob(r) {
  return {
    id: r.id,
    type: "restore",
    application_id: r.application_id,
    application_name: r.application_name,
    hostname: r.target_hostname,
    state: RESTORE_JOB_STATE[r.state],
    detail: r.step ?? (r.mode === "alternate_host" ? "alternate host" : "in place"),
    trigger: "manual",
    requested_by: r.requested_by,
    ...(r.error ? { error: r.error } : {}),
    started_at: r.started_at ?? r.created_at,
    finished_at: r.finished_at,
  };
}

// ---------------------------------------------------------------------------
// Fleet-wide containers and volumes
// ---------------------------------------------------------------------------

function containers() {
  const out = [];
  for (const app of fleetData.apps) {
    if (app.missing_since) continue;
    for (const c of app.containers_detail) {
      out.push({
        id: c.id,
        name: c.name,
        image: c.image,
        state: c.state,
        host_id: app.host_id,
        hostname: fleetData.hostname(app.host_id),
        application_id: app.id,
        application_name: fleetData.appName(app),
        ports: c.ports,
        mounts: c.mounts.length,
        networks: c.networks,
        created: iso(-3 * DAY),
        restart_policy: app.kind === "compose" ? "unless-stopped" : "always",
      });
    }
  }
  // A stopped one-off container that belongs to no application.
  out.push({
    id: "b1c2d3e4f5a60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
    name: "db-migrate-oneoff",
    image: "registry.example.com/shop/web:2.4.1",
    state: "exited",
    host_id: fleetData.agents[0].id,
    hostname: fleetData.agents[0].hostname,
    application_id: null,
    application_name: null,
    ports: null,
    mounts: 0,
    networks: ["shop_default"],
    created: iso(-12 * DAY),
    restart_policy: "no",
  });
  return out;
}

function volumes() {
  const out = [];
  for (const app of fleetData.apps) {
    if (!app.analysis) continue;
    const latest = protectionData.recoveryPoints
      .filter((r) => r.application_id === app.id && r.state === "committed")
      .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
    for (const v of app.analysis.volumes) {
      const comp = latest?._manifest?.components.find((c) => c.name === `volume:${v.name}` && c.status === "succeeded");
      out.push({
        name: v.name,
        driver: v.driver,
        host_id: app.host_id,
        hostname: fleetData.hostname(app.host_id),
        class: v.class,
        application_id: app.id,
        application_name: fleetData.appName(app),
        used_by: v.used_by,
        protected: Boolean(comp),
        last_backup_at: comp ? latest.committed_at : null,
        last_size_bytes: comp?.size_bytes ?? 0,
      });
    }
  }
  out.push({
    name: "shop_pgdata_old",
    driver: "local",
    host_id: fleetData.agents[0].id,
    hostname: fleetData.agents[0].hostname,
    class: "unused",
    application_id: null,
    application_name: null,
    used_by: null,
    protected: false,
    last_backup_at: null,
    last_size_bytes: 0,
  });
  return out;
}

// ---------------------------------------------------------------------------
// Users, roles, group mappings (internal/rbac)
// ---------------------------------------------------------------------------

const P = {
  hostRead: "host.read",
  hostManage: "host.manage",
  appRead: "application.read",
  appManage: "application.manage",
  backupRead: "backup.read",
  backupExecute: "backup.execute",
  backupDelete: "backup.delete",
  restoreRead: "restore.read",
  restoreExecute: "restore.execute",
  restoreProduction: "restore.production",
  repoRead: "repository.read",
  repoManage: "repository.manage",
  policyRead: "policy.read",
  policyManage: "policy.manage",
  auditRead: "audit.read",
  secretsRead: "secrets.read",
  userRead: "user.read",
  userManage: "user.manage",
};
export const ALL_PERMISSIONS = Object.values(P);
const readAll = [P.hostRead, P.appRead, P.backupRead, P.restoreRead, P.repoRead, P.policyRead];

export const roles = [
  { role: "administrator", display_name: "Administrator", description: "Full control of DBR², including users, secrets and production restores.", permissions: ALL_PERMISSIONS },
  {
    role: "application_operator",
    display_name: "Application Operator",
    description: "Backs up and restores applications (delegated per application in v2).",
    permissions: [P.appRead, P.backupRead, P.backupExecute, P.restoreRead, P.restoreExecute],
  },
  { role: "auditor", display_name: "Auditor", description: "Read-only access to everything including the audit log and users.", permissions: [...readAll, P.auditRead, P.userRead] },
  {
    role: "backup_administrator",
    display_name: "Backup Administrator",
    description: "Manages hosts, applications, policies, repositories and backups; may run production restores.",
    permissions: [
      P.hostRead, P.hostManage, P.appRead, P.appManage, P.backupRead, P.backupExecute, P.backupDelete,
      P.restoreRead, P.restoreExecute, P.restoreProduction, P.repoRead, P.repoManage, P.policyRead, P.policyManage, P.auditRead,
    ],
  },
  { role: "read_only", display_name: "Read Only", description: "Read-only access to inventory and protection state.", permissions: readAll },
  { role: "restore_operator", display_name: "Restore Operator", description: "Runs non-production restores and reviews recovery points.", permissions: [...readAll, P.restoreExecute] },
];

const groupMappings = [
  { provider: "entra", group_id: "4f0c2b1a-9d8e-4c7b-a6f5-e4d3c2b1a090", role: "backup_administrator" },
  { provider: "entra", group_id: "8a7b6c5d-4e3f-4a2b-9c1d-0e9f8a7b6c5d", role: "restore_operator" },
];

const users = [
  {
    id: "00000000-0000-0000-0000-000000000001",
    kind: "master_admin",
    username: "admin",
    display_name: "Master Admin",
    email: null,
    disabled: false,
    last_login_at: iso(-5 * MIN),
    roles: [{ role: "administrator", source: "master_admin" }],
  },
  {
    id: "7d1f6c1e-5b8a-4c35-9d0e-6a3b2b8f1a10",
    kind: "oidc",
    username: "ada@example.com",
    display_name: "Ada Lovelace",
    email: "ada@example.com",
    disabled: false,
    last_login_at: iso(-2 * HOUR),
    roles: [{ role: "restore_operator", source: "oidc_group" }],
  },
  {
    id: "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f",
    kind: "oidc",
    username: "linus@example.com",
    display_name: "Linus Torvalds",
    email: "linus@example.com",
    disabled: false,
    last_login_at: iso(-DAY),
    roles: [
      { role: "backup_administrator", source: "oidc_group" },
      { role: "auditor", source: "manual" },
    ],
  },
  {
    id: "3d4e5f6a-7b8c-4d9e-8f0a-2b3c4d5e6f7a",
    kind: "oidc",
    username: "mallory@example.com",
    display_name: "Mallory Example",
    email: "mallory@example.com",
    disabled: true,
    last_login_at: iso(-20 * DAY),
    roles: [{ role: "read_only", source: "manual" }],
  },
];

/** Permissions of a user from their role assignments (for the mock sessions). */
export function permissionsOf(roleNames) {
  const set = new Set();
  for (const r of roleNames) for (const p of roles.find((x) => x.role === r)?.permissions ?? []) set.add(p);
  return [...set].sort();
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

/**
 * @param {object} h helpers from mock-api.mjs: send, problem, readJson, audit
 * @returns {Array<[string, RegExp, Function]>}
 */
export function liveRoutes({ send, problem, readJson, audit }) {
  const need = (res, user, perm) => {
    if (user.permissions.includes(perm)) return true;
    problem(res, 403, "forbidden", "Forbidden", `Missing permission ${perm}.`);
    return false;
  };
  const invalid = (res, detail, location) =>
    send(
      res,
      400,
      { title: "Bad Request", status: 400, detail, code: "validation_failed", errors: [{ message: detail, location }] },
      { "content-type": "application/problem+json" },
    );
  const reasonOf = (b) => (typeof b?.reason === "string" ? b.reason.trim() : "");
  const hostFilter = (url) => url.searchParams.get("host_id");

  return [
    [
      "GET",
      /^\/api\/v1\/jobs$/,
      (req, res, url, user) => {
        if (!need(res, user, "backup.read")) return;
        const app = url.searchParams.get("application_id");
        const type = url.searchParams.get("type");
        const state = url.searchParams.get("state");
        const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 100) || 100, 1), 500);
        if (type && !["backup", "restore"].includes(type)) return invalid(res, "type must be backup or restore", "query.type");
        if (type === "restore" && !need(res, user, "restore.read")) return;
        const items = [
          ...(type === "restore" ? [] : protectionData.recoveryPoints.map(backupJob)),
          ...(type === "backup" || !user.permissions.includes("restore.read") ? [] : restoreData.runs.map(restoreJob)),
        ]
          .filter((j) => (!app || j.application_id === app) && (!state || j.state === state))
          .sort((a, b) => b.started_at.localeCompare(a.started_at))
          .slice(0, limit);
        send(res, 200, { items });
      },
    ],
    [
      "GET",
      /^\/api\/v1\/containers$/,
      (req, res, url, user) => {
        if (!need(res, user, "host.read")) return;
        const host = hostFilter(url);
        send(res, 200, { items: containers().filter((c) => !host || c.host_id === host) });
      },
    ],
    [
      "GET",
      /^\/api\/v1\/volumes$/,
      (req, res, url, user) => {
        if (!need(res, user, "host.read")) return;
        const host = hostFilter(url);
        send(res, 200, { items: volumes().filter((v) => !host || v.host_id === host) });
      },
    ],
    ["GET", /^\/api\/v1\/users$/, (req, res, url, user) => need(res, user, "user.read") && send(res, 200, { items: users })],
    ["GET", /^\/api\/v1\/roles$/, (req, res, url, user) => need(res, user, "user.read") && send(res, 200, { items: roles })],
    [
      "PUT",
      /^\/api\/v1\/users\/([0-9a-f-]{36})\/roles$/,
      async (req, res, url, user, m) => {
        if (!need(res, user, "user.manage")) return;
        const u = users.find((x) => x.id === m[1]);
        if (!u) return problem(res, 404, "not_found", "Not Found", "user not found");
        const b = await readJson(req);
        const reason = reasonOf(b);
        if (!reason || reason.length > 500) return invalid(res, "reason must be 1-500 characters", "body.reason");
        const wanted = Array.isArray(b.roles) ? b.roles : [];
        const bad = wanted.find((r) => !roles.some((x) => x.role === r));
        if (bad) return invalid(res, `unknown role ${bad}`, "body.roles");
        if (u.kind === "master_admin") return invalid(res, "the master admin's roles cannot be changed", "path.id");
        if (u.id === user.id && !wanted.includes("administrator") && u.roles.some((r) => r.role === "administrator" && r.source === "manual")) {
          return invalid(res, "you cannot remove your own administrator role", "body.roles");
        }
        u.roles = [...u.roles.filter((r) => r.source !== "manual"), ...[...new Set(wanted)].map((role) => ({ role, source: "manual" }))];
        audit("user.roles_changed", "success", req, { actor: user.display_name, target_type: "user", target_id: u.id, reason });
        send(res, 204);
      },
    ],
    [
      "PUT",
      /^\/api\/v1\/users\/([0-9a-f-]{36})\/status$/,
      async (req, res, url, user, m) => {
        if (!need(res, user, "user.manage")) return;
        const u = users.find((x) => x.id === m[1]);
        if (!u) return problem(res, 404, "not_found", "Not Found", "user not found");
        const b = await readJson(req);
        const reason = reasonOf(b);
        if (!reason || reason.length > 500) return invalid(res, "reason must be 1-500 characters", "body.reason");
        if (typeof b.disabled !== "boolean") return invalid(res, "disabled must be a boolean", "body.disabled");
        if (u.kind === "master_admin") return invalid(res, "the master admin cannot be disabled", "path.id");
        u.disabled = b.disabled;
        audit(b.disabled ? "user.disabled" : "user.enabled", "success", req, { actor: user.display_name, target_type: "user", target_id: u.id, reason });
        send(res, 204);
      },
    ],
    ["GET", /^\/api\/v1\/oidc\/group-mappings$/, (req, res, url, user) => need(res, user, "user.read") && send(res, 200, { items: groupMappings })],
    [
      "POST",
      /^\/api\/v1\/oidc\/group-mappings$/,
      async (req, res, url, user) => {
        if (!need(res, user, "user.manage")) return;
        const b = await readJson(req);
        const gid = typeof b?.group_id === "string" ? b.group_id.trim() : "";
        if (!gid || gid.length > 256) return invalid(res, "group_id must be 1-256 characters", "body.group_id");
        if (!roles.some((x) => x.role === b.role)) return invalid(res, `unknown role ${b.role}`, "body.role");
        if (!groupMappings.some((g) => g.provider === b.provider && g.group_id === gid && g.role === b.role)) {
          groupMappings.push({ provider: b.provider || "entra", group_id: gid, role: b.role });
        }
        audit("oidc.group_mapping_added", "success", req, { actor: user.display_name, target_type: "group_mapping", target_id: gid });
        send(res, 204);
      },
    ],
    [
      "POST",
      /^\/api\/v1\/oidc\/group-mappings\/remove$/,
      async (req, res, url, user) => {
        if (!need(res, user, "user.manage")) return;
        const b = await readJson(req);
        const i = groupMappings.findIndex((g) => g.provider === b?.provider && g.group_id === b?.group_id && g.role === b?.role);
        if (i < 0) return problem(res, 404, "not_found", "Not Found", "mapping not found");
        groupMappings.splice(i, 1);
        audit("oidc.group_mapping_removed", "success", req, { actor: user.display_name, target_type: "group_mapping", target_id: b.group_id });
        send(res, 204);
      },
    ],
  ];
}

// SPDX-License-Identifier: Apache-2.0
//
// Phase 7 part of the mock API, imported by scripts/mock-api.mjs: Protection
// Policies (schedules + grandfather-father-son retention, assignments),
// Recovery Contracts (evaluated on every read like internal/protection),
// deletion with a 7-day grace period (recovery points and Repositories), and
// notification channels / deliveries / SMTP settings (internal/notify).
// Shapes follow api/openapi.yaml. In-memory only.
//
// Seed data:
//   policies   Nightly production (daily 01:00 America/Chicago; Web shop and
//              mft-pg), Hourly critical (15 * * * *, UTC, live; redis-cache),
//              Weekly archive (Sunday 03:00 Europe/Berlin, DISABLED, keeps 3
//              yearly, offsite-nfs)
//   contracts  Web shop (RPO 24 h; config, volume:shop_pgdata,
//              bind:/srv/shop/uploads) satisfied; redis-cache (RPO 1 h)
//              violated; wiki (no RPO) unknown (missing from inventory)
//   channels   Ops email (email, all events, ≥ warning), PagerDuty webhook
//              (signed; backup.failed, contract.*, agent.offline; critical;
//              its last delivery failed with 503: "Send test" fails too —
//              any webhook host containing "pagerduty" or "fail" does)
//   smtp       smtp.example.com:587 STARTTLS, user dbr2, password stored

import { randomUUID } from "node:crypto";
import { publish } from "./mock-events.mjs";
import { fleetData } from "./mock-fleet.mjs";
import { protectionData } from "./mock-protection.mjs";

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();
const GRACE_MS = 7 * DAY;

const { SHOP, MFT, REDIS, WIKI, OFFSITE } = protectionData.ids;

// ---------------------------------------------------------------------------
// Cron (robfig/cron standard 5-field parser, as internal/protection uses)
// ---------------------------------------------------------------------------

const PRESETS = { hourly: "0 * * * *", daily: "0 1 * * *", weekly: "0 1 * * 0", monthly: "0 1 1 * *" };
const MONTHS = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"];
const DAYS = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
const FIELDS = [
  { name: "minute", min: 0, max: 59 },
  { name: "hour", min: 0, max: 23 },
  { name: "day of month", min: 1, max: 31 },
  { name: "month", min: 1, max: 12, names: MONTHS },
  { name: "day of week", min: 0, max: 6, names: DAYS },
];

function cronValue(t, f) {
  if (/^\d+$/.test(t)) {
    const n = Number(t);
    if (n < f.min || n > f.max) throw new Error(`${f.name}: end of range (${n}) above maximum (${f.max})`);
    return n;
  }
  const i = f.names?.indexOf(t.toUpperCase()) ?? -1;
  if (i < 0) throw new Error(`failed to parse int from ${t}`);
  return f.min + i;
}

function cronField(raw, f) {
  const set = new Set();
  for (const part of raw.split(",")) {
    const [range, stepText, extra] = part.split("/");
    if (extra !== undefined || range === "") throw new Error(`too many slashes: ${part}`);
    let step = 1;
    if (stepText !== undefined) {
      if (!/^\d+$/.test(stepText) || Number(stepText) < 1) throw new Error(`failed to parse int from ${stepText}`);
      step = Number(stepText);
    }
    let lo;
    let hi;
    if (range === "*" || range === "?") [lo, hi] = [f.min, f.max];
    else if (range.includes("-")) {
      const [a, b] = range.split("-");
      [lo, hi] = [cronValue(a, f), cronValue(b, f)];
      if (lo > hi) throw new Error(`beginning of range (${lo}) beyond end of range (${hi}): ${range}`);
    } else {
      lo = cronValue(range, f);
      hi = stepText !== undefined ? f.max : lo;
    }
    for (let v = lo; v <= hi; v += step) set.add(v);
  }
  return { star: raw === "*" || raw === "?", values: set };
}

/** Normalises a preset or validates a cron expression; throws with the parser's reason. */
function normalizeSchedule(s) {
  const t = String(s ?? "").trim();
  if (PRESETS[t.toLowerCase()]) return PRESETS[t.toLowerCase()];
  const parts = t.split(/\s+/).filter(Boolean);
  if (parts.length !== 5) throw new Error(`expected exactly 5 fields, found ${parts.length}: [${parts.join(" ")}]`);
  parts.forEach((p, i) => cronField(p, FIELDS[i]));
  return parts.join(" ");
}

/** Wall-clock parts of `ms` in `tz`. */
function wall(ms, tz) {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    hourCycle: "h23",
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "numeric",
    minute: "numeric",
  }).formatToParts(new Date(ms));
  const get = (t) => Number(parts.find((p) => p.type === t)?.value);
  return { y: get("year"), mo: get("month"), d: get("day"), h: get("hour"), mi: get("minute") };
}

/** UTC ms of a wall-clock time in `tz`. */
function fromWall(y, mo, d, h, mi, tz) {
  const guess = Date.UTC(y, mo - 1, d, h, mi);
  let t = guess;
  for (let i = 0; i < 2; i++) {
    const w = wall(t, tz);
    t += guess - Date.UTC(w.y, w.mo - 1, w.d, w.h, w.mi);
  }
  return t;
}

/** The next time `cron` fires in `tz` after now (null when none within ~400 days). */
function nextRun(cron, tz) {
  const [mi, h, dom, mo, dow] = cron.split(" ").map((p, i) => cronField(p, FIELDS[i]));
  const now = Date.now();
  const today = wall(now, tz);
  for (let i = 0; i < 400; i++) {
    const day = new Date(Date.UTC(today.y, today.mo - 1, today.d + i));
    const [y, m, d, wd] = [day.getUTCFullYear(), day.getUTCMonth() + 1, day.getUTCDate(), day.getUTCDay()];
    if (!mo.values.has(m)) continue;
    const domOk = dom.values.has(d);
    const dowOk = dow.values.has(wd);
    if (!(dom.star || dow.star ? domOk && dowOk : domOk || dowOk)) continue;
    for (const hh of [...h.values].sort((a, b) => a - b)) {
      for (const mm of [...mi.values].sort((a, b) => a - b)) {
        const t = fromWall(y, m, d, hh, mm, tz);
        if (t > now) return new Date(t).toISOString();
      }
    }
  }
  return null;
}

const validTz = (tz) => {
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
};

// ---------------------------------------------------------------------------
// Policies
// ---------------------------------------------------------------------------

const RETENTION_DEFAULTS = { keep_last: 7, keep_hourly: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 12, keep_yearly: 0 };

export const POLICY_IDS = {
  nightly: "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e01",
  hourly: "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e02",
  weekly: "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e03",
};

const policies = [
  {
    id: POLICY_IDS.nightly,
    name: "Nightly production",
    description: "Every production application, once a night after the business day",
    schedule: PRESETS.daily,
    timezone: "America/Chicago",
    enabled: true,
    consistency_mode: null,
    repository_id: null,
    retention: { ...RETENTION_DEFAULTS },
    created_at: iso(-20 * DAY),
    updated_at: iso(-6 * DAY),
  },
  {
    id: POLICY_IDS.hourly,
    name: "Hourly critical",
    description: "Caches and queues that must lose at most an hour",
    schedule: "15 * * * *",
    timezone: "UTC",
    enabled: true,
    consistency_mode: "live",
    repository_id: null,
    retention: { keep_last: 24, keep_hourly: 48, keep_daily: 7, keep_weekly: 4, keep_monthly: 6, keep_yearly: 0 },
    created_at: iso(-15 * DAY),
    updated_at: iso(-15 * DAY),
  },
  {
    id: POLICY_IDS.weekly,
    name: "Weekly archive",
    description: "Long-term copies in the second data center",
    schedule: "0 3 * * 0",
    timezone: "Europe/Berlin",
    enabled: false,
    consistency_mode: null,
    repository_id: OFFSITE,
    retention: { keep_last: 4, keep_hourly: 0, keep_daily: 0, keep_weekly: 8, keep_monthly: 12, keep_yearly: 3 },
    created_at: iso(-60 * DAY),
    updated_at: iso(-10 * DAY),
  },
];

/** application id -> policy id */
const assignments = new Map([
  [SHOP, POLICY_IDS.nightly],
  [MFT, POLICY_IDS.nightly],
  [REDIS, POLICY_IDS.hourly],
]);
fleetData.setPolicyOf((id) => assignments.get(id) ?? null);

function policyOut(p) {
  let next = null;
  if (p.enabled) {
    try {
      next = nextRun(p.schedule, p.timezone);
    } catch {
      next = null;
    }
  }
  return { ...p, applications: [...assignments.values()].filter((x) => x === p.id).length, next_run: next };
}

function policyDetail(p) {
  const assigned = [...assignments.entries()]
    .filter(([, pid]) => pid === p.id)
    .map(([aid]) => fleetData.apps.find((a) => a.id === aid))
    .filter(Boolean)
    .map((a) => ({ id: a.id, name: fleetData.appName(a), host_id: a.host_id }))
    .sort((a, b) => a.name.localeCompare(b.name));
  return { ...policyOut(p), assigned_applications: assigned };
}

// ---------------------------------------------------------------------------
// Recovery Contracts
// ---------------------------------------------------------------------------

/** application id -> stored contract */
const contracts = new Map([
  [SHOP, { max_rpo_minutes: 1440, required_components: ["config", "volume:shop_pgdata", "bind:/srv/shop/uploads"], violated_since: null, updated_at: iso(-10 * DAY) }],
  [REDIS, { max_rpo_minutes: 60, required_components: ["config"], violated_since: iso(-4 * HOUR + 5 * MIN), updated_at: iso(-12 * DAY) }],
  [WIKI, { max_rpo_minutes: null, required_components: ["config"], violated_since: null, updated_at: iso(-30 * DAY) }],
]);

const hoursText = (ms) => (ms >= 2 * HOUR ? `${Math.round(ms / HOUR)} h` : `${Math.max(1, Math.round(ms / MIN))} min`);
const rpoText = (min) => (min % 60 === 0 ? `${min / 60} h` : `${min} min`);

/** internal/protection contract evaluation: state and reasons. */
function evaluate(appId, c) {
  const app = fleetData.apps.find((a) => a.id === appId);
  if (!app || app.missing_since) {
    return { state: "unknown", reasons: ["the application is missing from its host's latest inventory"] };
  }
  const latest = protectionData.recoveryPoints
    .filter((r) => r.application_id === appId && r.state === "committed")
    .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
  const reasons = [];
  if (!latest) {
    reasons.push("no recovery point exists");
  } else {
    const age = Date.now() - Date.parse(latest.committed_at ?? latest.created_at);
    if (c.max_rpo_minutes !== null && age > c.max_rpo_minutes * MIN) {
      reasons.push(`the latest recovery point is ${hoursText(age)} old (maximum RPO ${rpoText(c.max_rpo_minutes)})`);
    }
    const got = new Set((latest._manifest?.components ?? []).filter((x) => x.status === "succeeded").map((x) => x.name));
    for (const r of c.required_components) {
      if (!got.has(r)) reasons.push(`required component ${r} is not in the latest recovery point`);
    }
  }
  return { state: reasons.length ? "violated" : "satisfied", reasons };
}

function contractOut(appId) {
  const c = contracts.get(appId);
  const { state, reasons } = evaluate(appId, c);
  if (state === "violated" && !c.violated_since) c.violated_since = iso();
  if (state === "satisfied") c.violated_since = null;
  const app = fleetData.apps.find((a) => a.id === appId);
  return {
    application_id: appId,
    application_name: app ? fleetData.appName(app) : appId,
    max_rpo_minutes: c.max_rpo_minutes,
    required_components: c.required_components,
    state,
    state_reasons: reasons,
    evaluated_at: iso(),
    violated_since: state === "violated" ? c.violated_since : null,
  };
}

// Contract evaluation recorded in the manifest of every new recovery point.
protectionData.setContractAtCapture((appId, components) => {
  const c = contracts.get(appId);
  if (!c) return undefined;
  const got = new Set(components.filter((x) => x.status === "succeeded").map((x) => x.name));
  const missing = c.required_components.filter((r) => !got.has(r));
  return {
    satisfied: missing.length === 0,
    details: { max_rpo_minutes: c.max_rpo_minutes, required_components: c.required_components, missing_components: missing },
  };
});

// ---------------------------------------------------------------------------
// Notification channels, deliveries, SMTP (internal/notify)
// ---------------------------------------------------------------------------

export const CHANNEL_IDS = {
  email: "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a01",
  webhook: "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a02",
};

const PD_URL = "https://events.pagerduty.example.com/integration/dbr2/enqueue";

const channels = [
  {
    id: CHANNEL_IDS.email,
    name: "Ops email",
    kind: "email",
    enabled: true,
    config: { to: ["ops@example.com", "oncall@example.com"] },
    _secret: null,
    events: [],
    min_severity: "warning",
    created_at: iso(-20 * DAY),
    updated_at: iso(-20 * DAY),
    last_delivery_at: iso(-2 * HOUR),
    last_error: null,
  },
  {
    id: CHANNEL_IDS.webhook,
    name: "PagerDuty webhook",
    kind: "webhook",
    enabled: true,
    config: { url: PD_URL },
    _secret: "pd-signing-secret-0123456789",
    events: ["backup.failed", "contract.*", "agent.offline"],
    min_severity: "critical",
    created_at: iso(-18 * DAY),
    updated_at: iso(-3 * DAY),
    last_delivery_at: iso(-DAY),
    last_error: "POST https://events.pagerduty.example.com/integration/dbr2/enqueue: 503 Service Unavailable",
  },
];

let deliverySeq = 900;
let notificationSeq = 300;
function delivery(channelId, over) {
  return {
    id: ++deliverySeq,
    _channel: channelId,
    notification_id: ++notificationSeq,
    attempts: 1,
    next_attempt_at: null,
    last_error: null,
    sent_at: null,
    ...over,
  };
}

const deliveries = [
  delivery(CHANNEL_IDS.email, {
    event_type: "backup.failed",
    severity: "critical",
    message: "Backup of redis-cache failed: snapshot of config failed: agent docker-prod-01: context deadline exceeded",
    state: "sent",
    sent_at: iso(-4 * HOUR + MIN),
    created_at: iso(-4 * HOUR),
  }),
  delivery(CHANNEL_IDS.email, {
    event_type: "contract.violated",
    severity: "critical",
    message: "Recovery Contract of redis-cache violated: no recovery point exists",
    state: "sent",
    sent_at: iso(-2 * HOUR),
    created_at: iso(-2 * HOUR - MIN),
  }),
  delivery(CHANNEL_IDS.email, {
    event_type: "backup.completed",
    severity: "warning",
    message: "Backup of Web shop committed as Partial: optional components failed",
    state: "sent",
    sent_at: iso(-30 * HOUR + 6 * MIN),
    created_at: iso(-30 * HOUR + 5 * MIN),
  }),
  delivery(CHANNEL_IDS.webhook, {
    event_type: "contract.violated",
    severity: "critical",
    message: "Recovery Contract of redis-cache violated: no recovery point exists",
    state: "pending",
    attempts: 3,
    next_attempt_at: iso(25 * MIN),
    last_error: "POST https://events.pagerduty.example.com/integration/dbr2/enqueue: 503 Service Unavailable",
    created_at: iso(-3 * HOUR - 55 * MIN),
  }),
  delivery(CHANNEL_IDS.webhook, {
    event_type: "backup.failed",
    severity: "critical",
    message: "Backup of Web shop failed: pre-hook 1 (db) exited with status 2",
    state: "failed",
    attempts: 6,
    last_error: "POST https://events.pagerduty.example.com/integration/dbr2/enqueue: 503 Service Unavailable",
    created_at: iso(-54 * HOUR + 3 * MIN),
  }),
  delivery(CHANNEL_IDS.webhook, {
    event_type: "agent.offline",
    severity: "critical",
    message: "Agent legacy-db is offline",
    state: "sent",
    sent_at: iso(-DAY),
    created_at: iso(-DAY - MIN),
  }),
];

const smtp = {
  configured: true,
  host: "smtp.example.com",
  port: 587,
  username: "dbr2",
  from: "DBR2 <dbr2@example.com>",
  tls: "starttls",
  _password: "mock-smtp-password",
  updated_at: iso(-20 * DAY),
};
const smtpOut = () => {
  const { _password, ...rest } = smtp;
  return { ...rest, password_set: Boolean(_password) };
};

function channelOut(c) {
  const { _secret, config, ...rest } = c;
  return { ...rest, config: c.kind === "email" ? { to: config.to } : { url: config.url }, secret_set: Boolean(_secret) };
}

const EVENT_RE = /^(\*|[a-z0-9_]+(\.[a-z0-9_]+)*(\.\*)?)$/;
const EMAIL_RE = /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/;

function webhookUrlError(raw) {
  let u;
  try {
    u = new URL(String(raw ?? "").trim());
  } catch {
    return "url must be an absolute https URL";
  }
  if (u.protocol === "https:") return null;
  const h = u.hostname.replace(/^\[|\]$/g, "");
  if (u.protocol === "http:" && (h === "localhost" || h === "::1" || /^127\./.test(h))) return null;
  return "webhook URLs must use https (plain http only for localhost / loopback addresses)";
}

/** internal/notify validateChannel; returns [error, validated]. */
function validateChannel(kind, b) {
  const name = typeof b?.name === "string" ? b.name.trim() : "";
  if (!name || name.length > 100) return ["name is required (at most 100 characters)", "body.name"];
  const cfg = b.config ?? {};
  const to = Array.isArray(cfg.to) ? cfg.to : [];
  const secret = b.secret;
  if (secret !== undefined && typeof secret !== "string") return ["secret must be a string", "body.secret"];
  let config;
  if (kind === "email") {
    if (to.length === 0 || to.length > 50) return ["an email channel needs 1-50 recipients", "body.config.to"];
    if (cfg.url) return ["url applies to webhook channels only", "body.config.url"];
    if (secret) return ["secret applies to webhook channels only", "body.secret"];
    const bad = to.find((t) => typeof t !== "string" || !EMAIL_RE.test(t.trim()));
    if (bad !== undefined) return [`mail: invalid address ${JSON.stringify(bad)}`, "body.config.to"];
    config = { to: to.map((t) => t.trim()) };
  } else if (kind === "webhook") {
    if (to.length) return ["to applies to email channels only", "body.config.to"];
    const err = webhookUrlError(cfg.url);
    if (err) return [err, "body.config.url"];
    if (secret && secret.length < 16) return ["the webhook secret must be at least 16 characters", "body.secret"];
    config = { url: String(cfg.url).trim() };
  } else {
    return ["kind must be email or webhook", "body.kind"];
  }
  const events = [];
  const list = Array.isArray(b.events) ? b.events : [];
  if (list.length > 100) return ["at most 100 event patterns", "body.events"];
  for (const e0 of list) {
    const e = String(e0).trim();
    if (!e || events.includes(e)) continue;
    if (e.length > 100 || !EVENT_RE.test(e)) {
      return [`invalid event pattern "${e}" (use an event type such as backup.failed, or a prefix wildcard such as backup.*)`, "body.events"];
    }
    events.push(e);
  }
  const min = b.min_severity ?? "warning";
  if (!["info", "warning", "critical"].includes(min)) return ["min_severity must be info, warning or critical", "body.min_severity"];
  return [null, { name, config, events, min_severity: min, enabled: b.enabled ?? true, secret }];
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

/**
 * @param {object} h helpers from mock-api.mjs: send, problem, readJson, audit
 * @returns {Array<[string, RegExp, Function]>}
 */
export function policyRoutes({ send, problem, readJson, audit }) {
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
  const conflict = (res, detail) => problem(res, 409, "conflict", "Conflict", detail);
  const notFound = (res, what) => problem(res, 404, "not_found", "Not Found", `${what} not found`);
  const findApp = (id) => fleetData.apps.find((a) => a.id === id);
  const UUID = "([0-9a-f-]{36})";
  const RP = "(rp_[0-9A-HJKMNP-TV-Z]{26})";
  const reasonOf = (b) => (typeof b?.reason === "string" ? b.reason.trim() : "");
  const act = (user, req, type, target_type, target_id, extra = {}) =>
    audit(type, "success", req, { actor: user.display_name, target_type, target_id, ...extra });

  /** Validates a PolicyBody; returns [error, location] or the policy fields. */
  function readPolicy(b) {
    if (!b || typeof b !== "object") return { error: ["body must be a JSON object", "body"] };
    const allowed = ["name", "description", "schedule", "timezone", "enabled", "consistency_mode", "repository_id", "retention"];
    const extra = Object.keys(b).find((k) => !allowed.includes(k));
    if (extra) return { error: [`unexpected property ${extra}`, `body.${extra}`] };
    const name = typeof b.name === "string" ? b.name.trim() : "";
    if (!name || name.length > 100) return { error: ["name is required (at most 100 characters)", "body.name"] };
    if (typeof b.enabled !== "boolean") return { error: ["enabled is required", "body.enabled"] };
    let schedule;
    try {
      schedule = normalizeSchedule(b.schedule);
    } catch (err) {
      return { error: [`schedule must be hourly, daily, weekly, monthly or a 5-field cron expression: ${err.message}`, "body.schedule"] };
    }
    const timezone = b.timezone || "UTC";
    if (!validTz(timezone)) return { error: [`unknown timezone "${timezone}"`, "body.timezone"] };
    if (b.consistency_mode !== undefined && !["live", "quiesced", "offline"].includes(b.consistency_mode)) {
      return { error: ["consistency_mode must be live, quiesced or offline", "body.consistency_mode"] };
    }
    if (b.repository_id !== undefined && !protectionData.repositories.some((r) => r.id === b.repository_id)) {
      return { error: ["unknown repository", "body.repository_id"] };
    }
    const retention = { ...RETENTION_DEFAULTS, ...(b.retention ?? {}) };
    for (const [k, v] of Object.entries(retention)) {
      if (!Number.isInteger(v) || v < (k === "keep_last" ? 1 : 0)) {
        return { error: ["keep_last must be at least 1 and the other retention counts non-negative", `body.retention.${k}`] };
      }
    }
    return {
      value: {
        name,
        description: typeof b.description === "string" ? b.description.trim() : "",
        schedule,
        timezone,
        enabled: b.enabled,
        consistency_mode: b.consistency_mode ?? null,
        repository_id: b.repository_id ?? null,
        retention,
      },
    };
  }

  return [
    // --- Policies -------------------------------------------------------------
    ["GET", /^\/api\/v1\/policies$/, (req, res, url, user) => need(res, user, "policy.read") && send(res, 200, { items: policies.map(policyOut) })],
    [
      "POST",
      /^\/api\/v1\/policies$/,
      async (req, res, url, user) => {
        if (!need(res, user, "policy.manage")) return;
        const r = readPolicy(await readJson(req));
        if (r.error) return invalid(res, ...r.error);
        if (policies.some((p) => p.name === r.value.name)) return conflict(res, `a policy named ${r.value.name} already exists`);
        const p = { id: randomUUID(), ...r.value, created_at: iso(), updated_at: iso() };
        policies.push(p);
        act(user, req, "policy.created", "policy", p.id);
        send(res, 201, policyOut(p));
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/policies/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.read")) return;
        const p = policies.find((x) => x.id === m[1]);
        if (!p) return notFound(res, "policy");
        send(res, 200, policyDetail(p));
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/policies/${UUID}$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        const p = policies.find((x) => x.id === m[1]);
        if (!p) return notFound(res, "policy");
        const r = readPolicy(await readJson(req));
        if (r.error) return invalid(res, ...r.error);
        if (policies.some((x) => x.id !== p.id && x.name === r.value.name)) return conflict(res, `a policy named ${r.value.name} already exists`);
        Object.assign(p, r.value, { updated_at: iso() });
        act(user, req, "policy.updated", "policy", p.id);
        send(res, 200, policyOut(p));
      },
    ],
    [
      "DELETE",
      new RegExp(`^/api/v1/policies/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        const i = policies.findIndex((x) => x.id === m[1]);
        if (i < 0) return notFound(res, "policy");
        for (const [aid, pid] of assignments) if (pid === m[1]) assignments.delete(aid);
        policies.splice(i, 1);
        act(user, req, "policy.deleted", "policy", m[1]);
        send(res, 204);
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/applications/${UUID}/policy$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        if (!findApp(m[1])) return notFound(res, "application");
        const b = await readJson(req);
        if (!b || !("policy_id" in b)) return invalid(res, "policy_id is required (null removes the assignment)", "body.policy_id");
        if (b.policy_id === null || b.policy_id === "") {
          assignments.delete(m[1]);
        } else {
          if (!policies.some((p) => p.id === b.policy_id)) return invalid(res, "unknown policy", "body.policy_id");
          assignments.set(m[1], b.policy_id);
        }
        act(user, req, "policy.assigned", "application", m[1], { details: { policy_id: b.policy_id } });
        send(res, 204);
      },
    ],

    // --- Recovery Contracts ---------------------------------------------------
    [
      "GET",
      /^\/api\/v1\/contracts$/,
      (req, res, url, user) => {
        if (!need(res, user, "policy.read")) return;
        const items = [...contracts.keys()].map(contractOut).sort((a, b) => a.application_name.localeCompare(b.application_name));
        send(res, 200, { items });
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/applications/${UUID}/contract$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.read")) return;
        if (!findApp(m[1])) return notFound(res, "application");
        if (!contracts.has(m[1])) return notFound(res, "contract");
        send(res, 200, contractOut(m[1]));
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/applications/${UUID}/contract$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        if (!findApp(m[1])) return notFound(res, "application");
        const b = await readJson(req);
        if (!b || typeof b !== "object") return invalid(res, "body must be a JSON object", "body");
        const rpo = b.max_rpo_minutes ?? null;
        if (rpo !== null && (!Number.isInteger(rpo) || rpo < 1)) return invalid(res, "max_rpo_minutes must be at least 1", "body.max_rpo_minutes");
        const req2 = b.required_components ?? [];
        if (!Array.isArray(req2) || req2.some((c) => typeof c !== "string" || !c.trim())) {
          return invalid(res, "required_components must be a list of component names", "body.required_components");
        }
        const prev = contracts.get(m[1]);
        contracts.set(m[1], {
          max_rpo_minutes: rpo,
          required_components: [...new Set(req2.map((c) => c.trim()))],
          violated_since: prev?.violated_since ?? null,
          updated_at: iso(),
        });
        act(user, req, "contract.updated", "application", m[1]);
        send(res, 200, contractOut(m[1]));
      },
    ],
    [
      "DELETE",
      new RegExp(`^/api/v1/applications/${UUID}/contract$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        if (!findApp(m[1])) return notFound(res, "application");
        if (!contracts.delete(m[1])) return notFound(res, "contract");
        act(user, req, "contract.deleted", "application", m[1]);
        send(res, 204);
      },
    ],

    // --- Deletion with a grace period -------------------------------------------
    [
      "POST",
      new RegExp(`^/api/v1/recovery-points/${RP}/delete$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "backup.delete")) return;
        const r = protectionData.recoveryPoints.find((x) => x.id === m[1]);
        if (!r) return notFound(res, "recovery point");
        const b = await readJson(req);
        const reason = reasonOf(b);
        if (reason.length < 3 || reason.length > 500) return invalid(res, "reason must be 3-500 characters", "body.reason");
        if (typeof b?.confirmation !== "string" || b.confirmation.trim() !== r.application_name) {
          return invalid(res, "confirmation must match the application name", "body.confirmation");
        }
        if (!["committed", "missing"].includes(r.state) || r.delete_after) {
          return conflict(res, r.delete_after ? "the recovery point is already scheduled for deletion" : `a ${r.state} recovery point cannot be deleted`);
        }
        r.delete_after = iso(GRACE_MS);
        r.delete_reason = reason;
        act(user, req, "backup.delete_scheduled", "recovery_point", r.id, { reason });
        publish("backup.updated", "backup.read", { recovery_point_id: r.id, application_id: r.application_id, state: "committed", workflow_id: r.workflow_id });
        send(res, 200, protectionData.publicRepo(r));
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/recovery-points/${RP}/undelete$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "backup.delete")) return;
        const r = protectionData.recoveryPoints.find((x) => x.id === m[1]);
        if (!r) return notFound(res, "recovery point");
        if (!r.delete_after || !["committed", "missing"].includes(r.state)) return conflict(res, "the recovery point is not scheduled for deletion");
        r.delete_after = null;
        r.delete_reason = null;
        act(user, req, "backup.delete_cancelled", "recovery_point", r.id);
        publish("backup.updated", "backup.read", { recovery_point_id: r.id, application_id: r.application_id, state: "committed", workflow_id: r.workflow_id });
        send(res, 200, protectionData.publicRepo(r));
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/delete$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = protectionData.repositories.find((x) => x.id === m[1]);
        if (!r) return notFound(res, "repository");
        const b = await readJson(req);
        const reason = reasonOf(b);
        if (reason.length < 3 || reason.length > 500) return invalid(res, "reason must be 3-500 characters", "body.reason");
        if (typeof b?.confirmation !== "string" || b.confirmation.trim() !== r.name) {
          return invalid(res, "confirmation must match the Repository name", "body.confirmation");
        }
        if (!["ready", "awaiting_escrow", "unavailable"].includes(r.status)) return conflict(res, `a ${r.status.replace("_", " ")} Repository cannot be deleted`);
        r.status = "pending_deletion";
        r.is_default = false;
        r.delete_after = iso(GRACE_MS);
        r._deleteReason = reason;
        act(user, req, "repository.delete_scheduled", "repository", r.id, { reason });
        send(res, 200, protectionData.publicRepo(r));
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/undelete$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = protectionData.repositories.find((x) => x.id === m[1]);
        if (!r) return notFound(res, "repository");
        if (r.status !== "pending_deletion") return conflict(res, "the Repository is not pending deletion");
        r.status = r.escrow_confirmed_at ? "ready" : "awaiting_escrow";
        r.delete_after = null;
        r._deleteReason = null;
        act(user, req, "repository.delete_cancelled", "repository", r.id);
        send(res, 200, protectionData.publicRepo(r));
      },
    ],

    // --- Notification channels --------------------------------------------------
    [
      "GET",
      /^\/api\/v1\/notification-channels$/,
      (req, res, url, user) => need(res, user, "policy.read") && send(res, 200, { items: channels.map(channelOut) }),
    ],
    [
      "POST",
      /^\/api\/v1\/notification-channels$/,
      async (req, res, url, user) => {
        if (!need(res, user, "policy.manage")) return;
        const b = await readJson(req);
        const [err, v] = validateChannel(b?.kind, b ?? {});
        if (err) return invalid(res, err, v);
        const c = {
          id: randomUUID(),
          name: v.name,
          kind: b.kind,
          enabled: v.enabled,
          config: v.config,
          _secret: v.secret || null,
          events: v.events,
          min_severity: v.min_severity,
          created_at: iso(),
          updated_at: iso(),
          last_delivery_at: null,
          last_error: null,
        };
        channels.push(c);
        act(user, req, "notification.channel.created", "notification_channel", c.id);
        send(res, 201, channelOut(c));
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/notification-channels/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.read")) return;
        const c = channels.find((x) => x.id === m[1]);
        if (!c) return notFound(res, "notification channel");
        send(res, 200, channelOut(c));
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/notification-channels/${UUID}$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        const c = channels.find((x) => x.id === m[1]);
        if (!c) return notFound(res, "notification channel");
        const b = (await readJson(req)) ?? {};
        if ("kind" in b) return invalid(res, "unexpected property kind (a channel's kind cannot change)", "body.kind");
        if (typeof b.enabled !== "boolean") return invalid(res, "enabled is required", "body.enabled");
        const [err, v] = validateChannel(c.kind, b);
        if (err) return invalid(res, err, v);
        Object.assign(c, { name: v.name, config: v.config, events: v.events, min_severity: v.min_severity, enabled: v.enabled, updated_at: iso() });
        if (v.secret !== undefined) c._secret = v.secret || null;
        act(user, req, "notification.channel.updated", "notification_channel", c.id);
        send(res, 200, channelOut(c));
      },
    ],
    [
      "DELETE",
      new RegExp(`^/api/v1/notification-channels/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        const i = channels.findIndex((x) => x.id === m[1]);
        if (i < 0) return notFound(res, "notification channel");
        channels.splice(i, 1);
        for (let j = deliveries.length - 1; j >= 0; j--) if (deliveries[j]._channel === m[1]) deliveries.splice(j, 1);
        act(user, req, "notification.channel.deleted", "notification_channel", m[1]);
        send(res, 204);
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/notification-channels/${UUID}/test$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        const c = channels.find((x) => x.id === m[1]);
        if (!c) return notFound(res, "notification channel");
        let error = null;
        if (c.kind === "email") {
          if (!smtp.configured) error = "SMTP is not configured";
        } else {
          const host = new URL(c.config.url).hostname;
          if (host.includes("pagerduty") || host.includes("fail")) error = `POST ${c.config.url}: 503 Service Unavailable`;
        }
        audit("notification.channel.tested", error ? "failure" : "success", req, {
          actor: user.display_name,
          target_type: "notification_channel",
          target_id: c.id,
        });
        send(res, 200, error ? { delivered: false, duration_ms: 1843, error } : { delivered: true, duration_ms: c.kind === "email" ? 412 : 187 });
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/notification-channels/${UUID}/deliveries$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.read")) return;
        if (!channels.some((x) => x.id === m[1])) return notFound(res, "notification channel");
        const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 50) || 50, 1), 500);
        const items = deliveries
          .filter((d) => d._channel === m[1])
          .sort((a, b) => b.created_at.localeCompare(a.created_at))
          .slice(0, limit)
          .map((d) => Object.fromEntries(Object.entries(d).filter(([k]) => k !== "_channel")));
        send(res, 200, { items });
      },
    ],

    // --- SMTP ----------------------------------------------------------------------
    ["GET", /^\/api\/v1\/settings\/smtp$/, (req, res, url, user) => need(res, user, "policy.read") && send(res, 200, smtpOut())],
    [
      "PUT",
      /^\/api\/v1\/settings\/smtp$/,
      async (req, res, url, user) => {
        if (!need(res, user, "policy.manage")) return;
        const b = await readJson(req);
        const host = typeof b?.host === "string" ? b.host.trim() : "";
        if (!host || host.length > 253) return invalid(res, "host is required", "body.host");
        if (!Number.isInteger(b.port) || b.port < 1 || b.port > 65535) return invalid(res, "port must be 1-65535", "body.port");
        const from = typeof b.from === "string" ? b.from.trim() : "";
        if (from.length < 3 || !/@/.test(from)) return invalid(res, `mail: invalid sender address ${JSON.stringify(from)}`, "body.from");
        if (!["starttls", "tls", "none"].includes(b.tls)) return invalid(res, "tls must be starttls, tls or none", "body.tls");
        if (b.tls === "none" && !["localhost", "127.0.0.1", "::1"].includes(host)) {
          return invalid(res, "tls none (plain text) is allowed only for a relay on localhost", "body.tls");
        }
        Object.assign(smtp, { configured: true, host, port: b.port, username: (b.username ?? "").trim(), from, tls: b.tls, updated_at: iso() });
        if (typeof b.password === "string") smtp._password = b.password || null;
        act(user, req, "settings.smtp.updated", "settings", "smtp");
        send(res, 200, smtpOut());
      },
    ],
  ];
}

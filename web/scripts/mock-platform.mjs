// SPDX-License-Identifier: Apache-2.0
//
// Phase 9 part of the mock API, imported by scripts/mock-api.mjs: Repository
// verification (repository/{id}/verify), escrow health / package
// regeneration / escrow drills (internal/protection/escrow_health.go), and
// platform self-protection (ADR-0008: Platform Recovery Bundles and the
// System Repository). Shapes follow api/openapi.yaml. In-memory only.
//
// Seed data:
//   escrow health  unhealthy: nas01-backups recipients_changed (critical),
//                  lab-scratch not_confirmed (critical), offsite-nfs
//                  reconfirm_due (warning), drill_due (warning: the last
//                  drill was completed 400 days ago)
//   drills         the mock drill "package" is NOT encrypted: it carries a
//                  comment line with the confirmation code (also logged)
//   platform       succeeded (1 d ago), partial (2 d: offsite-nfs state export
//                  failed), failed (3 d: System Repository snapshot timed
//                  out; the bundle directory copy exists), succeeded (8 d)
//   timings        "Verify now" finishes after MOCK_VERIFY_MS (default 4 s),
//                  "Run now" after MOCK_PLATFORM_MS (default 6 s)

import { randomBytes, randomUUID } from "node:crypto";
import { publish } from "./mock-events.mjs";
import { protectionData } from "./mock-protection.mjs";

const VERIFY_MS = Number(process.env.MOCK_VERIFY_MS ?? 4_000);
const PLATFORM_MS = Number(process.env.MOCK_PLATFORM_MS ?? 6_000);

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const MiB = 1024 ** 2;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();
const hex = (n) => randomBytes(n).toString("hex");

const { NAS01, OFFSITE, MFT } = protectionData.ids;
const { repositories, recipients, recoveryPoints, alerts } = protectionData;

// ---------------------------------------------------------------------------
// Escrow health and drills
// ---------------------------------------------------------------------------

const MIN_RECIPIENTS = 2;
const RECONFIRM_MS = 90 * DAY;
const DRILL_MS = 365 * DAY;

const drills = [
  {
    id: "4d5e6f70-8192-4a3b-9c4d-5e6f708192a1",
    created_at: iso(-400 * DAY - HOUR),
    completed_at: iso(-400 * DAY),
    recipients: 2,
    _code: protectionData.newCode(),
  },
];

const sameSet = (a, b) => a.length === b.length && [...a].sort().join() === [...b].sort().join();

function escrowHealth() {
  const problems = [];
  const now = Date.now();
  if (recipients.length < MIN_RECIPIENTS) {
    problems.push({
      code: "too_few_recipients",
      severity: "critical",
      message: `${recipients.length} escrow recipients are configured; ${MIN_RECIPIENTS} are required`,
    });
  }
  const current = recipients.map((r) => r.id);
  for (const r of repositories) {
    if (r.status === "retired") continue;
    if (!r.escrow_confirmed_at) {
      problems.push({
        code: "not_confirmed",
        severity: "critical",
        repository_id: r.id,
        message: `the escrow package of Repository ${r.name} has not been confirmed`,
      });
    } else if (now - Date.parse(r.escrow_confirmed_at) > RECONFIRM_MS) {
      problems.push({
        code: "reconfirm_due",
        severity: "warning",
        repository_id: r.id,
        message: `escrow of Repository ${r.name} was last confirmed more than 90 days ago; re-confirm it`,
      });
    }
    if (!sameSet(r._recipientIds ?? [], current)) {
      problems.push({
        code: "recipients_changed",
        severity: "critical",
        repository_id: r.id,
        message: `the escrow recipients changed since the package of Repository ${r.name} was generated; regenerate it`,
      });
    }
  }
  const last = drills
    .filter((d) => d.completed_at)
    .map((d) => d.completed_at)
    .sort()
    .pop();
  if (!last || now - Date.parse(last) > DRILL_MS) {
    problems.push({
      code: "drill_due",
      severity: "warning",
      message: "no escrow drill in the last 12 months: decrypt a drill package with an escrow identity from the safe",
    });
  }
  return { healthy: problems.length === 0, problems, recipients: recipients.length, last_drill_at: last ?? null, checked_at: iso() };
}

function drillPackage(code) {
  const body = randomBytes(270).toString("base64").replace(/(.{64})/g, "$1\n");
  return (
    "-----BEGIN AGE ENCRYPTED FILE-----\n" +
    body.trim() +
    "\n-----END AGE ENCRYPTED FILE-----\n" +
    `# MOCK ONLY (not encrypted): escrow drill, confirmation_code ${code}\n`
  );
}

const drillOut = (d) => Object.fromEntries(Object.entries(d).filter(([k]) => !k.startsWith("_")));

// ---------------------------------------------------------------------------
// Platform self-protection
// ---------------------------------------------------------------------------

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
function pbId(offsetMs = 0) {
  let t = Date.now() + offsetMs;
  let time = "";
  for (let i = 0; i < 10; i++) {
    time = CROCKFORD[t % 32] + time;
    t = Math.floor(t / 32);
  }
  return `pb_${time}${Array.from(randomBytes(16), (x) => CROCKFORD[x % 32]).join("")}`;
}

const stamp = (ms) => new Date(ms).toISOString().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z");

const TABLES = [
  ["agents", 6],
  ["applications", 9],
  ["audit_events", 4_812],
  ["policies", 3],
  ["recovery_points", 418],
  ["repositories", 4],
  ["restore_runs", 27],
  ["users", 4],
];

function bundleManifest(startedMs, { missing = null } = {}) {
  return {
    format_version: 1,
    kind: "dbr2-platform-bundle",
    created_at: new Date(startedMs + 40_000).toISOString(),
    platform_version: "0.1.0.0",
    components: { server: "0.1.0.0", worker: "0.1.0.0", "db-schema": "0.1.0.0" },
    schema_version: 14,
    tables: TABLES.map(([name, rows]) => ({ name, rows, file: `db/${name}.jsonl.zst` })),
    secrets: ["DBR2_SECRET_KEY", "internal token"],
    repositories: repositories
      .filter((r) => r.status !== "retired")
      .map((r) => ({
        id: r.id,
        name: r.name,
        management_url: r.management_url,
        ...(r.id === missing ? { missing: true, error: "state export failed: dial tcp 10.20.5.20:8091: connect: connection refused" } : { file: `reposervers/${r.id}.tar` }),
      })),
    escrow_recipients: recipients.map((r) => r.id),
    temporal: { included: false, note: "Temporal history is not needed to recover; schedules are re-created from the database." },
  };
}

function platformRun(offsetMs, durationMs, state, over = {}) {
  const started = Date.now() + offsetMs;
  const file = `dbr2-platform-${stamp(started)}.tar.zst.age`;
  return {
    id: pbId(offsetMs),
    state,
    trigger: "schedule",
    workflow_id: "platform/backup",
    started_at: new Date(started).toISOString(),
    finished_at: new Date(started + durationMs).toISOString(),
    size_bytes: Math.round(46.3 * MiB),
    sha256: hex(32),
    file_name: file,
    snapshot_id: `k${hex(16)}`,
    repository_id: NAS01,
    bundle_path: `/var/lib/dbr2/platform-bundles/${file}`,
    error: null,
    manifest: bundleManifest(started),
    ...over,
  };
}

const platformBackups = [
  platformRun(-DAY, 94_000, "succeeded"),
  (() => {
    const r = platformRun(-2 * DAY, 131_000, "partial", {
      error: "reposerver offsite-nfs: state export failed: dial tcp 10.20.5.20:8091: connect: connection refused",
      size_bytes: Math.round(45.9 * MiB),
    });
    r.manifest = bundleManifest(Date.parse(r.started_at), { missing: OFFSITE });
    return r;
  })(),
  platformRun(-3 * DAY, 301_000, "failed", {
    error: "System Repository nas01-backups: kopia snapshot create: context deadline exceeded",
    snapshot_id: null,
  }),
  platformRun(-8 * DAY, 88_000, "succeeded", { size_bytes: Math.round(44.8 * MiB), trigger: "manual" }),
];

function finishPlatform(run) {
  const sys = repositories.find((r) => r.is_system && r.status !== "retired");
  const started = Date.parse(run.started_at);
  run.finished_at = iso();
  run.file_name = `dbr2-platform-${stamp(started)}.tar.zst.age`;
  run.bundle_path = `/var/lib/dbr2/platform-bundles/${run.file_name}`;
  run.sha256 = hex(32);
  run.size_bytes = Math.round(46.4 * MiB);
  run.manifest = bundleManifest(started);
  if (sys) {
    run.state = "succeeded";
    run.snapshot_id = `k${hex(16)}`;
    run.repository_id = sys.id;
  } else {
    run.state = "partial";
    run.error = "no System Repository is designated";
  }
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

/**
 * @param {object} h helpers from mock-api.mjs: send, problem, readJson, audit
 * @returns {Array<[string, RegExp, Function]>}
 */
export function platformRoutes({ send, problem, readJson, audit }) {
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
  const findRepo = (res, id) => {
    const r = repositories.find((x) => x.id === id);
    if (!r) problem(res, 404, "not_found", "Not Found", "repository not found");
    return r;
  };
  const UUID = "([0-9a-f-]{36})";
  const act = (user, req, type, target_type, target_id, result = "success") =>
    audit(type, result, req, { actor: user.display_name, target_type, target_id });

  return [
    // --- Verification ---------------------------------------------------------
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/verify$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const repo = findRepo(res, m[1]);
        if (!repo) return;
        const b = (await readJson(req)) ?? {};
        const pct = b.read_percent ?? 10;
        if (typeof pct !== "number" || pct < 0 || pct > 100) return invalid(res, "read_percent must be 0-100", "body.read_percent");
        if (["unavailable", "retired"].includes(repo.status)) return conflict(res, `the Repository is ${repo.status}`);
        let targets;
        if (b.recovery_point_id) {
          const rp = recoveryPoints.find((r) => r.id === b.recovery_point_id && r.repository_id === repo.id);
          if (!rp) return invalid(res, "recovery point not found in this Repository", "body.recovery_point_id");
          targets = [rp];
        } else {
          targets = recoveryPoints.filter((r) => r.repository_id === repo.id && r.state === "committed" && r.verification !== "verification_failed");
        }
        act(user, req, "repository.verify.requested", "repository", repo.id);
        setTimeout(() => {
          for (const rp of targets) {
            if (rp.state !== "committed" && rp.state !== "missing") continue;
            rp.verified_at = iso();
            if (rp.application_id === MFT) {
              rp.verification = "verification_failed";
              const alert = {
                id: Math.max(0, ...alerts.map((x) => x.id)) + 1,
                severity: "critical",
                type: "verification.failed",
                target_type: "recovery_point",
                target_id: rp.id,
                message: `Verification of an ${rp.application_name} recovery point failed: 2 errors in 1 component`,
                details: { recovery_point_id: rp.id, repository_id: repo.id },
                created_at: iso(),
                acknowledged_at: null,
              };
              alerts.push(alert);
              publish("alert.created", "backup.read", { severity: alert.severity, type: alert.type, target_type: alert.target_type, target_id: alert.target_id, message: alert.message });
            } else {
              rp.verification = "verified";
              rp._verification = protectionData.verificationOf(rp._manifest, pct);
            }
            publish("backup.updated", "backup.read", { recovery_point_id: rp.id, application_id: rp.application_id, state: "committed", workflow_id: rp.workflow_id });
          }
          repo.last_verified_at = iso();
        }, VERIFY_MS).unref();
        send(res, 202, { workflow_id: `repository/${repo.id}/verify` });
      },
    ],

    // --- Escrow health, regeneration, drills ------------------------------------
    ["GET", /^\/api\/v1\/escrow\/health$/, (req, res, url, user) => need(res, user, "repository.read") && send(res, 200, escrowHealth())],
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/escrow/regenerate$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = findRepo(res, m[1]);
        if (!r) return;
        if (recipients.length < MIN_RECIPIENTS) return conflict(res, `${MIN_RECIPIENTS} escrow recipients are required`);
        if (r.status === "unavailable" || r.status === "retired") return conflict(res, "reposerver: the Repository password cannot be read");
        const code = protectionData.newCode();
        r._code = code;
        r._package = protectionData.fakePackage(r.name, code);
        r._recipientIds = recipients.map((x) => x.id);
        r.escrow_recipients = recipients.length;
        r.escrow_generated_at = iso();
        r.escrow_confirmed_at = null;
        act(user, req, "repository.escrow.regenerated", "repository", r.id);
        console.log(`[mock] regenerated escrow confirmation code for ${r.name}: ${code}`);
        send(res, 200, { repository: protectionData.publicRepo(r), escrow_package: r._package, escrow_filename: protectionData.escrowFilename(r) });
      },
    ],
    [
      "GET",
      /^\/api\/v1\/escrow\/drills$/,
      (req, res, url, user) => {
        if (!need(res, user, "repository.read")) return;
        send(res, 200, { items: [...drills].sort((a, b) => b.created_at.localeCompare(a.created_at)).map(drillOut) });
      },
    ],
    [
      "POST",
      /^\/api\/v1\/escrow\/drills$/,
      (req, res, url, user) => {
        if (!need(res, user, "repository.manage")) return;
        if (recipients.length === 0) return conflict(res, "no escrow recipients are configured");
        const code = protectionData.newCode();
        const d = { id: randomUUID(), created_at: iso(), completed_at: null, recipients: recipients.length, _code: code };
        drills.push(d);
        act(user, req, "escrow.drill.started", "escrow_drill", d.id);
        console.log(`[mock] escrow drill ${d.id} confirmation code: ${code}`);
        send(res, 201, { ...drillOut(d), package: drillPackage(code) });
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/escrow/drills/${UUID}/complete$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const d = drills.find((x) => x.id === m[1]);
        if (!d) return problem(res, 404, "not_found", "Not Found", "escrow drill not found");
        const b = await readJson(req);
        const code = typeof b?.confirmation_code === "string" ? b.confirmation_code : "";
        if (code.length < 16 || code.length > 40) return invalid(res, "confirmation_code must be 16-40 characters", "body.confirmation_code");
        if (protectionData.normCode(code) !== protectionData.normCode(d._code)) {
          act(user, req, "escrow.drill.completed", "escrow_drill", d.id, "failure");
          return invalid(res, "the confirmation code does not match this drill package", "body.confirmation_code");
        }
        if (d.completed_at) return conflict(res, "the drill was already completed");
        d.completed_at = iso();
        act(user, req, "escrow.drill.completed", "escrow_drill", d.id);
        send(res, 200, drillOut(d));
      },
    ],

    // --- Platform self-protection ----------------------------------------------
    [
      "GET",
      /^\/api\/v1\/platform\/backups$/,
      (req, res, url, user) => {
        if (!need(res, user, "repository.manage")) return;
        const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 50) || 50, 1), 500);
        send(res, 200, { items: [...platformBackups].sort((a, b) => b.started_at.localeCompare(a.started_at)).slice(0, limit) });
      },
    ],
    [
      "POST",
      /^\/api\/v1\/platform\/backups$/,
      (req, res, url, user) => {
        if (!need(res, user, "repository.manage")) return;
        if (platformBackups.some((b) => b.state === "running")) return conflict(res, "a platform backup is already running");
        const run = {
          id: pbId(),
          state: "running",
          trigger: "manual",
          workflow_id: "platform/backup",
          started_at: iso(),
          finished_at: null,
          size_bytes: 0,
          sha256: null,
          file_name: null,
          snapshot_id: null,
          repository_id: repositories.find((r) => r.is_system)?.id ?? null,
          bundle_path: null,
          error: null,
        };
        platformBackups.push(run);
        act(user, req, "platform.backup.requested", "platform", "platform");
        setTimeout(() => finishPlatform(run), PLATFORM_MS).unref();
        send(res, 202, { workflow_id: run.workflow_id });
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/repositories/${UUID}/system$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = findRepo(res, m[1]);
        if (!r) return;
        if (r.status === "retired" || r.status === "pending_deletion") return conflict(res, `a ${r.status.replace("_", " ")} Repository cannot be the System Repository`);
        for (const x of repositories) x.is_system = false;
        r.is_system = true;
        act(user, req, "repository.system_designated", "repository", r.id);
        send(res, 200, protectionData.publicRepo(r));
      },
    ],
  ];
}

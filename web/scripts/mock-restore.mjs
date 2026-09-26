// SPDX-License-Identifier: Apache-2.0
//
// Restores (Phase 5) part of the mock API, imported by scripts/mock-api.mjs.
// Shapes follow api/openapi.yaml (Preview, RestoreBody, StartRestoreBody,
// RestoreRunDTO) and the result document of workflows/restore. In-memory only.
//
// Previews are computed from the mock recovery points' manifests and the
// application's containers / networks (mock-fleet.mjs):
//   docker-prod-01  the source host of the seeded recovery points: a clean
//                   IN-PLACE restore that counts as PRODUCTION (the shop is
//                   tagged production and runs), stopping its containers
//   docker-edge-02  alternate host with COLLISIONS (container name, port,
//                   volume of another Compose project, bind path in use
//                   unless remapped): blocked
//   docker-dr-03    clean ALTERNATE-HOST restore: volumes and containers are
//                   created, networks created, images pulled by digest
//   other hosts     blocked (target host is not active)
//
// Starting a restore creates a run that goes requested → running through the
// applicable steps over ~15 s and ends succeeded. Seeded history: a failed
// alternate-host restore (image pull denied), a rolled-back in-place
// production restore (health check failed, with a log tail) and a succeeded
// restore of mft-pg.

import { randomBytes, randomUUID } from "node:crypto";
import { publish } from "./mock-events.mjs";
import { fleetData } from "./mock-fleet.mjs";
import { protectionData } from "./mock-protection.mjs";

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();

const EDGE = "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02";
const DR = "f7b3d4c5-8a9e-4c0f-9b2a-3c4d5e6f7a06";

/** Total simulated duration of a started restore. */
const RUN_MS = Number(process.env.MOCK_RESTORE_MS ?? 15_000);
/** Time a new run stays "requested" before the workflow picks it up. */
const QUEUE_MS = 1_000;

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
function restoreId(offsetMs = 0) {
  let t = Date.now() + offsetMs;
  let time = "";
  for (let i = 0; i < 10; i++) {
    time = CROCKFORD[t % 32] + time;
    t = Math.floor(t / 32);
  }
  const rand = Array.from(randomBytes(16), (x) => CROCKFORD[x % 32]).join("");
  return `rs_${time}${rand}`;
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

const STEPS = [
  "grant-access",
  "agent-access",
  "images",
  "stop-application",
  "restore-data",
  "recreate-containers",
  "start-application",
  "restore-database",
  "health-check",
  "commit",
];
const RUNNING = new Set(["running", "paused", "restarting"]);

class HttpError extends Error {
  constructor(status, code, detail) {
    super(detail);
    this.status = status;
    this.code = code;
  }
}

function remap(p, remaps) {
  for (const r of remaps) {
    const from = r.from.replace(/\/+$/, "");
    if (p === from || p.startsWith(`${from}/`)) return `${r.to.replace(/\/+$/, "")}${p.slice(from.length)}` || "/";
  }
  return p;
}

/** Validates the body like resolveRestore in internal/protection/restore.go. */
function resolve(rp, body) {
  if (!rp) throw new HttpError(404, "not_found", "recovery point not found");
  if (rp.state !== "committed" || !rp._manifest) {
    throw new HttpError(409, "conflict", `recovery point ${rp.id} is ${rp.state}; only committed recovery points can be restored`);
  }
  const m = rp._manifest;
  const targetId = body?.target_host_id || rp.host_id;
  const target = fleetData.agents.find((a) => a.id === targetId);
  if (!target) throw new HttpError(404, "not_found", "target host not found");
  const remaps = Array.isArray(body?.path_remaps) ? body.path_remaps : [];
  for (const r of remaps) {
    if (typeof r?.from !== "string" || typeof r?.to !== "string" || !r.from.startsWith("/") || !r.to.startsWith("/") || r.to.includes("..")) {
      throw new HttpError(400, "validation_failed", "path remaps must be absolute paths without '..'");
    }
  }
  let want = Array.isArray(body?.components) ? body.components : [];
  if (want.length === 0) {
    want = m.components.filter((c) => c.kind !== "fsmeta" && c.kind !== "image" && c.status === "succeeded").map((c) => c.name);
  }
  const selected = [];
  for (const n of want) {
    const c = m.components.find((x) => x.name === n);
    if (!c) throw new HttpError(400, "validation_failed", `the recovery point has no component "${n}"`);
    if (c.status !== "succeeded") throw new HttpError(400, "validation_failed", `component ${n} was not captured (${c.status})`);
    if (c.kind === "fsmeta" || c.kind === "image") throw new HttpError(400, "validation_failed", `component ${n} cannot be restored on its own`);
    if (!selected.includes(c)) selected.push(c);
  }
  return { m, target, remaps, selected };
}

/** Containers, published ports and networks of the source application (from the fleet mock). */
function topology(rp) {
  const app = fleetData.apps.find((a) => a.id === rp.application_id);
  const containers = (app?.containers_detail ?? []).map((c) => ({
    id: c.id,
    name: c.name,
    state: c.state,
    ports: (c.ports ?? []).filter((p) => p.host_port).map((p) => `${p.host_port}/${p.protocol}`),
  }));
  if (containers.length === 0) {
    containers.push({ id: randomBytes(32).toString("hex"), name: rp.application_name, state: "running", ports: [] });
  }
  const networks = (app?.analysis?.networks ?? []).filter((n) => !["bridge", "host", "none"].includes(n.name));
  return { app, containers, networks };
}

export function computePreview(rp, body) {
  const { m, target, remaps, selected } = resolve(rp, body);
  const { app, containers, networks } = topology(rp);
  const inPlace = target.id === rp.host_id;
  const edge = target.id === EDGE;
  const p = {
    recovery_point_id: rp.id,
    application_name: m.application.name,
    source_host_id: rp.host_id,
    target_host_id: target.id,
    target_hostname: target.hostname,
    mode: inPlace ? "in_place" : "alternate_host",
    production: false,
    production_reasons: [],
    components: [],
    stop_containers: [],
    create_containers: [],
    networks: [],
    images: [],
    ports: [...new Set(containers.flatMap((c) => c.ports))].sort(),
    collisions: [],
    warnings: [],
    blocked: false,
  };
  if (inPlace && app) p.target_application_id = app.id;

  if (inPlace) {
    p.stop_containers = containers.map((c) => ({ id: c.id, name: c.name, state: c.state }));
    if (app?.environment === "production") {
      p.production = true;
      p.production_reasons.push("the target application is tagged environment: production");
    }
    if (containers.some((c) => RUNNING.has(c.state))) {
      p.production = true;
      p.production_reasons.push("the restore overwrites a running application in place");
    }
    p.networks = networks.map((n) => ({ name: n.name, action: "exists" }));
  } else {
    for (const c of containers) {
      if (edge && c === containers[0]) {
        p.collisions.push({ kind: "container_name", name: c.name, detail: "a container with this name exists on the target and belongs to Compose project shop-staging" });
      } else {
        p.create_containers.push(c.name);
      }
    }
    if (edge) {
      for (const port of p.ports) p.collisions.push({ kind: "port", name: port, detail: "already published by container staging-proxy" });
    }
    for (const n of networks) {
      if (n.external) {
        // Both alternate hosts already have the shared reverse-proxy network.
        p.networks.push({ name: n.name, action: "exists" });
      } else {
        p.networks.push({ name: n.name, action: "create" });
      }
    }
    if (edge) p.warnings.push("the latest inventory of docker-edge-02 is 3 hours old: collisions are checked against it");
  }

  for (const im of m.images ?? []) {
    const present = inPlace;
    p.images.push({ ref: im.ref, ...(im.digest ? { digest: im.digest } : {}), action: present ? "present" : "pull" });
    if (!present && !im.digest) {
      p.warnings.push(`image ${im.ref} has no registry digest (local build): it must be pulled by tag or built on the target first`);
    }
  }

  const workingDir = m.application.working_dir || `/srv/${rp.application_name}`;
  for (const c of selected) {
    const pc = { name: c.name, kind: c.kind, size_bytes: c.size_bytes ?? 0, action: "overwrite", target: "" };
    if (c.kind === "volume") {
      pc.target = c.volume_name ?? c.name.replace(/^volume:/, "");
      pc.action = inPlace ? "overwrite" : "create";
      if (edge) {
        pc.action = "overwrite";
        p.collisions.push({ kind: "volume", name: pc.target, detail: "exists and belongs to Compose project shop-staging" });
      }
    } else if (c.kind === "bind_mount") {
      pc.target = remap(c.path ?? c.name.replace(/^bind:/, ""), remaps);
      if (edge && pc.target === (c.path ?? "")) {
        p.collisions.push({ kind: "bind_path", name: pc.target, detail: "path is mounted by container legacy-files (another application)" });
      }
    } else if (c.kind === "config") {
      pc.action = "restore_files";
      pc.target = remap(workingDir, remaps);
    } else if (c.kind === "database") {
      pc.action = "load_dump";
      pc.target = c.database ? `${c.database.engine} in ${c.database.container || c.database.service}` : "database";
    }
    p.components.push(pc);
  }

  if (target.status !== "active") {
    p.collisions.push({ kind: "dependency", name: target.hostname, detail: `target host is ${target.status}` });
  }
  p.blocked = p.collisions.length > 0;
  return p;
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

/** The steps workflows/restore runs for this preview (see src/lib/restore.ts). */
function stepsFor(preview) {
  const running = preview.stop_containers.some((c) => RUNNING.has(c.state));
  return STEPS.filter((s) => {
    switch (s) {
      case "grant-access":
        return preview.target_host_id !== preview.source_host_id;
      case "images":
        return preview.images.length > 0;
      case "stop-application":
        return running;
      case "recreate-containers":
        return preview.create_containers.length > 0;
      case "restore-database":
        return preview.components.some((c) => c.action === "load_dump");
      case "health-check":
        return running || preview.create_containers.length > 0;
      default:
        return true;
    }
  });
}

const hex = (n) => randomBytes(n).toString("hex");

function successResult(run) {
  const p = run.preview;
  const manifest = protectionData.recoveryPoints.find((r) => r.id === run.recovery_point_id)?._manifest;
  const byName = new Map((manifest?.components ?? []).map((c) => [c.name, c]));
  const fsmeta = (name) => (manifest?.components ?? []).find((c) => c.kind === "fsmeta" && c.parent === name);
  const healthNames = p.mode === "in_place" ? p.stop_containers.map((c) => c.name) : p.create_containers;
  return {
    images: p.images.map((im) => ({ ref: im.ref, ...(im.digest ? { digest: im.digest } : {}), status: im.action === "pull" ? "pulled" : "present" })),
    components: p.components.map((c) => {
      const src = byName.get(c.name);
      const path = c.kind === "volume" ? `/var/lib/docker/volumes/${c.target}/_data` : c.target;
      return {
        name: c.name,
        status: "restored",
        target_path: path,
        bytes: c.size_bytes,
        files: src?.files ?? 1,
        ...(fsmeta(c.name) ? { metadata_applied: src?.files ?? 1 } : {}),
        ...(c.action === "create" ? { created_volume: c.kind === "volume" } : { previous_path: `${path}.dbr2-prev-${run.id.slice(-8).toLowerCase()}` }),
      };
    }),
    containers: p.create_containers.map((name) => ({ name, status: "created", container_id: hex(32), was_running: true })),
    health: {
      ok: true,
      containers: healthNames.map((name) => ({ container_id: hex(32), name, state: "running", health: name.includes("db") ? "healthy" : "none", ok: true })),
    },
  };
}

/**
 * Moves started runs along their steps, publishing `restore.updated` on every
 * state / step change and `job.progress` (restore_components) while data is
 * restored. Runs on a timer and whenever restores are read. A cancelled run
 * rolls back (compensation) and ends rolled_back, or failed when nothing had
 * changed yet.
 */
function advanceRuns() {
  const now = Date.now();
  for (const r of runs) {
    if (!r._startMs || (r.state !== "requested" && r.state !== "running")) continue;
    const before = `${r.state}/${r.step}`;
    const emit = () => {
      if (`${r.state}/${r.step}` === before) return;
      publish("restore.updated", "restore.read", {
        restore_id: r.id,
        application_id: r.application_id,
        state: r.state,
        ...(r.step ? { step: r.step } : {}),
        ...(r.error ? { error: r.error } : {}),
      });
    };
    const elapsed = now - r._startMs;
    if (r._cancelAt) {
      const changed = r.started_at !== null && r.step !== "grant-access" && r.step !== "agent-access";
      r.state = changed ? "rolled_back" : "failed";
      r.error = "cancelled by " + r._cancelBy;
      r.finished_at = iso();
      r.result = changed ? { ...successResult(r), rolled_back: true, health: null } : undefined;
      r.step = null;
      emit();
      continue;
    }
    if (elapsed < QUEUE_MS) continue;
    const steps = stepsFor(r.preview);
    const per = (RUN_MS - QUEUE_MS) / steps.length;
    const i = Math.floor((elapsed - QUEUE_MS) / per);
    if (!r.started_at) r.started_at = new Date(r._startMs + QUEUE_MS).toISOString();
    if (i >= steps.length) {
      r.state = "succeeded";
      r.step = null;
      r.finished_at = new Date(r._startMs + RUN_MS).toISOString();
      r.result = successResult(r);
      emit();
    } else {
      r.state = "running";
      r.step = steps[i];
      emit();
      if (r.step === "restore-data" && r.preview.components.length > 0) {
        const within = (elapsed - QUEUE_MS - i * per) / per;
        const n = r.preview.components.length;
        const j = Math.min(n - 1, Math.floor(within * n));
        const c = r.preview.components[j];
        r._runId = r._runId ?? randomUUID();
        publish("job.progress", "backup.read", {
          command_id: `application/${r.target_application_id || r.application_id}/${r._runId}/restore-components`,
          host_id: r.target_host_id,
          kind: "restore_components",
          state: "running",
          workflow_id: r.workflow_id,
          run_id: r._runId,
          application_id: r.target_application_id || r.application_id,
          progress: { done: j, total: n, component: c.name },
        });
      }
    }
  }
}
setInterval(advanceRuns, 700).unref();

/** The list view: no private fields, no preview / result (detail only). */
const listItem = (r) =>
  Object.fromEntries(Object.entries(r).filter(([k]) => !k.startsWith("_") && k !== "preview" && k !== "result"));
const detail = (r) => ({ ...listItem(r), preview: r.preview, ...(r.result ? { result: r.result } : {}) });

const runs = [];

function seed() {
  const shopRp = protectionData.recoveryPoints.find((r) => r.application_name === "Web shop" && r.state === "committed" && r.status === "complete");
  const mftRp = protectionData.recoveryPoints.find((r) => r.application_name === "mft-pg" && r.state === "committed");
  if (!shopRp || !mftRp) return;
  const base = (rp, preview, over) => ({
    id: restoreId(Date.parse(over.created_at) - Date.now()),
    recovery_point_id: rp.id,
    application_id: rp.application_id,
    application_name: rp.application_name,
    source_host_id: rp.host_id,
    source_hostname: rp.hostname,
    target_host_id: preview.target_host_id,
    target_hostname: preview.target_hostname,
    target_application_id: preview.target_application_id ?? null,
    mode: preview.mode,
    production: preview.production,
    components: preview.components.map((c) => c.name),
    path_remaps: [],
    reason: null,
    requested_by: "admin",
    step: null,
    error: null,
    workflow_id: null,
    started_at: null,
    finished_at: null,
    preview,
    ...over,
  });

  // Failed: alternate host, the private registry refused the pull (no data touched).
  const drPreview = computePreview(shopRp, { target_host_id: DR, path_remaps: [{ from: "/srv/shop", to: "/srv/shop-restored" }] });
  const failedAt = -4 * HOUR;
  const failed = base(shopRp, drPreview, {
    created_at: iso(failedAt),
    started_at: iso(failedAt + 2_000),
    finished_at: iso(failedAt + 41_000),
    path_remaps: [{ from: "/srv/shop", to: "/srv/shop-restored" }],
    reason: "DR drill Q3",
    state: "failed",
    step: "images",
    error: "images: pull registry.example.com/shop/web@sha256:a1ea29fa2835: unauthorized: authentication required",
    result: {
      images: [
        { ref: "registry.example.com/shop/web:2.4.1", digest: drPreview.images[0]?.digest, status: "failed", error: "unauthorized: authentication required" },
        { ref: "postgres:18-alpine", digest: drPreview.images[1]?.digest, status: "pulled" },
      ],
    },
  });
  failed.workflow_id = `application/${shopRp.application_id}`;

  // Rolled back: in-place production restore, the database did not start.
  const prodPreview = computePreview(shopRp, {});
  const rbAt = -2 * DAY;
  const rolledBack = base(shopRp, prodPreview, {
    created_at: iso(rbAt),
    started_at: iso(rbAt + 3_000),
    finished_at: iso(rbAt + 9 * MIN),
    reason: "INC-48391: orders table corrupted by failed migration",
    requested_by: "ada@example.com",
    state: "rolled_back",
    step: "health-check",
    error: "the restored application did not become healthy: shop-db-1 (exited/none)",
    result: {
      images: prodPreview.images.map((im) => ({ ref: im.ref, ...(im.digest ? { digest: im.digest } : {}), status: "present" })),
      components: prodPreview.components.map((c) => ({
        name: c.name,
        status: "restored",
        target_path: c.kind === "volume" ? `/var/lib/docker/volumes/${c.target}/_data` : c.target,
        bytes: c.size_bytes,
        files: c.kind === "volume" ? 2143 : c.kind === "config" ? 4 : 18_433,
        ...(c.kind === "config" ? {} : { metadata_applied: c.kind === "volume" ? 2143 : 18_433 }),
        previous_path: `${c.kind === "volume" ? `/var/lib/docker/volumes/${c.target}/_data` : c.target}.dbr2-prev-4f2a`,
      })),
      health: {
        ok: false,
        containers: [
          { container_id: hex(32), name: "shop-web-1", state: "running", health: "none", ok: true },
          {
            container_id: hex(32),
            name: "shop-db-1",
            state: "exited",
            health: "none",
            ok: false,
            exit_code: 1,
            log_tail: [
              "2026-09-23 09:14:02.113 UTC [1] LOG:  starting PostgreSQL 18.0 on x86_64-pc-linux-musl",
              '2026-09-23 09:14:02.118 UTC [1] LOG:  listening on IPv4 address "0.0.0.0", port 5432',
              "2026-09-23 09:14:02.131 UTC [29] LOG:  database system was interrupted; last known up at 2026-09-23 08:00:03 UTC",
              "2026-09-23 09:14:02.139 UTC [29] LOG:  invalid primary checkpoint record",
              "2026-09-23 09:14:02.139 UTC [29] PANIC:  could not locate a valid checkpoint record",
              "2026-09-23 09:14:02.412 UTC [1] LOG:  startup process (PID 29) was terminated by signal 6: Aborted",
              "2026-09-23 09:14:02.412 UTC [1] LOG:  aborting startup due to startup process failure",
              "2026-09-23 09:14:02.419 UTC [1] LOG:  database system is shut down",
            ].join("\n"),
          },
          { container_id: hex(32), name: "shop-worker-1", state: "running", health: "none", ok: true },
        ],
      },
      rolled_back: true,
    },
  });
  rolledBack.workflow_id = `application/${shopRp.application_id}`;

  // Succeeded: mft-pg to the DR host.
  const mftPreview = computePreview(mftRp, { target_host_id: DR });
  const okAt = -26 * HOUR;
  const ok = base(mftRp, mftPreview, {
    created_at: iso(okAt),
    started_at: iso(okAt + 1_500),
    finished_at: iso(okAt + 3 * MIN + 12_000),
    reason: "Restore test for the quarterly DR report",
    requested_by: "admin",
    state: "succeeded",
  });
  ok.result = successResult(ok);
  ok.workflow_id = `application/${mftRp.application_id}`;

  runs.push(failed, rolledBack, ok);
}
seed();

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

export function restoreRoutes({ send, problem, readJson, audit }) {
  const need = (res, user, perm) => {
    if (user.permissions.includes(perm)) return true;
    problem(res, 403, "forbidden", "Forbidden", `Missing permission ${perm}.`);
    return false;
  };
  const fail = (res, err) => {
    if (!(err instanceof HttpError)) throw err;
    if (err.status === 400) {
      return send(
        res,
        400,
        { title: "Bad Request", status: 400, detail: err.message, code: "validation_failed", errors: [{ message: err.message, location: "body" }] },
        { "content-type": "application/problem+json" },
      );
    }
    return problem(res, err.status, err.code, err.status === 404 ? "Not Found" : err.status === 403 ? "Forbidden" : "Conflict", err.message);
  };
  const findRp = (id) => {
    protectionData.advance();
    return protectionData.recoveryPoints.find((r) => r.id === id);
  };
  const RP = "(rp_[0-9A-HJKMNP-TV-Z]{26})";

  return [
    [
      "POST",
      new RegExp(`^/api/v1/recovery-points/${RP}/restore-preview$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "restore.execute")) return;
        const body = await readJson(req);
        try {
          send(res, 200, computePreview(findRp(m[1]), body ?? {}));
        } catch (err) {
          fail(res, err);
        }
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/recovery-points/${RP}/restores$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "restore.execute")) return;
        const body = (await readJson(req)) ?? {};
        const rp = findRp(m[1]);
        let preview;
        try {
          preview = computePreview(rp, body);
        } catch (err) {
          return fail(res, err);
        }
        if (preview.blocked) {
          return problem(res, 409, "conflict", "Conflict", `the restore is blocked by ${preview.collisions.length} collision(s); see the preview`);
        }
        const reason = typeof body.reason === "string" ? body.reason.trim() : "";
        if (preview.production) {
          if (!user.permissions.includes("restore.production")) {
            return problem(res, 403, "forbidden", "Forbidden", `this is a production restore (${preview.production_reasons.join("; ")}) and requires restore.production`);
          }
          if (String(body.confirmation ?? "").trim() !== preview.application_name) {
            return fail(res, new HttpError(400, "validation_failed", `type the application name "${preview.application_name}" to confirm a production restore`));
          }
          if (reason.length < 3) {
            return fail(res, new HttpError(400, "validation_failed", "a reason (or change ticket) is required for a production restore"));
          }
        }
        advanceRuns();
        if (runs.some((r) => r.application_id === rp.application_id && (r.state === "requested" || r.state === "running"))) {
          return problem(res, 409, "conflict", "Conflict", "an operation is already running for this application");
        }
        const run = {
          id: restoreId(),
          recovery_point_id: rp.id,
          application_id: rp.application_id,
          application_name: rp.application_name,
          source_host_id: rp.host_id,
          source_hostname: rp.hostname,
          target_host_id: preview.target_host_id,
          target_hostname: preview.target_hostname,
          target_application_id: preview.target_application_id ?? null,
          mode: preview.mode,
          production: preview.production,
          components: preview.components.map((c) => c.name),
          path_remaps: Array.isArray(body.path_remaps) ? body.path_remaps : [],
          reason: reason || null,
          requested_by: user.username,
          state: "requested",
          step: null,
          error: null,
          workflow_id: `application/${preview.target_application_id || rp.application_id}`,
          created_at: iso(),
          started_at: null,
          finished_at: null,
          preview,
          _startMs: Date.now(),
        };
        runs.unshift(run);
        audit("restore.requested", "success", req, {
          actor: user.display_name,
          target_type: "application",
          target_id: rp.application_id,
          reason: reason || undefined,
        });
        send(res, 202, listItem(run));
      },
    ],
    [
      "GET",
      /^\/api\/v1\/restores$/,
      (req, res, url, user) => {
        if (!need(res, user, "restore.read")) return;
        advanceRuns();
        const app = url.searchParams.get("application_id");
        const state = url.searchParams.get("state");
        const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 100) || 100, 1), 500);
        const items = runs
          .filter((r) => (!app || r.application_id === app || r.target_application_id === app) && (!state || r.state === state))
          .sort((a, b) => b.created_at.localeCompare(a.created_at))
          .slice(0, limit)
          .map(listItem);
        send(res, 200, { items });
      },
    ],
    [
      "POST",
      /^\/api\/v1\/restores\/(rs_[0-9A-HJKMNP-TV-Z]{26})\/cancel$/,
      (req, res, url, user, m) => {
        if (!need(res, user, "restore.execute")) return;
        advanceRuns();
        const r = runs.find((x) => x.id === m[1]);
        if (!r) return problem(res, 404, "not_found", "Not Found", "restore not found");
        if (r.state !== "requested" && r.state !== "running") {
          return problem(res, 409, "conflict", "Conflict", `the restore is ${r.state}; only a running restore can be cancelled`);
        }
        r._cancelAt = Date.now();
        r._cancelBy = user.username;
        audit("restore.cancel_requested", "success", req, { actor: user.display_name, target_type: "restore", target_id: r.id });
        send(res, 202);
        setTimeout(advanceRuns, 1_500);
      },
    ],
    [
      "GET",
      /^\/api\/v1\/restores\/(rs_[0-9A-HJKMNP-TV-Z]{26})$/,
      (req, res, url, user, m) => {
        if (!need(res, user, "restore.read")) return;
        advanceRuns();
        const r = runs.find((x) => x.id === m[1]);
        if (!r) return problem(res, 404, "not_found", "Not Found", "restore not found");
        send(res, 200, detail(r));
      },
    ],
  ];
}

/** Read access to the restore runs for the other mock modules (mock-live.mjs). */
export const restoreData = {
  runs,
  advance: advanceRuns,
};

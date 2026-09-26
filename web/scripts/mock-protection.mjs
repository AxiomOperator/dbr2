// SPDX-License-Identifier: Apache-2.0
//
// Repositories & backup (Phase 4) part of the mock API, imported by
// scripts/mock-api.mjs. Shapes follow api/openapi.yaml (EscrowRecipientDTO,
// RepositoryDTO, CreatedRepoBody, BackupSettingsDTO, RecoveryPointDTO,
// AlertDTO, HostSettingsDTO). In-memory only.
//
// Seed data:
//   escrow        two recipients (an age key and an SSH key)
//   repositories  nas01-backups (ready, default, NFS, healthy),
//                 lab-scratch (awaiting_escrow; confirmation code
//                 K7QX-M2DA-PL4W-ZT6R), offsite-nfs (unavailable, reposerver
//                 unreachable)
//   recovery pts  shop: committed/complete (quiesced), committed/partial
//                 (live, crash-consistent, optional bind mount failed),
//                 failed; mft-pg: committed; monitoring: in progress (commits
//                 after ~2 min), wiki: missing; redis-cache: failed (no
//                 success yet); metrics-agent: committed/partial
//   progress      pending recovery points publish job.progress every 0.7 s and
//                 backup.updated / alert.created when they commit
//   alerts        backup failed (critical), partial RP (warning), agent
//                 auto-resume (critical), reindex finished (info, acknowledged)
//   escrow codes  the mock "package" is NOT encrypted: it carries a comment
//                 line with the confirmation code, for local testing only.

import { randomBytes, randomUUID } from "node:crypto";
import { publish } from "./mock-events.mjs";
import { fleetData } from "./mock-fleet.mjs";

/** Simulated duration of a "Back up now" (progress events every ~0.7 s). */
const BACKUP_MS = Number(process.env.MOCK_BACKUP_MS ?? 15_000);
const TICK_MS = 700;

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();
const GiB = 1024 ** 3;
const TiB = 1024 * GiB;

const SHOP = "5f939a00-fccb-4376-a6cc-37eeb5542abe";
const MFT = "9fc3a8cb-13d7-4c97-a291-4aade4ca63ee";
const MONITORING = "1a2b3c4d-0000-4000-8000-000000000006";
const WIKI = "1a2b3c4d-0000-4000-8000-000000000007";
const REDIS = "1a2b3c4d-0000-4000-8000-000000000003";
const METRICS = "1a2b3c4d-0000-4000-8000-000000000008";
const PROD = "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01";
const EDGE = "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02";

const NAS01 = "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e01";
const LAB = "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e02";
const OFFSITE = "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e03";

// ---------------------------------------------------------------------------
// Escrow
// ---------------------------------------------------------------------------

const recipients = [
  {
    id: "6a1c2d3e-4f50-4617-8293-a4b5c6d7e801",
    name: "Ada Lovelace (IT security)",
    public_key: "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p",
    created_at: iso(-40 * DAY),
  },
  {
    id: "6a1c2d3e-4f50-4617-8293-a4b5c6d7e802",
    name: "Grace Hopper (infrastructure lead)",
    public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl grace@ops",
    created_at: iso(-40 * DAY + HOUR),
  },
];

const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
function newCode() {
  const b = randomBytes(16);
  const s = Array.from(b, (x) => B32[x % 32]).join("");
  return `${s.slice(0, 4)}-${s.slice(4, 8)}-${s.slice(8, 12)}-${s.slice(12, 16)}`;
}
const normCode = (c) => String(c ?? "").replace(/[\s-]/g, "").toUpperCase();

function fakePackage(repoName, code) {
  const body = randomBytes(360).toString("base64").replace(/(.{64})/g, "$1\n");
  return (
    "-----BEGIN AGE ENCRYPTED FILE-----\n" +
    body.trim() +
    "\n-----END AGE ENCRYPTED FILE-----\n" +
    `# MOCK ONLY (not encrypted): repository ${repoName}, confirmation_code ${code}\n`
  );
}

// ---------------------------------------------------------------------------
// Repositories
// ---------------------------------------------------------------------------

const hex = (n) => randomBytes(n).toString("hex");

function repo(over) {
  return {
    description: "",
    backend: "nfs",
    status: "ready",
    is_default: false,
    internal_server_url: "",
    management_url: "http://dbr2-reposerver:8091",
    cert_sha256: hex(32),
    kopia_repository_id: hex(16),
    splitter: "DYNAMIC-4M-BUZHASH",
    escrow_recipients: 2,
    escrow_generated_at: iso(-30 * DAY),
    escrow_confirmed_at: iso(-30 * DAY + 20 * MIN),
    last_reindex_at: null,
    created_at: iso(-30 * DAY),
    live: {
      initialized: true,
      server_running: true,
      kopia_version: "0.22.3",
      storage_healthy: true,
      storage_total_bytes: 8 * TiB,
      storage_free_bytes: Math.round(4.8 * TiB),
      storage_used_bytes: Math.round(3.2 * TiB),
    },
    usage_by_host: [],
    ...over,
  };
}

const repositories = [
  repo({
    id: NAS01,
    name: "nas01-backups",
    description: "Primary Repository on nas01 (NFS, RAID 6)",
    is_default: true,
    server_url: "https://backup.example.lan:51515",
    internal_server_url: "https://dbr2-reposerver:51515",
    last_reindex_at: iso(-2 * DAY),
    usage_by_host: [
      { host_id: PROD, hostname: "docker-prod-01", applications: 3, latest_bytes: Math.round(412.6 * GiB) },
      { host_id: EDGE, hostname: "docker-edge-02", applications: 1, latest_bytes: Math.round(18.3 * GiB) },
    ],
  }),
  repo({
    id: LAB,
    name: "lab-scratch",
    description: "Scratch Repository for the lab hosts",
    backend: "filesystem",
    status: "awaiting_escrow",
    server_url: "https://lab-backup.example.lan:51515",
    kopia_repository_id: hex(16),
    escrow_generated_at: iso(-3 * HOUR),
    escrow_confirmed_at: null,
    created_at: iso(-3 * HOUR),
    live: {
      initialized: true,
      server_running: true,
      kopia_version: "0.22.3",
      storage_healthy: true,
      storage_total_bytes: 2 * TiB,
      storage_free_bytes: Math.round(0.3 * TiB),
      storage_used_bytes: Math.round(1.7 * TiB),
    },
    _code: "K7QX-M2DA-PL4W-ZT6R",
  }),
  repo({
    id: OFFSITE,
    name: "offsite-nfs",
    description: "Replica target in the second data center",
    status: "unavailable",
    server_url: "https://offsite-backup.example.net:51515",
    live: null,
    live_error: "dial tcp 10.20.5.20:8091: connect: connection refused",
    last_reindex_at: iso(-12 * DAY),
    created_at: iso(-60 * DAY),
    escrow_generated_at: iso(-60 * DAY),
    escrow_confirmed_at: iso(-60 * DAY + 30 * MIN),
  }),
];
for (const r of repositories) r._package = fakePackage(r.name, r._code ?? newCode());

const publicRepo = (r) => Object.fromEntries(Object.entries(r).filter(([k]) => !k.startsWith("_")));
const escrowFilename = (r) => `dbr2-escrow-${r.name}-${r.id.slice(0, 8)}.age`;

// ---------------------------------------------------------------------------
// Backup settings, recovery points, alerts, host limits
// ---------------------------------------------------------------------------

const settings = new Map([
  [
    SHOP,
    {
      repository_id: null,
      consistency_mode: null,
      max_quiesce_seconds: 1800,
      pre_hooks: [{ container: "db", command: ["sh", "-c", 'psql -U shop -c "CHECKPOINT"'], timeout_seconds: 120 }],
      post_hooks: [],
      optional_components: ["bind:/srv/shop/uploads"],
      excluded_components: [],
      updated_at: iso(-5 * DAY),
    },
  ],
]);

function defaultSettings() {
  return {
    repository_id: null,
    consistency_mode: null,
    max_quiesce_seconds: 3600,
    pre_hooks: [],
    post_hooks: [],
    optional_components: [],
    excluded_components: [],
  };
}

function settingsOut(appId) {
  const s = settings.get(appId) ?? defaultSettings();
  const hooks = s.pre_hooks.length + s.post_hooks.length > 0;
  return { ...s, effective_mode: s.consistency_mode ?? (hooks ? "quiesced" : "live") };
}

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
function rpId(offsetMs = 0) {
  let t = Date.now() + offsetMs;
  let time = "";
  for (let i = 0; i < 10; i++) {
    time = CROCKFORD[t % 32] + time;
    t = Math.floor(t / 32);
  }
  const rand = Array.from(randomBytes(16), (x) => CROCKFORD[x % 32]).join("");
  return `rp_${time}${rand}`;
}

function comp(name, kind, over = {}) {
  return {
    name,
    kind,
    required: true,
    status: "succeeded",
    snapshot_id: `k${hex(16)}`,
    root_object_id: `k${hex(16)}`,
    size_bytes: 0,
    started_at: iso(-DAY),
    finished_at: iso(-DAY + MIN),
    ...over,
  };
}

function shopManifest(rp, { partial = false } = {}) {
  const components = [
    comp("config", "config", {
      snapshot_source: "maint@dbr2:/config/shop",
      size_bytes: 48_213,
      files: 4,
      path: "/srv/shop",
      capture_method: "agent-archive",
    }),
    comp("volume:shop_pgdata", "volume", {
      snapshot_source: "agent@docker-prod-01:/var/lib/docker/volumes/shop_pgdata/_data",
      size_bytes: 12_884_901_888,
      files: 2143,
      path: "/var/lib/docker/volumes/shop_pgdata/_data",
      volume_name: "shop_pgdata",
      owner_uid: 999,
      owner_gid: 999,
      mode: "0700",
      selinux_context: "system_u:object_r:container_file_t:s0",
      capture_method: "live-path",
    }),
    comp("fsmeta:volume:shop_pgdata", "fsmeta", {
      snapshot_source: "maint@dbr2:/fsmeta/shop/volume-shop_pgdata",
      size_bytes: 91_442,
      files: 1,
      parent: "volume:shop_pgdata",
    }),
    partial
      ? comp("bind:/srv/shop/uploads", "bind_mount", {
          required: false,
          status: "failed",
          snapshot_id: undefined,
          root_object_id: undefined,
          error: "open /srv/shop/uploads/tmp/.lock: permission denied",
          path: "/srv/shop/uploads",
        })
      : comp("bind:/srv/shop/uploads", "bind_mount", {
          required: false,
          snapshot_source: "agent@docker-prod-01:/srv/shop/uploads",
          size_bytes: 3_221_225_472,
          files: 18_433,
          path: "/srv/shop/uploads",
          owner_uid: 1000,
          owner_gid: 1000,
          mode: "0755",
          selinux_context: "system_u:object_r:container_file_t:s0:c12,c345",
          capture_method: "live-path",
        }),
    comp("fsmeta:bind:/srv/shop/uploads", "fsmeta", {
      snapshot_source: "maint@dbr2:/fsmeta/shop/bind-srv-shop-uploads",
      size_bytes: partial ? 0 : 402_113,
      files: 1,
      parent: "bind:/srv/shop/uploads",
      ...(partial ? { status: "skipped", snapshot_id: undefined, root_object_id: undefined } : {}),
    }),
  ];
  return {
    schema_version: 1,
    recovery_point_id: rp.id,
    status: partial ? "partial" : "complete",
    created_at: rp.created_at,
    consistency_mode: rp.consistency_mode,
    consistency_point: rp.consistency_point,
    crash_consistent_only: rp.crash_consistent_only,
    ...(rp.consistency_mode === "quiesced"
      ? { quiesce_started_at: rp.consistency_point, quiesce_ended_at: iso(Date.parse(rp.consistency_point) - Date.now() + 94_000) }
      : {}),
    application: { id: SHOP, name: "Web shop", compose_project: "shop", working_dir: "/srv/shop" },
    source: {
      host_id: PROD,
      agent_id: PROD,
      hostname: "docker-prod-01",
      os_release: "Rocky Linux 9.6 (Blue Onyx)",
      architecture: "amd64",
      runtime_version: "docker 29.8.1",
      agent_version: "0.1.0.0",
    },
    repository: { id: NAS01, name: "nas01-backups" },
    components,
    images: [
      { service: "web", ref: "registry.example.com/shop/web:2.4.1", digest: "registry.example.com/shop/web@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e" },
      { service: "db", ref: "postgres:18-alpine", digest: "postgres@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2" },
      { service: "worker", ref: "shop-worker:dev" },
    ],
    topology: topologyOf(fleetData.apps.find((a) => a.id === SHOP)),
    workflow: { workflow_id: rp.workflow_id, run_id: randomUUID(), trigger: rp.trigger },
    producer: { component: "dbr2-worker", version: "0.1.0.0" },
  };
}

/** The application's shape (internal/manifest: Topology) from the fleet mock; no secrets. */
function topologyOf(app) {
  if (!app?.analysis) return undefined;
  const serviceOf = new Map();
  for (const svc of app.analysis.services) for (const c of svc.containers) serviceOf.set(c.name, svc.name);
  return {
    containers: app.containers_detail.map((c) => ({
      id: c.id,
      name: c.name,
      ...(serviceOf.get(c.name) && serviceOf.get(c.name) !== c.name ? { service: serviceOf.get(c.name) } : {}),
      image: c.image,
      state: c.state,
      ...(c.ports.length ? { ports: c.ports } : {}),
      ...(c.mounts.length
        ? {
            mounts: c.mounts.map((m) => ({
              type: m.type,
              ...(m.name ? { name: m.name } : {}),
              ...(m.type === "bind" && m.source ? { source: m.source } : {}),
              destination: m.destination,
              rw: m.rw,
            })),
          }
        : {}),
      ...(c.networks.length ? { networks: c.networks } : {}),
    })),
    networks: app.analysis.networks.map((n) => ({ name: n.name, driver: n.driver, ...(n.external ? { external: true } : {}) })),
    volumes: app.analysis.volumes.map((v) => ({ name: v.name, driver: v.driver, ...(v.external ? { external: true } : {}) })),
  };
}

/** Deterministic pseudo size for a component name (bytes). */
function sizeFor(name) {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return 64 * 1024 ** 2 + (h % 2048) * 1024 ** 2;
}

// Mirrors skipBind in internal/protection/backups.go.
const SKIP_BIND = ["/proc", "/sys", "/dev", "/run", "/var/run", "/etc/localtime", "/etc/timezone", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf", "/etc/machine-id", "/tmp/.X11-unix"];
const skipBind = (src) => src.endsWith(".sock") || src === "/" || SKIP_BIND.some((p) => src === p || src.startsWith(`${p}/`));

/**
 * The components a backup captures (internal/protection buildPlan): config,
 * protected volumes, distinct bind sources; minus excluded; optional ones
 * are not required.
 */
function planFor(app) {
  const x = app?.analysis;
  if (!x) return [];
  const s = settings.get(app.id) ?? defaultSettings();
  const out = [{ name: "config", kind: "config", path: x.working_dir ?? "" }];
  for (const v of x.volumes) if (v.protected_by_default && v.mountpoint) out.push({ name: `volume:${v.name}`, kind: "volume", path: v.mountpoint, volume: v.name });
  const seen = new Set();
  for (const b of x.bind_mounts) {
    if (!b.source || seen.has(b.source) || skipBind(b.source)) continue;
    seen.add(b.source);
    out.push({ name: `bind:${b.source}`, kind: "bind_mount", path: b.source });
  }
  return out
    .filter((c) => !s.excluded_components.includes(c.name))
    .map((c) => ({ ...c, required: c.kind === "config" || !s.optional_components.includes(c.name), size: c.kind === "config" ? 48_213 : sizeFor(c.name) }));
}

/** A complete manifest for a recovery point of `app` captured with `plan`. */
function appManifest(r, app, plan) {
  const components = [];
  for (const c of plan) {
    components.push(
      comp(c.name, c.kind, {
        required: c.required,
        snapshot_source: c.kind === "config" ? `maint@dbr2:/config/${app.name}` : `agent@${r.hostname}:${c.path}`,
        size_bytes: c.size,
        files: c.kind === "config" ? 3 : Math.round(c.size / 1_048_576) + 17,
        ...(c.path ? { path: c.path } : {}),
        ...(c.volume ? { volume_name: c.volume } : {}),
        started_at: r.created_at,
        finished_at: iso(),
      }),
    );
    if (c.kind !== "config") {
      components.push(
        comp(`fsmeta:${c.name}`, "fsmeta", { size_bytes: 40_960, files: 1, parent: c.name, snapshot_source: `maint@dbr2:/fsmeta/${app.name}`, started_at: r.created_at, finished_at: iso() }),
      );
    }
  }
  return {
    schema_version: 1,
    recovery_point_id: r.id,
    status: "complete",
    created_at: r.created_at,
    consistency_mode: r.consistency_mode,
    consistency_point: r.consistency_point ?? r.created_at,
    crash_consistent_only: r.crash_consistent_only,
    application: { id: app.id, name: fleetData.appName(app), ...(app.analysis?.compose_project ? { compose_project: app.analysis.compose_project } : {}), ...(app.analysis?.working_dir ? { working_dir: app.analysis.working_dir } : {}) },
    source: { host_id: r.host_id, agent_id: r.host_id, hostname: r.hostname, agent_version: "0.1.0.0" },
    repository: { id: r.repository_id, name: repositories.find((x) => x.id === r.repository_id)?.name ?? "nas01-backups" },
    components,
    images: (app.analysis?.images ?? []).map((im) => ({ ref: im.reference, ...(im.digests?.[0] ? { digest: im.digests[0] } : {}) })),
    topology: topologyOf(app),
    workflow: { workflow_id: r.workflow_id, run_id: r._runId ?? randomUUID(), trigger: r.trigger },
    producer: { component: "dbr2-worker", version: "0.1.0.0" },
  };
}

function simpleManifest(rp, appName, volume) {
  return {
    schema_version: 1,
    recovery_point_id: rp.id,
    status: "complete",
    created_at: rp.created_at,
    consistency_mode: rp.consistency_mode,
    consistency_point: rp.consistency_point,
    crash_consistent_only: rp.crash_consistent_only,
    application: { id: rp.application_id, name: appName },
    source: { host_id: rp.host_id, agent_id: rp.host_id, hostname: rp.hostname, agent_version: "0.1.0.0" },
    repository: { id: rp.repository_id, name: "nas01-backups" },
    components: [
      comp("config", "config", { snapshot_source: `maint@dbr2:/config/${appName}`, size_bytes: 12_004, files: 1 }),
      comp(`volume:${volume}`, "volume", {
        snapshot_source: `agent@${rp.hostname}:/var/lib/docker/volumes/${volume}/_data`,
        size_bytes: Math.max(0, rp.size_bytes - 12_004),
        files: 1311,
        volume_name: volume,
        owner_uid: 999,
        owner_gid: 999,
        mode: "0700",
      }),
    ],
    workflow: { workflow_id: rp.workflow_id, run_id: randomUUID(), trigger: rp.trigger },
    producer: { component: "dbr2-worker", version: "0.1.0.0" },
  };
}

function rp(over) {
  const created = over.created_at ?? iso(-DAY);
  return {
    id: rpId(Date.parse(created) - Date.now()),
    repository_id: NAS01,
    status: null,
    verification: "unverified",
    consistency_mode: "live",
    consistency_point: created,
    crash_consistent_only: true,
    trigger: "manual",
    // One exclusive workflow per application (temporalx.ApplicationWorkflowID).
    workflow_id: `application/${over.application_id}`,
    size_bytes: 0,
    component_count: 0,
    error: null,
    created_at: created,
    committed_at: null,
    ...over,
  };
}

const recoveryPoints = [];
{
  const complete = rp({
    application_id: SHOP,
    application_name: "Web shop",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "committed",
    status: "complete",
    verification: "verified",
    consistency_mode: "quiesced",
    crash_consistent_only: false,
    trigger: "schedule",
    size_bytes: 16_106_838_415,
    component_count: 5,
    created_at: iso(-6 * HOUR),
    committed_at: iso(-6 * HOUR + 7 * MIN),
  });
  complete._manifest = shopManifest(complete);
  const partial = rp({
    application_id: SHOP,
    application_name: "Web shop",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "committed",
    status: "partial",
    consistency_mode: "live",
    crash_consistent_only: true,
    size_bytes: 12_885_041_543,
    component_count: 5,
    error: "optional component bind:/srv/shop/uploads failed: permission denied",
    created_at: iso(-30 * HOUR),
    committed_at: iso(-30 * HOUR + 5 * MIN),
  });
  partial._manifest = shopManifest(partial, { partial: true });
  const failed = rp({
    application_id: SHOP,
    application_name: "Web shop",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "failed",
    consistency_mode: "quiesced",
    crash_consistent_only: false,
    consistency_point: null,
    trigger: "schedule",
    error: "pre-hook 1 (db) exited with status 2: psql: error: connection to server on socket failed",
    created_at: iso(-54 * HOUR),
  });
  const mft = rp({
    application_id: MFT,
    application_name: "mft-pg",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "committed",
    status: "complete",
    size_bytes: 1_342_177_280,
    component_count: 2,
    created_at: iso(-9 * HOUR),
    committed_at: iso(-9 * HOUR + 2 * MIN),
  });
  mft._manifest = simpleManifest(mft, "mft-pg", "3ab8b905bbca892b57bde6f1c10c79e8e86abd14032aa7e854cc8f15bac0eba0");
  mft._manifest.topology = topologyOf(fleetData.apps.find((a) => a.id === MFT));
  const running = rp({
    application_id: MONITORING,
    application_name: "Monitoring stack",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "pending",
    consistency_point: null,
    created_at: iso(-40_000),
    _startMs: Date.now() - 40_000,
    _finishAt: Date.now() + 2 * MIN,
  });
  const missing = rp({
    application_id: WIKI,
    application_name: "wiki",
    host_id: EDGE,
    hostname: "docker-edge-02",
    state: "missing",
    status: "complete",
    size_bytes: 19_649_872_332,
    component_count: 3,
    created_at: iso(-8 * DAY),
    committed_at: iso(-8 * DAY + 11 * MIN),
    error: "manifest not found in the Repository during the last reindex",
  });
  const redisFailed = rp({
    application_id: REDIS,
    application_name: "redis-cache",
    host_id: PROD,
    hostname: "docker-prod-01",
    state: "failed",
    trigger: "schedule",
    consistency_point: null,
    error: "snapshot of config failed: agent docker-prod-01: context deadline exceeded",
    created_at: iso(-4 * HOUR),
  });
  const metricsPartial = rp({
    application_id: METRICS,
    application_name: "metrics-agent",
    host_id: EDGE,
    hostname: "docker-edge-02",
    state: "committed",
    status: "partial",
    size_bytes: 52_113,
    component_count: 2,
    error: "optional component bind:/etc/alloy failed: no such file or directory",
    created_at: iso(-20 * HOUR),
    committed_at: iso(-20 * HOUR + MIN),
  });
  metricsPartial._manifest = {
    ...simpleManifest(metricsPartial, "metrics-agent", "unused"),
    status: "partial",
    components: [
      comp("config", "config", { snapshot_source: "maint@dbr2:/config/metrics-agent", size_bytes: 52_113, files: 2 }),
      comp("bind:/etc/alloy", "bind_mount", {
        required: false,
        status: "failed",
        snapshot_id: undefined,
        root_object_id: undefined,
        error: "lstat /etc/alloy: no such file or directory",
        path: "/etc/alloy",
      }),
    ],
  };
  recoveryPoints.push(complete, partial, failed, mft, running, missing, redisFailed, metricsPartial);
}

/**
 * Moves pending recovery points along: publishes `job.progress` for the
 * component being captured (hashed / uploaded bytes, files, n of m) and, when
 * done, commits the recovery point and publishes `backup.updated`. Runs on a
 * timer and whenever recovery points are read.
 */
function advance() {
  const now = Date.now();
  for (const r of recoveryPoints) {
    if (r.state !== "pending" || !r._finishAt) continue;
    const app = fleetData.apps.find((a) => a.id === r.application_id);
    r._plan = r._plan ?? planFor(app);
    r._runId = r._runId ?? randomUUID();
    const plan = r._plan;
    const base = {
      command_id: `application/${r.application_id}/${r._runId}/snapshot-components`,
      host_id: r.host_id,
      kind: "snapshot_components",
      workflow_id: r.workflow_id,
      run_id: r._runId,
      application_id: r.application_id,
    };
    if (r._finishAt <= now) {
      r.state = "committed";
      r.status = "complete";
      r.committed_at = iso();
      r.consistency_point = r.consistency_point ?? r.created_at;
      r._manifest = app ? appManifest(r, app, plan) : simpleManifest(r, r.application_name, "data");
      r.size_bytes = r._manifest.components.reduce((n, c) => n + (c.size_bytes ?? 0), 0);
      r.component_count = r._manifest.components.length;
      publish("job.progress", "backup.read", { ...base, state: "succeeded", progress: { done: plan.length, total: plan.length } });
      publish("backup.updated", "backup.read", { recovery_point_id: r.id, application_id: r.application_id, state: "committed", workflow_id: r.workflow_id });
      const alert = {
        id: Math.max(0, ...alerts.map((x) => x.id)) + 1,
        severity: "info",
        type: "backup.completed",
        target_type: "application",
        target_id: r.application_id,
        message: `Backup of ${r.application_name} committed (Complete, ${r.component_count} components)`,
        details: { recovery_point_id: r.id },
        created_at: iso(),
        acknowledged_at: null,
      };
      alerts.push(alert);
      publish("alert.created", "backup.read", { severity: alert.severity, type: alert.type, target_type: alert.target_type, target_id: alert.target_id, message: alert.message });
      continue;
    }
    if (!r._announced) {
      r._announced = true;
      publish("backup.updated", "backup.read", { recovery_point_id: r.id, application_id: r.application_id, state: "pending", workflow_id: r.workflow_id });
      publish("job.progress", "backup.read", { ...base, state: "accepted", progress: { queued: true } });
      continue;
    }
    if (plan.length === 0) continue;
    const start = r._startMs ?? Date.parse(r.created_at);
    const frac = Math.min(0.999, Math.max(0, (now - start) / (r._finishAt - start)));
    const pos = frac * plan.length;
    const i = Math.min(plan.length - 1, Math.floor(pos));
    const c = plan[i];
    const within = pos - i;
    const hashed = Math.round(c.size * within);
    publish("job.progress", "backup.read", {
      ...base,
      state: "running",
      progress: {
        component: c.name,
        hashed_bytes: hashed,
        uploaded_bytes: Math.round(hashed * 0.37),
        files: Math.round((c.kind === "config" ? 3 : c.size / 1_048_576 + 17) * within),
        done: i,
        total: plan.length,
      },
    });
  }
}
setInterval(advance, TICK_MS).unref();

const alerts = [
  {
    id: 44,
    severity: "critical",
    type: "backup.failed",
    target_type: "application",
    target_id: SHOP,
    message: "Backup of Web shop failed: pre-hook 1 (db) exited with status 2",
    details: {},
    created_at: iso(-54 * HOUR + 3 * MIN),
    acknowledged_at: null,
  },
  {
    id: 43,
    severity: "warning",
    type: "backup.completed",
    target_type: "application",
    target_id: SHOP,
    message: "Backup of Web shop committed as Partial: optional components failed",
    details: {},
    created_at: iso(-30 * HOUR + 5 * MIN),
    acknowledged_at: null,
  },
  {
    id: 42,
    severity: "critical",
    type: "agent.auto_resumed",
    target_type: "agent",
    target_id: EDGE,
    message: "docker-edge-02 resumed wiki on its own after the quiesce lease expired",
    details: { application_id: WIKI },
    created_at: iso(-3 * HOUR),
    acknowledged_at: null,
  },
  {
    id: 41,
    severity: "info",
    type: "repository.reindexed",
    target_type: "repository",
    target_id: NAS01,
    message: "Reindex of nas01-backups finished: 412 recovery points, 1 missing",
    details: {},
    created_at: iso(-2 * DAY),
    acknowledged_at: iso(-2 * DAY + HOUR),
  },
];
alerts[0].details = { recovery_point_id: recoveryPoints[2].id };
alerts[1].details = { recovery_point_id: recoveryPoints[1].id };

const hostSettings = new Map([
  [PROD, { max_concurrent_jobs: 2, backup_window_start: 22 * 60, backup_window_end: 5 * 60 + 30, backup_window_timezone: "America/Chicago" }],
]);
const hostSettingsOut = (id) =>
  hostSettings.get(id) ?? { max_concurrent_jobs: 2, backup_window_start: null, backup_window_end: null, backup_window_timezone: "UTC" };

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

/**
 * @param {object} h helpers from mock-api.mjs: send, problem, readJson, audit
 * @returns {Array<[string, RegExp, Function]>} [method, path regex, handler(req, res, url, user, match)]
 */
export function protectionRoutes({ send, problem, readJson, audit }) {
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
  const findRepo = (res, id) => {
    const r = repositories.find((x) => x.id === id);
    if (!r) problem(res, 404, "not_found", "Not Found", "repository not found");
    return r;
  };
  const findApp = (res, id) => {
    const a = fleetData.apps.find((x) => x.id === id);
    if (!a) problem(res, 404, "not_found", "Not Found", "application not found");
    return a;
  };
  const isUrl = (v) => typeof v === "string" && /^https?:\/\/[^\s/]+/.test(v);
  const UUID = "([0-9a-f-]{36})";

  return [
    // --- Escrow recipients --------------------------------------------------
    [
      "GET",
      /^\/api\/v1\/escrow\/recipients$/,
      (req, res, url, user) => need(res, user, "repository.read") && send(res, 200, { items: recipients }),
    ],
    [
      "POST",
      /^\/api\/v1\/escrow\/recipients$/,
      async (req, res, url, user) => {
        if (!need(res, user, "repository.manage")) return;
        const body = await readJson(req);
        const name = typeof body?.name === "string" ? body.name.trim() : "";
        const key = typeof body?.public_key === "string" ? body.public_key.trim() : "";
        if (!name || name.length > 100) return invalid(res, "name must be 1-100 characters", "body.name");
        if (/AGE-SECRET-KEY-|PRIVATE KEY/i.test(key)) {
          return invalid(res, "that is a private identity — paste the public key (age1…) only, and keep the identity offline", "body.public_key");
        }
        if (!key.startsWith("age1") && !key.startsWith("ssh-")) {
          return invalid(res, "recipient must be an age X25519 public key (age1…) or an SSH ed25519/RSA public key", "body.public_key");
        }
        if (recipients.some((r) => r.public_key === key)) return problem(res, 409, "conflict", "Conflict", "this public key is already a recipient");
        const r = { id: randomUUID(), name, public_key: key, created_at: iso() };
        recipients.push(r);
        audit("escrow.recipient.added", "success", req, { actor: user.display_name, target_type: "escrow_recipient", target_id: r.id });
        send(res, 201, r);
      },
    ],
    [
      "DELETE",
      new RegExp(`^/api/v1/escrow/recipients/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const i = recipients.findIndex((r) => r.id === m[1]);
        if (i < 0) return problem(res, 404, "not_found", "Not Found", "escrow recipient not found");
        recipients.splice(i, 1);
        audit("escrow.recipient.removed", "success", req, { actor: user.display_name, target_type: "escrow_recipient", target_id: m[1] });
        send(res, 204);
      },
    ],

    // --- Repositories -------------------------------------------------------
    [
      "GET",
      /^\/api\/v1\/repositories$/,
      (req, res, url, user) => need(res, user, "repository.read") && send(res, 200, { items: repositories.map(publicRepo) }),
    ],
    [
      "POST",
      /^\/api\/v1\/repositories$/,
      async (req, res, url, user) => {
        if (!need(res, user, "repository.manage")) return;
        const body = await readJson(req);
        const name = typeof body?.name === "string" ? body.name.trim() : "";
        if (!name || name.length > 100) return invalid(res, "name must be 1-100 characters", "body.name");
        if (!["nfs", "filesystem"].includes(body.backend)) return invalid(res, "backend must be nfs or filesystem", "body.backend");
        if (!isUrl(body.management_url)) return invalid(res, "management_url must be an http(s) URL", "body.management_url");
        if (!isUrl(body.server_url)) return invalid(res, "server_url must be an http(s) URL", "body.server_url");
        if (body.internal_server_url && !isUrl(body.internal_server_url)) {
          return invalid(res, "internal_server_url must be an http(s) URL", "body.internal_server_url");
        }
        if (recipients.length < 2) {
          return problem(res, 409, "conflict", "Conflict", `at least 2 escrow recipients are required (have ${recipients.length})`);
        }
        if (repositories.some((r) => r.name === name)) return problem(res, 409, "conflict", "Conflict", `a Repository named ${name} already exists`);
        const code = newCode();
        if (body.default) for (const r of repositories) r.is_default = false;
        const r = repo({
          id: randomUUID(),
          name,
          description: typeof body.description === "string" ? body.description : "",
          backend: body.backend,
          status: "awaiting_escrow",
          is_default: Boolean(body.default) || repositories.length === 0,
          server_url: body.server_url,
          internal_server_url: body.internal_server_url ?? "",
          management_url: body.management_url,
          escrow_recipients: recipients.length,
          escrow_generated_at: iso(),
          escrow_confirmed_at: null,
          created_at: iso(),
          live: {
            initialized: true,
            server_running: true,
            kopia_version: "0.22.3",
            storage_healthy: true,
            storage_total_bytes: 4 * TiB,
            storage_free_bytes: 4 * TiB - 2 * 1024 ** 2,
            storage_used_bytes: 2 * 1024 ** 2,
          },
          _code: code,
        });
        r._package = fakePackage(r.name, code);
        repositories.push(r);
        audit("repository.created", "success", req, { actor: user.display_name, target_type: "repository", target_id: r.id });
        console.log(`[mock] escrow confirmation code for ${r.name}: ${code}`);
        send(res, 201, { repository: publicRepo(r), escrow_package: r._package, escrow_filename: escrowFilename(r) });
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/repositories/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.read")) return;
        const r = findRepo(res, m[1]);
        if (r) send(res, 200, publicRepo(r));
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/repositories/${UUID}/escrow-package$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = findRepo(res, m[1]);
        if (!r) return;
        audit("repository.escrow.downloaded", "success", req, { actor: user.display_name, target_type: "repository", target_id: r.id });
        send(res, 200, { filename: escrowFilename(r), package: r._package });
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/escrow/confirm$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = findRepo(res, m[1]);
        if (!r) return;
        const body = await readJson(req);
        const code = typeof body?.confirmation_code === "string" ? body.confirmation_code : "";
        if (code.length < 16 || code.length > 40) return invalid(res, "confirmation_code must be 16-40 characters", "body.confirmation_code");
        if (r.status !== "awaiting_escrow") return problem(res, 409, "conflict", "Conflict", "escrow is already confirmed");
        if (normCode(code) !== normCode(r._code)) {
          audit("repository.escrow.confirmed", "failure", req, { actor: user.display_name, target_type: "repository", target_id: r.id });
          return invalid(res, "the confirmation code does not match this Repository's escrow package", "body.confirmation_code");
        }
        r.status = "ready";
        r.escrow_confirmed_at = iso();
        audit("repository.escrow.confirmed", "success", req, { actor: user.display_name, target_type: "repository", target_id: r.id });
        send(res, 200, publicRepo(r));
      },
    ],
    [
      "POST",
      new RegExp(`^/api/v1/repositories/${UUID}/reindex$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "repository.manage")) return;
        const r = findRepo(res, m[1]);
        if (!r) return;
        if (r.status === "unavailable") return problem(res, 409, "conflict", "Conflict", "the reposerver is unreachable");
        r.last_reindex_at = iso();
        audit("repository.reindex.requested", "success", req, { actor: user.display_name, target_type: "repository", target_id: r.id });
        send(res, 202, { workflow_id: `reindex-${r.id}-${Date.now()}` });
      },
    ],

    // --- Backups -------------------------------------------------------------
    [
      "POST",
      new RegExp(`^/api/v1/applications/${UUID}/backups$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "backup.execute")) return;
        const a = findApp(res, m[1]);
        if (!a) return;
        const body = (await readJson(req)) ?? {};
        if (body.consistency_mode !== undefined && !["live", "quiesced", "offline"].includes(body.consistency_mode)) {
          return invalid(res, "consistency_mode must be live, quiesced or offline", "body.consistency_mode");
        }
        advance();
        if (recoveryPoints.some((r) => r.application_id === a.id && r.state === "pending")) {
          return problem(res, 409, "conflict", "Conflict", "an operation is already running for this application");
        }
        if (!a.analysis) return problem(res, 409, "conflict", "Conflict", "the application is not in the latest inventory");
        const mode = body.consistency_mode ?? settingsOut(a.id).effective_mode;
        const r = rp({
          application_id: a.id,
          application_name: fleetData.appName(a),
          host_id: a.host_id,
          hostname: fleetData.agents.find((x) => x.id === a.host_id)?.hostname ?? "unknown",
          repository_id: settingsOut(a.id).repository_id ?? NAS01,
          state: "pending",
          consistency_mode: mode,
          crash_consistent_only: mode === "live",
          consistency_point: null,
          created_at: iso(),
          _startMs: Date.now(),
          _finishAt: Date.now() + BACKUP_MS,
        });
        recoveryPoints.push(r);
        audit("backup.requested", "success", req, { actor: user.display_name, target_type: "application", target_id: a.id });
        send(res, 202, { workflow_id: r.workflow_id });
      },
    ],
    [
      "GET",
      new RegExp(`^/api/v1/applications/${UUID}/backup-settings$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "policy.read")) return;
        if (findApp(res, m[1])) send(res, 200, settingsOut(m[1]));
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/applications/${UUID}/backup-settings$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "policy.manage")) return;
        if (!findApp(res, m[1])) return;
        const b = await readJson(req);
        if (!b || typeof b !== "object") return invalid(res, "body must be a JSON object", "body");
        const extra = Object.keys(b).filter(
          (k) => !["repository_id", "consistency_mode", "max_quiesce_seconds", "pre_hooks", "post_hooks", "optional_components", "excluded_components"].includes(k),
        );
        if (extra.length) return invalid(res, `unexpected property ${extra[0]}`, `body.${extra[0]}`);
        if (b.consistency_mode !== null && !["live", "quiesced", "offline"].includes(b.consistency_mode)) {
          return invalid(res, "consistency_mode must be live, quiesced or offline", "body.consistency_mode");
        }
        if (!Number.isInteger(b.max_quiesce_seconds) || b.max_quiesce_seconds < 60 || b.max_quiesce_seconds > 86400) {
          return invalid(res, "max_quiesce_seconds must be 60–86400", "body.max_quiesce_seconds");
        }
        for (const [k, hs] of [["pre_hooks", b.pre_hooks], ["post_hooks", b.post_hooks]]) {
          for (const [i, h] of (hs ?? []).entries()) {
            if (!h?.container || !Array.isArray(h.command) || !h.command[0]) return invalid(res, `hook ${i + 1} needs a container and a command`, `body.${k}`);
            if ((h.timeout_seconds ?? 0) > 3600) return invalid(res, `hook ${i + 1} timeout exceeds 3600 s`, `body.${k}`);
          }
        }
        if (b.repository_id !== null && !repositories.some((r) => r.id === b.repository_id)) {
          return invalid(res, "unknown repository", "body.repository_id");
        }
        settings.set(m[1], {
          repository_id: b.repository_id,
          consistency_mode: b.consistency_mode,
          max_quiesce_seconds: b.max_quiesce_seconds,
          pre_hooks: b.pre_hooks ?? [],
          post_hooks: b.post_hooks ?? [],
          optional_components: b.optional_components ?? [],
          excluded_components: b.excluded_components ?? [],
          updated_at: iso(),
        });
        audit("backup.settings.updated", "success", req, { actor: user.display_name, target_type: "application", target_id: m[1] });
        send(res, 200, settingsOut(m[1]));
      },
    ],
    [
      "GET",
      /^\/api\/v1\/recovery-points$/,
      (req, res, url, user) => {
        if (!need(res, user, "backup.read")) return;
        advance();
        const app = url.searchParams.get("application_id");
        const state = url.searchParams.get("state");
        const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 100) || 100, 1), 500);
        const items = recoveryPoints
          .filter((r) => (!app || r.application_id === app) && (!state || r.state === state))
          .sort((a, b) => b.created_at.localeCompare(a.created_at))
          .slice(0, limit)
          .map(publicRepo);
        send(res, 200, { items });
      },
    ],
    [
      "GET",
      /^\/api\/v1\/recovery-points\/(rp_[0-9A-HJKMNP-TV-Z]{26})$/,
      (req, res, url, user, m) => {
        if (!need(res, user, "backup.read")) return;
        advance();
        const r = recoveryPoints.find((x) => x.id === m[1]);
        if (!r) return problem(res, 404, "not_found", "Not Found", "recovery point not found");
        send(res, 200, { ...publicRepo(r), ...(r._manifest ? { manifest: r._manifest } : {}) });
      },
    ],
    [
      "GET",
      /^\/api\/v1\/alerts$/,
      (req, res, url, user) => {
        if (!need(res, user, "backup.read")) return;
        const all = url.searchParams.get("all") === "true";
        const items = alerts.filter((a) => all || !a.acknowledged_at).sort((a, b) => b.created_at.localeCompare(a.created_at));
        send(res, 200, { items });
      },
    ],
    [
      "POST",
      /^\/api\/v1\/alerts\/(\d+)\/acknowledge$/,
      (req, res, url, user, m) => {
        if (!need(res, user, "backup.execute")) return;
        const a = alerts.find((x) => x.id === Number(m[1]));
        if (!a) return problem(res, 404, "not_found", "Not Found", "alert not found");
        a.acknowledged_at = a.acknowledged_at ?? iso();
        audit("alert.acknowledged", "success", req, { actor: user.display_name, target_type: "alert", target_id: String(a.id) });
        send(res, 204);
      },
    ],

    // --- Host limits ----------------------------------------------------------
    [
      "GET",
      new RegExp(`^/api/v1/agents/${UUID}/settings$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "host.read")) return;
        if (!fleetData.agents.some((a) => a.id === m[1])) return problem(res, 404, "not_found", "Not Found", "agent not found");
        send(res, 200, hostSettingsOut(m[1]));
      },
    ],
    [
      "PUT",
      new RegExp(`^/api/v1/agents/${UUID}/settings$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "host.manage")) return;
        if (!fleetData.agents.some((a) => a.id === m[1])) return problem(res, 404, "not_found", "Not Found", "agent not found");
        const b = await readJson(req);
        if (!b || !Number.isInteger(b.max_concurrent_jobs) || b.max_concurrent_jobs < 1 || b.max_concurrent_jobs > 16) {
          return invalid(res, "max_concurrent_jobs must be 1-16", "body.max_concurrent_jobs");
        }
        const s = b.backup_window_start ?? null;
        const e = b.backup_window_end ?? null;
        if ((s === null) !== (e === null)) return invalid(res, "set both backup window start and end, or neither", "body.backup_window_start");
        for (const v of [s, e]) {
          if (v !== null && (!Number.isInteger(v) || v < 0 || v > 1439)) return invalid(res, "backup window bounds are minutes of the day (0–1439)", "body.backup_window_start");
        }
        const tz = typeof b.backup_window_timezone === "string" && b.backup_window_timezone ? b.backup_window_timezone : "UTC";
        try {
          new Intl.DateTimeFormat("en-US", { timeZone: tz });
        } catch {
          return invalid(res, `unknown timezone ${tz}`, "body.backup_window_timezone");
        }
        const out = { max_concurrent_jobs: b.max_concurrent_jobs, backup_window_start: s, backup_window_end: e, backup_window_timezone: tz };
        hostSettings.set(m[1], out);
        audit("agent.settings.updated", "success", req, { actor: user.display_name, target_type: "agent", target_id: m[1] });
        send(res, 200, out);
      },
    ],
  ];
}

/** Read access to the recovery points for the other mock modules (mock-restore.mjs). */
export const protectionData = {
  recoveryPoints,
  repositories,
  alerts,
  planFor,
  settingsOut,
  /** Lets pending recovery points commit (they finish lazily when read). */
  advance,
};

// SPDX-License-Identifier: Apache-2.0
//
// Hosts (Phase 2) and Applications (Phase 3) part of the mock API, imported by
// scripts/mock-api.mjs. Shapes follow api/openapi.yaml (AgentDTO,
// ApplicationSummary, ApplicationDetail, ComposeDTO, ...). In-memory only.
//
// Seed data:
//   hosts         docker-prod-01 (active, connected), docker-edge-02 (active,
//                 offline, outdated, Docker unreachable), docker-dr-03
//                 (active, empty disaster-recovery standby: the clean
//                 alternate restore target), build-runner-03 (pending),
//                 legacy-db (suspended), old-host (revoked)
//   applications  shop (Compose, original, 2 high + 1 low unprotected paths,
//                 external NFS volume, locally built image), mft-pg
//                 (container, RECONSTRUCTED), redis-cache / nginx-proxy
//                 (standalone containers, groupable), monitoring (manual),
//                 wiki (missing from the latest inventory), metrics-agent
//   compose       masked by default; ?reveal=true needs secrets.read and
//                 returns the real values (audited)

import { randomBytes, randomUUID } from "node:crypto";
import { publish } from "./mock-events.mjs";

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const iso = (offsetMs = 0) => new Date(Date.now() + offsetMs).toISOString();
const MASK = "********";

// ---------------------------------------------------------------------------
// Hosts
// ---------------------------------------------------------------------------

const PROD = "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01";
const EDGE = "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02";
const RUNNER = "c47a9e10-5d2b-4e8f-a1c3-9b8d7e6f5a03";
const LEGACY = "d5f1b2a3-6e7c-4a8d-9f0e-1a2b3c4d5e04";
const OLD = "e6a2c3b4-7f8d-4b9e-8a1f-2b3c4d5e6f05";
const DR = "f7b3d4c5-8a9e-4c0f-9b2a-3c4d5e6f7a06";

function agent(over) {
  return {
    status_reason: null,
    connected: false,
    outdated: false,
    agent_version: "0.1.0.0",
    protocol_version: "0.1.0.0",
    os_release: "Rocky Linux 9.6 (Blue Onyx)",
    architecture: "amd64",
    enrolled_at: iso(-30 * DAY),
    approved_at: iso(-30 * DAY + 5 * MIN),
    last_seen_at: iso(-20_000),
    latency_ms: 4,
    docker_reachable: true,
    docker_version: "29.8.1",
    health_error: null,
    certificate_not_after: iso(80 * DAY),
    ...over,
  };
}

const agents = [
  agent({ id: PROD, hostname: "docker-prod-01", status: "active", status_reason: "initial enrollment", connected: true }),
  agent({
    id: EDGE,
    hostname: "docker-edge-02",
    status: "active",
    status_reason: "approved after rack move",
    outdated: true,
    agent_version: "0.0.9.3",
    os_release: "Fedora Linux 44 (Server Edition)",
    last_seen_at: iso(-3 * HOUR),
    latency_ms: 38,
    docker_reachable: false,
    docker_version: null,
    health_error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
    certificate_not_after: iso(10 * DAY),
  }),
  agent({
    id: DR,
    hostname: "docker-dr-03",
    status: "active",
    status_reason: "disaster-recovery standby (restore target)",
    connected: true,
    latency_ms: 9,
    enrolled_at: iso(-12 * DAY),
    approved_at: iso(-12 * DAY + 10 * MIN),
  }),
  agent({
    id: RUNNER,
    hostname: "build-runner-03",
    status: "pending",
    enrolled_at: iso(-15 * MIN),
    approved_at: null,
    last_seen_at: null,
    latency_ms: null,
    docker_reachable: null,
    docker_version: null,
    os_release: null,
    architecture: null,
    certificate_not_after: iso(89 * DAY),
  }),
  agent({
    id: LEGACY,
    hostname: "legacy-db",
    status: "suspended",
    status_reason: "maintenance window: storage migration",
    last_seen_at: iso(-2 * DAY),
    latency_ms: null,
    architecture: "arm64",
  }),
  agent({
    id: OLD,
    hostname: "old-host",
    status: "revoked",
    status_reason: "decommissioned",
    last_seen_at: iso(-40 * DAY),
    latency_ms: null,
    docker_reachable: null,
    docker_version: null,
    certificate_not_after: iso(-5 * DAY),
  }),
];

const tokens = [
  {
    id: randomUUID(),
    prefix: "dbr2reg_Qm3xT1",
    description: "docker-prod-01",
    created_at: iso(-30 * DAY),
    expires_at: iso(-29 * DAY),
    used_at: iso(-30 * DAY),
    used_by_agent: PROD,
    revoked_at: null,
  },
  {
    id: randomUUID(),
    prefix: "dbr2reg_Zk8pL4",
    description: "spare token for the lab",
    created_at: iso(-2 * HOUR),
    expires_at: iso(22 * HOUR),
    used_at: null,
    used_by_agent: null,
    revoked_at: null,
  },
];

const CA_SHA256 = "5c1f0e9a7d3b2c4e6f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e";
const GATEWAY = "dbr2.example.com:8443";

function hostFacts(hostname, extra = {}) {
  return {
    hostname,
    operating_system: "Rocky Linux 9.6 (Blue Onyx)",
    os_type: "linux",
    kernel_version: "5.14.0-570.el9.x86_64",
    architecture: "x86_64",
    runtime: "docker",
    engine_version: "29.8.1",
    api_version: "1.56",
    root_dir: "/var/lib/docker",
    storage_driver: "overlay2",
    cgroup_version: "2",
    security_options: ["name=seccomp,profile=builtin", "name=selinux", "name=cgroupns"],
    rootless: false,
    selinux: true,
    cpus: 8,
    memory_bytes: 34_359_738_368,
    ...extra,
  };
}

const range = (n, f) => Array.from({ length: n }, (_, i) => f(i));

function inventoryFor(agentId) {
  const a = agents.find((x) => x.id === agentId);
  if (!a) return null;
  if (a.status === "pending") return null;
  const small = agentId !== PROD;
  return {
    received_at: iso(-2 * MIN),
    inventory: {
      schema_version: 1,
      collected_at: iso(-2 * MIN - 5000),
      host: hostFacts(a.hostname, small ? { selinux: false, storage_driver: "overlayfs", cpus: 4 } : {}),
      containers: range(small ? 3 : 9, (i) => ({
        id: randomBytes(32).toString("hex"),
        name: `c${i}`,
        image: "alpine:3.22",
        image_id: `sha256:${randomBytes(32).toString("hex")}`,
        state: "running",
        created: iso(-DAY),
      })),
      volumes: range(small ? 2 : 7, (i) => ({ name: `v${i}`, driver: "local", mountpoint: `/var/lib/docker/volumes/v${i}/_data` })),
      networks: range(small ? 3 : 5, (i) => ({ id: randomBytes(8).toString("hex"), name: `n${i}`, driver: "bridge" })),
      images: range(small ? 3 : 8, () => ({ id: `sha256:${randomBytes(32).toString("hex")}`, os: "linux", architecture: "amd64", size: 1e8 })),
      compose_projects: small ? [] : [{ name: "shop", working_dir: "/srv/shop" }],
      warnings: small
        ? []
        : ["image 388d74569326: Error response from daemon: No such image: sha256:388d74569326235001e1419d8b2196bbb5c3c04ff146a31e4ffa59f81f480a3b"],
    },
  };
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

/** Set by mock-live.mjs: computes `protection` (it needs recovery points and restores). */
const hooks = { protection: null, policyOf: null };

const hostname = (id) => agents.find((a) => a.id === id)?.hostname ?? "unknown";

function env(key, value, sensitive = false) {
  return sensitive ? { key, value: MASK, sensitive: true, _real: value } : { key, value };
}

function container(name, image, state, envs, mounts, ports = [], networks = []) {
  return { id: randomBytes(32).toString("hex"), name, image, state, env: envs, ports, mounts, networks };
}

function analysis(over) {
  return {
    services: [],
    volumes: [],
    bind_mounts: [],
    tmpfs: [],
    networks: [],
    images: [],
    dependencies: [],
    unprotected: [],
    secrets_count: 0,
    containers: [],
    ...over,
  };
}

function simpleContainerApp(id, host, name, image, digest, envs, extra = {}) {
  const c = container(name, image, "running", envs, extra.mounts ?? [], extra.ports ?? [], ["bridge"]);
  return {
    id,
    name,
    display_name: null,
    kind: "container",
    host_id: host,
    source: "reconstructed",
    owner: null,
    environment: null,
    criticality: null,
    last_seen_at: iso(-2 * MIN),
    missing_since: null,
    manual_containers: [],
    containers_detail: [c],
    analysis: analysis({
      key: `container:${name}`,
      kind: "container",
      name,
      source: "reconstructed",
      source_reason: "no Compose definition: generated from Docker runtime metadata",
      services: [{ name, image, containers: [{ id: c.id, name, state: "running" }] }],
      volumes: extra.volumes ?? [],
      networks: [{ name: "bridge", driver: "bridge" }],
      images: [
        {
          reference: image,
          image_id: `sha256:${digest}`,
          digests: [`${image.split(":")[0]}@sha256:${digest}`],
          platform: "linux/amd64",
        },
      ],
      unprotected: extra.unprotected ?? [],
      secrets_count: envs.filter((e) => e.sensitive).length,
      containers: [name],
    }),
  };
}

const shopWeb = container(
  "shop-web-1",
  "registry.example.com/shop/web:2.4.1",
  "running",
  [
    env("DATABASE_URL", "postgres://shop:S3cr3t-pg@db:5432/shop", true),
    env("SESSION_SECRET", "b7f0c2d9e4a1", true),
    env("NODE_ENV", "production"),
    env("PORT", "3000"),
  ],
  [
    { type: "bind", source: "/srv/shop/uploads", destination: "/app/uploads", rw: true, mode: "rw" },
    { type: "volume", name: "shop_media", source: "/mnt/nfs/shop_media", destination: "/app/media", driver: "local", rw: true },
  ],
  [{ container_port: "3000", protocol: "tcp", host_ip: "0.0.0.0", host_port: "8080" }],
  ["shop_default", "proxy"],
);
const shopDb = container(
  "shop-db-1",
  "postgres:18-alpine",
  "running",
  [env("POSTGRES_PASSWORD", "S3cr3t-pg", true), env("POSTGRES_USER", "shop"), env("POSTGRES_DB", "shop"), env("PGDATA", "/var/lib/postgresql/data")],
  [{ type: "volume", name: "shop_pgdata", source: "/var/lib/docker/volumes/shop_pgdata/_data", destination: "/var/lib/postgresql/data", driver: "local", rw: true }],
  [],
  ["shop_default"],
);
const shopWorker = container(
  "shop-worker-1",
  "shop-worker:dev",
  "running",
  [env("QUEUE_TOKEN", "q-4f9a2b", true), env("CONCURRENCY", "4")],
  [{ type: "tmpfs", destination: "/tmp", rw: true }],
  [],
  ["shop_default"],
);

const apps = [
  {
    id: "5f939a00-fccb-4376-a6cc-37eeb5542abe",
    name: "shop",
    display_name: "Web shop",
    kind: "compose",
    host_id: PROD,
    source: "original",
    owner: "E-commerce team",
    environment: "production",
    criticality: "critical",
    last_seen_at: iso(-2 * MIN),
    missing_since: null,
    manual_containers: [],
    containers_detail: [shopWeb, shopDb, shopWorker],
    analysis: analysis({
      key: "compose:shop",
      kind: "compose",
      name: "shop",
      compose_project: "shop",
      working_dir: "/srv/shop",
      source: "original",
      services: [
        { name: "web", image: shopWeb.image, containers: [{ id: shopWeb.id, name: shopWeb.name, state: "running" }] },
        { name: "db", image: shopDb.image, containers: [{ id: shopDb.id, name: shopDb.name, state: "running" }] },
        { name: "worker", image: shopWorker.image, containers: [{ id: shopWorker.id, name: shopWorker.name, state: "running" }] },
      ],
      volumes: [
        {
          name: "shop_pgdata",
          driver: "local",
          mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data",
          class: "local",
          used_by: ["shop-db-1:/var/lib/postgresql/data"],
          protected_by_default: true,
        },
        {
          name: "shop_media",
          driver: "local",
          mountpoint: "/var/lib/docker/volumes/shop_media/_data",
          class: "external",
          reasons: ["local driver with type=nfs options (addr=nas01.example.com)"],
          external: true,
          used_by: ["shop-web-1:/app/media"],
          protected_by_default: false,
        },
        {
          name: "4e1c8b2f9a7d6e5c4b3a29181716151413121110",
          driver: "local",
          mountpoint: "/var/lib/docker/volumes/4e1c8b2f9a7d6e5c4b3a29181716151413121110/_data",
          class: "ephemeral",
          reasons: ["anonymous volume of the image's VOLUME /cache, recreated with the container"],
          anonymous: true,
          used_by: ["shop-worker-1:/cache"],
          protected_by_default: false,
        },
      ],
      bind_mounts: [{ container: "shop-web-1", source: "/srv/shop/uploads", destination: "/app/uploads", rw: true }],
      tmpfs: [{ container: "shop-worker-1", destination: "/tmp" }],
      networks: [
        { name: "shop_default", driver: "bridge" },
        { name: "proxy", driver: "bridge", external: true },
      ],
      images: [
        {
          reference: shopWeb.image,
          image_id: "sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e",
          digests: ["registry.example.com/shop/web@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e"],
          platform: "linux/amd64",
        },
        {
          reference: shopDb.image,
          image_id: "sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2",
          digests: ["postgres@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"],
          platform: "linux/amd64",
        },
        {
          reference: shopWorker.image,
          image_id: "sha256:7d5c166046f28974a5ed8ec38df648804004026a11c75152b7707b514223d825",
          platform: "linux/amd64",
        },
      ],
      dependencies: [
        { kind: "external_network", name: "proxy", detail: "declared external: true; must exist before the project starts" },
        { kind: "network_storage", name: "shop_media", detail: "local driver with type=nfs options (addr=nas01.example.com)" },
      ],
      unprotected: [
        {
          container: "shop-worker-1",
          path: "/var/log/worker",
          files: 12,
          severity: "low",
          reason: "container-local logs, caches, certificates or runtime config: usually regenerated, but lost on recreation",
        },
        {
          container: "shop-web-1",
          path: "/app/data/invoices",
          files: 1843,
          severity: "high",
          reason: "writable path not backed by a volume or bind mount: lost when the container is recreated",
        },
        {
          container: "shop-worker-1",
          path: "/srv/state/queue.db",
          files: 1,
          severity: "high",
          reason: "writable path not backed by a volume or bind mount: lost when the container is recreated",
        },
      ],
      secrets_count: 4,
      containers: ["shop-web-1", "shop-db-1", "shop-worker-1"],
    }),
  },
  simpleContainerApp(
    "9fc3a8cb-13d7-4c97-a291-4aade4ca63ee",
    PROD,
    "mft-pg",
    "postgres:18",
    "4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280",
    [env("POSTGRES_PASSWORD", "mft-local-pw", true), env("PGDATA", "/var/lib/postgresql/18/docker")],
    {
      mounts: [{ type: "volume", name: "3ab8b905bbca", source: "/var/lib/docker/volumes/3ab8b905bbca/_data", destination: "/var/lib/postgresql", driver: "local", rw: true }],
      ports: [{ container_port: "5432", protocol: "tcp", host_ip: "0.0.0.0", host_port: "5432" }],
      volumes: [
        {
          name: "3ab8b905bbca892b57bde6f1c10c79e8e86abd14032aa7e854cc8f15bac0eba0",
          driver: "local",
          mountpoint: "/var/lib/docker/volumes/3ab8b905bbca892b57bde6f1c10c79e8e86abd14032aa7e854cc8f15bac0eba0/_data",
          class: "local",
          anonymous: true,
          used_by: ["mft-pg:/var/lib/postgresql"],
          protected_by_default: true,
        },
      ],
    },
  ),
  simpleContainerApp("1a2b3c4d-0000-4000-8000-000000000003", PROD, "redis-cache", "redis:8-alpine", "11aa22bb33cc44dd55ee66ff77889900aabbccddeeff00112233445566778899", [env("REDIS_ARGS", "--save 60 1")], {
    unprotected: [
      { container: "redis-cache", path: "/data/dump.rdb", files: 1, severity: "high", reason: "writable path not backed by a volume or bind mount: lost when the container is recreated" },
    ],
  }),
  simpleContainerApp("1a2b3c4d-0000-4000-8000-000000000004", PROD, "nginx-proxy", "nginx:1.29", "99887766554433221100ffeeddccbbaa99887766554433221100ffeeddccbbaa", [env("NGINX_HOST", "shop.example.com")]),
  simpleContainerApp("1a2b3c4d-0000-4000-8000-000000000005", PROD, "adminer", "adminer:5", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", [env("ADMINER_DEFAULT_SERVER", "shop-db-1")]),
  {
    ...simpleContainerApp("1a2b3c4d-0000-4000-8000-000000000006", PROD, "monitoring", "prom/prometheus:v3.6.0", "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210", [env("GF_SECURITY_ADMIN_PASSWORD", "grafana-admin", true)]),
    kind: "manual",
    display_name: "Monitoring stack",
    owner: "Platform team",
    environment: "production",
    criticality: "high",
    manual_containers: ["prometheus", "grafana"],
  },
  {
    id: "1a2b3c4d-0000-4000-8000-000000000007",
    name: "wiki",
    display_name: null,
    kind: "compose",
    host_id: EDGE,
    source: "unknown",
    owner: null,
    environment: null,
    criticality: null,
    last_seen_at: iso(-3 * HOUR),
    missing_since: iso(-3 * HOUR + MIN),
    manual_containers: [],
    containers_detail: [],
    analysis: null,
  },
  simpleContainerApp("1a2b3c4d-0000-4000-8000-000000000008", EDGE, "metrics-agent", "grafana/alloy:v1.11", "aa00bb11cc22dd33ee44ff5566778899aa00bb11cc22dd33ee44ff5566778899", [env("GCLOUD_RW_API_KEY", "glc_abc123", true)]),
];
// The manual application is analysed as one "service" for the mock.
apps[5].analysis.key = "manual:monitoring";
apps[5].analysis.kind = "manual";

function summary(a) {
  const x = a.analysis;
  return {
    id: a.id,
    name: a.name,
    display_name: a.display_name,
    kind: a.kind,
    host_id: a.host_id,
    hostname: hostname(a.host_id),
    source: x ? x.source : "unknown",
    services: x ? x.services.length : 0,
    containers: x ? x.containers.length : 0,
    volumes: x ? x.volumes.length : 0,
    bind_mounts: x ? x.bind_mounts.length : 0,
    unprotected_high: x ? x.unprotected.filter((u) => u.severity === "high").length : 0,
    dependencies: x ? x.dependencies.length : 0,
    secrets_count: x ? x.secrets_count : 0,
    owner: a.owner,
    environment: a.environment,
    criticality: a.criticality,
    last_seen_at: a.last_seen_at,
    missing_since: a.missing_since,
    policy_id: hooks.policyOf ? hooks.policyOf(a.id) : null,
    ...(hooks.protection ? { protection: hooks.protection(a) } : {}),
  };
}

const stripEnv = (e) => ({ key: e.key, value: e.value, ...(e.sensitive ? { sensitive: true } : {}) });

function detail(a) {
  return {
    ...summary(a),
    ...(a.manual_containers.length ? { manual_containers: a.manual_containers } : {}),
    analysis: a.analysis,
    containers_detail: a.containers_detail.map((c) => ({ ...c, env: c.env.map(stripEnv) })),
    collected_at: a.analysis ? iso(-2 * MIN - 5000) : null,
  };
}

const SHOP_COMPOSE = (reveal) => `name: shop
services:
  web:
    image: registry.example.com/shop/web:2.4.1
    ports: ["8080:3000"]
    environment:
      DATABASE_URL: ${reveal ? "postgres://shop:S3cr3t-pg@db:5432/shop" : MASK}
      SESSION_SECRET: ${reveal ? "b7f0c2d9e4a1" : MASK}
      NODE_ENV: production
    volumes:
      - ./uploads:/app/uploads
      - shop_media:/app/media
    networks: [default, proxy]
  db:
    image: postgres:18-alpine
    environment:
      POSTGRES_PASSWORD: ${reveal ? "S3cr3t-pg" : MASK}
      POSTGRES_USER: shop
    volumes:
      - shop_pgdata:/var/lib/postgresql/data
  worker:
    build: ./worker
    image: shop-worker:dev
    env_file: .env
    tmpfs: [/tmp]
volumes:
  shop_pgdata: {}
  shop_media:
    driver_opts: { type: nfs, o: "addr=nas01.example.com,rw", device: ":/export/shop_media" }
networks:
  proxy:
    external: true
`;

const SHOP_ENV = (reveal) => `# shop worker settings
QUEUE_TOKEN=${reveal ? "q-4f9a2b" : MASK}
CONCURRENCY=4
`;

function reconstructed(a) {
  const c = a.containers_detail[0];
  const secrets = c.env.filter((e) => e.sensitive).map((e) => e.key);
  const envLines = c.env.map((e) => `      ${e.key}: ${e.sensitive ? `\${${e.key}}` : JSON.stringify(e.value)}`).join("\n");
  return `# RECONSTRUCTED by DBR² from Docker runtime metadata (${iso(-2 * MIN).slice(0, 19)}Z).
# This is NOT the original Compose file: review it before use.
# Reason: no Compose definition: generated from Docker runtime metadata
${secrets.length ? `# Sensitive values were replaced by placeholders; supply them at restore time: ${secrets.join(", ")}\n` : ""}services:
  ${a.name}:
    image: ${c.image}
    container_name: ${c.name}
${c.env.length ? `    environment:\n${envLines}\n` : ""}${c.ports.length ? `    ports:\n${c.ports.map((p) => `      - ${p.host_port}:${p.container_port}`).join("\n")}\n` : ""}`;
}

function compose(a, reveal) {
  if (!a.analysis) return null;
  if (a.analysis.source === "original") {
    return {
      source: "original",
      config_files: [{ path: "/srv/shop/docker-compose.yml", content: SHOP_COMPOSE(reveal), masked: !reveal }],
      env_files: [{ path: "/srv/shop/.env", content: SHOP_ENV(reveal), masked: !reveal }],
      revealed: reveal,
    };
  }
  const out = {
    source: "reconstructed",
    reason: a.analysis.source_reason ?? "no Compose definition: generated from Docker runtime metadata",
    config_files: [],
    env_files: [],
    reconstructed: reconstructed(a),
    revealed: reveal,
  };
  if (reveal) {
    out.secrets = Object.fromEntries(
      a.containers_detail.flatMap((c) => c.env.filter((e) => e.sensitive).map((e) => [e.key, e._real])),
    );
  }
  return out;
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

const TRANSITIONS = {
  approve: { from: ["pending"], to: "active" },
  suspend: { from: ["active"], to: "suspended" },
  resume: { from: ["suspended"], to: "active" },
  revoke: { from: ["pending", "active", "suspended"], to: "revoked" },
};

/**
 * @param {object} h helpers from mock-api.mjs: send, problem, readJson, audit
 * @returns {Array<[string, RegExp, Function]>} [method, path regex, handler(req, res, url, user, match)]
 */
export function fleetRoutes({ send, problem, readJson, audit }) {
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
  const findAgent = (res, id) => {
    const a = agents.find((x) => x.id === id);
    if (!a) problem(res, 404, "not_found", "Not Found", "agent not found");
    return a;
  };
  const findApp = (res, id) => {
    const a = apps.find((x) => x.id === id);
    if (!a) problem(res, 404, "not_found", "Not Found", "application not found");
    return a;
  };
  const UUID = "([0-9a-f-]{36})";

  return [
    ["GET", /^\/api\/v1\/agents$/, (req, res, url, user) => need(res, user, "host.read") && send(res, 200, { items: agents })],

    [
      "GET",
      /^\/api\/v1\/agents\/registration-tokens$/,
      (req, res, url, user) => need(res, user, "host.manage") && send(res, 200, { items: tokens }),
    ],

    [
      "POST",
      /^\/api\/v1\/agents\/registration-tokens$/,
      async (req, res, url, user) => {
        if (!need(res, user, "host.manage")) return;
        const body = await readJson(req);
        const description = typeof body?.description === "string" ? body.description.trim() : "";
        const hours = body?.expires_in_hours ?? 24;
        if (!description || description.length > 200) return invalid(res, "description must be 1-200 characters", "body.description");
        if (!Number.isInteger(hours) || hours < 1 || hours > 168) return invalid(res, "expires_in_hours must be 1-168", "body.expires_in_hours");
        const secret = `dbr2reg_${randomBytes(18).toString("base64url")}`;
        const t = {
          id: randomUUID(),
          prefix: secret.slice(0, 14),
          description,
          created_at: iso(),
          expires_at: iso(hours * HOUR),
          used_at: null,
          used_by_agent: null,
          revoked_at: null,
        };
        tokens.unshift(t);
        audit("agent.registration_token_created", "success", req, { actor: user.display_name, target_type: "registration_token", target_id: t.id });
        send(res, 201, {
          token: secret,
          gateway_address: GATEWAY,
          ca_sha256: CA_SHA256,
          join_command: `sudo dbr2-agent enroll --server ${GATEWAY} --token ${secret} --ca-sha256 ${CA_SHA256}`,
          registration_token: t,
        });
      },
    ],

    [
      "DELETE",
      new RegExp(`^/api/v1/agents/registration-tokens/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "host.manage")) return;
        const t = tokens.find((x) => x.id === m[1]);
        if (!t) return problem(res, 404, "not_found", "Not Found", "registration token not found");
        if (t.used_at || t.revoked_at) return problem(res, 409, "conflict", "Conflict", "the token is already used or revoked");
        t.revoked_at = iso();
        audit("agent.registration_token_revoked", "success", req, { actor: user.display_name, target_type: "registration_token", target_id: t.id });
        send(res, 204);
      },
    ],

    [
      "GET",
      new RegExp(`^/api/v1/agents/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "host.read")) return;
        const a = findAgent(res, m[1]);
        if (a) send(res, 200, a);
      },
    ],

    [
      "GET",
      new RegExp(`^/api/v1/agents/${UUID}/inventory$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "host.read")) return;
        const a = findAgent(res, m[1]);
        if (!a) return;
        const inv = inventoryFor(a.id);
        if (!inv) return problem(res, 404, "not_found", "Not Found", "no inventory reported yet");
        send(res, 200, inv);
      },
    ],

    [
      "POST",
      new RegExp(`^/api/v1/agents/${UUID}/(approve|suspend|resume|revoke)$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "host.manage")) return;
        const a = findAgent(res, m[1]);
        if (!a) return;
        const action = m[2];
        const body = await readJson(req);
        const reason = typeof body?.reason === "string" ? body.reason.trim() : "";
        if (!reason || reason.length > 500) return invalid(res, "reason must be 1-500 characters", "body.reason");
        const t = TRANSITIONS[action];
        if (!t.from.includes(a.status)) {
          return problem(res, 409, "conflict", "Conflict", `cannot ${action} an agent that is ${a.status}`);
        }
        a.status = t.to;
        a.status_reason = reason;
        if (action === "approve") {
          a.approved_at = iso();
          a.connected = true;
          a.last_seen_at = iso();
          a.latency_ms = 6;
          a.docker_reachable = true;
          a.docker_version = "29.8.1";
          a.os_release = "Rocky Linux 9.6 (Blue Onyx)";
          a.architecture = "amd64";
        }
        if (action === "suspend" || action === "revoke") a.connected = false;
        if (action === "revoke") a.certificate_not_after = iso();
        if (action !== "resume") publish("agent.status", "host.read", { host_id: a.id, connected: a.connected });
        audit(`agent.${action}`, "success", req, { actor: user.display_name, target_type: "agent", target_id: a.id, reason });
        send(res, 200, a);
      },
    ],

    [
      "POST",
      new RegExp(`^/api/v1/agents/${UUID}/discover$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "host.manage")) return;
        const a = findAgent(res, m[1]);
        if (!a) return;
        if (a.status !== "active") return problem(res, 409, "conflict", "Conflict", `the agent is ${a.status}`);
        send(res, 202, { workflow_id: `discover-host-${a.id}-${Date.now()}` });
        setTimeout(() => {
          const count = apps.filter((x) => x.host_id === a.id && !x.missing_since).length;
          publish("inventory.updated", "host.read", { host_id: a.id, applications: count });
        }, 2_000);
      },
    ],

    [
      "GET",
      /^\/api\/v1\/applications$/,
      (req, res, url, user) =>
        need(res, user, "application.read") &&
        send(res, 200, { items: apps.map(summary).sort((x, y) => x.name.localeCompare(y.name)) }),
    ],

    [
      "POST",
      /^\/api\/v1\/applications$/,
      async (req, res, url, user) => {
        if (!need(res, user, "application.manage")) return;
        const body = await readJson(req);
        const name = typeof body?.name === "string" ? body.name.trim() : "";
        if (!name || name.length > 200) return invalid(res, "name must be 1-200 characters", "body.name");
        if (!agents.some((a) => a.id === body.host_id)) return invalid(res, "unknown host", "body.host_id");
        const wanted = Array.isArray(body.containers) ? body.containers : [];
        if (wanted.length === 0) return invalid(res, "pick at least one container", "body.containers");
        const members = wanted.map((c) => apps.find((a) => a.kind === "container" && a.host_id === body.host_id && a.name === c));
        if (members.some((x) => !x)) return invalid(res, "every container must be a standalone container on that host", "body.containers");
        if (apps.some((a) => a.host_id === body.host_id && a.name === name)) {
          return problem(res, 409, "conflict", "Conflict", `an application named ${name} already exists on this host`);
        }
        const id = randomUUID();
        const cds = members.flatMap((x) => x.containers_detail);
        const merged = {
          id,
          name,
          display_name: null,
          kind: "manual",
          host_id: body.host_id,
          source: "reconstructed",
          owner: null,
          environment: null,
          criticality: null,
          last_seen_at: iso(),
          missing_since: null,
          manual_containers: wanted,
          containers_detail: cds,
          _members: members,
          analysis: analysis({
            key: `manual:${name}`,
            kind: "manual",
            name,
            source: "reconstructed",
            source_reason: "manual application: generated from Docker runtime metadata",
            services: members.flatMap((x) => x.analysis.services),
            volumes: members.flatMap((x) => x.analysis.volumes),
            networks: [{ name: "bridge", driver: "bridge" }],
            images: members.flatMap((x) => x.analysis.images),
            unprotected: members.flatMap((x) => x.analysis.unprotected),
            secrets_count: members.reduce((n, x) => n + x.analysis.secrets_count, 0),
            containers: wanted,
          }),
        };
        for (const x of members) apps.splice(apps.indexOf(x), 1);
        apps.push(merged);
        audit("application.created", "success", req, { actor: user.display_name, target_type: "application", target_id: id });
        send(res, 201, { id });
      },
    ],

    [
      "GET",
      new RegExp(`^/api/v1/applications/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "application.read")) return;
        const a = findApp(res, m[1]);
        if (a) send(res, 200, detail(a));
      },
    ],

    [
      "PATCH",
      new RegExp(`^/api/v1/applications/${UUID}$`),
      async (req, res, url, user, m) => {
        if (!need(res, user, "application.manage")) return;
        const a = findApp(res, m[1]);
        if (!a) return;
        const body = (await readJson(req)) ?? {};
        const envs = ["production", "staging", "development", "test", "other", ""];
        const crits = ["critical", "high", "medium", "low", ""];
        if (body.environment !== undefined && !envs.includes(body.environment)) return invalid(res, "invalid environment", "body.environment");
        if (body.criticality !== undefined && !crits.includes(body.criticality)) return invalid(res, "invalid criticality", "body.criticality");
        for (const k of ["display_name", "owner"]) {
          if (body[k] !== undefined && (typeof body[k] !== "string" || body[k].length > 200)) return invalid(res, `invalid ${k}`, `body.${k}`);
        }
        for (const k of ["display_name", "owner", "environment", "criticality"]) {
          if (body[k] !== undefined) a[k] = body[k] === "" ? null : body[k];
        }
        audit("application.updated", "success", req, { actor: user.display_name, target_type: "application", target_id: a.id });
        send(res, 200, summary(a));
      },
    ],

    [
      "DELETE",
      new RegExp(`^/api/v1/applications/${UUID}$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "application.manage")) return;
        const a = findApp(res, m[1]);
        if (!a) return;
        if (a.kind !== "manual") return invalid(res, "only manual applications can be deleted", "path.id");
        apps.splice(apps.indexOf(a), 1);
        if (a._members) apps.push(...a._members);
        audit("application.deleted", "success", req, { actor: user.display_name, target_type: "application", target_id: a.id });
        send(res, 204);
      },
    ],

    [
      "GET",
      new RegExp(`^/api/v1/applications/${UUID}/compose$`),
      (req, res, url, user, m) => {
        if (!need(res, user, "application.read")) return;
        const a = findApp(res, m[1]);
        if (!a) return;
        const reveal = url.searchParams.get("reveal") === "true";
        if (reveal && !need(res, user, "secrets.read")) return;
        const c = compose(a, reveal);
        if (!c) return problem(res, 409, "conflict", "Conflict", "the application is not in the latest inventory");
        if (reveal) {
          audit("secrets.revealed", "success", req, { actor: user.display_name, target_type: "application", target_id: a.id });
        }
        send(res, 200, c);
      },
    ],
  ];
}

/** Read access to the in-memory fleet for the other mock modules (mock-protection.mjs). */
export const fleetData = {
  agents,
  apps,
  appName: (a) => a.display_name || a.name,
  hostname,
  /** @param {(app: object) => object} fn */
  setProtection: (fn) => {
    hooks.protection = fn;
  },
  /** @param {(appId: string) => string | null} fn */
  setPolicyOf: (fn) => {
    hooks.policyOf = fn;
  },
};

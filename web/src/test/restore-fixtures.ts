// SPDX-License-Identifier: Apache-2.0
//
// Real-shaped Restores fixtures (api/openapi.yaml Preview / RestoreRunDTO, as
// the mock API in scripts/mock-restore.mjs returns them).

import { APP_ID, HOST_ID, MANIFEST, RP_COMMITTED, RP_ID } from "@/test/protection-fixtures";

export const DR_HOST_ID = "f7b3d4c5-8a9e-4c0f-9b2a-3c4d5e6f7a06";
export const EDGE_HOST_ID = "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02";
export const RS_ID = "rs_01K5Z9M2Q4R6S8T0V2W4X6Y8Z0";
export const RS_FAILED_ID = "rs_01K5Z9M2Q4R6S8T0V2W4X6Y8Z1";
export const RS_ROLLED_BACK_ID = "rs_01K5Z9M2Q4R6S8T0V2W4X6Y8Z2";
export const RS_SUCCEEDED_ID = "rs_01K5Z9M2Q4R6S8T0V2W4X6Y8Z3";

/** The protection manifest plus a failed optional bind mount (and its skipped fsmeta). */
export const RESTORE_MANIFEST = {
  ...MANIFEST,
  application: { id: APP_ID, name: "Web shop", compose_project: "shop", working_dir: "/srv/shop" },
  components: [
    ...MANIFEST.components,
    {
      name: "bind:/srv/shop/uploads",
      kind: "bind_mount",
      required: false,
      status: "failed",
      error: "open /srv/shop/uploads/tmp/.lock: permission denied",
      path: "/srv/shop/uploads",
      size_bytes: 0,
    },
    {
      name: "fsmeta:bind:/srv/shop/uploads",
      kind: "fsmeta",
      required: false,
      status: "skipped",
      parent: "bind:/srv/shop/uploads",
      size_bytes: 0,
    },
  ],
};

export const RP_WITH_MANIFEST = { ...RP_COMMITTED, manifest: RESTORE_MANIFEST };

export const AGENTS = [
  {
    id: HOST_ID,
    hostname: "docker-prod-01",
    status: "active",
    status_reason: null,
    connected: true,
    outdated: false,
    agent_version: "0.1.0.0",
    protocol_version: "0.1.0.0",
    os_release: "Rocky Linux 9.6 (Blue Onyx)",
    architecture: "amd64",
    enrolled_at: "2026-08-26T10:00:00Z",
    approved_at: "2026-08-26T10:05:00Z",
    last_seen_at: "2026-09-25T09:00:00Z",
    latency_ms: 4,
    docker_reachable: true,
    docker_version: "29.8.1",
    health_error: null,
    certificate_not_after: "2026-12-24T17:50:12Z",
  },
];

const IMAGES = [
  {
    ref: "registry.example.com/shop/web:2.4.1",
    digest: "registry.example.com/shop/web@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e",
    action: "present",
  },
  { ref: "shop-worker:dev", action: "present" },
];

/** Clean in-place production restore on the source host. */
export const PREVIEW_PROD = {
  recovery_point_id: RP_ID,
  application_name: "Web shop",
  source_host_id: HOST_ID,
  target_host_id: HOST_ID,
  target_hostname: "docker-prod-01",
  mode: "in_place",
  target_application_id: APP_ID,
  production: true,
  production_reasons: [
    "the target application is tagged environment: production",
    "the restore overwrites a running application in place",
  ],
  components: [
    { name: "config", kind: "config", action: "restore_files", target: "/srv/shop", size_bytes: 48213 },
    { name: "volume:shop_pgdata", kind: "volume", action: "overwrite", target: "shop_pgdata", size_bytes: 12884901888 },
  ],
  stop_containers: [
    { id: "a1", name: "shop-web-1", state: "running" },
    { id: "a2", name: "shop-db-1", state: "running" },
  ],
  create_containers: [],
  networks: [{ name: "shop_default", action: "exists" }],
  images: IMAGES,
  ports: ["8080/tcp"],
  collisions: [],
  warnings: [],
  blocked: false,
};

/** Alternate host with collisions (blocked). */
export const PREVIEW_COLLISION = {
  ...PREVIEW_PROD,
  target_host_id: EDGE_HOST_ID,
  target_hostname: "docker-edge-02",
  mode: "alternate_host",
  target_application_id: undefined,
  production: false,
  production_reasons: null,
  stop_containers: [],
  create_containers: ["shop-db-1"],
  networks: [{ name: "shop_default", action: "create" }],
  images: IMAGES.map((i) => ({ ...i, action: "pull" })),
  collisions: [
    { kind: "container_name", name: "shop-web-1", detail: "a container with this name exists on the target and belongs to Compose project shop-staging" },
    { kind: "port", name: "8080/tcp", detail: "already published by container staging-proxy" },
  ],
  warnings: ["the latest inventory of docker-edge-02 is 3 hours old: collisions are checked against it"],
  blocked: true,
};

/** Clean alternate-host restore (not production). */
export const PREVIEW_DR = {
  ...PREVIEW_COLLISION,
  target_host_id: DR_HOST_ID,
  target_hostname: "docker-dr-03",
  create_containers: ["shop-web-1", "shop-db-1"],
  components: [
    { name: "config", kind: "config", action: "restore_files", target: "/srv/shop-restored", size_bytes: 48213 },
    { name: "volume:shop_pgdata", kind: "volume", action: "create", target: "shop_pgdata", size_bytes: 12884901888 },
  ],
  collisions: [],
  warnings: [],
  blocked: false,
};

const RUN_BASE = {
  recovery_point_id: RP_ID,
  application_id: APP_ID,
  application_name: "Web shop",
  source_host_id: HOST_ID,
  target_application_id: APP_ID,
  components: ["config", "volume:shop_pgdata"],
  path_remaps: [],
  requested_by: "admin",
  workflow_id: `application-${APP_ID}`,
};

export const RUN_RUNNING = {
  ...RUN_BASE,
  id: RS_ID,
  target_host_id: HOST_ID,
  target_hostname: "docker-prod-01",
  mode: "in_place",
  production: true,
  reason: "INC-48391: orders table corrupted",
  state: "running",
  step: "restore-data",
  error: null,
  created_at: "2026-09-25T10:00:00Z",
  started_at: "2026-09-25T10:00:01Z",
  finished_at: null,
  preview: PREVIEW_PROD,
};

export const RUN_FAILED = {
  ...RUN_BASE,
  id: RS_FAILED_ID,
  target_host_id: DR_HOST_ID,
  target_hostname: "docker-dr-03",
  target_application_id: null,
  mode: "alternate_host",
  production: false,
  path_remaps: [{ from: "/srv/shop", to: "/srv/shop-restored" }],
  reason: null,
  state: "failed",
  step: "images",
  error: "images: pull registry.example.com/shop/web: unauthorized: authentication required",
  created_at: "2026-09-25T06:00:00Z",
  started_at: "2026-09-25T06:00:02Z",
  finished_at: "2026-09-25T06:00:41Z",
  preview: PREVIEW_DR,
  result: {
    images: [{ ref: "registry.example.com/shop/web:2.4.1", status: "failed", error: "unauthorized: authentication required" }],
  },
};

export const RUN_ROLLED_BACK = {
  ...RUN_BASE,
  id: RS_ROLLED_BACK_ID,
  target_host_id: HOST_ID,
  target_hostname: "docker-prod-01",
  mode: "in_place",
  production: true,
  reason: "INC-48391: orders table corrupted",
  requested_by: "ada@example.com",
  state: "rolled_back",
  step: "health-check",
  error: "the restored application did not become healthy: shop-db-1 (exited/none)",
  created_at: "2026-09-23T09:10:00Z",
  started_at: "2026-09-23T09:10:03Z",
  finished_at: "2026-09-23T09:19:03Z",
  preview: PREVIEW_PROD,
  result: {
    components: [
      {
        name: "volume:shop_pgdata",
        status: "restored",
        target_path: "/var/lib/docker/volumes/shop_pgdata/_data",
        bytes: 12884901888,
        files: 2143,
        metadata_applied: 2143,
        previous_path: "/var/lib/docker/volumes/shop_pgdata/_data.dbr2-prev-4f2a",
      },
    ],
    health: {
      ok: false,
      containers: [
        { container_id: "c1", name: "shop-web-1", state: "running", health: "none", ok: true },
        {
          container_id: "c2",
          name: "shop-db-1",
          state: "exited",
          health: "none",
          ok: false,
          exit_code: 1,
          log_tail: "PANIC:  could not locate a valid checkpoint record",
        },
      ],
    },
    rolled_back: true,
  },
};

export const RUN_SUCCEEDED = {
  ...RUN_BASE,
  id: RS_SUCCEEDED_ID,
  target_host_id: DR_HOST_ID,
  target_hostname: "docker-dr-03",
  target_application_id: null,
  mode: "alternate_host",
  production: false,
  reason: "DR drill",
  state: "succeeded",
  step: null,
  error: null,
  created_at: "2026-09-24T08:00:00Z",
  started_at: "2026-09-24T08:00:01Z",
  finished_at: "2026-09-24T08:03:13Z",
};

// SPDX-License-Identifier: Apache-2.0
//
// Real-shaped Hosts / Applications fixtures (copied from api/openapi.yaml
// shapes and a dev-stack response; secrets masked as the server does).

import type { Me } from "@/lib/api/schemas";

export function meWith(permissions: string[]): Me {
  return {
    id: "17fd57ac-bddb-4233-9ae8-155a8639cf74",
    username: "dbr2-admin",
    display_name: "Master Admin",
    email: null,
    kind: "master_admin",
    roles: ["administrator"],
    permissions,
    totp_enabled: false,
  };
}

export const AGENT_ACTIVE = {
  id: "21bec70a-ad2d-41e1-b653-7029ab81f326",
  hostname: "sysadm",
  status: "active",
  status_reason: "dev box e2e",
  connected: true,
  outdated: false,
  agent_version: "0.1.0.0",
  protocol_version: "0.1.0.0",
  os_release: "Fedora Linux 44 (KDE Plasma Desktop Edition)",
  architecture: "amd64",
  enrolled_at: "2026-09-25T17:50:12.885208Z",
  approved_at: "2026-09-25T17:50:27.888853Z",
  last_seen_at: "2026-09-25T17:53:37.487921Z",
  latency_ms: 1,
  docker_reachable: true,
  docker_version: "29.8.1",
  health_error: null,
  certificate_not_after: "2026-12-24T17:50:12.886867Z",
};

export const AGENT_PENDING = {
  ...AGENT_ACTIVE,
  id: "c47a9e10-5d2b-4e8f-a1c3-9b8d7e6f5a03",
  hostname: "build-runner-03",
  status: "pending",
  status_reason: null,
  connected: false,
  os_release: null,
  architecture: null,
  approved_at: null,
  last_seen_at: null,
  latency_ms: null,
  docker_reachable: null,
  docker_version: null,
};

export const REGISTRATION_TOKEN = {
  id: "4c6492b4-d25d-4084-8f58-ac13e3dc2be8",
  prefix: "dbr2reg_WSSYb4",
  description: "dev box e2e",
  created_at: "2026-09-25T17:50:12.819073Z",
  expires_at: "2026-09-25T18:50:12.819123Z",
  used_at: null,
  used_by_agent: null,
  revoked_at: null,
};

export const CREATE_TOKEN_RESPONSE = {
  token: "dbr2reg_WSSYb4c2hBqP0lFtXn9vYk",
  gateway_address: "dbr2.example.com:8443",
  ca_sha256: "5c1f0e9a7d3b2c4e6f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e",
  join_command:
    "sudo dbr2-agent enroll --server dbr2.example.com:8443 --token dbr2reg_WSSYb4c2hBqP0lFtXn9vYk --ca-sha256 5c1f0e9a7d3b2c4e6f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e",
  registration_token: REGISTRATION_TOKEN,
};

export const INVENTORY = {
  received_at: "2026-09-25T17:53:17.79355Z",
  inventory: {
    schema_version: 1,
    collected_at: "2026-09-25T17:53:13.545478Z",
    host: {
      hostname: "sysadm",
      operating_system: "Fedora Linux 44 (KDE Plasma Desktop Edition)",
      os_type: "linux",
      kernel_version: "7.2.7-200.fc44.x86_64",
      architecture: "x86_64",
      runtime: "docker",
      engine_version: "29.8.1",
      api_version: "1.56",
      root_dir: "/var/lib/docker",
      storage_driver: "overlayfs",
      cgroup_version: "2",
      security_options: ["name=seccomp,profile=builtin", "name=cgroupns"],
      rootless: false,
      selinux: false,
      cpus: 16,
      memory_bytes: 66833326080,
    },
    containers: [
      {
        id: "e6f40d27a3689517d2fc9a17f9f0a0bc3e7120c9e96168fe934eb33dbc69a558",
        name: "fbcad-minio-1",
        image: "minio/minio:RELEASE.2025-04-22T22-12-26Z",
        image_id: "sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e",
        state: "running",
        created: "2026-09-20T10:00:00Z",
        labels: { "com.docker.compose.project": "fbcad" },
      },
    ],
    volumes: [{ name: "fbcad_pgdata", driver: "local", mountpoint: "/var/lib/docker/volumes/fbcad_pgdata/_data" }],
    networks: [{ id: "ab12", name: "fbcad_default", driver: "bridge" }],
    images: [{ id: "sha256:d3e1", os: "linux", architecture: "amd64", size: 123 }],
    compose_projects: null,
    warnings: ["image 388d74569326: Error response from daemon: No such image"],
  },
};

export const APP_SUMMARY = {
  id: "83b1bb43-cbd9-45cb-add1-c25c29807ba8",
  name: "fbcad",
  display_name: null,
  kind: "compose",
  host_id: "21bec70a-ad2d-41e1-b653-7029ab81f326",
  hostname: "sysadm",
  source: "original",
  services: 2,
  containers: 2,
  volumes: 2,
  bind_mounts: 0,
  unprotected_high: 1,
  dependencies: 0,
  secrets_count: 8,
  owner: null,
  environment: null,
  criticality: null,
  last_seen_at: "2026-09-25T17:53:13.545478Z",
  missing_since: null,
  policy_id: null,
  protection: {
    status: "at_risk",
    reasons: ["1 of 2 components are not in the latest recovery point"],
    last_backup_at: "2026-09-25T17:40:02.118Z",
    last_recovery_point_id: "rp_01K5ZQ3M8XJ9T6V2B4N7C1D0EF",
    last_status: "complete",
    last_mode: "live",
    last_attempt_at: "2026-09-25T17:39:51.004Z",
    last_attempt_state: "committed",
    components: [
      { name: "config", kind: "config", required: true, protected: true, last_size_bytes: 48213 },
      { name: "volume:fbcad_miniodata", kind: "volume", required: true, protected: false, last_size_bytes: 0 },
    ],
    components_total: 2,
    components_protected: 1,
    unresolved_dependencies: 0,
  },
};

export const APP_DETAIL = {
  ...APP_SUMMARY,
  analysis: {
    key: "compose:fbcad",
    kind: "compose",
    name: "fbcad",
    compose_project: "fbcad",
    working_dir: "/home/garrettpost/Projects/fbcad",
    source: "original",
    services: [
      {
        name: "minio",
        image: "minio/minio:RELEASE.2025-04-22T22-12-26Z",
        containers: [
          {
            id: "e6f40d27a3689517d2fc9a17f9f0a0bc3e7120c9e96168fe934eb33dbc69a558",
            name: "fbcad-minio-1",
            state: "running",
          },
        ],
      },
    ],
    volumes: [
      {
        name: "fbcad_miniodata",
        driver: "local",
        mountpoint: "/var/lib/docker/volumes/fbcad_miniodata/_data",
        class: "local",
        used_by: ["fbcad-minio-1:/data"],
        protected_by_default: true,
      },
    ],
    bind_mounts: [],
    tmpfs: null,
    networks: [{ name: "fbcad_default", driver: "bridge" }],
    images: [
      {
        reference: "minio/minio:RELEASE.2025-04-22T22-12-26Z",
        image_id: "sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e",
        digests: ["minio/minio@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e"],
        platform: "linux/amd64",
      },
      { reference: "shop-worker:dev", image_id: "sha256:7d5c" },
    ],
    dependencies: [],
    unprotected: [
      {
        container: "fbcad-minio-1",
        path: "/root/.minio",
        files: 1,
        severity: "high",
        reason: "writable path not backed by a volume or bind mount: lost when the container is recreated",
      },
    ],
    secrets_count: 8,
    containers: ["fbcad-minio-1"],
  },
  containers_detail: [
    {
      id: "e6f40d27a3689517d2fc9a17f9f0a0bc3e7120c9e96168fe934eb33dbc69a558",
      name: "fbcad-minio-1",
      image: "minio/minio:RELEASE.2025-04-22T22-12-26Z",
      state: "running",
      env: [
        { key: "MINIO_ROOT_PASSWORD", value: "********", sensitive: true },
        { key: "MINIO_ROOT_USER", value: "fbcad" },
      ],
      ports: [{ container_port: "9000", protocol: "tcp", host_ip: "127.0.0.1", host_port: "9100" }],
      mounts: [
        {
          type: "volume",
          name: "fbcad_miniodata",
          source: "/var/lib/docker/volumes/fbcad_miniodata/_data",
          destination: "/data",
          driver: "local",
          rw: true,
          mode: "rw",
        },
      ],
      networks: ["fbcad_default"],
    },
  ],
  collected_at: "2026-09-25T17:53:13.545478Z",
};

export const COMPOSE_RECONSTRUCTED = {
  source: "reconstructed",
  reason: "no Compose definition: generated from Docker runtime metadata",
  config_files: [],
  env_files: [],
  reconstructed:
    "# RECONSTRUCTED by DBR² from Docker runtime metadata (2026-09-25T17:53:13Z).\nservices:\n  mft-pg:\n    image: postgres:18\n    environment:\n      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}\n",
  revealed: false,
};

export const COMPOSE_ORIGINAL = {
  source: "original",
  config_files: [
    {
      path: "/home/garrettpost/Projects/fbcad/docker-compose.yml",
      content: "services:\n  postgres:\n    environment:\n      POSTGRES_PASSWORD: ********\n",
      masked: true,
    },
  ],
  env_files: [{ path: "/home/garrettpost/Projects/fbcad/.env", content: "PAYLOAD_SECRET=********\n", masked: true }],
  revealed: false,
};

export const COMPOSE_ORIGINAL_REVEALED = {
  ...COMPOSE_ORIGINAL,
  config_files: [
    {
      path: "/home/garrettpost/Projects/fbcad/docker-compose.yml",
      content: "services:\n  postgres:\n    environment:\n      POSTGRES_PASSWORD: hunter2-really\n",
      masked: false,
    },
  ],
  env_files: [{ path: "/home/garrettpost/Projects/fbcad/.env", content: "PAYLOAD_SECRET=s3cr3t\n", masked: false }],
  revealed: true,
};

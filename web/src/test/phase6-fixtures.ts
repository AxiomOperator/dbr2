// SPDX-License-Identifier: Apache-2.0
//
// Contract-shaped fixtures for the Phase 6 endpoints (jobs, fleet-wide
// containers / volumes, users, roles, group mappings), as dbr2-server
// returns them (api/openapi.yaml).

export const HOST_PROD = "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01";
export const HOST_EDGE = "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02";
export const APP_SHOP = "5f939a00-fccb-4376-a6cc-37eeb5542abe";
export const APP_MFT = "9fc3a8cb-13d7-4c97-a291-4aade4ca63ee";

export const CONTAINERS = [
  {
    id: "1a29da9377468074d18f2e829abc39998a11159239456e49555a3eee3b2e12a3",
    name: "shop-web-1",
    image: "registry.example.com/shop/web:2.4.1",
    state: "running",
    host_id: HOST_PROD,
    hostname: "docker-prod-01",
    application_id: APP_SHOP,
    application_name: "Web shop",
    ports: [{ container_port: "3000", protocol: "tcp", host_ip: "0.0.0.0", host_port: "8080" }],
    mounts: 2,
    networks: ["shop_default", "proxy"],
    created: "2026-09-22T10:00:00Z",
    restart_policy: "unless-stopped",
  },
  {
    id: "b1c2d3e4f5a60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
    name: "db-migrate-oneoff",
    image: "registry.example.com/shop/web:2.4.1",
    state: "exited",
    host_id: HOST_PROD,
    hostname: "docker-prod-01",
    application_id: null,
    application_name: null,
    ports: null,
    mounts: 0,
    networks: null,
    created: "2026-09-13T10:00:00Z",
    restart_policy: "no",
  },
  {
    id: "c2d3e4f5a6b70718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
    name: "metrics-agent",
    image: "grafana/alloy:v1.11",
    state: "running",
    host_id: HOST_EDGE,
    hostname: "docker-edge-02",
    application_id: "1a2b3c4d-0000-4000-8000-000000000008",
    application_name: "metrics-agent",
    ports: [],
    mounts: 0,
    networks: ["bridge"],
    created: "2026-09-20T10:00:00Z",
    restart_policy: "always",
  },
];

export const VOLUMES = [
  {
    name: "shop_pgdata",
    driver: "local",
    host_id: HOST_PROD,
    hostname: "docker-prod-01",
    class: "local",
    application_id: APP_SHOP,
    application_name: "Web shop",
    used_by: ["shop-db-1:/var/lib/postgresql/data"],
    protected: true,
    last_backup_at: "2026-09-25T15:07:00Z",
    last_size_bytes: 12_884_901_888,
  },
  {
    name: "shop_media",
    driver: "local",
    host_id: HOST_PROD,
    hostname: "docker-prod-01",
    class: "external",
    application_id: APP_SHOP,
    application_name: "Web shop",
    used_by: ["shop-web-1:/app/media"],
    protected: false,
    last_backup_at: null,
    last_size_bytes: 0,
  },
  {
    name: "shop_pgdata_old",
    driver: "local",
    host_id: HOST_PROD,
    hostname: "docker-prod-01",
    class: "unused",
    application_id: null,
    application_name: null,
    used_by: null,
    protected: false,
    last_backup_at: null,
    last_size_bytes: 0,
  },
];

export const JOBS = [
  {
    id: "rp_01M3DSQAPGHCVMS1BYX84CRQ0H",
    type: "backup",
    application_id: APP_SHOP,
    application_name: "Web shop",
    hostname: "docker-prod-01",
    state: "running",
    detail: "quiesced",
    trigger: "manual",
    started_at: "2026-09-25T21:50:00Z",
    finished_at: null,
  },
  {
    id: "rs_01M3DSQAPGHCVMS1BYX84CRQ0J",
    type: "restore",
    application_id: APP_MFT,
    application_name: "mft-pg",
    hostname: "docker-dr-03",
    state: "rolled_back",
    detail: "alternate host",
    trigger: "manual",
    requested_by: "admin",
    error: "health check failed: mft-pg exited with code 1",
    started_at: "2026-09-25T20:00:00Z",
    finished_at: "2026-09-25T20:02:10Z",
  },
  {
    id: "rp_01M3DSQAPGHCVMS1BYX84CRQ0K",
    type: "backup",
    application_id: APP_MFT,
    application_name: "mft-pg",
    hostname: "docker-prod-01",
    state: "partial",
    detail: "live",
    trigger: "schedule",
    size_bytes: 1_342_177_280,
    started_at: "2026-09-25T12:00:00Z",
    finished_at: "2026-09-25T12:02:00Z",
  },
];

export const ROLES = [
  {
    role: "administrator",
    display_name: "Administrator",
    description: "Full control of DBR², including users, secrets and production restores.",
    permissions: ["host.read", "user.manage"],
  },
  {
    role: "auditor",
    display_name: "Auditor",
    description: "Read-only access to everything including the audit log and users.",
    permissions: ["audit.read", "user.read"],
  },
  {
    role: "backup_administrator",
    display_name: "Backup Administrator",
    description: "Manages hosts, applications, policies, repositories and backups; may run production restores.",
    permissions: ["backup.execute"],
  },
  {
    role: "read_only",
    display_name: "Read Only",
    description: "Read-only access to inventory and protection state.",
    permissions: ["host.read"],
  },
];

export const USER_LINUS = "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f";

export const USERS = [
  {
    id: "00000000-0000-0000-0000-000000000001",
    kind: "master_admin",
    username: "admin",
    display_name: "Master Admin",
    email: null,
    disabled: false,
    last_login_at: "2026-09-25T21:00:00Z",
    roles: [{ role: "administrator", source: "master_admin" }],
  },
  {
    id: USER_LINUS,
    kind: "oidc",
    username: "linus@example.com",
    display_name: "Linus Torvalds",
    email: "linus@example.com",
    disabled: false,
    last_login_at: null,
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
    last_login_at: "2026-09-05T10:00:00Z",
    roles: null,
  },
];

export const GROUP_MAPPINGS = [
  { provider: "entra", group_id: "4f0c2b1a-9d8e-4c7b-a6f5-e4d3c2b1a090", role: "backup_administrator" },
];

// SPDX-License-Identifier: Apache-2.0
//
// Real-shaped Repositories & backup fixtures (api/openapi.yaml shapes, as the
// mock API in scripts/mock-protection.mjs returns them).

export const REPO_ID = "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e01";
export const REPO_AWAITING_ID = "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e02";
export const APP_ID = "5f939a00-fccb-4376-a6cc-37eeb5542abe";
export const HOST_ID = "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01";
export const CERT = "6eee7256c6599efb86333af41fc18aef90406eb2c17a697098a055d6844920dd";

export const RECIPIENTS = [
  {
    id: "6a1c2d3e-4f50-4617-8293-a4b5c6d7e801",
    name: "Ada Lovelace",
    public_key: "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p",
    created_at: "2026-08-16T10:00:00Z",
  },
  {
    id: "6a1c2d3e-4f50-4617-8293-a4b5c6d7e802",
    name: "Grace Hopper",
    public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl grace@ops",
    created_at: "2026-08-16T11:00:00Z",
  },
];

export const LIVE = {
  initialized: true,
  server_running: true,
  kopia_version: "0.22.3",
  storage_healthy: true,
  storage_total_bytes: 8_796_093_022_208,
  storage_free_bytes: 5_277_655_813_325,
  storage_used_bytes: 3_518_437_208_883,
};

export const REPO_READY = {
  id: REPO_ID,
  name: "nas01-backups",
  description: "Primary Repository on nas01",
  backend: "nfs",
  status: "ready",
  is_default: true,
  server_url: "https://backup.example.lan:51515",
  internal_server_url: "https://dbr2-reposerver:51515",
  management_url: "http://dbr2-reposerver:8091",
  cert_sha256: CERT,
  kopia_repository_id: "c12ea26c72147518607c27a98ee43f10",
  splitter: "DYNAMIC-4M-BUZHASH",
  escrow_recipients: 2,
  escrow_generated_at: "2026-08-26T10:00:00Z",
  escrow_confirmed_at: "2026-08-26T10:20:00Z",
  last_reindex_at: "2026-09-23T10:00:00Z",
  last_verified_at: "2026-09-21T03:00:00Z",
  is_system: true,
  delete_reason: null,
  delete_after: null,
  created_at: "2026-08-26T10:00:00Z",
  live: LIVE,
  usage_by_host: [
    { host_id: HOST_ID, hostname: "docker-prod-01", applications: 3, latest_bytes: 443_033_452_134 },
    { host_id: "8b2e61c4-0f3a-4d59-b7e8-6c1a2d3e4f02", hostname: "docker-edge-02", applications: 1, latest_bytes: 19_649_872_332 },
  ],
};

export const REPO_AWAITING = {
  ...REPO_READY,
  id: REPO_AWAITING_ID,
  name: "lab-scratch",
  description: "",
  backend: "filesystem",
  status: "awaiting_escrow",
  is_default: false,
  internal_server_url: "",
  escrow_confirmed_at: null,
  last_reindex_at: null,
  usage_by_host: null,
};

export const REPO_UNAVAILABLE = {
  ...REPO_READY,
  id: "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e03",
  name: "offsite-nfs",
  status: "unavailable",
  is_default: false,
  live: null,
  live_error: "dial tcp 10.20.5.20:8091: connect: connection refused",
};

export const CREATE_REPO_RESPONSE = {
  repository: REPO_AWAITING,
  escrow_package: "-----BEGIN AGE ENCRYPTED FILE-----\nYWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUxOQ\n-----END AGE ENCRYPTED FILE-----\n",
  escrow_filename: "dbr2-escrow-lab-scratch-0b6f1d2e.age",
};

export const RP_ID = "rp_01K5Z3YQ8M4N6P7R9S0T1V2W3X";
export const RP_PARTIAL_ID = "rp_01K5Z3YQ8M4N6P7R9S0T1V2W3Y";

export const MANIFEST = {
  schema_version: 1,
  recovery_point_id: RP_ID,
  status: "complete",
  created_at: "2026-09-25T08:00:00Z",
  consistency_mode: "quiesced",
  consistency_point: "2026-09-25T08:00:05Z",
  crash_consistent_only: false,
  application: { id: APP_ID, name: "Web shop" },
  source: { host_id: HOST_ID, agent_id: HOST_ID, hostname: "docker-prod-01" },
  repository: { id: REPO_ID, name: "nas01-backups" },
  components: [
    {
      name: "config",
      kind: "config",
      required: true,
      status: "succeeded",
      snapshot_id: "k0f1e2d3c4b5a6978",
      snapshot_source: "maint@dbr2:/config/shop",
      size_bytes: 48213,
      files: 4,
      started_at: "2026-09-25T08:00:00Z",
      finished_at: "2026-09-25T08:00:01Z",
    },
    {
      name: "volume:shop_pgdata",
      kind: "volume",
      required: true,
      status: "succeeded",
      snapshot_id: "k8a7b6c5d4e3f2a1b",
      snapshot_source: "agent@docker-prod-01:/var/lib/docker/volumes/shop_pgdata/_data",
      size_bytes: 12884901888,
      files: 2143,
      owner_uid: 999,
      owner_gid: 999,
      mode: "0700",
      selinux_context: "system_u:object_r:container_file_t:s0",
      started_at: "2026-09-25T08:00:01Z",
      finished_at: "2026-09-25T08:06:01Z",
    },
    {
      name: "fsmeta:volume:shop_pgdata",
      kind: "fsmeta",
      required: true,
      status: "succeeded",
      snapshot_id: "k1234567890abcdef",
      size_bytes: 91442,
      files: 1,
      parent: "volume:shop_pgdata",
      started_at: "2026-09-25T08:06:01Z",
      finished_at: "2026-09-25T08:06:02Z",
    },
  ],
  workflow: { workflow_id: "backup-x", run_id: "run-1" },
  producer: { component: "dbr2-worker", version: "0.1.0.0" },
};

export const RP_COMMITTED = {
  id: RP_ID,
  application_id: APP_ID,
  application_name: "Web shop",
  host_id: HOST_ID,
  hostname: "docker-prod-01",
  repository_id: REPO_ID,
  state: "committed",
  status: "complete",
  verification: "verified",
  consistency_mode: "quiesced",
  consistency_point: "2026-09-25T08:00:05Z",
  crash_consistent_only: false,
  trigger: "schedule",
  workflow_id: "backup-x",
  size_bytes: 12_885_041_543,
  component_count: 3,
  error: null,
  created_at: "2026-09-25T08:00:00Z",
  committed_at: "2026-09-25T08:07:00Z",
  verified_at: "2026-09-25T09:00:00Z",
  delete_after: null,
  delete_reason: null,
  deleted_at: null,
};

export const RP_PARTIAL = {
  ...RP_COMMITTED,
  id: RP_PARTIAL_ID,
  status: "partial",
  verification: "unverified",
  verified_at: null,
  consistency_mode: "live",
  crash_consistent_only: true,
  error: "optional component bind:/srv/shop/uploads failed: permission denied",
};

export const RP_FAILED = {
  ...RP_COMMITTED,
  id: "rp_01K5Z3YQ8M4N6P7R9S0T1V2W3Z",
  state: "failed",
  status: null,
  consistency_point: null,
  size_bytes: 0,
  component_count: 0,
  committed_at: null,
  error: "pre-hook 1 (db) exited with status 2",
};

export const BACKUP_SETTINGS = {
  repository_id: null,
  consistency_mode: null,
  effective_mode: "quiesced",
  max_quiesce_seconds: 1800,
  pre_hooks: [{ container: "db", command: ["sh", "-c", 'psql -U shop -c "CHECKPOINT"'], timeout_seconds: 120 }],
  post_hooks: null,
  optional_components: ["bind:/srv/shop/uploads"],
  excluded_components: null,
  database_strategy: "logical",
  updated_at: "2026-09-20T10:00:00Z",
};

export const ALERTS = [
  {
    id: 43,
    severity: "warning",
    type: "backup.completed",
    target_type: "application",
    target_id: APP_ID,
    message: "Backup of Web shop committed as Partial: optional components failed",
    details: { recovery_point_id: RP_PARTIAL_ID },
    created_at: "2026-09-24T08:05:00Z",
    acknowledged_at: null,
  },
  {
    id: 44,
    severity: "critical",
    type: "backup.failed",
    target_type: "application",
    target_id: APP_ID,
    message: "Backup of Web shop failed: pre-hook 1 (db) exited with status 2",
    details: {},
    created_at: "2026-09-23T08:05:00Z",
    acknowledged_at: null,
  },
  {
    id: 42,
    severity: "critical",
    type: "agent.auto_resumed",
    target_type: "agent",
    target_id: HOST_ID,
    message: "docker-prod-01 resumed wiki on its own",
    details: {},
    created_at: "2026-09-25T09:00:00Z",
    acknowledged_at: null,
  },
  {
    id: 41,
    severity: "info",
    type: "repository.reindexed",
    target_type: "repository",
    target_id: REPO_ID,
    message: "Reindex of nas01-backups finished",
    details: null,
    created_at: "2026-09-22T08:00:00Z",
    acknowledged_at: "2026-09-22T09:00:00Z",
  },
];

export const HOST_SETTINGS = {
  max_concurrent_jobs: 2,
  backup_window_start: 1320,
  backup_window_end: 330,
  backup_window_timezone: "America/Chicago",
};

// SPDX-License-Identifier: Apache-2.0
//
// Real-shaped Phase 7–9 fixtures (api/openapi.yaml shapes, as the mock API in
// scripts/mock-policies.mjs and scripts/mock-platform.mjs returns them).

import { APP_ID, REPO_AWAITING_ID, REPO_ID } from "@/test/protection-fixtures";

export const POLICY_ID = "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e01";
export const POLICY_HOURLY_ID = "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e02";

const RETENTION = { keep_last: 7, keep_hourly: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 12, keep_yearly: 0 };

export const POLICY = {
  id: POLICY_ID,
  name: "Nightly production",
  description: "Every production application, once a night",
  schedule: "0 1 * * *",
  timezone: "America/Chicago",
  enabled: true,
  consistency_mode: null,
  repository_id: null,
  retention: RETENTION,
  applications: 1,
  next_run: "2026-09-26T06:00:00Z",
  created_at: "2026-09-01T10:00:00Z",
  updated_at: "2026-09-20T10:00:00Z",
};

export const POLICY_HOURLY = {
  ...POLICY,
  id: POLICY_HOURLY_ID,
  name: "Hourly critical",
  description: "",
  schedule: "15 * * * *",
  timezone: "UTC",
  consistency_mode: "live",
  retention: { keep_last: 24, keep_hourly: 48, keep_daily: 7, keep_weekly: 4, keep_monthly: 6, keep_yearly: 0 },
  applications: 0,
};

export const POLICY_DETAIL = {
  ...POLICY,
  assigned_applications: [{ id: APP_ID, name: "Web shop", host_id: "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01" }],
};

export const CONTRACT_SATISFIED = {
  application_id: APP_ID,
  application_name: "Web shop",
  max_rpo_minutes: 1440,
  required_components: ["config", "volume:shop_pgdata"],
  state: "satisfied",
  state_reasons: null,
  evaluated_at: "2026-09-25T09:00:00Z",
  violated_since: null,
};

export const CONTRACT_VIOLATED = {
  application_id: "1a2b3c4d-0000-4000-8000-000000000003",
  application_name: "redis-cache",
  max_rpo_minutes: 60,
  required_components: ["config"],
  state: "violated",
  state_reasons: ["no recovery point exists"],
  evaluated_at: "2026-09-25T09:00:00Z",
  violated_since: "2026-09-24T09:00:00Z",
};

export const CHANNEL_EMAIL = {
  id: "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a01",
  name: "Ops email",
  kind: "email",
  enabled: true,
  config: { to: ["ops@example.com", "oncall@example.com"] },
  secret_set: false,
  events: null,
  min_severity: "warning",
  created_at: "2026-09-01T10:00:00Z",
  updated_at: "2026-09-01T10:00:00Z",
  last_delivery_at: "2026-09-25T07:00:00Z",
  last_error: null,
};

export const CHANNEL_WEBHOOK = {
  ...CHANNEL_EMAIL,
  id: "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a02",
  name: "PagerDuty webhook",
  kind: "webhook",
  config: { url: "https://events.pagerduty.example.com/integration/dbr2/enqueue" },
  secret_set: true,
  events: ["backup.failed", "contract.*"],
  min_severity: "critical",
  last_error: "503 Service Unavailable",
};

export const DELIVERIES = [
  {
    id: 12,
    notification_id: 40,
    event_type: "backup.failed",
    severity: "critical",
    message: "Backup of Web shop failed",
    state: "failed",
    attempts: 6,
    next_attempt_at: null,
    last_error: "503 Service Unavailable",
    sent_at: null,
    created_at: "2026-09-24T09:00:00Z",
  },
  {
    id: 11,
    notification_id: 39,
    event_type: "contract.violated",
    severity: "critical",
    message: "Recovery Contract of redis-cache violated",
    state: "pending",
    attempts: 2,
    next_attempt_at: "2026-09-25T10:00:00Z",
    last_error: "timeout",
    sent_at: null,
    created_at: "2026-09-25T08:00:00Z",
  },
];

export const SMTP = {
  configured: true,
  host: "smtp.example.com",
  port: 587,
  username: "dbr2",
  from: "DBR2 <dbr2@example.com>",
  tls: "starttls",
  password_set: true,
  updated_at: "2026-09-01T10:00:00Z",
};

export const ESCROW_HEALTH = {
  healthy: false,
  recipients: 2,
  last_drill_at: null,
  checked_at: "2026-09-25T09:00:00Z",
  problems: [
    { code: "drill_due", severity: "warning", message: "no escrow drill in the last 12 months" },
    {
      code: "recipients_changed",
      severity: "critical",
      repository_id: REPO_ID,
      message: "the escrow recipients changed since the package of Repository nas01-backups was generated; regenerate it",
    },
    {
      code: "not_confirmed",
      severity: "critical",
      repository_id: REPO_AWAITING_ID,
      message: "the escrow package of Repository lab-scratch has not been confirmed",
    },
  ],
};

export const DRILL_ID = "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b01";

export const DRILL_NEW = {
  id: DRILL_ID,
  created_at: "2026-09-25T09:00:00Z",
  completed_at: null,
  recipients: 2,
  package: "-----BEGIN AGE ENCRYPTED FILE-----\nYWdl\n-----END AGE ENCRYPTED FILE-----\n",
};

export const PLATFORM_BACKUPS = [
  {
    id: "pb_01K5Z3YQ8M4N6P7R9S0T1V2W3X",
    state: "partial",
    trigger: "schedule",
    workflow_id: "platform/backup",
    started_at: "2026-09-24T02:00:00Z",
    finished_at: "2026-09-24T02:01:30Z",
    size_bytes: 48_234_496,
    sha256: "6eee7256c6599efb86333af41fc18aef90406eb2c17a697098a055d6844920dd",
    file_name: "dbr2-platform-20260924T020000Z.tar.zst.age",
    snapshot_id: "k0f1e2d3c4b5a6978",
    repository_id: REPO_ID,
    bundle_path: "/var/lib/dbr2/platform-bundles/dbr2-platform-20260924T020000Z.tar.zst.age",
    error: "reposerver offsite-nfs: state export failed",
    manifest: {
      platform_version: "0.1.0.0",
      tables: [
        { name: "applications", rows: 12 },
        { name: "recovery_points", rows: 400 },
      ],
      repositories: [{ name: "nas01-backups" }, { name: "offsite-nfs", missing: true }],
    },
  },
];

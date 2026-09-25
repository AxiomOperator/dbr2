// SPDX-License-Identifier: Apache-2.0
//
// Zod schemas for the Phase 4 (Repositories & backup) API contract, derived
// from `api/openapi.yaml` (operations tagged `Repositories` and `Backups`, plus
// the host limits under `Hosts`). As in `fleet-schemas.ts`, response objects
// are non-strict and nullable arrays are normalised to `[]`. Request schemas
// only contain writable fields: the server rejects unknown properties
// (`additionalProperties: false`), including read-only ones such as
// `effective_mode`. Byte counts (int64) are plain numbers, not safe integers.

import { z } from "zod";

/** A nullable (or omitted) array, normalised to `[]`. */
function list<T extends z.ZodType>(item: T) {
  return z
    .array(item)
    .nullish()
    .transform((v): z.output<T>[] => v ?? []);
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

export const PERMISSION_REPOSITORY_READ = "repository.read";
export const PERMISSION_REPOSITORY_MANAGE = "repository.manage";
export const PERMISSION_BACKUP_READ = "backup.read";
export const PERMISSION_BACKUP_EXECUTE = "backup.execute";
export const PERMISSION_POLICY_READ = "policy.read";
export const PERMISSION_POLICY_MANAGE = "policy.manage";

// ---------------------------------------------------------------------------
// Escrow recipients (ADR-0008)
// ---------------------------------------------------------------------------

/** ADR-0008: v1.0 expects two recipients held by two people. */
export const MIN_ESCROW_RECIPIENTS = 2;

export const EscrowRecipientSchema = z.object({
  id: z.string(),
  name: z.string(),
  public_key: z.string(),
  created_at: z.string(),
});
export type EscrowRecipient = z.infer<typeof EscrowRecipientSchema>;

export const EscrowRecipientListSchema = z.object({ items: list(EscrowRecipientSchema) });

/** True when `text` looks like a private key or age identity (never upload those). */
export function looksLikePrivateKey(text: string): boolean {
  return /AGE-SECRET-KEY-/i.test(text) || /PRIVATE KEY/i.test(text);
}

export const AddEscrowRecipientRequestSchema = z.object({
  name: z.string().trim().min(1, "Enter a name for the key holder.").max(100, "Use at most 100 characters."),
  public_key: z
    .string()
    .trim()
    .min(1, "Paste the public key.")
    .max(2000, "The public key is too long (at most 2000 characters).")
    .refine((v) => !looksLikePrivateKey(v), {
      message:
        "This is a PRIVATE identity. Never paste private keys into DBR²: paste the public key (age1… or ssh-…) and keep the identity offline.",
    })
    .refine((v) => v.startsWith("age1") || v.startsWith("ssh-"), {
      message: "Paste an age public key (age1…) or an SSH ed25519 / RSA public key (ssh-…).",
    }),
});
export type AddEscrowRecipientRequest = z.infer<typeof AddEscrowRecipientRequestSchema>;

// ---------------------------------------------------------------------------
// Repositories
// ---------------------------------------------------------------------------

export const REPOSITORY_STATUSES = ["awaiting_escrow", "ready", "unavailable", "retired"] as const;
export const RepositoryStatusSchema = z.enum(REPOSITORY_STATUSES);
export type RepositoryStatus = z.infer<typeof RepositoryStatusSchema>;

export const REPOSITORY_BACKENDS = ["nfs", "filesystem"] as const;
export const RepositoryBackendSchema = z.enum(REPOSITORY_BACKENDS);
export type RepositoryBackend = z.infer<typeof RepositoryBackendSchema>;

/** Live reposerver status (`LiveDTO`). */
export const RepositoryLiveSchema = z.object({
  initialized: z.boolean(),
  server_running: z.boolean(),
  kopia_version: z.string(),
  storage_healthy: z.boolean(),
  storage_error: z.string().optional(),
  storage_total_bytes: z.number(),
  storage_free_bytes: z.number(),
  storage_used_bytes: z.number(),
});
export type RepositoryLive = z.infer<typeof RepositoryLiveSchema>;

/** Per-host logical size of each application's latest recovery point. */
export const HostUsageSchema = z.object({
  host_id: z.string(),
  hostname: z.string(),
  applications: z.number().int(),
  latest_bytes: z.number(),
});
export type HostUsage = z.infer<typeof HostUsageSchema>;

export const RepositorySchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  backend: RepositoryBackendSchema,
  status: RepositoryStatusSchema,
  is_default: z.boolean(),
  server_url: z.string(),
  // "" means the same as server_url.
  internal_server_url: z.string().optional().default(""),
  management_url: z.string(),
  cert_sha256: z.string(),
  kopia_repository_id: z.string().nullable(),
  splitter: z.string().nullable(),
  escrow_recipients: z.number().int(),
  escrow_generated_at: z.string().nullable(),
  escrow_confirmed_at: z.string().nullable(),
  last_reindex_at: z.string().nullable(),
  created_at: z.string(),
  // Described as "null when unreachable" (see live_error), although the
  // contract does not declare it nullable: accept both.
  live: RepositoryLiveSchema.nullish().transform((v) => v ?? null),
  live_error: z.string().optional(),
  // Logical sizes; deduplicated physical usage is shared, not attributable.
  usage_by_host: list(HostUsageSchema),
});
export type Repository = z.infer<typeof RepositorySchema>;

export const RepositoryListSchema = z.object({ items: list(RepositorySchema) });

export const DEFAULT_MANAGEMENT_URL = "http://dbr2-reposerver:8091";
export const INTERNAL_SERVER_URL_PLACEHOLDER = "https://dbr2-reposerver:51515";

const url = (what: string) =>
  z
    .string()
    .trim()
    .min(1, `Enter the ${what}.`)
    .refine((v) => /^https?:\/\/[^\s/]+/.test(v), { message: `The ${what} must be an http:// or https:// URL.` });

export const CreateRepositoryRequestSchema = z.object({
  name: z.string().trim().min(1, "Enter a name.").max(100, "Use at most 100 characters."),
  description: z.string().trim().max(500, "Use at most 500 characters.").optional(),
  backend: RepositoryBackendSchema,
  management_url: url("management URL"),
  server_url: url("server URL"),
  internal_server_url: z
    .string()
    .trim()
    .refine((v) => v === "" || /^https?:\/\/[^\s/]+/.test(v), {
      message: "The internal server URL must be an http:// or https:// URL.",
    })
    .optional(),
  default: z.boolean().optional(),
});
export type CreateRepositoryRequest = z.infer<typeof CreateRepositoryRequestSchema>;

export const CreateRepositoryResponseSchema = z.object({
  repository: RepositorySchema,
  escrow_package: z.string(),
  escrow_filename: z.string(),
});
export type CreateRepositoryResponse = z.infer<typeof CreateRepositoryResponseSchema>;

export const EscrowPackageSchema = z.object({ filename: z.string(), package: z.string() });
export type EscrowPackage = z.infer<typeof EscrowPackageSchema>;

/** Base32 alphabet of the confirmation code (internal/escrow: XXXX-XXXX-XXXX-XXXX). */
const CODE_RE = /^[A-Z2-7]{16}$/;

/**
 * Normalises a typed confirmation code: case-, space- and dash-insensitive,
 * returned as `XXXX-XXXX-XXXX-XXXX`, or null when it cannot be a valid code.
 */
export function normalizeConfirmationCode(input: string): string | null {
  const raw = input.replace(/[\s-]/g, "").toUpperCase();
  if (!CODE_RE.test(raw)) return null;
  return `${raw.slice(0, 4)}-${raw.slice(4, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}`;
}

export const ConfirmEscrowRequestSchema = z.object({
  confirmation_code: z.string().transform((v, ctx) => {
    const n = normalizeConfirmationCode(v);
    if (n === null) {
      ctx.addIssue({
        code: "custom",
        message: "Enter the 16-character code from the decrypted package (format XXXX-XXXX-XXXX-XXXX).",
      });
      return z.NEVER;
    }
    return n;
  }),
});
export type ConfirmEscrowRequest = z.input<typeof ConfirmEscrowRequestSchema>;

export const WorkflowResponseSchema = z.object({ workflow_id: z.string() });
export type WorkflowResponse = z.infer<typeof WorkflowResponseSchema>;

// ---------------------------------------------------------------------------
// Backups: settings, recovery points, alerts
// ---------------------------------------------------------------------------

export const CONSISTENCY_MODES = ["live", "quiesced", "offline"] as const;
export const ConsistencyModeSchema = z.enum(CONSISTENCY_MODES);
export type ConsistencyMode = z.infer<typeof ConsistencyModeSchema>;

export const StartBackupRequestSchema = z.object({ consistency_mode: ConsistencyModeSchema.optional() });
export type StartBackupRequest = z.infer<typeof StartBackupRequestSchema>;

export const HOOK_MAX_TIMEOUT_SECONDS = 3600;

export const HookSchema = z.object({
  container: z.string(),
  command: list(z.string()),
  timeout_seconds: z.number().int().optional(),
  optional: z.boolean().optional(),
});
export type Hook = z.infer<typeof HookSchema>;

export const BackupSettingsSchema = z.object({
  repository_id: z.string().nullable(),
  // null = automatic: quiesced when hooks are defined, otherwise live.
  consistency_mode: ConsistencyModeSchema.nullable(),
  effective_mode: z.string().optional(),
  max_quiesce_seconds: z.number().int(),
  pre_hooks: list(HookSchema),
  post_hooks: list(HookSchema),
  optional_components: list(z.string()),
  excluded_components: list(z.string()),
  updated_at: z.string().optional(),
});
export type BackupSettings = z.infer<typeof BackupSettingsSchema>;

export const MAX_QUIESCE_MIN_MINUTES = 1;
export const MAX_QUIESCE_MAX_MINUTES = 1440;

const HookRequestSchema = z.object({
  container: z.string().trim().min(1, "Every hook needs a container."),
  command: z.array(z.string()).min(1, "Every hook needs a command."),
  timeout_seconds: z
    .number()
    .int("Hook timeouts are whole seconds.")
    .min(0, "Hook timeouts cannot be negative.")
    .max(HOOK_MAX_TIMEOUT_SECONDS, `Hook timeouts are at most ${HOOK_MAX_TIMEOUT_SECONDS} seconds.`)
    .optional(),
  optional: z.boolean().optional(),
});

export const UpdateBackupSettingsRequestSchema = z.object({
  repository_id: z.string().nullable(),
  consistency_mode: ConsistencyModeSchema.nullable(),
  max_quiesce_seconds: z
    .number({ error: "Enter the maximum quiesce time." })
    .int()
    .min(60, "The maximum quiesce time is at least 1 minute.")
    .max(86400, "The maximum quiesce time is at most 1440 minutes (24 hours)."),
  pre_hooks: z.array(HookRequestSchema),
  post_hooks: z.array(HookRequestSchema),
  optional_components: z.array(z.string()),
  excluded_components: z.array(z.string()),
});
export type UpdateBackupSettingsRequest = z.infer<typeof UpdateBackupSettingsRequestSchema>;

export const RECOVERY_POINT_STATES = ["pending", "committed", "failed", "missing", "deleting"] as const;
export const RecoveryPointStateSchema = z.enum(RECOVERY_POINT_STATES);
export type RecoveryPointState = z.infer<typeof RecoveryPointStateSchema>;

export const RecoveryPointSchema = z.object({
  id: z.string(),
  application_id: z.string(),
  application_name: z.string(),
  host_id: z.string(),
  hostname: z.string(),
  repository_id: z.string(),
  state: RecoveryPointStateSchema,
  // "complete" | "partial" (null until committed)
  status: z.string().nullable(),
  // "unverified" | "verified" | "verification_failed"
  verification: z.string(),
  consistency_mode: z.string(),
  consistency_point: z.string().nullable(),
  crash_consistent_only: z.boolean(),
  trigger: z.string(),
  workflow_id: z.string(),
  size_bytes: z.number(),
  component_count: z.number().int(),
  error: z.string().nullable(),
  created_at: z.string(),
  committed_at: z.string().nullable(),
  /** Detail view only; parsed separately with {@link ManifestSchema}. */
  manifest: z.unknown().optional(),
});
export type RecoveryPoint = z.infer<typeof RecoveryPointSchema>;

export const RecoveryPointListSchema = z.object({ items: list(RecoveryPointSchema) });

/** One manifest component (internal/manifest: Component). */
export const ManifestComponentSchema = z.object({
  name: z.string(),
  kind: z.string(),
  required: z.boolean().optional().default(false),
  status: z.string(),
  error: z.string().optional(),
  snapshot_id: z.string().optional(),
  snapshot_source: z.string().optional(),
  size_bytes: z.number().optional().default(0),
  files: z.number().optional(),
  path: z.string().optional(),
  volume_name: z.string().optional(),
  owner_uid: z.number().optional(),
  owner_gid: z.number().optional(),
  mode: z.string().optional(),
  selinux_context: z.string().optional(),
  parent: z.string().optional(),
  capture_method: z.string().optional(),
});
export type ManifestComponent = z.infer<typeof ManifestComponentSchema>;

/** The parts of the recovery manifest (schema_version 1) the console shows. */
export const ManifestSchema = z.object({
  schema_version: z.number(),
  recovery_point_id: z.string().optional(),
  status: z.string().optional(),
  consistency_mode: z.string().optional(),
  quiesce_started_at: z.string().optional(),
  quiesce_ended_at: z.string().optional(),
  auto_resumed: z.boolean().optional(),
  components: list(ManifestComponentSchema),
  producer: z.object({ component: z.string(), version: z.string() }).partial().optional(),
});
export type Manifest = z.infer<typeof ManifestSchema>;

export const ALERT_SEVERITIES = ["critical", "warning", "info"] as const;
export const AlertSeveritySchema = z.enum(ALERT_SEVERITIES);
export type AlertSeverity = z.infer<typeof AlertSeveritySchema>;

export const AlertSchema = z.object({
  id: z.number().int(),
  severity: AlertSeveritySchema,
  type: z.string(),
  target_type: z.string().nullable(),
  target_id: z.string().nullable(),
  message: z.string(),
  details: z.unknown(),
  created_at: z.string(),
  acknowledged_at: z.string().nullable(),
});
export type Alert = z.infer<typeof AlertSchema>;

export const AlertListSchema = z.object({ items: list(AlertSchema) });

// ---------------------------------------------------------------------------
// Host limits
// ---------------------------------------------------------------------------

export const MAX_CONCURRENT_JOBS = 16;

export const HostSettingsSchema = z.object({
  max_concurrent_jobs: z.number().int(),
  backup_window_start: z.number().int().nullable(),
  backup_window_end: z.number().int().nullable(),
  backup_window_timezone: z.string(),
});
export type HostSettings = z.infer<typeof HostSettingsSchema>;

const minuteOfDay = z.number().int().min(0).max(1439).nullable();

export const UpdateHostSettingsRequestSchema = z
  .object({
    max_concurrent_jobs: z
      .number({ error: "Enter the maximum number of concurrent jobs." })
      .int("Enter a whole number of jobs.")
      .min(1, "At least 1 concurrent job.")
      .max(MAX_CONCURRENT_JOBS, `At most ${MAX_CONCURRENT_JOBS} concurrent jobs.`),
    backup_window_start: minuteOfDay,
    backup_window_end: minuteOfDay,
    backup_window_timezone: z.string().trim().min(1, "Choose a timezone."),
  })
  .refine((v) => (v.backup_window_start === null) === (v.backup_window_end === null), {
    message: "Set both the start and the end of the backup window, or choose “no window”.",
  });
export type UpdateHostSettingsRequest = z.infer<typeof UpdateHostSettingsRequestSchema>;

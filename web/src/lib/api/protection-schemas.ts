// SPDX-License-Identifier: Apache-2.0
//
// Repositories & backup (Phase 4) and jobs (Phase 6). Response schemas are the
// generated OpenAPI Zod schemas wrapped by `contract()` (nullable arrays
// normalised to `[]`). The request schemas are the console's form schemas
// (user-facing messages, confirmation-code normalisation), typed against the
// generated request types; they only contain writable fields because the
// server rejects unknown properties (`additionalProperties: false`). The
// recovery manifest is untyped in the contract (`manifest: {}`) and is parsed
// leniently with {@link ManifestSchema}.

import { z } from "zod";
import { contract } from "./contract";
import type {
  AddEscrowRecipientRequest as AddEscrowRecipientRequestContract,
  BackupSettingsDtoWritable,
  ConfirmRepositoryEscrowRequest,
  CreateRepositoryRequest as CreateRepositoryRequestContract,
  HostSettingsDto,
  StartBackupRequest as StartBackupRequestContract,
} from "./generated";
import {
  zAlertDto,
  zBackupSettingsDto,
  zCreatedRepoBody,
  zEscrowRecipientDto,
  zGetRepositoryEscrowPackageResponse,
  zHostSettingsDto,
  zListAlertsResponse,
  zListEscrowRecipientsResponse,
  zListJobsResponse,
  zListRecoveryPointsResponse,
  zListRepositoriesResponse,
  zRecoveryPointDto,
  zRepositoryDto,
  zWorkflowOutBody,
} from "./generated/zod.gen";

/** A nullable (or omitted) array, normalised to `[]` (manifest parsing only). */
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

export const EscrowRecipientSchema = contract(zEscrowRecipientDto);
export type EscrowRecipient = z.infer<typeof EscrowRecipientSchema>;

export const EscrowRecipientListSchema = contract(zListEscrowRecipientsResponse);

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
}) satisfies z.ZodType<AddEscrowRecipientRequestContract>;
export type AddEscrowRecipientRequest = z.infer<typeof AddEscrowRecipientRequestSchema>;

// ---------------------------------------------------------------------------
// Repositories
// ---------------------------------------------------------------------------

export const REPOSITORY_STATUSES = ["awaiting_escrow", "ready", "unavailable", "retired"] as const;
export type RepositoryStatus = (typeof REPOSITORY_STATUSES)[number];

export const REPOSITORY_BACKENDS = ["nfs", "filesystem"] as const;
export const RepositoryBackendSchema = z.enum(REPOSITORY_BACKENDS);
export type RepositoryBackend = z.infer<typeof RepositoryBackendSchema>;

export const RepositorySchema = contract(zRepositoryDto);
export type Repository = z.infer<typeof RepositorySchema>;
/** Live reposerver status (`LiveDTO`); null when the reposerver is unreachable. */
export type RepositoryLive = NonNullable<Repository["live"]>;
/** Per-host logical size of each application's latest recovery point. */
export type HostUsage = Repository["usage_by_host"][number];

export const RepositoryListSchema = contract(zListRepositoriesResponse);

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
}) satisfies z.ZodType<CreateRepositoryRequestContract>;
export type CreateRepositoryRequest = z.infer<typeof CreateRepositoryRequestSchema>;

export const CreateRepositoryResponseSchema = contract(zCreatedRepoBody);
export type CreateRepositoryResponse = z.infer<typeof CreateRepositoryResponseSchema>;

export const EscrowPackageSchema = contract(zGetRepositoryEscrowPackageResponse);
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
}) satisfies z.ZodType<ConfirmRepositoryEscrowRequest, unknown>;
export type ConfirmEscrowRequest = z.input<typeof ConfirmEscrowRequestSchema>;

export const WorkflowResponseSchema = contract(zWorkflowOutBody);
export type WorkflowResponse = z.infer<typeof WorkflowResponseSchema>;

// ---------------------------------------------------------------------------
// Backups: settings, recovery points, alerts
// ---------------------------------------------------------------------------

export const CONSISTENCY_MODES = ["live", "quiesced", "offline"] as const;
export const ConsistencyModeSchema = z.enum(CONSISTENCY_MODES);
export type ConsistencyMode = z.infer<typeof ConsistencyModeSchema>;

export const StartBackupRequestSchema = z.object({ consistency_mode: ConsistencyModeSchema.optional() }) satisfies z.ZodType<StartBackupRequestContract>;
export type StartBackupRequest = z.infer<typeof StartBackupRequestSchema>;

export const HOOK_MAX_TIMEOUT_SECONDS = 3600;

export const BackupSettingsSchema = contract(zBackupSettingsDto);
export type BackupSettings = z.infer<typeof BackupSettingsSchema>;
export type Hook = BackupSettings["pre_hooks"][number];

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
}) satisfies z.ZodType<BackupSettingsDtoWritable>;
export type UpdateBackupSettingsRequest = z.infer<typeof UpdateBackupSettingsRequestSchema>;

export const RECOVERY_POINT_STATES = ["pending", "committed", "failed", "missing", "deleting"] as const;
export type RecoveryPointState = (typeof RECOVERY_POINT_STATES)[number];

export const RecoveryPointSchema = contract(zRecoveryPointDto);
export type RecoveryPoint = z.infer<typeof RecoveryPointSchema>;

export const RecoveryPointListSchema = contract(zListRecoveryPointsResponse);

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
  file_name: z.string().optional(),
  /** Logical database dumps (Phase 8 produces them). */
  database: z
    .object({
      engine: z.string(),
      format: z.string(),
      service: z.string().optional(),
      container: z.string().optional(),
    })
    .optional(),
});
export type ManifestComponent = z.infer<typeof ManifestComponentSchema>;

const str = z.string().optional().default("");

/** The application's shape at capture time (internal/manifest: Topology; no secrets). */
export const ManifestTopologySchema = z.object({
  containers: list(
    z.object({
      id: str,
      name: z.string(),
      service: str,
      image: str,
      state: str,
      ports: list(
        z.object({ container_port: z.string(), protocol: str, host_ip: str, host_port: str }),
      ),
      mounts: list(
        z.object({
          type: z.string(),
          name: str,
          source: str,
          destination: z.string(),
          rw: z.boolean().optional().default(false),
        }),
      ),
      networks: list(z.string()),
    }),
  ),
  networks: list(
    z.object({
      name: z.string(),
      driver: str,
      external: z.boolean().optional().default(false),
      internal: z.boolean().optional().default(false),
    }),
  ),
  volumes: list(
    z.object({ name: z.string(), driver: str, external: z.boolean().optional().default(false) }),
  ),
});
export type ManifestTopology = z.infer<typeof ManifestTopologySchema>;

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
  images: list(z.object({ service: str, ref: z.string(), digest: str })),
  /** Absent in manifests written before Phase 5. */
  topology: ManifestTopologySchema.nullish().transform((v) => v ?? null),
  source: z
    .object({ hostname: str, os_release: str, architecture: str, runtime_version: str, agent_version: str })
    .partial()
    .optional(),
  producer: z.object({ component: z.string(), version: z.string() }).partial().optional(),
});
export type Manifest = z.infer<typeof ManifestSchema>;

export const ALERT_SEVERITIES = ["critical", "warning", "info"] as const;
export type AlertSeverity = (typeof ALERT_SEVERITIES)[number];

export const AlertSchema = contract(zAlertDto);
export type Alert = z.infer<typeof AlertSchema>;

export const AlertListSchema = contract(zListAlertsResponse);

// ---------------------------------------------------------------------------
// Host limits
// ---------------------------------------------------------------------------

export const MAX_CONCURRENT_JOBS = 16;

export const HostSettingsSchema = contract(zHostSettingsDto);
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
  }) satisfies z.ZodType<HostSettingsDto>;
export type UpdateHostSettingsRequest = z.infer<typeof UpdateHostSettingsRequestSchema>;

// ---------------------------------------------------------------------------
// Jobs (Phase 6): backups and restores in one list
// ---------------------------------------------------------------------------

export const JobListSchema = contract(zListJobsResponse);
export type Job = z.infer<typeof JobListSchema>["items"][number];
export type JobType = Job["type"];
export type JobState = Job["state"];
export const JOB_TYPES = ["backup", "restore"] as const satisfies readonly JobType[];
export const JOB_STATES = ["running", "succeeded", "partial", "failed", "rolled_back", "missing"] as const satisfies readonly JobState[];

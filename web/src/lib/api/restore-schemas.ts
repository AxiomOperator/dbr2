// SPDX-License-Identifier: Apache-2.0
//
// Zod schemas for the Phase 5 (Restores) API contract, derived from
// `api/openapi.yaml` (operations tagged `Restores`: Preview, RestoreBody,
// StartRestoreBody, RestoreRunDTO). As elsewhere, response objects are
// non-strict and nullable arrays are normalised to `[]`. The `result`
// document of a restore is untyped in the contract (`result: {}`); it is
// parsed leniently with {@link RestoreResultSchema}, which mirrors
// `workflows/restore.Result` (agent protobuf JSON: empty fields omitted).

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

export const PERMISSION_RESTORE_READ = "restore.read";
export const PERMISSION_RESTORE_EXECUTE = "restore.execute";
export const PERMISSION_RESTORE_PRODUCTION = "restore.production";

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

export const PathRemapSchema = z.object({ from: z.string(), to: z.string() });
export type PathRemap = z.infer<typeof PathRemapSchema>;

export const RestoreBodySchema = z.object({
  target_host_id: z.string().optional(),
  components: z.array(z.string()).optional(),
  path_remaps: z.array(PathRemapSchema).optional(),
});
export type RestoreBody = z.infer<typeof RestoreBodySchema>;

/** A production restore needs a reason of at least this many characters (server rule). */
export const MIN_PRODUCTION_REASON = 3;
export const MAX_REASON = 500;
export const MAX_CONFIRMATION = 200;

export const StartRestoreBodySchema = RestoreBodySchema.extend({
  reason: z.string().max(MAX_REASON, `Use at most ${MAX_REASON} characters.`).optional(),
  confirmation: z.string().max(MAX_CONFIRMATION).optional(),
});
export type StartRestoreBody = z.infer<typeof StartRestoreBodySchema>;

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

export const RESTORE_MODES = ["in_place", "alternate_host"] as const;
export type RestoreMode = (typeof RESTORE_MODES)[number];

export const PreviewComponentSchema = z.object({
  name: z.string(),
  kind: z.string(),
  // overwrite | create | restore_files | load_dump
  action: z.string(),
  target: z.string(),
  size_bytes: z.number(),
});
export type PreviewComponent = z.infer<typeof PreviewComponentSchema>;

export const PreviewContainerSchema = z.object({ id: z.string(), name: z.string(), state: z.string() });
export type PreviewContainer = z.infer<typeof PreviewContainerSchema>;

/** A network: exists | create | missing_external. */
export const PreviewItemSchema = z.object({ name: z.string(), action: z.string() });
export type PreviewItem = z.infer<typeof PreviewItemSchema>;

/** An image: present | pull (by digest when known). */
export const PreviewImageSchema = z.object({
  ref: z.string(),
  digest: z.string().optional().default(""),
  action: z.string(),
});
export type PreviewImage = z.infer<typeof PreviewImageSchema>;

export const CollisionSchema = z.object({
  // container_name | port | network | volume | bind_path | dependency
  kind: z.string(),
  name: z.string(),
  detail: z.string(),
});
export type Collision = z.infer<typeof CollisionSchema>;

export const PreviewSchema = z.object({
  recovery_point_id: z.string(),
  application_name: z.string(),
  source_host_id: z.string(),
  target_host_id: z.string(),
  target_hostname: z.string(),
  mode: z.string().optional().default("in_place"),
  target_application_id: z.string().optional().default(""),
  production: z.boolean().optional().default(false),
  production_reasons: list(z.string()),
  components: list(PreviewComponentSchema),
  stop_containers: list(PreviewContainerSchema),
  create_containers: list(z.string()),
  networks: list(PreviewItemSchema),
  images: list(PreviewImageSchema),
  ports: list(z.string()),
  collisions: list(CollisionSchema),
  warnings: list(z.string()),
  blocked: z.boolean().optional().default(false),
});
export type Preview = z.infer<typeof PreviewSchema>;

// ---------------------------------------------------------------------------
// Result document (workflows/restore.Result)
// ---------------------------------------------------------------------------

const str = z.string().optional().default("");
const num = z.number().optional().default(0);
const bool = z.boolean().optional().default(false);

export const ImageResultSchema = z.object({ ref: str, digest: str, status: str, error: str });
export type ImageResult = z.infer<typeof ImageResultSchema>;

export const ComponentResultSchema = z.object({
  name: str,
  // restored | failed | skipped
  status: str,
  error: str,
  target_path: str,
  bytes: num,
  files: num,
  metadata_applied: num,
  verify_mismatches: num,
  // Where the previous content was kept ("" = the target did not exist).
  previous_path: str,
  created_volume: bool,
});
export type ComponentResult = z.infer<typeof ComponentResultSchema>;

export const ContainerResultSchema = z.object({
  name: str,
  // created | existing | failed
  status: str,
  container_id: str,
  was_running: bool,
  error: str,
});
export type ContainerResult = z.infer<typeof ContainerResultSchema>;

export const ContainerHealthSchema = z.object({
  container_id: str,
  name: str,
  state: str,
  // healthy | unhealthy | starting | none
  health: str,
  ok: bool,
  exit_code: num,
  log_tail: str,
});
export type ContainerHealth = z.infer<typeof ContainerHealthSchema>;

export const RestoreResultSchema = z.object({
  images: list(ImageResultSchema),
  components: list(ComponentResultSchema),
  containers: list(ContainerResultSchema),
  databases: list(z.string()),
  health: z
    .object({ ok: bool, containers: list(ContainerHealthSchema) })
    .nullish()
    .transform((v) => v ?? null),
  rolled_back: bool,
  rollback_error: str,
});
export type RestoreResult = z.infer<typeof RestoreResultSchema>;

// ---------------------------------------------------------------------------
// Restore runs
// ---------------------------------------------------------------------------

export const RESTORE_STATES = ["requested", "running", "succeeded", "failed", "rolled_back"] as const;
export const RestoreStateSchema = z.enum(RESTORE_STATES);
export type RestoreState = z.infer<typeof RestoreStateSchema>;

/** True while the workflow may still change the run. */
export const isActiveRestore = (state: RestoreState) => state === "requested" || state === "running";

export const RestoreRunSchema = z.object({
  id: z.string(),
  recovery_point_id: z.string(),
  application_id: z.string(),
  application_name: z.string(),
  source_host_id: z.string(),
  target_host_id: z.string(),
  target_hostname: z.string(),
  target_application_id: z.string().nullable(),
  mode: z.string(),
  production: z.boolean(),
  components: list(z.string()),
  // Untyped in the contract (`path_remaps: {}`): keep only well-formed entries.
  path_remaps: z.unknown().transform((v): PathRemap[] =>
    Array.isArray(v) ? v.flatMap((r) => (PathRemapSchema.safeParse(r).success ? [r as PathRemap] : [])) : [],
  ),
  reason: z.string().nullable(),
  requested_by: z.string(),
  state: RestoreStateSchema,
  step: z.string().nullable(),
  error: z.string().nullable(),
  workflow_id: z.string().nullable(),
  created_at: z.string(),
  started_at: z.string().nullable(),
  finished_at: z.string().nullable(),
  /** Detail view only. */
  preview: z.unknown().optional(),
  /** Detail view only; parsed separately with {@link RestoreResultSchema}. */
  result: z.unknown().optional(),
});
export type RestoreRun = z.infer<typeof RestoreRunSchema>;

export const RestoreRunListSchema = z.object({ items: list(RestoreRunSchema) });

/** The preview recorded with a run, or null when absent / not understood. */
export function runPreview(run: RestoreRun): Preview | null {
  if (run.preview == null) return null;
  const p = PreviewSchema.safeParse(run.preview);
  return p.success ? p.data : null;
}

/** The result document of a run, or null when absent / not understood. */
export function runResult(run: RestoreRun): RestoreResult | null {
  if (run.result == null) return null;
  const r = RestoreResultSchema.safeParse(run.result);
  return r.success ? r.data : null;
}

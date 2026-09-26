// SPDX-License-Identifier: Apache-2.0
//
// Restores (Phase 5). Response schemas are the generated OpenAPI Zod schemas
// wrapped by `contract()` (nullable arrays normalised to `[]`). The `result`
// document and `path_remaps` of a restore are untyped in the contract
// (`result: {}`, `path_remaps: {}`); they are parsed leniently here:
// {@link RestoreResultSchema} mirrors `workflows/restore.Result` (agent
// protobuf JSON: empty fields omitted).

import { z } from "zod";
import { contract } from "./contract";
import type { RestoreBody as RestoreBodyContract, StartRestoreBody as StartRestoreBodyContract } from "./generated";
import { zListRestoresResponse, zPreview, zRestoreRunDto } from "./generated/zod.gen";

/** A nullable (or omitted) array, normalised to `[]` (result parsing only). */
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
}) satisfies z.ZodType<RestoreBodyContract>;
export type RestoreBody = z.infer<typeof RestoreBodySchema>;

/** A production restore needs a reason of at least this many characters (server rule). */
export const MIN_PRODUCTION_REASON = 3;
export const MAX_REASON = 500;
export const MAX_CONFIRMATION = 200;

export const StartRestoreBodySchema = RestoreBodySchema.extend({
  reason: z.string().max(MAX_REASON, `Use at most ${MAX_REASON} characters.`).optional(),
  confirmation: z.string().max(MAX_CONFIRMATION).optional(),
}) satisfies z.ZodType<StartRestoreBodyContract>;
export type StartRestoreBody = z.infer<typeof StartRestoreBodySchema>;

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

export const RESTORE_MODES = ["in_place", "alternate_host"] as const;
export type RestoreMode = (typeof RESTORE_MODES)[number];

export const PreviewSchema = contract(zPreview);
export type Preview = z.infer<typeof PreviewSchema>;
export type PreviewComponent = Preview["components"][number];
export type PreviewContainer = Preview["stop_containers"][number];
/** A network: exists | create | missing_external. */
export type PreviewItem = Preview["networks"][number];
/** An image: present | pull (by digest when known). */
export type PreviewImage = Preview["images"][number];
/** container_name | port | network | volume | bind_path | dependency */
export type Collision = Preview["collisions"][number];

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
export type RestoreState = (typeof RESTORE_STATES)[number];

/** True while the workflow may still change the run. */
export const isActiveRestore = (state: RestoreState) => state === "requested" || state === "running";

/** Untyped in the contract (`path_remaps: {}`): keep only well-formed entries. */
const PathRemapsSchema = z.unknown().transform((v): PathRemap[] =>
  Array.isArray(v) ? v.flatMap((r) => (PathRemapSchema.safeParse(r).success ? [r as PathRemap] : [])) : [],
);

export const RestoreRunSchema = contract(zRestoreRunDto.extend({ path_remaps: PathRemapsSchema }));
export type RestoreRun = z.infer<typeof RestoreRunSchema>;

export const RestoreRunListSchema = contract(
  zListRestoresResponse.extend({ items: z.array(zRestoreRunDto.extend({ path_remaps: PathRemapsSchema })).nullable() }),
);

/** The preview recorded with a run, or null when absent / not understood. */
export function runPreview(run: RestoreRun): Preview | null {
  return run.preview ?? null;
}

/** The result document of a run, or null when absent / not understood. */
export function runResult(run: RestoreRun): RestoreResult | null {
  if (run.result == null) return null;
  const r = RestoreResultSchema.safeParse(run.result);
  return r.success ? r.data : null;
}

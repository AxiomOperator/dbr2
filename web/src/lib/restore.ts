// SPDX-License-Identifier: Apache-2.0
//
// Pure helpers for the restore pages: which manifest components can be
// selected, bind-mount path remaps, the Start-button safeguards of the
// wizard and the workflow step indicator.

import type { ManifestComponent } from "@/lib/api/protection-schemas";
import {
  MIN_PRODUCTION_REASON,
  type PathRemap,
  type Preview,
  type RestoreRun,
} from "@/lib/api/restore-schemas";

// ---------------------------------------------------------------------------
// Components
// ---------------------------------------------------------------------------

export interface SelectableComponent {
  component: ManifestComponent;
  /** False when the component was not captured (failed / skipped). */
  restorable: boolean;
  /** fsmeta record restored automatically with this component, if any. */
  fsmeta: ManifestComponent | null;
}

/**
 * The components the wizard offers, in manifest order, mirroring the
 * server's selectComponents: fsmeta and image components are never
 * selectable on their own (fsmeta follows its parent automatically), and
 * only succeeded components can be restored.
 */
export function selectableComponents(components: ManifestComponent[]): SelectableComponent[] {
  return components
    .filter((c) => c.kind !== "fsmeta" && c.kind !== "image")
    .map((c) => ({
      component: c,
      restorable: c.status === "succeeded",
      fsmeta:
        components.find((f) => f.kind === "fsmeta" && f.parent === c.name && f.status === "succeeded") ?? null,
    }));
}

/** Default selection: every restorable component. */
export function defaultSelection(components: ManifestComponent[]): string[] {
  return selectableComponents(components)
    .filter((s) => s.restorable)
    .map((s) => s.component.name);
}

// ---------------------------------------------------------------------------
// Path remaps
// ---------------------------------------------------------------------------

export interface RemapRow {
  from: string;
  to: string;
}

const under = (p: string, prefix: string) => p === prefix || p.startsWith(`${prefix.replace(/\/+$/, "")}/`);

/**
 * Suggested remaps for an alternate-host restore: every distinct top-level
 * path (paths below an earlier one are covered by its remap, since the server
 * applies the first matching prefix) moves to `<path>-restored`.
 */
export function suggestRemaps(paths: string[]): RemapRow[] {
  const clean = [...new Set(paths.map((p) => p.trim().replace(/\/+$/, "")).filter((p) => p.startsWith("/") && p !== ""))];
  clean.sort((a, b) => a.length - b.length || a.localeCompare(b));
  const out: string[] = [];
  for (const p of clean) if (!out.some((q) => under(p, q))) out.push(p);
  return out.sort((a, b) => a.localeCompare(b)).map((p) => ({ from: p, to: `${p}-restored` }));
}

/** Host paths a restore of these components writes (bind mounts, config working directory). */
export function remappablePaths(components: ManifestComponent[], selected: string[], workingDir?: string): string[] {
  const paths: string[] = [];
  for (const c of components) {
    if (!selected.includes(c.name)) continue;
    if (c.kind === "bind_mount" && c.path) paths.push(c.path);
    if (c.kind === "config" && workingDir) paths.push(workingDir);
  }
  return paths;
}

export type RemapValidation =
  | { ok: true; remaps: PathRemap[] }
  | { ok: false; errors: Record<number, string> };

/**
 * Validates the remap rows like the server does: both paths absolute, no
 * `..` in the target. Rows with both fields empty are ignored.
 */
export function validateRemaps(rows: RemapRow[]): RemapValidation {
  const errors: Record<number, string> = {};
  const remaps: PathRemap[] = [];
  const seen = new Set<string>();
  rows.forEach((r, i) => {
    const from = r.from.trim();
    const to = r.to.trim();
    if (from === "" && to === "") return;
    if (!from.startsWith("/") || !to.startsWith("/")) {
      errors[i] = "Both paths must be absolute (start with /).";
    } else if (to.split("/").includes("..") || from.split("/").includes("..")) {
      errors[i] = "Paths must not contain “..”.";
    } else if (seen.has(from.replace(/\/+$/, ""))) {
      errors[i] = "This source path is already remapped above.";
    } else {
      seen.add(from.replace(/\/+$/, ""));
      remaps.push({ from, to });
    }
  });
  return Object.keys(errors).length > 0 ? { ok: false, errors } : { ok: true, remaps };
}

// ---------------------------------------------------------------------------
// Start safeguards (ADR-0014)
// ---------------------------------------------------------------------------

export interface StartCheck {
  canStart: boolean;
  /** Why Start is disabled, most important first (empty when it is enabled). */
  blockers: string[];
  reasonRequired: boolean;
  confirmationRequired: boolean;
  confirmationMatches: boolean;
}

/**
 * Mirrors the server's checks before a restore starts: blocked previews
 * cannot start; production restores need `restore.production`, the
 * application name typed exactly and a reason of at least three characters.
 */
export function checkStart(input: {
  preview: Preview;
  reason: string;
  confirmation: string;
  canProduction: boolean;
}): StartCheck {
  const { preview, reason, confirmation, canProduction } = input;
  const blockers: string[] = [];
  if (preview.blocked) {
    blockers.push(
      preview.collisions.length > 0
        ? `The restore is blocked by ${preview.collisions.length} collision${preview.collisions.length === 1 ? "" : "s"}.`
        : "The restore is blocked (see the preview).",
    );
  }
  const production = preview.production;
  const confirmationMatches = confirmation.trim() === preview.application_name;
  if (production) {
    if (!canProduction) blockers.push("This is a production restore, which requires the restore.production permission.");
    if (reason.trim().length < MIN_PRODUCTION_REASON) {
      blockers.push(`Enter a reason (at least ${MIN_PRODUCTION_REASON} characters) for this production restore.`);
    }
    if (!confirmationMatches) blockers.push(`Type ${preview.application_name} exactly to confirm.`);
  }
  return {
    canStart: blockers.length === 0,
    blockers,
    reasonRequired: production,
    confirmationRequired: production,
    confirmationMatches,
  };
}

// ---------------------------------------------------------------------------
// Workflow steps
// ---------------------------------------------------------------------------

export const RESTORE_STEPS = [
  "grant-access",
  "agent-access",
  "images",
  "stop-application",
  "restore-data",
  "recreate-containers",
  "start-application",
  "restore-database",
  "health-check",
  "commit",
] as const;
export type RestoreStep = (typeof RESTORE_STEPS)[number];

export const STEP_LABEL: Record<RestoreStep, string> = {
  "grant-access": "Grant access",
  "agent-access": "Agent access",
  images: "Images",
  "stop-application": "Stop application",
  "restore-data": "Restore data",
  "recreate-containers": "Re-create containers",
  "start-application": "Start application",
  "restore-database": "Restore database",
  "health-check": "Health check",
  commit: "Commit",
};

/** "restore-data" → "Restore data"; unknown steps are shown as sent. */
export const stepLabel = (step: string | null | undefined): string =>
  step ? (STEP_LABEL[step as RestoreStep] ?? step) : "";

const RUNNING_STATES = new Set(["running", "paused", "restarting"]);

/**
 * Whether the workflow runs `step` for this restore, derived from the preview
 * recorded at request time the same way workflows/restore decides (cross-host
 * access grant, images, stopping running containers, re-creating containers,
 * database dumps, a health check when anything was started). Without a
 * preview every step is assumed.
 */
export function stepApplies(step: RestoreStep, run: Pick<RestoreRun, "source_host_id" | "target_host_id">, preview: Preview | null): boolean {
  switch (step) {
    case "grant-access":
      return run.target_host_id !== run.source_host_id;
    case "images":
      return preview ? preview.images.length > 0 : true;
    case "stop-application":
      return preview ? preview.stop_containers.some((c) => RUNNING_STATES.has(c.state)) : true;
    case "recreate-containers":
      return preview ? preview.create_containers.length > 0 : true;
    case "restore-database":
      return preview ? preview.components.some((c) => c.action === "load_dump") : true;
    case "health-check":
      return preview
        ? preview.stop_containers.some((c) => RUNNING_STATES.has(c.state)) || preview.create_containers.length > 0
        : true;
    default:
      return true;
  }
}

export type StepStatus = "done" | "current" | "pending" | "failed";

export interface StepView {
  step: RestoreStep;
  label: string;
  status: StepStatus;
}

/**
 * The step indicator of a restore: steps that do not apply are left out;
 * before the current step everything is done. A finished run is all done
 * when it succeeded; a failed or rolled-back run marks the step it stopped at
 * (when the server still reports it) as failed.
 */
export function restoreSteps(
  run: Pick<RestoreRun, "state" | "step" | "source_host_id" | "target_host_id">,
  preview: Preview | null,
): StepView[] {
  const current = (RESTORE_STEPS as readonly string[]).includes(run.step ?? "") ? (run.step as RestoreStep) : null;
  const steps = RESTORE_STEPS.filter((s) => s === current || stepApplies(s, run, preview));
  const at = current ? steps.indexOf(current) : -1;
  return steps.map((step, i) => {
    let status: StepStatus = "pending";
    if (run.state === "succeeded") status = "done";
    else if (run.state === "running" && at >= 0) status = i < at ? "done" : i === at ? "current" : "pending";
    else if ((run.state === "failed" || run.state === "rolled_back") && at >= 0) {
      status = i < at ? "done" : i === at ? "failed" : "pending";
    }
    return { step, label: STEP_LABEL[step], status };
  });
}

// ---------------------------------------------------------------------------
// Display
// ---------------------------------------------------------------------------

/** "1 h 4 min", "3 min 12 s", "8 s"; null without a start. */
export function formatDuration(
  startIso: string | null | undefined,
  endIso: string | null | undefined,
  now: Date = new Date(),
): string | null {
  if (!startIso) return null;
  const start = Date.parse(startIso);
  const end = endIso ? Date.parse(endIso) : now.getTime();
  if (Number.isNaN(start) || Number.isNaN(end)) return null;
  const s = Math.max(0, Math.round((end - start) / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ${s % 60} s`;
  const h = Math.floor(m / 60);
  return `${h} h ${m % 60} min`;
}

export const COMPONENT_ACTION_LABEL: Record<string, string> = {
  overwrite: "Overwrite",
  create: "Create",
  restore_files: "Restore files",
  load_dump: "Load dump",
};

export const MODE_LABEL: Record<string, string> = {
  in_place: "In place",
  alternate_host: "Alternate host",
};

export const restoreModeLabel = (mode: string): string => MODE_LABEL[mode] ?? mode;

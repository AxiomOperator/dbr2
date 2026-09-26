// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  CircleCheckIcon,
  CircleDashedIcon,
  CircleXIcon,
  ClockIcon,
  LoaderCircleIcon,
  ServerIcon,
  ShieldAlertIcon,
  Undo2Icon,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { RestoreState } from "@/lib/api/restore-schemas";
import { COMPONENT_ACTION_LABEL, restoreModeLabel, stepLabel } from "@/lib/restore";

const AMBER = "border-amber-500/60 text-amber-700 dark:text-amber-400";
const BLUE = "border-sky-500/60 text-sky-700 dark:text-sky-400";
const GREEN = "text-emerald-700 dark:text-emerald-400";

export const RESTORE_STATE_LABEL: Record<RestoreState, string> = {
  requested: "Requested",
  running: "Running",
  succeeded: "Succeeded",
  failed: "Failed",
  rolled_back: "Rolled back",
};

/**
 * Restore state: running is blue (with the current step), succeeded green,
 * rolled back amber, failed red.
 */
export function RestoreStateBadge({ state, step }: { state: RestoreState; step?: string | null }) {
  switch (state) {
    case "requested":
      return (
        <Badge variant="outline" data-state={state}>
          <ClockIcon aria-hidden="true" /> {RESTORE_STATE_LABEL.requested}
        </Badge>
      );
    case "running":
      return (
        <Badge variant="outline" className={BLUE} data-state={state}>
          <LoaderCircleIcon aria-hidden="true" className="motion-safe:animate-spin" /> {RESTORE_STATE_LABEL.running}
          {step ? `: ${stepLabel(step)}` : ""}
        </Badge>
      );
    case "succeeded":
      return (
        <Badge variant="secondary" className={GREEN} data-state={state}>
          <CircleCheckIcon aria-hidden="true" /> {RESTORE_STATE_LABEL.succeeded}
        </Badge>
      );
    case "rolled_back":
      return (
        <Badge variant="outline" className={AMBER} data-state={state}>
          <Undo2Icon aria-hidden="true" /> {RESTORE_STATE_LABEL.rolled_back}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" data-state={state}>
          <CircleXIcon aria-hidden="true" /> {RESTORE_STATE_LABEL.failed}
        </Badge>
      );
  }
}

/** "In place" / "Alternate host". */
export function RestoreModeBadge({ mode }: { mode: string }) {
  return (
    <Badge variant="outline" data-mode={mode}>
      {mode === "alternate_host" && <ServerIcon aria-hidden="true" />}
      {restoreModeLabel(mode)}
    </Badge>
  );
}

/** Red "Production" marker. */
export function ProductionBadge() {
  return (
    <Badge variant="destructive">
      <ShieldAlertIcon aria-hidden="true" /> Production
    </Badge>
  );
}

/** What a restore does to one component: overwrite (amber), create, restore files, load dump. */
export function ComponentActionBadge({ action }: { action: string }) {
  const label = COMPONENT_ACTION_LABEL[action] ?? action;
  if (action === "overwrite") {
    return (
      <Badge variant="outline" className={AMBER}>
        {label}
      </Badge>
    );
  }
  if (action === "create") {
    return (
      <Badge variant="secondary" className={GREEN}>
        {label}
      </Badge>
    );
  }
  return <Badge variant="outline">{label}</Badge>;
}

/** Outcome of one item in a restore result (restored / created / pulled / present / failed / skipped …). */
export function OutcomeBadge({ status }: { status: string }) {
  if (status === "failed") {
    return (
      <Badge variant="destructive">
        <CircleXIcon aria-hidden="true" /> Failed
      </Badge>
    );
  }
  if (["restored", "created", "pulled", "present", "existing"].includes(status)) {
    return (
      <Badge variant="secondary" className={GREEN}>
        <CircleCheckIcon aria-hidden="true" /> {status.charAt(0).toUpperCase() + status.slice(1)}
      </Badge>
    );
  }
  if (!status) return <span className="text-muted-foreground">—</span>;
  return (
    <Badge variant="outline">
      <CircleDashedIcon aria-hidden="true" /> {status.charAt(0).toUpperCase() + status.slice(1)}
    </Badge>
  );
}

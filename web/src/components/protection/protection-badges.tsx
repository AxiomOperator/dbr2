// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  ArchiveIcon,
  CircleXIcon,
  HistoryIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { Protection, ProtectionStatus } from "@/lib/api/fleet-schemas";
import { cn } from "@/lib/utils";

const AMBER = "border-amber-500/60 text-amber-700 dark:text-amber-400";
const BLUE = "border-sky-500/60 text-sky-700 dark:text-sky-400";

export const PROTECTION_STATUS_LABEL: Record<ProtectionStatus, string> = {
  protected: "Protected",
  at_risk: "At risk",
  failed: "Failed",
  unprotected: "Unprotected",
};

export const PROTECTION_STATUS_DESCRIPTION: Record<ProtectionStatus, string> = {
  protected: "The latest recovery point is complete and contains every component.",
  at_risk: "The latest recovery point is Partial or misses components the application has now.",
  failed: "The last backup attempt failed.",
  unprotected: "No recovery point exists yet.",
};

/** Protection status (icon + text, never color alone). */
export function ProtectionStatusBadge({ status }: { status: ProtectionStatus }) {
  const label = PROTECTION_STATUS_LABEL[status];
  switch (status) {
    case "protected":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400" data-status={status}>
          <ShieldCheckIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "at_risk":
      return (
        <Badge variant="outline" className={AMBER} data-status={status}>
          <ShieldAlertIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" data-status={status}>
          <CircleXIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "unprotected":
      return (
        <Badge variant="outline" className="text-muted-foreground" data-status={status}>
          <ShieldOffIcon aria-hidden="true" /> {label}
        </Badge>
      );
  }
}

/** "3/4 components": how many of the application's components the latest recovery point contains. */
export function CoverageText({ protection, className }: { protection: Protection; className?: string }) {
  const { components_protected: done, components_total: total } = protection;
  if (total === 0) return <span className={cn("text-xs text-muted-foreground", className)}>No components</span>;
  const complete = done >= total;
  return (
    <span
      className={cn(
        "text-xs whitespace-nowrap tabular-nums",
        complete ? "text-muted-foreground" : "text-amber-700 dark:text-amber-400",
        className,
      )}
      title={`${done} of ${total} components are in the latest recovery point`}
    >
      <span className="font-medium">
        {done}/{total}
      </span>{" "}
      components
    </span>
  );
}

/** A backup or restore running for the application right now. */
export function RunningBadge({ running }: { running: string | undefined }) {
  if (running !== "backup" && running !== "restore") return null;
  return (
    <Badge variant="outline" className={BLUE} data-running={running}>
      {running === "backup" ? (
        <ArchiveIcon aria-hidden="true" className="motion-safe:animate-pulse" />
      ) : (
        <HistoryIcon aria-hidden="true" className="motion-safe:animate-pulse" />
      )}
      {running === "backup" ? "Backing up" : "Restoring"}
    </Badge>
  );
}

/** Status, coverage and running indicator in one cell (applications list). */
export function ProtectionCell({ protection }: { protection: Protection | undefined }) {
  if (!protection) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="flex min-w-28 flex-col items-start gap-1">
      <ProtectionStatusBadge status={protection.status} />
      <CoverageText protection={protection} />
      <RunningBadge running={protection.running} />
    </div>
  );
}

// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  ArchiveXIcon,
  CalendarClockIcon,
  CircleAlertIcon,
  CircleCheckIcon,
  CircleDashedIcon,
  CircleXIcon,
  InfoIcon,
  SearchXIcon,
  ShieldCheckIcon,
  ShieldQuestionIcon,
  ShieldXIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { AlertSeverity, RecoveryPointState, VerificationState } from "@/lib/api/protection-schemas";
import { formatDate, formatDateTime } from "@/lib/format";

const AMBER = "border-amber-500/60 text-amber-700 dark:text-amber-400";
const cap = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

export const STATE_LABEL: Record<RecoveryPointState, string> = {
  pending: "In progress",
  committed: "Committed",
  failed: "Failed",
  missing: "Missing",
  deleting: "Deleting",
  deleted: "Deleted",
};

/** Recovery point lifecycle state (icon + text). */
export function RecoveryPointStateBadge({ state }: { state: RecoveryPointState }) {
  switch (state) {
    case "committed":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> {STATE_LABEL.committed}
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="outline">
          <CircleDashedIcon aria-hidden="true" className="motion-safe:animate-spin" /> {STATE_LABEL.pending}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> {STATE_LABEL.failed}
        </Badge>
      );
    case "missing":
      return (
        <Badge variant="outline" className={AMBER}>
          <SearchXIcon aria-hidden="true" /> {STATE_LABEL.missing}
        </Badge>
      );
    case "deleting":
      return (
        <Badge variant="outline">
          <Trash2Icon aria-hidden="true" /> {STATE_LABEL.deleting}
        </Badge>
      );
    case "deleted":
      return (
        <Badge variant="outline" className="text-muted-foreground">
          <ArchiveXIcon aria-hidden="true" /> {STATE_LABEL.deleted}
        </Badge>
      );
  }
}

/** "Deletion scheduled" marker for a recovery point in its grace period. */
export function ScheduledDeletionBadge({ deleteAfter }: { deleteAfter: string | null }) {
  if (!deleteAfter) return null;
  return (
    <Badge variant="outline" className={AMBER} title={`Deleted after ${formatDateTime(deleteAfter)}`}>
      <CalendarClockIcon aria-hidden="true" /> Deletion {formatDate(deleteAfter)}
    </Badge>
  );
}

export const VERIFICATION_LABEL: Record<VerificationState, string> = {
  unverified: "Not verified",
  verified: "Verified",
  verification_failed: "Verification failed",
};

/** Verification state of a recovery point (Phase 9), icon + text. */
export function VerificationBadge({ state, verifiedAt }: { state: VerificationState; verifiedAt?: string | null }) {
  const title = verifiedAt ? `Checked ${formatDateTime(verifiedAt)}` : undefined;
  switch (state) {
    case "verified":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400" title={title}>
          <ShieldCheckIcon aria-hidden="true" /> {VERIFICATION_LABEL.verified}
        </Badge>
      );
    case "verification_failed":
      return (
        <Badge variant="destructive" title={title}>
          <ShieldXIcon aria-hidden="true" /> {VERIFICATION_LABEL.verification_failed}
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="text-muted-foreground">
          <ShieldQuestionIcon aria-hidden="true" /> {VERIFICATION_LABEL.unverified}
        </Badge>
      );
  }
}

/** Complete / Partial (null until the recovery point is committed). */
export function RecoveryPointStatusBadge({ status }: { status: string | null }) {
  if (!status) return <span className="text-muted-foreground">—</span>;
  if (status === "partial") {
    return (
      <Badge variant="outline" className={AMBER}>
        <TriangleAlertIcon aria-hidden="true" /> Partial
      </Badge>
    );
  }
  if (status === "complete") return <Badge variant="secondary">Complete</Badge>;
  return <Badge variant="outline">{cap(status)}</Badge>;
}

export const MODE_LABEL: Record<string, string> = {
  live: "Live",
  quiesced: "Quiesced",
  offline: "Offline",
};

export const modeLabel = (mode: string | null | undefined): string =>
  mode ? (MODE_LABEL[mode] ?? cap(mode)) : "Automatic";

/** Consistency mode plus a "crash-consistent" marker for live captures. */
export function ModeCell({ mode, crashConsistent }: { mode: string; crashConsistent: boolean }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <span>{modeLabel(mode)}</span>
      {crashConsistent && (
        <Badge variant="outline" className={AMBER} title="Captured while running: crash-consistent only">
          Crash-consistent
        </Badge>
      )}
    </span>
  );
}

/** Component outcome in a manifest (succeeded / failed / skipped). */
export function ComponentStatusBadge({ status }: { status: string }) {
  if (status === "succeeded") {
    return (
      <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
        <CircleCheckIcon aria-hidden="true" /> Succeeded
      </Badge>
    );
  }
  if (status === "failed") {
    return (
      <Badge variant="destructive">
        <CircleXIcon aria-hidden="true" /> Failed
      </Badge>
    );
  }
  return <Badge variant="outline">{cap(status)}</Badge>;
}

export const SEVERITY_LABEL: Record<AlertSeverity, string> = {
  critical: "Critical",
  warning: "Warning",
  info: "Info",
};

/** Alert severity: critical is red. */
export function SeverityBadge({ severity }: { severity: AlertSeverity }) {
  switch (severity) {
    case "critical":
      return (
        <Badge variant="destructive">
          <CircleAlertIcon aria-hidden="true" /> {SEVERITY_LABEL.critical}
        </Badge>
      );
    case "warning":
      return (
        <Badge variant="outline" className={AMBER}>
          <TriangleAlertIcon aria-hidden="true" /> {SEVERITY_LABEL.warning}
        </Badge>
      );
    case "info":
      return (
        <Badge variant="outline">
          <InfoIcon aria-hidden="true" /> {SEVERITY_LABEL.info}
        </Badge>
      );
  }
}

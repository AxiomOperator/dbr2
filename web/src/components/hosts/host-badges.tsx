// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  BanIcon,
  CircleCheckIcon,
  ClockIcon,
  PauseCircleIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { AgentStatus } from "@/lib/api/fleet-schemas";
import { daysUntil, formatDate, formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

const STATUS_LABEL: Record<AgentStatus, string> = {
  pending: "Pending approval",
  active: "Active",
  suspended: "Suspended",
  revoked: "Revoked",
};

/** Host status with an icon and text (never color alone). */
export function AgentStatusBadge({ status }: { status: AgentStatus }) {
  switch (status) {
    case "active":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> {STATUS_LABEL.active}
        </Badge>
      );
    case "pending":
      return (
        <Badge
          variant="outline"
          className="border-amber-500/60 text-amber-700 dark:text-amber-400"
        >
          <ClockIcon aria-hidden="true" /> {STATUS_LABEL.pending}
        </Badge>
      );
    case "suspended":
      return (
        <Badge variant="outline">
          <PauseCircleIcon aria-hidden="true" /> {STATUS_LABEL.suspended}
        </Badge>
      );
    case "revoked":
      return (
        <Badge variant="destructive">
          <BanIcon aria-hidden="true" /> {STATUS_LABEL.revoked}
        </Badge>
      );
  }
}

/** Live gateway session indicator: a dot plus a text label. */
export function ConnectedIndicator({ connected }: { connected: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-sm">
      <span
        aria-hidden="true"
        className={cn(
          "inline-block size-2 rounded-full",
          connected ? "bg-emerald-500 motion-safe:animate-pulse" : "bg-muted-foreground/40",
        )}
      />
      <span className={connected ? undefined : "text-muted-foreground"}>
        {connected ? "Connected" : "Offline"}
      </span>
    </span>
  );
}

export function OutdatedBadge() {
  return (
    <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
      <TriangleAlertIcon aria-hidden="true" /> Outdated
    </Badge>
  );
}

/** Docker reachability: yes / no / unknown, plus the engine version. */
export function DockerState({
  reachable,
  version,
}: {
  reachable: boolean | null;
  version: string | null;
}) {
  if (reachable === null) return <span className="text-muted-foreground">Unknown</span>;
  if (!reachable) {
    return (
      <Badge variant="destructive">
        <TriangleAlertIcon aria-hidden="true" /> Unreachable
      </Badge>
    );
  }
  return (
    <span className="whitespace-nowrap">
      Reachable{version ? <span className="text-muted-foreground"> · {version}</span> : null}
    </span>
  );
}

/** Certificate expiry with a warning within 14 days. */
export function CertExpiry({ iso }: { iso: string | null }) {
  const days = daysUntil(iso);
  if (days === null) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5 whitespace-nowrap">
      <time dateTime={iso ?? undefined} title={formatDateTime(iso)}>
        {formatDate(iso)}
      </time>
      {days < 0 ? (
        <Badge variant="destructive">Expired</Badge>
      ) : days <= 14 ? (
        <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
          {days === 0 ? "Expires today" : `${days} d left`}
        </Badge>
      ) : null}
    </span>
  );
}

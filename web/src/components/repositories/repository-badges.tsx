// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  ArchiveIcon,
  CircleCheckIcon,
  CircleXIcon,
  KeyRoundIcon,
  StarIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { CopyButton } from "@/components/common/copy-button";
import { Badge } from "@/components/ui/badge";
import type { Repository, RepositoryStatus } from "@/lib/api/protection-schemas";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

export const REPOSITORY_STATUS_LABEL: Record<RepositoryStatus, string> = {
  awaiting_escrow: "Escrow not confirmed",
  ready: "Ready",
  unavailable: "Unavailable",
  retired: "Retired",
};

const AMBER = "border-amber-500/60 text-amber-700 dark:text-amber-400";

/** Repository status with an icon and text (never color alone). */
export function RepositoryStatusBadge({ status }: { status: RepositoryStatus }) {
  switch (status) {
    case "ready":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> {REPOSITORY_STATUS_LABEL.ready}
        </Badge>
      );
    case "awaiting_escrow":
      return (
        <Badge variant="outline" className={AMBER}>
          <KeyRoundIcon aria-hidden="true" /> {REPOSITORY_STATUS_LABEL.awaiting_escrow}
        </Badge>
      );
    case "unavailable":
      return (
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> {REPOSITORY_STATUS_LABEL.unavailable}
        </Badge>
      );
    case "retired":
      return (
        <Badge variant="outline">
          <ArchiveIcon aria-hidden="true" /> {REPOSITORY_STATUS_LABEL.retired}
        </Badge>
      );
  }
}

export function DefaultBadge() {
  return (
    <Badge variant="outline">
      <StarIcon aria-hidden="true" /> Default
    </Badge>
  );
}

export const BACKEND_LABEL: Record<Repository["backend"], string> = {
  nfs: "NFS",
  filesystem: "Filesystem",
};

/** Used share of the storage, 0–100 (null when the total is unknown). */
export function usedPercent(used: number, total: number): number | null {
  if (!Number.isFinite(total) || total <= 0) return null;
  return Math.min(100, Math.max(0, Math.round((used / total) * 100)));
}

/** Capacity bar: used / total with the percentage in text. Amber ≥ 80 %, red ≥ 90 %. */
export function CapacityBar({ used, total }: { used: number; total: number }) {
  const pct = usedPercent(used, total);
  if (pct === null) return <span className="text-muted-foreground">Capacity unknown</span>;
  return (
    <div className="w-40 space-y-1">
      <div
        role="progressbar"
        aria-label="Storage used"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        aria-valuetext={`${pct}% used (${formatBytes(used)} of ${formatBytes(total)})`}
        className="h-2 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className={cn(
            "h-full rounded-full",
            pct >= 90 ? "bg-destructive" : pct >= 80 ? "bg-amber-500" : "bg-emerald-500",
          )}
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="text-xs whitespace-nowrap text-muted-foreground">
        {formatBytes(used)} of {formatBytes(total)} · {pct}%
      </div>
    </div>
  );
}

/** Live reposerver storage health plus capacity, or the reason it is unknown. */
export function StorageHealth({ repo }: { repo: Repository }) {
  const live = repo.live;
  if (!live) {
    return (
      <div className="space-y-1">
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> Unreachable
        </Badge>
        {repo.live_error && <div className="max-w-56 text-xs break-words text-muted-foreground">{repo.live_error}</div>}
      </div>
    );
  }
  return (
    <div className="space-y-1.5">
      {live.storage_healthy ? (
        <span className="inline-flex items-center gap-1 text-sm text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" className="size-3.5" /> Healthy
        </span>
      ) : (
        <Badge variant="outline" className={AMBER}>
          <TriangleAlertIcon aria-hidden="true" /> Storage unhealthy
        </Badge>
      )}
      {live.storage_error && <div className="max-w-56 text-xs break-words text-destructive">{live.storage_error}</div>}
      <CapacityBar used={live.storage_used_bytes} total={live.storage_total_bytes} />
    </div>
  );
}

/** "ab12cd34…ef56" for a long hex fingerprint. */
export function truncateFingerprint(fp: string, head = 8, tail = 4): string {
  return fp.length <= head + tail + 1 ? fp : `${fp.slice(0, head)}…${fp.slice(-tail)}`;
}

/** Certificate fingerprint, truncated, with the full value in a title and a copy button. */
export function Fingerprint({ value }: { value: string }) {
  if (!value) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <code className="font-mono text-xs" title={value}>
        {truncateFingerprint(value)}
      </code>
      <CopyButton value={value} label="certificate fingerprint" />
    </span>
  );
}

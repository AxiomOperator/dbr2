// SPDX-License-Identifier: Apache-2.0
"use client";

import { LoaderCircleIcon } from "lucide-react";
import { useJobProgress, useLiveStatus } from "@/components/live/live-events";
import { formatBytes } from "@/lib/format";
import { isActiveProgress, jobKindLabel, type JobProgress } from "@/lib/live/events";
import { cn } from "@/lib/utils";

/** "Component 2 of 5" (1-based, capped at the total). */
export function componentPosition(p: Pick<JobProgress, "done" | "total">): string | null {
  if (p.total === undefined || p.total <= 0) return null;
  const n = Math.min((p.done ?? 0) + 1, p.total);
  return `Component ${n} of ${p.total}`;
}

/** Fraction of components finished, 0–1 (null when unknown). */
export function componentFraction(p: Pick<JobProgress, "done" | "total">): number | null {
  if (p.total === undefined || p.total <= 0) return null;
  return Math.min(1, Math.max(0, (p.done ?? 0) / p.total));
}

/** The live progress of one command (pure rendering; tested directly). */
export function JobProgressView({ progress, className }: { progress: JobProgress; className?: string }) {
  const position = componentPosition(progress);
  const fraction = componentFraction(progress);
  const finished = !isActiveProgress(progress);
  return (
    <div className={cn("space-y-2 text-sm", className)} data-testid="job-progress">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1.5 font-medium">
          {!finished && <LoaderCircleIcon aria-hidden="true" className="size-4 motion-safe:animate-spin" />}
          {jobKindLabel(progress.kind)}
          {progress.queued && !finished ? ": queued on the agent" : ""}
          {finished ? (progress.state === "succeeded" ? ": finished" : `: ${progress.state}`) : ""}
        </span>
        {position && <span className="text-xs text-muted-foreground tabular-nums">{position}</span>}
      </div>
      {fraction !== null && (
        <div
          role="progressbar"
          aria-label="Components finished"
          aria-valuemin={0}
          aria-valuemax={progress.total}
          aria-valuenow={Math.min(progress.done ?? 0, progress.total ?? 0)}
          className="h-2 w-full overflow-hidden rounded-full bg-muted"
        >
          <div
            className={cn("h-full rounded-full", finished && progress.state !== "succeeded" ? "bg-destructive" : "bg-sky-500")}
            style={{ width: `${Math.round((finished && progress.state === "succeeded" ? 1 : fraction) * 100)}%` }}
          />
        </div>
      )}
      {progress.component && !finished && (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
          <div className="col-span-2 min-w-0 sm:col-span-4">
            <dt className="sr-only">Component</dt>
            <dd className="truncate font-mono" title={progress.component}>
              {progress.component}
            </dd>
          </div>
          {progress.hashedBytes !== undefined && (
            <div>
              <dt className="text-muted-foreground">Hashed</dt>
              <dd className="tabular-nums">{formatBytes(progress.hashedBytes)}</dd>
            </div>
          )}
          {progress.uploadedBytes !== undefined && (
            <div>
              <dt className="text-muted-foreground">Uploaded</dt>
              <dd className="tabular-nums">{formatBytes(progress.uploadedBytes)}</dd>
            </div>
          )}
          {progress.files !== undefined && (
            <div>
              <dt className="text-muted-foreground">Files</dt>
              <dd className="tabular-nums">{progress.files.toLocaleString()}</dd>
            </div>
          )}
        </dl>
      )}
      {progress.error && <p className="text-xs text-destructive">{progress.error}</p>}
    </div>
  );
}

/**
 * Live progress of an application's running backup / restore from
 * `job.progress` events. `running` (from `protection.running`) shows a
 * waiting state before the first event arrives.
 */
export function ApplicationJobProgress({
  applicationId,
  running,
  className,
}: {
  applicationId: string;
  running?: string;
  className?: string;
}) {
  const progress = useJobProgress(applicationId);
  const live = useLiveStatus();
  if (progress) return <JobProgressView progress={progress} className={className} />;
  if (running !== "backup" && running !== "restore") return null;
  return (
    <p className={cn("inline-flex items-center gap-1.5 text-sm text-muted-foreground", className)} data-testid="job-progress-waiting">
      <LoaderCircleIcon aria-hidden="true" className="size-4 motion-safe:animate-spin" />
      {running === "backup" ? "Backup running" : "Restore running"}
      {live === "live" ? ": waiting for progress from the agent…" : " (live progress unavailable)"}
    </p>
  );
}

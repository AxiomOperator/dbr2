// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import {
  ArchiveIcon,
  ArchiveRestoreIcon,
  CircleCheckIcon,
  CircleXIcon,
  LoaderCircleIcon,
  SearchXIcon,
  TriangleAlertIcon,
  Undo2Icon,
} from "lucide-react";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel } from "@/components/backups/backup-badges";
import { DataTable } from "@/components/common/data-table";
import { ALL, distinctOptions, FilterSelect, useUrlFilters } from "@/components/common/filters";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { useJobProgress, useLiveStatus } from "@/components/live/live-events";
import { JobProgressView } from "@/components/live/job-progress";
import { Badge } from "@/components/ui/badge";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import { JOBS_LIMIT, JOBS_POLL_MS, JOBS_REFRESH_MS, useJobs } from "@/lib/api/hooks";
import {
  JOB_STATES,
  JOB_TYPES,
  PERMISSION_BACKUP_READ,
  type Job,
  type JobState,
  type JobType,
} from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_READ } from "@/lib/api/restore-schemas";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";
import { isActiveProgress } from "@/lib/live/events";
import { formatDuration, stepLabel } from "@/lib/restore";

const AMBER = "border-amber-500/60 text-amber-700 dark:text-amber-400";
const BLUE = "border-sky-500/60 text-sky-700 dark:text-sky-400";

export const JOB_STATE_LABEL: Record<JobState, string> = {
  running: "Running",
  succeeded: "Succeeded",
  partial: "Partial",
  failed: "Failed",
  rolled_back: "Rolled back",
  missing: "Missing",
};

export function JobStateBadge({ state }: { state: JobState }) {
  const label = JOB_STATE_LABEL[state];
  switch (state) {
    case "running":
      return (
        <Badge variant="outline" className={BLUE} data-state={state}>
          <LoaderCircleIcon aria-hidden="true" className="motion-safe:animate-spin" /> {label}
        </Badge>
      );
    case "succeeded":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400" data-state={state}>
          <CircleCheckIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "partial":
      return (
        <Badge variant="outline" className={AMBER} data-state={state}>
          <TriangleAlertIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" data-state={state}>
          <CircleXIcon aria-hidden="true" /> {label}
        </Badge>
      );
    case "rolled_back":
      return (
        <Badge variant="outline" className={AMBER} data-state={state}>
          <Undo2Icon aria-hidden="true" /> {label}
        </Badge>
      );
    case "missing":
      return (
        <Badge variant="outline" className={AMBER} data-state={state}>
          <SearchXIcon aria-hidden="true" /> {label}
        </Badge>
      );
  }
}

/** The recovery point (backups) or restore (restores) a job opens. */
export function jobHref(job: Job): string {
  return job.type === "backup" ? `/recovery-points/${encodeURIComponent(job.id)}` : `/restores/${encodeURIComponent(job.id)}`;
}

/** Consistency mode of a backup; current step (or mode) of a restore. */
export function jobDetail(job: Job): string {
  if (job.type === "backup") return modeLabel(job.detail);
  return job.state === "running" ? stepLabel(job.detail) : job.detail;
}

function RunningProgress({ job }: { job: Job }) {
  const progress = useJobProgress(job.application_id);
  if (!progress || !isActiveProgress(progress)) return null;
  const kindMatches = job.type === "backup" ? progress.kind === "snapshot_components" : progress.kind !== "snapshot_components";
  if (!kindMatches) return null;
  return <JobProgressView progress={progress} className="mt-2 max-w-sm" />;
}

const features = tableFeatures({});
const col = createColumnHelper<typeof features, Job>();

function buildColumns(canApps: boolean) {
  return col.columns([
    col.accessor("type", {
      header: "Job",
      cell: ({ row }) => {
        const j = row.original;
        return (
          <Link href={jobHref(j)} className="inline-flex items-center gap-1.5 font-medium underline-offset-4 hover:underline">
            {j.type === "backup" ? (
              <ArchiveIcon aria-hidden="true" className="size-4" />
            ) : (
              <ArchiveRestoreIcon aria-hidden="true" className="size-4" />
            )}
            {j.type === "backup" ? "Backup" : "Restore"}
            <span className="sr-only"> {j.id}</span>
          </Link>
        );
      },
    }),
    col.accessor("application_name", {
      header: "Application",
      cell: ({ row }) => {
        const j = row.original;
        return (
          <div className="min-w-32">
            {canApps ? (
              <Link href={`/applications/${j.application_id}`} className="underline-offset-4 hover:underline">
                {j.application_name}
              </Link>
            ) : (
              j.application_name
            )}
            <div className="text-xs text-muted-foreground">on {j.hostname}</div>
          </div>
        );
      },
    }),
    col.accessor("state", {
      header: "State",
      cell: ({ row }) => (
        <div className="min-w-28">
          <JobStateBadge state={row.original.state} />
          {row.original.state === "running" && <RunningProgress job={row.original} />}
          {row.original.error && (
            <p className="mt-1 max-w-sm text-xs break-words whitespace-normal text-destructive">{row.original.error}</p>
          )}
        </div>
      ),
    }),
    col.accessor("detail", { header: "Detail", cell: ({ row }) => <span className="text-sm">{jobDetail(row.original)}</span> }),
    col.display({
      id: "who",
      header: "Trigger",
      cell: ({ row }) => (
        <span className="text-xs">
          {row.original.requested_by ?? row.original.trigger ?? <span className="text-muted-foreground">—</span>}
        </span>
      ),
    }),
    col.accessor("size_bytes", {
      header: "Size",
      cell: (info) => {
        const v = info.getValue();
        return v ? <span className="tabular-nums">{formatBytes(v)}</span> : <span className="text-muted-foreground">—</span>;
      },
    }),
    col.accessor("started_at", {
      header: "Started",
      cell: (info) => (
        <time dateTime={info.getValue()} title={formatDateTime(info.getValue())} className="whitespace-nowrap">
          {formatRelative(info.getValue())}
        </time>
      ),
    }),
    col.display({
      id: "duration",
      header: "Duration",
      cell: ({ row }) => (
        <span className="whitespace-nowrap tabular-nums">
          {row.original.state === "missing" ? "—" : (formatDuration(row.original.started_at, row.original.finished_at) ?? "—")}
        </span>
      ),
    }),
  ]);
}

function JobsTable() {
  const me = useCurrentUser();
  const canRestores = hasPermission(me, PERMISSION_RESTORE_READ);
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const live = useLiveStatus();
  const [filters, setFilters] = useUrlFilters(
    { type: ALL, state: ALL, application: ALL },
    { type: [ALL, ...(canRestores ? JOB_TYPES : ["backup"])], state: [ALL, ...JOB_STATES] },
  );
  // Without restore.read the API rejects restores: ask for backups only.
  const type = filters.type === ALL ? (canRestores ? null : "backup") : (filters.type as JobType);
  const jobs = useJobs({
    type,
    state: filters.state === ALL ? null : (filters.state as JobState),
    applicationId: filters.application === ALL ? null : filters.application,
  });
  const allForOptions = useJobs({ type: canRestores ? null : "backup" });
  const data = useMemo(() => jobs.data ?? [], [jobs.data]);
  const columns = useMemo(() => buildColumns(canApps), [canApps]);
  const table = useTable({ features, columns, data, getRowId: (j) => `${j.type}/${j.id}` });
  const apps = useMemo(
    () => distinctOptions(allForOptions.data ?? [], (j) => j.application_id, (j) => j.application_name),
    [allForOptions.data],
  );

  if (jobs.isPending) return <RowsSkeleton label="Loading jobs…" />;
  if (jobs.isError && !jobs.data) {
    return <QueryError title="Could not load jobs" error={jobs.error} onRetry={() => void jobs.refetch()} />;
  }
  const running = data.some((j) => j.state === "running");

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <FilterSelect
          label="Type"
          value={filters.type}
          onChange={(v) => setFilters({ type: v })}
          options={[
            { value: "backup", label: "Backups" },
            ...(canRestores ? [{ value: "restore", label: "Restores" }] : []),
          ]}
          allLabel={canRestores ? "Backups and restores" : undefined}
          className="min-w-44"
        />
        <FilterSelect
          label="State"
          value={filters.state}
          onChange={(v) => setFilters({ state: v })}
          options={JOB_STATES.map((s) => ({ value: s, label: JOB_STATE_LABEL[s] }))}
          allLabel="Any state"
          className="min-w-32"
        />
        <FilterSelect
          label="Application"
          value={filters.application}
          onChange={(v) => setFilters({ application: v })}
          options={apps}
          allLabel="All applications"
        />
      </div>
      <DataTable
        table={table}
        columns={columns.length}
        label="Jobs"
        empty="No jobs match the filters."
        rowProps={(j) => ({ "data-state": j.state, "data-type": j.type })}
      />
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {data.length} job{data.length === 1 ? "" : "s"}, newest first
        {data.length >= JOBS_LIMIT ? ` (only the latest ${JOBS_LIMIT})` : ""}.{" "}
        {live === "live"
          ? "Updates live."
          : `Refreshes every ${(running ? JOBS_POLL_MS : JOBS_REFRESH_MS) / 1000} s.`}
        {!canRestores && " Restores are hidden: they need the restore.read permission."}
        {jobs.isError && " The last refresh failed; showing earlier data."}
      </p>
    </div>
  );
}

/** Protection → Jobs: backups and restores in one live list. */
export function JobsView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Jobs</h1>
        <p className="text-sm text-muted-foreground">
          Backups and restores in one list: running, succeeded, partial, failed, rolled back and missing.
        </p>
      </div>
      {hasPermission(me, PERMISSION_BACKUP_READ) ? (
        <JobsTable />
      ) : (
        <AccessDenied what="Viewing jobs" permission={PERMISSION_BACKUP_READ} />
      )}
    </div>
  );
}

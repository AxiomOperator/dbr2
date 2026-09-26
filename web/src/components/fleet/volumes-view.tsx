// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import { CircleCheckIcon, CircleMinusIcon } from "lucide-react";
import Link from "next/link";
import { useMemo } from "react";
import { VolumeClassBadge } from "@/components/applications/app-badges";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { DataTable } from "@/components/common/data-table";
import { ALL, distinctOptions, FilterSelect, useUrlFilters } from "@/components/common/filters";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { NO_APPLICATION } from "@/components/fleet/containers-view";
import {
  FLEET_VOLUME_CLASSES,
  PERMISSION_APPLICATION_READ,
  PERMISSION_HOST_READ,
  type FleetVolume,
} from "@/lib/api/fleet-schemas";
import { FLEET_REFRESH_MS, useVolumes } from "@/lib/api/hooks";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";

export interface VolumeFilters {
  host: string;
  application: string;
  class: string;
  protected: string;
}

export function filterVolumes(items: FleetVolume[], f: VolumeFilters): FleetVolume[] {
  return items.filter(
    (v) =>
      (f.host === ALL || v.host_id === f.host) &&
      (f.application === ALL ||
        (f.application === NO_APPLICATION ? !v.application_id : v.application_id === f.application)) &&
      (f.class === ALL || v.class === f.class) &&
      (f.protected === ALL || (f.protected === "yes") === v.protected),
  );
}

const CLASS_LABEL: Record<string, string> = { local: "Local", external: "External", ephemeral: "Ephemeral", unused: "Unused" };

const features = tableFeatures({});
const col = createColumnHelper<typeof features, FleetVolume>();

function buildColumns(canApps: boolean) {
  return col.columns([
    col.accessor("name", {
      header: "Volume",
      cell: ({ row }) => (
        <div className="max-w-64 min-w-32">
          <div className="font-mono text-sm break-all" title={row.original.name}>
            {row.original.name.length > 40 ? `${row.original.name.slice(0, 16)}…` : row.original.name}
          </div>
          <div className="text-xs text-muted-foreground">driver {row.original.driver}</div>
        </div>
      ),
    }),
    col.accessor("hostname", {
      header: "Host",
      cell: ({ row }) => (
        <Link href={`/hosts/${row.original.host_id}`} className="underline-offset-4 hover:underline">
          {row.original.hostname}
        </Link>
      ),
    }),
    col.accessor("class", { header: "Class", cell: (info) => <VolumeClassBadge cls={info.getValue()} /> }),
    col.display({
      id: "application",
      header: "Application",
      cell: ({ row }) => {
        const v = row.original;
        if (!v.application_id) return <span className="text-muted-foreground">None</span>;
        return canApps ? (
          <Link href={`/applications/${v.application_id}`} className="underline-offset-4 hover:underline">
            {v.application_name ?? v.application_id}
          </Link>
        ) : (
          (v.application_name ?? v.application_id)
        );
      },
    }),
    col.display({
      id: "used_by",
      header: "Used by",
      cell: ({ row }) =>
        row.original.used_by.length === 0 ? (
          <span className="text-muted-foreground">No container</span>
        ) : (
          <ul className="font-mono text-xs">
            {row.original.used_by.map((u) => (
              <li key={u} className="break-all">
                {u}
              </li>
            ))}
          </ul>
        ),
    }),
    col.accessor("protected", {
      header: "Protected",
      cell: ({ row }) => {
        const v = row.original;
        return v.protected ? (
          <div className="space-y-0.5">
            <span className="inline-flex items-center gap-1.5 text-emerald-700 dark:text-emerald-400">
              <CircleCheckIcon aria-hidden="true" className="size-4" /> Protected
            </span>
            {v.last_backup_at && (
              <div className="text-xs text-muted-foreground" title={formatDateTime(v.last_backup_at)}>
                backed up {formatRelative(v.last_backup_at)}
              </div>
            )}
          </div>
        ) : (
          <span className="inline-flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
            <CircleMinusIcon aria-hidden="true" className="size-4" /> Not protected
          </span>
        );
      },
    }),
    col.accessor("last_size_bytes", {
      header: "Last size",
      cell: ({ row }) =>
        row.original.protected ? (
          <span className="tabular-nums">{formatBytes(row.original.last_size_bytes)}</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    }),
  ]);
}

function VolumesTable() {
  const me = useCurrentUser();
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const [filters, setFilters] = useUrlFilters(
    { host: ALL, application: ALL, class: ALL, protected: ALL },
    { class: [ALL, ...FLEET_VOLUME_CLASSES], protected: [ALL, "yes", "no"] },
  );
  const volumes = useVolumes();
  const all = useMemo(() => volumes.data ?? [], [volumes.data]);
  const data = useMemo(() => filterVolumes(all, filters), [all, filters]);
  const columns = useMemo(() => buildColumns(canApps), [canApps]);
  const table = useTable({ features, columns, data, getRowId: (v) => `${v.host_id}/${v.name}` });
  const hosts = useMemo(() => distinctOptions(all, (v) => v.host_id, (v) => v.hostname), [all]);
  const apps = useMemo(() => distinctOptions(all, (v) => v.application_id, (v) => v.application_name), [all]);

  if (volumes.isPending) return <RowsSkeleton label="Loading volumes…" />;
  if (volumes.isError && !volumes.data) {
    return <QueryError title="Could not load volumes" error={volumes.error} onRetry={() => void volumes.refetch()} />;
  }
  const unprotectedLocal = all.filter((v) => v.class === "local" && !v.protected).length;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <FilterSelect label="Host" value={filters.host} onChange={(v) => setFilters({ host: v })} options={hosts} allLabel="All hosts" />
        <FilterSelect
          label="Application"
          value={filters.application}
          onChange={(v) => setFilters({ application: v })}
          options={[...apps, { value: NO_APPLICATION, label: "Not in an application" }]}
          allLabel="All applications"
        />
        <FilterSelect
          label="Class"
          value={filters.class}
          onChange={(v) => setFilters({ class: v })}
          options={FLEET_VOLUME_CLASSES.map((c) => ({ value: c, label: CLASS_LABEL[c] ?? c }))}
          allLabel="Any class"
          className="min-w-32"
        />
        <FilterSelect
          label="Protected"
          value={filters.protected}
          onChange={(v) => setFilters({ protected: v })}
          options={[
            { value: "yes", label: "Protected" },
            { value: "no", label: "Not protected" },
          ]}
          allLabel="Either"
          className="min-w-32"
        />
      </div>
      {unprotectedLocal > 0 && (
        <p className="text-sm text-amber-700 dark:text-amber-400" role="status">
          {unprotectedLocal} local {unprotectedLocal === 1 ? "volume is" : "volumes are"} not in their application&apos;s
          latest recovery point.
        </p>
      )}
      <DataTable
        table={table}
        columns={columns.length}
        label="Volumes"
        empty={all.length === 0 ? "No volumes reported yet." : "No volumes match the filters."}
      />
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {data.length} of {all.length} volume{all.length === 1 ? "" : "s"} shown. “Protected” means the application&apos;s
        latest recovery point contains the volume. Refreshes every {FLEET_REFRESH_MS / 1000} s.
      </p>
    </div>
  );
}

/** Docker → Volumes: every named volume, its class and whether it is protected. */
export function VolumesView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Volumes</h1>
        <p className="text-sm text-muted-foreground">
          Every named volume on every host: its class (ADR-0006), the application using it and whether the
          latest recovery point contains it.
        </p>
      </div>
      {hasPermission(me, PERMISSION_HOST_READ) ? (
        <VolumesTable />
      ) : (
        <AccessDenied what="Viewing volumes" permission={PERMISSION_HOST_READ} />
      )}
    </div>
  );
}

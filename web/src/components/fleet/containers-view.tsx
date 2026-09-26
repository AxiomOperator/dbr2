// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { DataTable } from "@/components/common/data-table";
import { ALL, distinctOptions, FilterSelect, useUrlFilters } from "@/components/common/filters";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { Badge } from "@/components/ui/badge";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ, type FleetContainer } from "@/lib/api/fleet-schemas";
import { FLEET_REFRESH_MS, useContainers } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

/** Application filter value for containers that belong to no application. */
export const NO_APPLICATION = "none";

export interface ContainerFilters {
  host: string;
  application: string;
  state: string;
}

export function filterContainers(items: FleetContainer[], f: ContainerFilters): FleetContainer[] {
  return items.filter(
    (c) =>
      (f.host === ALL || c.host_id === f.host) &&
      (f.application === ALL ||
        (f.application === NO_APPLICATION ? !c.application_id : c.application_id === f.application)) &&
      (f.state === ALL || c.state === f.state),
  );
}

export function StateBadge({ state }: { state: string }) {
  if (state === "running") {
    return (
      <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
        Running
      </Badge>
    );
  }
  if (state === "exited" || state === "dead") return <Badge variant="outline">{state === "dead" ? "Dead" : "Exited"}</Badge>;
  return (
    <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
      {state.charAt(0).toUpperCase() + state.slice(1)}
    </Badge>
  );
}

const features = tableFeatures({});
const col = createColumnHelper<typeof features, FleetContainer>();

function buildColumns(canApps: boolean) {
  return col.columns([
    col.accessor("name", {
      header: "Container",
      cell: ({ row }) => (
        <div className="min-w-36">
          <div className="font-mono text-sm font-medium">{row.original.name}</div>
          <div className="font-mono text-xs text-muted-foreground" title={row.original.id}>
            {row.original.id.slice(0, 12)}
          </div>
        </div>
      ),
    }),
    col.accessor("image", {
      header: "Image",
      cell: (info) => <span className="font-mono text-xs break-all whitespace-normal">{info.getValue()}</span>,
    }),
    col.accessor("state", { header: "State", cell: (info) => <StateBadge state={info.getValue()} /> }),
    col.accessor("hostname", {
      header: "Host",
      cell: ({ row }) => (
        <Link href={`/hosts/${row.original.host_id}`} className="underline-offset-4 hover:underline">
          {row.original.hostname}
        </Link>
      ),
    }),
    col.display({
      id: "application",
      header: "Application",
      cell: ({ row }) => {
        const c = row.original;
        if (!c.application_id) return <span className="text-muted-foreground">None</span>;
        return canApps ? (
          <Link href={`/applications/${c.application_id}`} className="underline-offset-4 hover:underline">
            {c.application_name ?? c.application_id}
          </Link>
        ) : (
          (c.application_name ?? c.application_id)
        );
      },
    }),
    col.display({
      id: "ports",
      header: "Ports",
      cell: ({ row }) => {
        const ports = row.original.ports;
        if (ports.length === 0) return <span className="text-muted-foreground">—</span>;
        return (
          <ul className="font-mono text-xs whitespace-nowrap">
            {ports.map((p) => (
              <li key={`${p.host_ip}-${p.host_port}-${p.container_port}-${p.protocol}`}>
                {p.host_port ? `${p.host_ip && p.host_ip !== "0.0.0.0" ? `${p.host_ip}:` : ""}${p.host_port}→` : ""}
                {p.container_port}/{p.protocol}
              </li>
            ))}
          </ul>
        );
      },
    }),
    col.display({
      id: "networks",
      header: "Networks",
      cell: ({ row }) => (
        <span className="text-xs">{row.original.networks.join(", ") || <span className="text-muted-foreground">—</span>}</span>
      ),
    }),
    col.accessor("mounts", {
      header: "Mounts",
      cell: (info) => <span className="tabular-nums">{info.getValue()}</span>,
    }),
    col.accessor("restart_policy", {
      header: "Restart",
      cell: (info) => <span className="text-xs">{info.getValue() || "—"}</span>,
    }),
    col.accessor("created", {
      header: "Created",
      cell: (info) => (
        <time dateTime={info.getValue()} title={formatDateTime(info.getValue())} className="whitespace-nowrap">
          {formatRelative(info.getValue())}
        </time>
      ),
    }),
  ]);
}

function ContainersTable() {
  const me = useCurrentUser();
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const [filters, setFilters] = useUrlFilters({ host: ALL, application: ALL, state: ALL });
  const containers = useContainers();
  const all = useMemo(() => containers.data ?? [], [containers.data]);
  const data = useMemo(() => filterContainers(all, filters), [all, filters]);
  const columns = useMemo(() => buildColumns(canApps), [canApps]);
  const table = useTable({ features, columns, data, getRowId: (c) => `${c.host_id}/${c.id}` });

  const hosts = useMemo(() => distinctOptions(all, (c) => c.host_id, (c) => c.hostname), [all]);
  const apps = useMemo(() => distinctOptions(all, (c) => c.application_id, (c) => c.application_name), [all]);
  const states = useMemo(
    () => [...new Set(all.map((c) => c.state))].sort().map((s) => ({ value: s, label: s.charAt(0).toUpperCase() + s.slice(1) })),
    [all],
  );

  if (containers.isPending) return <RowsSkeleton label="Loading containers…" />;
  if (containers.isError && !containers.data) {
    return (
      <QueryError title="Could not load containers" error={containers.error} onRetry={() => void containers.refetch()} />
    );
  }

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
          label="State"
          value={filters.state}
          onChange={(v) => setFilters({ state: v })}
          options={states}
          allLabel="Any state"
          className="min-w-32"
        />
      </div>
      <DataTable
        table={table}
        columns={columns.length}
        label="Containers"
        empty={all.length === 0 ? "No containers reported yet. Approve a host and run discovery." : "No containers match the filters."}
      />
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {data.length} of {all.length} container{all.length === 1 ? "" : "s"} shown, from each host&apos;s latest
        inventory. Refreshes every {FLEET_REFRESH_MS / 1000} s and after each discovery. Secrets are never listed.
      </p>
    </div>
  );
}

/** Docker → Containers: every container on every host. */
export function ContainersView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Containers</h1>
        <p className="text-sm text-muted-foreground">
          Every container on every host, with the application it belongs to.
        </p>
      </div>
      {hasPermission(me, PERMISSION_HOST_READ) ? (
        <ContainersTable />
      ) : (
        <AccessDenied what="Viewing containers" permission={PERMISSION_HOST_READ} />
      )}
    </div>
  );
}

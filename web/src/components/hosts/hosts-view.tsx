// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { DataTable } from "@/components/common/data-table";
import { AgentStatusBadge, ConnectedIndicator, DockerState } from "@/components/hosts/host-badges";
import {
  PERMISSION_APPLICATION_READ,
  PERMISSION_HOST_READ,
  type Agent,
} from "@/lib/api/fleet-schemas";
import { useAgents, useApplications, useContainers, useVolumes } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

/** One Docker host with counts from the fleet-wide lists. */
export interface HostRow {
  agent: Agent;
  applications: number | null;
  containers: number;
  running: number;
  volumes: number;
  protectedVolumes: number;
}

export function hostRows(
  agents: Agent[],
  containers: { host_id: string; state: string }[],
  volumes: { host_id: string; protected: boolean; class: string }[],
  applications: { host_id: string; missing_since: string | null }[] | null,
): HostRow[] {
  return agents
    .filter((a) => a.status === "active" || a.status === "suspended")
    .map((agent) => {
      const cs = containers.filter((c) => c.host_id === agent.id);
      const vs = volumes.filter((v) => v.host_id === agent.id);
      return {
        agent,
        applications: applications ? applications.filter((x) => x.host_id === agent.id && !x.missing_since).length : null,
        containers: cs.length,
        running: cs.filter((c) => c.state === "running").length,
        volumes: vs.length,
        protectedVolumes: vs.filter((v) => v.protected).length,
      };
    })
    .sort((a, b) => a.agent.hostname.localeCompare(b.agent.hostname));
}

const features = tableFeatures({});
const col = createColumnHelper<typeof features, HostRow>();

function buildColumns(showApps: boolean) {
  return col.columns([
    col.accessor((r) => r.agent.hostname, {
      id: "hostname",
      header: "Host",
      cell: ({ row }) => {
        const a = row.original.agent;
        return (
          <div className="min-w-36 space-y-0.5">
            <Link href={`/hosts/${a.id}`} className="font-medium underline-offset-4 hover:underline">
              {a.hostname}
            </Link>
            <div className="text-xs text-muted-foreground">
              {[a.os_release, a.architecture].filter(Boolean).join(" · ") || "Unknown OS"}
            </div>
          </div>
        );
      },
    }),
    col.display({
      id: "connection",
      header: "Connection",
      cell: ({ row }) => {
        const a = row.original.agent;
        return (
          <div className="space-y-1">
            <ConnectedIndicator connected={a.connected} />
            {a.status !== "active" && <AgentStatusBadge status={a.status} />}
            {a.last_seen_at && !a.connected && (
              <div className="text-xs text-muted-foreground" title={formatDateTime(a.last_seen_at)}>
                seen {formatRelative(a.last_seen_at)}
              </div>
            )}
          </div>
        );
      },
    }),
    col.display({
      id: "engine",
      header: "Docker engine",
      cell: ({ row }) => (
        <DockerState reachable={row.original.agent.docker_reachable} version={row.original.agent.docker_version} />
      ),
    }),
    ...(showApps
      ? [
          col.display({
            id: "applications",
            header: "Applications",
            cell: ({ row }) => (
              <Link
                href={`/applications?host=${row.original.agent.id}`}
                className="tabular-nums underline-offset-4 hover:underline"
              >
                {row.original.applications ?? "—"}
              </Link>
            ),
          }),
        ]
      : []),
    col.display({
      id: "containers",
      header: "Containers",
      cell: ({ row }) => (
        <Link href={`/containers?host=${row.original.agent.id}`} className="whitespace-nowrap underline-offset-4 hover:underline">
          <span className="tabular-nums">{row.original.containers}</span>
          {row.original.containers > 0 && (
            <span className="text-xs text-muted-foreground"> ({row.original.running} running)</span>
          )}
        </Link>
      ),
    }),
    col.display({
      id: "volumes",
      header: "Volumes",
      cell: ({ row }) => (
        <Link href={`/volumes?host=${row.original.agent.id}`} className="whitespace-nowrap underline-offset-4 hover:underline">
          <span className="tabular-nums">{row.original.volumes}</span>
          {row.original.volumes > 0 && (
            <span className="text-xs text-muted-foreground"> ({row.original.protectedVolumes} protected)</span>
          )}
        </Link>
      ),
    }),
  ]);
}

function HostsTable() {
  const me = useCurrentUser();
  const showApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const agents = useAgents();
  const containers = useContainers();
  const volumes = useVolumes();
  const apps = useApplications({ enabled: showApps });
  const data = useMemo(
    () =>
      hostRows(agents.data ?? [], containers.data ?? [], volumes.data ?? [], showApps ? (apps.data ?? []) : null),
    [agents.data, containers.data, volumes.data, apps.data, showApps],
  );
  const columns = useMemo(() => buildColumns(showApps), [showApps]);
  const table = useTable({ features, columns, data, getRowId: (r) => r.agent.id });

  if (agents.isPending) return <RowsSkeleton label="Loading hosts…" />;
  if (agents.isError && !agents.data) {
    return <QueryError title="Could not load hosts" error={agents.error} onRetry={() => void agents.refetch()} />;
  }
  const hidden = (agents.data ?? []).length - data.length;

  return (
    <div className="space-y-3">
      <DataTable table={table} columns={columns.length} empty="No approved hosts yet. Enroll and approve an agent under Agents." />
      {(containers.isError || volumes.isError) && (
        <p className="text-sm text-destructive" role="alert">
          Container or volume counts are unavailable right now.
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        Counts come from each host&apos;s latest inventory.
        {hidden > 0 && (
          <>
            {" "}
            {hidden} pending or revoked {hidden === 1 ? "agent is" : "agents are"} listed under{" "}
            <Link href="/agents" className="underline underline-offset-4">
              Agents
            </Link>
            .
          </>
        )}
      </p>
    </div>
  );
}

/** Docker → Hosts: the host inventory (engine, OS, what runs where, connection). */
export function HostsView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Hosts</h1>
        <p className="text-sm text-muted-foreground">
          Docker hosts and what runs on them. Enrollment, approval and agent versions are under{" "}
          <Link href="/agents" className="underline underline-offset-4">
            Agents
          </Link>
          .
        </p>
      </div>
      {hasPermission(me, PERMISSION_HOST_READ) ? (
        <HostsTable />
      ) : (
        <AccessDenied what="Viewing hosts" permission={PERMISSION_HOST_READ} />
      )}
    </div>
  );
}

// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { AddHostDialog } from "@/components/hosts/add-host-dialog";
import { HostActions } from "@/components/hosts/host-actions";
import {
  AgentStatusBadge,
  CertExpiry,
  ConnectedIndicator,
  DockerState,
  OutdatedBadge,
} from "@/components/hosts/host-badges";
import { RegistrationTokens } from "@/components/hosts/registration-tokens";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  PERMISSION_HOST_MANAGE,
  PERMISSION_HOST_READ,
  type Agent,
} from "@/lib/api/fleet-schemas";
import { AGENTS_REFRESH_MS, useAgents } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

const EMPTY: Agent[] = [];
const features = tableFeatures({});
const col = createColumnHelper<typeof features, Agent>();

function buildColumns(canManage: boolean) {
  const base = [
    col.accessor("hostname", {
      header: "Hostname",
      cell: (info) => {
        const { id, os_release, architecture } = info.row.original;
        return (
          <div className="min-w-36 space-y-0.5">
            <Link href={`/hosts/${id}`} className="font-medium underline-offset-4 hover:underline">
              {info.getValue()}
            </Link>
            {(os_release || architecture) && (
              <div className="text-xs text-muted-foreground">
                {[os_release, architecture].filter(Boolean).join(" · ")}
              </div>
            )}
          </div>
        );
      },
    }),
    col.accessor("status", {
      header: "Status",
      cell: (info) => <AgentStatusBadge status={info.getValue()} />,
    }),
    col.accessor("connected", {
      header: "Connection",
      cell: (info) => {
        const latency = info.row.original.latency_ms;
        return (
          <div className="space-y-0.5">
            <ConnectedIndicator connected={info.getValue()} />
            {latency !== null && <div className="text-xs text-muted-foreground">{latency} ms latency</div>}
          </div>
        );
      },
    }),
    col.accessor("agent_version", {
      header: "Agent",
      cell: (info) => (
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <span className="font-mono text-xs">{info.getValue()}</span>
          {info.row.original.outdated && <OutdatedBadge />}
        </span>
      ),
    }),
    col.display({
      id: "docker",
      header: "Docker",
      cell: ({ row }) => (
        <DockerState reachable={row.original.docker_reachable} version={row.original.docker_version} />
      ),
    }),
    col.accessor("last_seen_at", {
      header: "Last seen",
      cell: (info) => {
        const v = info.getValue();
        return v ? (
          <time dateTime={v} title={formatDateTime(v)} className="whitespace-nowrap">
            {formatRelative(v)}
          </time>
        ) : (
          <span className="text-muted-foreground">Never</span>
        );
      },
    }),
    col.accessor("certificate_not_after", {
      header: "Certificate",
      cell: (info) => <CertExpiry iso={info.getValue()} />,
    }),
  ];
  if (!canManage) return col.columns(base);
  return col.columns([
    ...base,
    col.display({
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) => <HostActions agent={row.original} />,
    }),
  ]);
}

function AgentsTable({ canManage }: { canManage: boolean }) {
  const agents = useAgents();
  const columns = useMemo(() => buildColumns(canManage), [canManage]);
  const data = agents.data ?? EMPTY;
  const table = useTable({ features, columns, data, getRowId: (row) => row.id });

  if (agents.isPending) return <RowsSkeleton label="Loading agents…" />;
  if (agents.isError && !agents.data) {
    return <QueryError title="Could not load agents" error={agents.error} onRetry={() => void agents.refetch()} />;
  }

  const pending = data.filter((a) => a.status === "pending").length;

  return (
    <div className="space-y-3">
      {pending > 0 && (
        <p className="text-sm" role="status">
          <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
            {pending} pending
          </Badge>{" "}
          {pending === 1 ? "host is" : "hosts are"} waiting for approval.
        </p>
      )}
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((group) => (
              <TableRow key={group.id}>
                {group.headers.map((header) => (
                  <TableHead key={header.id} scope="col">
                    {header.isPlaceholder ? null : <table.FlexRender header={header} />}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">
                  No hosts enrolled yet.{canManage ? " Use “Add host” to create a registration token." : ""}
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row) => (
                <TableRow key={row.id}>
                  {row.getAllCells().map((cell) => (
                    <TableCell key={cell.id} className="align-top">
                      <table.FlexRender cell={cell} />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        Refreshes every {AGENTS_REFRESH_MS / 1000} s.
        {agents.isError && " The last refresh failed; showing earlier data."}
      </p>
    </div>
  );
}

/**
 * System → Agents: the agent lifecycle (enrollment, approval / suspension /
 * revocation, versions, certificates, registration tokens). The Docker host
 * inventory lives under Docker → Hosts.
 */
export function AgentsView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_HOST_READ);
  const canManage = hasPermission(me, PERMISSION_HOST_MANAGE);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Agents</h1>
          <p className="text-sm text-muted-foreground">
            The DBR² agent on each Docker host: enrollment, approval, versions, certificates and
            registration tokens. The hosts&apos; Docker inventory is under{" "}
            <Link href="/hosts" className="underline underline-offset-4">
              Hosts
            </Link>
            .
          </p>
        </div>
        {canRead && canManage && <AddHostDialog />}
      </div>
      {canRead ? (
        <>
          <AgentsTable canManage={canManage} />
          {canManage && <RegistrationTokens />}
        </>
      ) : (
        <AccessDenied what="Viewing agents" permission={PERMISSION_HOST_READ} />
      )}
    </div>
  );
}

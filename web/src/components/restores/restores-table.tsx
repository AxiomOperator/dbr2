// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { ProductionBadge, RestoreModeBadge, RestoreStateBadge } from "@/components/restores/restore-badges";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import { PERMISSION_BACKUP_READ } from "@/lib/api/protection-schemas";
import type { RestoreRun } from "@/lib/api/restore-schemas";
import { formatDateTime, formatRelative } from "@/lib/format";
import { formatDuration } from "@/lib/restore";

const features = tableFeatures({});
const col = createColumnHelper<typeof features, RestoreRun>();

function buildColumns(opts: { showApplication: boolean; canApps: boolean; canRps: boolean }) {
  return col.columns([
    col.accessor("id", {
      header: "Restore",
      cell: ({ row }) => {
        const r = row.original;
        return (
          <div className="space-y-0.5">
            <Link href={`/restores/${r.id}`} className="font-mono text-xs underline-offset-4 hover:underline">
              {r.id}
            </Link>
            {opts.showApplication && (
              <div className="text-sm">
                {opts.canApps ? (
                  <Link href={`/applications/${r.application_id}`} className="underline-offset-4 hover:underline">
                    {r.application_name}
                  </Link>
                ) : (
                  r.application_name
                )}
              </div>
            )}
          </div>
        );
      },
    }),
    col.accessor("recovery_point_id", {
      header: "Recovery point",
      cell: (info) =>
        opts.canRps ? (
          <Link href={`/recovery-points/${info.getValue()}`} className="font-mono text-xs underline-offset-4 hover:underline">
            {info.getValue()}
          </Link>
        ) : (
          <span className="font-mono text-xs">{info.getValue()}</span>
        ),
    }),
    col.accessor("target_hostname", {
      header: "Target",
      cell: ({ row }) => (
        <div className="flex flex-col items-start gap-1">
          <span>{row.original.target_hostname}</span>
          <span className="inline-flex flex-wrap gap-1">
            <RestoreModeBadge mode={row.original.mode} />
            {row.original.production && <ProductionBadge />}
          </span>
        </div>
      ),
    }),
    col.accessor("state", {
      header: "State",
      cell: ({ row }) => (
        <div className="space-y-1">
          <RestoreStateBadge state={row.original.state} step={row.original.step} />
          {row.original.error && (
            <span className="block max-w-64 text-xs break-words text-destructive">{row.original.error}</span>
          )}
        </div>
      ),
    }),
    col.accessor("requested_by", {
      header: "Requested by",
      cell: (info) => info.getValue() || <span className="text-muted-foreground">—</span>,
    }),
    col.accessor("created_at", {
      header: "Created",
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
          {formatDuration(row.original.started_at, row.original.finished_at) ?? (
            <span className="text-muted-foreground">—</span>
          )}
        </span>
      ),
    }),
  ]);
}

/** Recovery history table (optionally with the application). */
export function RestoresTable({
  items,
  showApplication = false,
  emptyText,
  label = "Restores",
}: {
  items: RestoreRun[];
  showApplication?: boolean;
  emptyText: string;
  label?: string;
}) {
  const me = useCurrentUser();
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const canRps = hasPermission(me, PERMISSION_BACKUP_READ);
  const columns = useMemo(
    () => buildColumns({ showApplication, canApps, canRps }),
    [showApplication, canApps, canRps],
  );
  const table = useTable({ features, columns, data: items, getRowId: (row) => row.id });
  return (
    <div className="rounded-lg border">
      <Table aria-label={label}>
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
                {emptyText}
              </TableCell>
            </TableRow>
          ) : (
            table.getRowModel().rows.map((row) => (
              <TableRow key={row.id} data-restore-state={row.original.state}>
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
  );
}

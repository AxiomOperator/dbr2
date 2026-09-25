// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import {
  ModeCell,
  RecoveryPointStateBadge,
  RecoveryPointStatusBadge,
} from "@/components/backups/backup-badges";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { RecoveryPoint } from "@/lib/api/protection-schemas";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";

const features = tableFeatures({});
const col = createColumnHelper<typeof features, RecoveryPoint>();

function When({ iso }: { iso: string | null }) {
  if (!iso) return <span className="text-muted-foreground">—</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className="whitespace-nowrap">
      {formatRelative(iso)}
    </time>
  );
}

function buildColumns(showApplication: boolean) {
  const idCol = col.accessor("id", {
    header: "Recovery point",
    cell: ({ row }) => {
      const rp = row.original;
      return (
        <div className="space-y-0.5">
          <Link href={`/recovery-points/${rp.id}`} className="font-mono text-xs underline-offset-4 hover:underline">
            {rp.id}
          </Link>
          {showApplication && (
            <div className="text-sm">
              {rp.application_name} <span className="text-xs text-muted-foreground">on {rp.hostname}</span>
            </div>
          )}
          <div className="text-xs text-muted-foreground">Trigger: {rp.trigger || "—"}</div>
        </div>
      );
    },
  });
  return col.columns([
    idCol,
    col.accessor("state", {
      header: "State",
      cell: ({ row }) => (
        <div className="flex flex-col items-start gap-1">
          <RecoveryPointStateBadge state={row.original.state} />
          <RecoveryPointStatusBadge status={row.original.status} />
        </div>
      ),
    }),
    col.accessor("consistency_mode", {
      header: "Mode",
      cell: ({ row }) => (
        <ModeCell mode={row.original.consistency_mode} crashConsistent={row.original.crash_consistent_only} />
      ),
    }),
    col.accessor("size_bytes", {
      header: "Size",
      cell: ({ row }) => (
        <div className="text-xs whitespace-nowrap">
          <div className="text-sm tabular-nums">{formatBytes(row.original.size_bytes)}</div>
          <div className="text-muted-foreground">
            {row.original.component_count} component{row.original.component_count === 1 ? "" : "s"}
          </div>
        </div>
      ),
    }),
    col.accessor("created_at", {
      header: "Created",
      cell: (info) => <When iso={info.getValue()} />,
    }),
    col.accessor("committed_at", {
      header: "Committed",
      cell: (info) => <When iso={info.getValue()} />,
    }),
    col.accessor("error", {
      header: "Error",
      cell: (info) =>
        info.getValue() ? (
          <span className="block max-w-72 text-xs break-words text-destructive">{info.getValue()}</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    }),
  ]);
}

/** Recovery points table (optionally with the application column). */
export function RecoveryPointsTable({
  items,
  showApplication = false,
  emptyText,
  label = "Recovery points",
}: {
  items: RecoveryPoint[];
  showApplication?: boolean;
  emptyText: string;
  label?: string;
}) {
  const columns = useMemo(() => buildColumns(showApplication), [showApplication]);
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
              <TableRow key={row.id} data-rp-state={row.original.state}>
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

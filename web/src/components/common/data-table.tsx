// SPDX-License-Identifier: Apache-2.0
"use client";

import type { ReactTable, RowData, TableFeatures } from "@tanstack/react-table";
import type { ReactNode } from "react";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

/** Renders a TanStack table (header groups, rows, empty state) in the console's table style. */
export function DataTable<TFeatures extends TableFeatures, TData extends RowData>({
  table,
  columns,
  empty,
  label,
  rowProps,
}: {
  table: ReactTable<TFeatures, TData>;
  /** Column count, for the empty row's colSpan. */
  columns: number;
  empty: ReactNode;
  /** Accessible name of the table. */
  label?: string;
  rowProps?: (row: TData) => Record<string, string | undefined>;
}) {
  const rows = table.getRowModel().rows;
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
          {rows.length === 0 ? (
            <TableRow>
              <TableCell colSpan={columns} className="h-24 text-center whitespace-normal text-muted-foreground">
                {empty}
              </TableCell>
            </TableRow>
          ) : (
            rows.map((row) => (
              <TableRow key={row.id} {...(rowProps?.(row.original) ?? {})}>
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

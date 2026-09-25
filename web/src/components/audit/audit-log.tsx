// SPDX-License-Identifier: Apache-2.0
"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PERMISSION_AUDIT_READ, type AuditEvent } from "@/lib/api/schemas";

const PAGE_SIZE = 50;
const EMPTY: AuditEvent[] = [];

const features = tableFeatures({});
const col = createColumnHelper<typeof features, AuditEvent>();

const dateFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
});

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : dateFormat.format(d);
}

function ResultBadge({ result }: { result: string }) {
  const r = result.toLowerCase();
  const variant =
    r === "success" || r === "ok" || r === "allowed"
      ? "secondary"
      : r === "failure" || r === "denied" || r === "error"
        ? "destructive"
        : "outline";
  return <Badge variant={variant}>{result}</Badge>;
}

function Details({ value }: { value: unknown }) {
  if (value === undefined || value === null) return <span className="text-muted-foreground">—</span>;
  if (typeof value === "object" && Object.keys(value as object).length === 0) {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <details>
      <summary className="cursor-pointer text-xs text-muted-foreground">Show</summary>
      <pre className="mt-1 max-w-md overflow-x-auto rounded bg-muted p-2 text-xs">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  );
}

const dash = (v: string | null | undefined) => (v ? v : "—");

const columns = col.columns([
  col.accessor("occurred_at", {
    header: "Time",
    cell: (info) => (
      <time dateTime={info.getValue()} className="whitespace-nowrap">
        {formatTime(info.getValue())}
      </time>
    ),
  }),
  col.accessor("event_type", {
    header: "Event",
    cell: (info) => <span className="font-mono text-xs">{info.getValue()}</span>,
  }),
  col.accessor("actor_display", { header: "Actor", cell: (info) => dash(info.getValue()) }),
  col.accessor("result", { header: "Result", cell: (info) => <ResultBadge result={info.getValue()} /> }),
  col.display({
    id: "target",
    header: "Target",
    cell: ({ row }) => {
      const { target_type, target_id } = row.original;
      if (!target_type && !target_id) return "—";
      return (
        <span className="font-mono text-xs">
          {dash(target_type)}
          {target_id ? `:${target_id}` : ""}
        </span>
      );
    },
  }),
  col.accessor("source_ip", {
    header: "Source IP",
    cell: (info) => <span className="font-mono text-xs">{dash(info.getValue())}</span>,
  }),
  col.accessor("reason", { header: "Reason", cell: (info) => dash(info.getValue()) }),
  col.accessor("details", { header: "Details", cell: (info) => <Details value={info.getValue()} /> }),
]);

function AuditTable() {
  const query = useInfiniteQuery({
    queryKey: queryKeys.auditEvents(PAGE_SIZE),
    queryFn: ({ pageParam, signal }) => api.auditEvents({ limit: PAGE_SIZE, cursor: pageParam }, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });

  const data = useMemo(
    () => (query.data ? query.data.pages.flatMap((p) => p.items) : EMPTY),
    [query.data],
  );
  const table = useTable({ features, columns, data, getRowId: (row) => row.event_id });

  if (query.isPending) {
    return (
      <div className="space-y-2" aria-busy="true">
        <span className="sr-only">Loading audit events…</span>
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </div>
    );
  }

  // With cached pages, a failed "load more" or refetch keeps the table visible.
  if (query.isError && !query.data) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Could not load audit events</AlertTitle>
        <AlertDescription>
          <p>{errorMessage(query.error)}</p>
          <Button className="mt-3" variant="outline" onClick={() => void query.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="space-y-4">
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
                  No audit events yet.
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
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span aria-live="polite">
          {data.length} event{data.length === 1 ? "" : "s"} shown
        </span>
        {query.hasNextPage ? (
          <Button
            variant="outline"
            onClick={() => void query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
          >
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        ) : (
          data.length > 0 && <span>End of log</span>
        )}
      </div>
      {query.isError && (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(query.error)}
        </p>
      )}
    </div>
  );
}

export function AuditLog() {
  const me = useCurrentUser();
  const allowed = hasPermission(me, PERMISSION_AUDIT_READ);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground">
          Security-relevant events, newest first.
        </p>
      </div>
      {allowed ? (
        <AuditTable />
      ) : (
        <Alert>
          <AlertTitle>Access denied</AlertTitle>
          <AlertDescription>
            Viewing the audit log requires the <code>{PERMISSION_AUDIT_READ}</code> permission.
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}

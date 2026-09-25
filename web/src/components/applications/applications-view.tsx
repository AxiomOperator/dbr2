// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useId, useMemo } from "react";
import {
  displayName,
  KindBadge,
  MissingBadge,
  SourceBadge,
  UnprotectedBadge,
} from "@/components/applications/app-badges";
import { DeleteApplicationButton } from "@/components/applications/delete-application-dialog";
import { GroupContainersDialog } from "@/components/applications/group-containers-dialog";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  APPLICATION_KINDS,
  PERMISSION_APPLICATION_MANAGE,
  PERMISSION_APPLICATION_READ,
  type ApplicationKind,
  type ApplicationSummary,
} from "@/lib/api/fleet-schemas";
import { useApplications } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

const EMPTY: ApplicationSummary[] = [];
const ALL = "all";
const features = tableFeatures({});
const col = createColumnHelper<typeof features, ApplicationSummary>();


function buildColumns(canManage: boolean) {
  const base = [
    col.accessor("name", {
      header: "Application",
      cell: ({ row }) => {
        const a = row.original;
        const shown = displayName(a);
        return (
          <div className="min-w-32">
            <Link
              href={`/applications/${a.id}`}
              className="font-medium underline-offset-4 hover:underline"
            >
              {shown}
            </Link>
            {shown !== a.name && <div className="font-mono text-xs text-muted-foreground">{a.name}</div>}
            <div className="text-xs text-muted-foreground">on {a.hostname}</div>
          </div>
        );
      },
    }),
    col.accessor("kind", { header: "Kind", cell: (info) => <KindBadge kind={info.getValue()} /> }),
    col.accessor("source", {
      header: "Definition",
      cell: (info) => <SourceBadge source={info.getValue()} />,
    }),
    col.display({
      id: "resources",
      header: "Resources",
      cell: ({ row }) => {
        const a = row.original;
        const parts: [number, string, string][] = [
          [a.services, "service", "services"],
          [a.volumes, "volume", "volumes"],
          [a.bind_mounts, "bind mount", "bind mounts"],
        ];
        return (
          <ul className="text-xs whitespace-nowrap">
            {parts.map(([n, one, many]) => (
              <li key={one}>
                <span className="tabular-nums">{n}</span> {n === 1 ? one : many}
              </li>
            ))}
          </ul>
        );
      },
    }),
    col.accessor("unprotected_high", {
      header: "Unprotected",
      cell: (info) =>
        info.getValue() > 0 ? (
          <UnprotectedBadge count={info.getValue()} />
        ) : (
          <span className="text-muted-foreground">None</span>
        ),
    }),
    col.display({
      id: "recovery",
      header: "Dependencies / secrets",
      cell: ({ row }) => (
        <ul className="text-xs whitespace-nowrap">
          <li>
            <span className="tabular-nums">{row.original.dependencies}</span>{" "}
            {row.original.dependencies === 1 ? "dependency" : "dependencies"}
          </li>
          <li>
            <span className="tabular-nums">{row.original.secrets_count}</span>{" "}
            {row.original.secrets_count === 1 ? "secret" : "secrets"}
          </li>
        </ul>
      ),
    }),
    col.display({
      id: "ownership",
      header: "Ownership",
      cell: ({ row }) => {
        const { owner, environment, criticality } = row.original;
        if (!owner && !environment && !criticality) return <span className="text-muted-foreground">—</span>;
        return (
          <div className="text-xs">
            <div>{owner ?? "—"}</div>
            <div className="text-muted-foreground">
              {environment ?? "—"} · {criticality ?? "—"}
            </div>
          </div>
        );
      },
    }),
    col.accessor("last_seen_at", {
      header: "Last seen",
      cell: ({ row }) => {
        const a = row.original;
        return (
          <div className="flex flex-col items-start gap-1">
            <time dateTime={a.last_seen_at} title={formatDateTime(a.last_seen_at)} className="whitespace-nowrap">
              {formatRelative(a.last_seen_at)}
            </time>
            {a.missing_since && <MissingBadge since={formatDateTime(a.missing_since)} />}
          </div>
        );
      },
    }),
  ];
  if (!canManage) return col.columns(base);
  return col.columns([
    ...base,
    col.display({
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) =>
        row.original.kind === "manual" ? (
          <DeleteApplicationButton id={row.original.id} name={displayName(row.original)} />
        ) : null,
    }),
  ]);
}

export interface ApplicationFilters {
  host: string;
  kind: ApplicationKind | typeof ALL;
  unprotectedOnly: boolean;
}

export function filterApplications(apps: ApplicationSummary[], f: ApplicationFilters): ApplicationSummary[] {
  return apps.filter(
    (a) =>
      (f.host === ALL || a.host_id === f.host) &&
      (f.kind === ALL || a.kind === f.kind) &&
      (!f.unprotectedOnly || a.unprotected_high > 0),
  );
}

function useFilters(): [ApplicationFilters, (next: Partial<ApplicationFilters>) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const kindParam = params.get("kind");
  const filters: ApplicationFilters = {
    host: params.get("host") || ALL,
    kind: (APPLICATION_KINDS as readonly string[]).includes(kindParam ?? "")
      ? (kindParam as ApplicationKind)
      : ALL,
    unprotectedOnly: params.get("unprotected") === "1",
  };
  const update = (next: Partial<ApplicationFilters>) => {
    const merged = { ...filters, ...next };
    const qs = new URLSearchParams();
    if (merged.host !== ALL) qs.set("host", merged.host);
    if (merged.kind !== ALL) qs.set("kind", merged.kind);
    if (merged.unprotectedOnly) qs.set("unprotected", "1");
    const s = qs.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };
  return [filters, update];
}

function ApplicationsTable({ canManage }: { canManage: boolean }) {
  const id = useId();
  const apps = useApplications();
  const [filters, setFilters] = useFilters();
  const all = apps.data ?? EMPTY;
  const data = useMemo(() => filterApplications(all, filters), [all, filters]);
  const columns = useMemo(() => buildColumns(canManage), [canManage]);
  const table = useTable({ features, columns, data, getRowId: (row) => row.id });

  const hosts = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of all) m.set(a.host_id, a.hostname);
    if (filters.host !== ALL && !m.has(filters.host)) m.set(filters.host, "Unknown host");
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [all, filters.host]);

  if (apps.isPending) return <RowsSkeleton label="Loading applications…" />;
  if (apps.isError && !apps.data) {
    return (
      <QueryError title="Could not load applications" error={apps.error} onRetry={() => void apps.refetch()} />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-host`}>Host</Label>
          <Select value={filters.host} onValueChange={(v) => setFilters({ host: v })}>
            <SelectTrigger id={`${id}-host`} className="min-w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All hosts</SelectItem>
              {hosts.map(([hid, hostname]) => (
                <SelectItem key={hid} value={hid}>
                  {hostname}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-kind`}>Kind</Label>
          <Select
            value={filters.kind}
            onValueChange={(v) => setFilters({ kind: v as ApplicationFilters["kind"] })}
          >
            <SelectTrigger id={`${id}-kind`} className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All kinds</SelectItem>
              <SelectItem value="compose">Compose</SelectItem>
              <SelectItem value="container">Container</SelectItem>
              <SelectItem value="manual">Manual</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="flex h-8 items-center gap-2">
          <Checkbox
            id={`${id}-unprotected`}
            checked={filters.unprotectedOnly}
            onCheckedChange={(v) => setFilters({ unprotectedOnly: v === true })}
          />
          <Label htmlFor={`${id}-unprotected`} className="font-normal">
            Only with unprotected data
          </Label>
        </div>
      </div>

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
                  {all.length === 0
                    ? "No applications discovered yet. Approve a host and run discovery."
                    : "No applications match the filters."}
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
      <p className="text-sm text-muted-foreground" aria-live="polite">
        {data.length} of {all.length} application{all.length === 1 ? "" : "s"} shown
      </p>
    </div>
  );
}

export function ApplicationsView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_APPLICATION_READ);
  const canManage = hasPermission(me, PERMISSION_APPLICATION_MANAGE);
  const apps = useApplications({ enabled: canRead });
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Applications</h1>
          <p className="text-sm text-muted-foreground">
            Discovered Compose projects, standalone containers and manual groupings, with what is
            and is not protected.
          </p>
        </div>
        {canRead && canManage && <GroupContainersDialog applications={apps.data ?? EMPTY} />}
      </div>
      {canRead ? (
        <ApplicationsTable canManage={canManage} />
      ) : (
        <AccessDenied what="Viewing applications" permission={PERMISSION_APPLICATION_READ} />
      )}
    </div>
  );
}

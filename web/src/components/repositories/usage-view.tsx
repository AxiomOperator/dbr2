// SPDX-License-Identifier: Apache-2.0
"use client";

import { InfoIcon } from "lucide-react";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import {
  BACKEND_LABEL,
  DefaultBadge,
  RepositoryStatusBadge,
  StorageHealth,
  usedPercent,
} from "@/components/repositories/repository-badges";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableFooter, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { REPOSITORIES_REFRESH_MS, useRepositories } from "@/lib/api/hooks";
import { PERMISSION_REPOSITORY_READ, type Repository } from "@/lib/api/protection-schemas";
import { formatBytes } from "@/lib/format";

export interface HostUsageRow {
  hostId: string;
  hostname: string;
  repositoryId: string;
  repositoryName: string;
  applications: number;
  latestBytes: number;
}

/** Per host and Repository logical usage, largest first. */
export function hostUsageRows(repos: Repository[]): HostUsageRow[] {
  return repos
    .flatMap((r) =>
      r.usage_by_host.map((u) => ({
        hostId: u.host_id,
        hostname: u.hostname,
        repositoryId: r.id,
        repositoryName: r.name,
        applications: u.applications,
        latestBytes: u.latest_bytes,
      })),
    )
    .sort((a, b) => b.latestBytes - a.latestBytes || a.hostname.localeCompare(b.hostname));
}

/** Capacity summed over the reachable Repositories. */
export function totalCapacity(repos: Repository[]): { used: number; total: number; unreachable: number } {
  let used = 0;
  let total = 0;
  let unreachable = 0;
  for (const r of repos) {
    if (!r.live) {
      unreachable++;
      continue;
    }
    used += r.live.storage_used_bytes;
    total += r.live.storage_total_bytes;
  }
  return { used, total, unreachable };
}

function UsageBody() {
  const repos = useRepositories();
  const list = useMemo(() => (repos.data ?? []).filter((r) => r.status !== "retired"), [repos.data]);
  const rows = useMemo(() => hostUsageRows(list), [list]);
  const cap = useMemo(() => totalCapacity(list), [list]);

  if (repos.isPending) return <RowsSkeleton label="Loading usage…" />;
  if (repos.isError && !repos.data) {
    return <QueryError title="Could not load Repositories" error={repos.error} onRetry={() => void repos.refetch()} />;
  }
  const pct = usedPercent(cap.used, cap.total);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Repository capacity</CardTitle>
          <CardDescription>
            Live storage status reported by each reposerver
            {pct !== null && ` · ${formatBytes(cap.used)} of ${formatBytes(cap.total)} used overall (${pct}%)`}
            {cap.unreachable > 0 && ` · ${cap.unreachable} unreachable`}.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {list.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Repositories yet.</p>
          ) : (
            <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-label="Repositories">
              {list.map((r) => (
                <li key={r.id} className="space-y-2 rounded-lg border p-3" data-testid="usage-repository">
                  <div className="flex flex-wrap items-center gap-2">
                    <Link href={`/repositories/${r.id}`} className="font-medium underline-offset-4 hover:underline">
                      {r.name}
                    </Link>
                    {r.is_default && <DefaultBadge />}
                  </div>
                  <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                    <RepositoryStatusBadge status={r.status} /> {BACKEND_LABEL[r.backend]}
                    {r.live?.kopia_version && <span>· Kopia {r.live.kopia_version}</span>}
                  </div>
                  <StorageHealth repo={r} />
                  {r.live && (
                    <p className="text-xs text-muted-foreground">{formatBytes(r.live.storage_free_bytes)} free</p>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Usage by host</CardTitle>
          <CardDescription className="flex items-start gap-1.5">
            <InfoIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
            Logical size of each application&apos;s latest recovery point, per host and Repository. Deduplicated
            physical usage is shared between hosts and applications and cannot be attributed.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="rounded-lg border">
            <Table aria-label="Usage by host">
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Host</TableHead>
                  <TableHead scope="col">Repository</TableHead>
                  <TableHead scope="col" className="text-right">
                    Applications
                  </TableHead>
                  <TableHead scope="col" className="text-right">
                    Latest recovery points (logical)
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={4} className="h-20 text-center text-muted-foreground">
                      Nothing backed up yet.
                    </TableCell>
                  </TableRow>
                ) : (
                  rows.map((u) => (
                    <TableRow key={`${u.repositoryId}/${u.hostId}`}>
                      <TableCell>
                        <Link href={`/hosts/${u.hostId}`} className="underline-offset-4 hover:underline">
                          {u.hostname}
                        </Link>
                      </TableCell>
                      <TableCell>
                        <Link href={`/repositories/${u.repositoryId}`} className="underline-offset-4 hover:underline">
                          {u.repositoryName}
                        </Link>
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{u.applications}</TableCell>
                      <TableCell className="text-right tabular-nums">{formatBytes(u.latestBytes)}</TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
              {rows.length > 1 && (
                <TableFooter>
                  <TableRow>
                    <TableCell colSpan={2}>Total</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {rows.reduce((n, r) => n + r.applications, 0)}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {formatBytes(rows.reduce((n, r) => n + r.latestBytes, 0))}
                    </TableCell>
                  </TableRow>
                </TableFooter>
              )}
            </Table>
          </div>
          <p className="mt-3 text-xs text-muted-foreground">Refreshes every {REPOSITORIES_REFRESH_MS / 1000} s.</p>
        </CardContent>
      </Card>
    </div>
  );
}

/** Storage → Usage: every Repository's capacity and the per-host logical usage. */
export function UsageView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Usage</h1>
        <p className="text-sm text-muted-foreground">Storage capacity of every Repository and what each host uses.</p>
      </div>
      {hasPermission(me, PERMISSION_REPOSITORY_READ) ? (
        <UsageBody />
      ) : (
        <AccessDenied what="Viewing storage usage" permission={PERMISSION_REPOSITORY_READ} />
      )}
    </div>
  );
}

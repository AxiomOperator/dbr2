// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { CreateRepositoryDialog } from "@/components/repositories/create-repository-dialog";
import { EscrowDrills, EscrowHealthPanel } from "@/components/repositories/escrow-health";
import { EscrowRecipients } from "@/components/repositories/escrow-recipients";
import {
  DownloadEscrowButton,
  ReindexButton,
  RepositoryUndeleteButton,
  VerifyButton,
} from "@/components/repositories/repository-actions";
import {
  BACKEND_LABEL,
  DefaultBadge,
  Fingerprint,
  RepositoryStatusBadge,
  StorageHealth,
  SystemBadge,
} from "@/components/repositories/repository-badges";
import { buttonVariants } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  PERMISSION_REPOSITORY_MANAGE,
  PERMISSION_REPOSITORY_READ,
  type Repository,
} from "@/lib/api/protection-schemas";
import { REPOSITORIES_REFRESH_MS, useRepositories } from "@/lib/api/hooks";
import { formatDate, formatDateTime, formatRelative } from "@/lib/format";

function Ago({ iso, never = "Never" }: { iso: string | null; never?: string }) {
  return iso ? (
    <time dateTime={iso} title={formatDateTime(iso)} className="whitespace-nowrap">
      {formatRelative(iso)}
    </time>
  ) : (
    <span className="text-muted-foreground">{never}</span>
  );
}

const EMPTY: Repository[] = [];
const features = tableFeatures({});
const col = createColumnHelper<typeof features, Repository>();

function buildColumns(canManage: boolean) {
  const base = [
    col.accessor("name", {
      header: "Repository",
      cell: ({ row }) => {
        const r = row.original;
        return (
          <div className="min-w-36 space-y-1">
            <div className="flex flex-wrap items-center gap-1.5">
              <Link href={`/repositories/${r.id}`} className="font-medium underline-offset-4 hover:underline">
                {r.name}
              </Link>
              {r.is_default && <DefaultBadge />}
              {r.is_system && <SystemBadge />}
            </div>
            {r.description && <div className="max-w-48 text-xs whitespace-normal text-muted-foreground">{r.description}</div>}
          </div>
        );
      },
    }),
    col.accessor("status", {
      header: "Status",
      cell: ({ row }) => (
        <div className="space-y-1">
          <RepositoryStatusBadge status={row.original.status} />
          {row.original.status === "pending_deletion" && row.original.delete_after && (
            <div className="text-xs whitespace-nowrap text-muted-foreground">
              Retires {formatDate(row.original.delete_after)}
            </div>
          )}
        </div>
      ),
    }),
    col.accessor("backend", {
      header: "Backend",
      cell: (info) => BACKEND_LABEL[info.getValue()],
    }),
    col.display({
      id: "storage",
      header: "Storage",
      cell: ({ row }) => <StorageHealth repo={row.original} />,
    }),
    col.accessor("cert_sha256", {
      header: "Certificate",
      cell: (info) => <Fingerprint value={info.getValue()} />,
    }),
    col.accessor("last_reindex_at", {
      header: "Last reindex",
      cell: (info) => <Ago iso={info.getValue()} />,
    }),
    col.accessor("last_verified_at", {
      header: "Last verified",
      cell: (info) => <Ago iso={info.getValue()} />,
    }),
  ];
  if (!canManage) return col.columns(base);
  return col.columns([
    ...base,
    col.display({
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) => {
        const r = row.original;
        return (
          <div className="flex flex-col items-stretch gap-1">
            {r.status === "awaiting_escrow" && (
              <Link
                href={`/repositories/${r.id}#escrow`}
                className={buttonVariants({ size: "xs" })}
                aria-label={`Confirm escrow of ${r.name}`}
              >
                Confirm escrow
              </Link>
            )}
            {r.status === "pending_deletion" ? (
              <RepositoryUndeleteButton repo={r} />
            ) : (
              <>
                <ReindexButton repo={r} />
                {r.status === "ready" && <VerifyButton repositoryId={r.id} label={r.name} size="xs" />}
              </>
            )}
            <DownloadEscrowButton repo={r} />
          </div>
        );
      },
    }),
  ]);
}

function RepositoriesTable({ canManage }: { canManage: boolean }) {
  const repos = useRepositories();
  const columns = useMemo(() => buildColumns(canManage), [canManage]);
  const data = repos.data ?? EMPTY;
  const table = useTable({ features, columns, data, getRowId: (row) => row.id });

  if (repos.isPending) return <RowsSkeleton label="Loading Repositories…" rows={3} />;
  if (repos.isError && !repos.data) {
    return <QueryError title="Could not load Repositories" error={repos.error} onRetry={() => void repos.refetch()} />;
  }

  return (
    <div className="space-y-3">
      <div className="rounded-lg border">
        <Table aria-label="Repositories">
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
                  No Repositories yet.{canManage ? " Register escrow recipients, then use “Create Repository”." : ""}
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row) => (
                <TableRow key={row.id} data-status={row.original.status}>
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
        Storage health refreshes every {REPOSITORIES_REFRESH_MS / 1000} s.
        {repos.isError && " The last refresh failed; showing earlier data."}
      </p>
    </div>
  );
}

function EscrowHealthSection({ canManage }: { canManage: boolean }) {
  const repos = useRepositories();
  return <EscrowHealthPanel repositories={repos.data ?? EMPTY} canManage={canManage} />;
}

export function RepositoriesView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_REPOSITORY_READ);
  const canManage = hasPermission(me, PERMISSION_REPOSITORY_MANAGE);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Repositories</h1>
          <p className="text-sm text-muted-foreground">
            Kopia repository servers that hold recovery points, their key escrow and storage health.
          </p>
        </div>
        {canRead && canManage && <CreateRepositoryDialog />}
      </div>
      {canRead ? (
        <>
          <RepositoriesTable canManage={canManage} />
          <EscrowHealthSection canManage={canManage} />
          <EscrowRecipients canManage={canManage} />
          <EscrowDrills canManage={canManage} />
        </>
      ) : (
        <AccessDenied what="Viewing Repositories" permission={PERMISSION_REPOSITORY_READ} />
      )}
    </div>
  );
}

// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon, InfoIcon, KeyRoundIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { CopyButton } from "@/components/common/copy-button";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import {
  DownloadEscrowButton,
  EscrowConfirmForm,
  EscrowInstructions,
  ReindexButton,
} from "@/components/repositories/repository-actions";
import {
  BACKEND_LABEL,
  CapacityBar,
  DefaultBadge,
  Fingerprint,
  RepositoryStatusBadge,
} from "@/components/repositories/repository-badges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { isApiError } from "@/lib/api/client";
import { PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import {
  PERMISSION_BACKUP_READ,
  PERMISSION_REPOSITORY_MANAGE,
  PERMISSION_REPOSITORY_READ,
  type HostUsage,
  type Repository,
} from "@/lib/api/protection-schemas";
import { useRepository } from "@/lib/api/hooks";
import { dash, formatBytes, formatDateTime, formatRelative } from "@/lib/format";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,11rem)_1fr] gap-3 py-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

const mono = (s: string) => <span className="font-mono text-xs break-all">{s}</span>;

function RepositoryCard({ repo }: { repo: Repository }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Repository</h2>
        </CardTitle>
        <CardDescription>Connection and escrow details.</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="divide-y text-sm">
          <Field label="Status">
            <RepositoryStatusBadge status={repo.status} />
          </Field>
          <Field label="Backend">{BACKEND_LABEL[repo.backend]}</Field>
          <Field label="Default">{repo.is_default ? "Yes" : "No"}</Field>
          <Field label="Server URL">{mono(repo.server_url)}</Field>
          <Field label="Internal server URL">
            {repo.internal_server_url ? (
              mono(repo.internal_server_url)
            ) : (
              <span className="text-muted-foreground">Same as the server URL</span>
            )}
          </Field>
          <Field label="Management URL">{mono(repo.management_url)}</Field>
          <Field label="Certificate">
            <Fingerprint value={repo.cert_sha256} />
          </Field>
          <Field label="Kopia repository ID">{repo.kopia_repository_id ? mono(repo.kopia_repository_id) : "—"}</Field>
          <Field label="Splitter">{dash(repo.splitter)}</Field>
          <Field label="Escrow recipients">{repo.escrow_recipients}</Field>
          <Field label="Escrow generated">{formatDateTime(repo.escrow_generated_at)}</Field>
          <Field label="Escrow confirmed">
            {repo.escrow_confirmed_at ? formatDateTime(repo.escrow_confirmed_at) : "Not confirmed"}
          </Field>
          <Field label="Last reindex">
            {repo.last_reindex_at
              ? `${formatRelative(repo.last_reindex_at)} (${formatDateTime(repo.last_reindex_at)})`
              : "Never"}
          </Field>
          <Field label="Created">{formatDateTime(repo.created_at)}</Field>
          <Field label="Repository ID">
            <span className="inline-flex flex-wrap items-center gap-1.5">
              {mono(repo.id)}
              <CopyButton value={repo.id} label="Repository ID" />
            </span>
          </Field>
        </dl>
      </CardContent>
    </Card>
  );
}

function LiveCard({ repo }: { repo: Repository }) {
  const live = repo.live;
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Reposerver</h2>
        </CardTitle>
        <CardDescription>Live status reported by the reposerver management API.</CardDescription>
      </CardHeader>
      <CardContent>
        {!live ? (
          <Alert variant="destructive">
            <AlertTitle>Reposerver unreachable</AlertTitle>
            <AlertDescription>{repo.live_error || "No status was returned."}</AlertDescription>
          </Alert>
        ) : (
          <div className="space-y-4">
            <CapacityBar used={live.storage_used_bytes} total={live.storage_total_bytes} />
            <dl className="divide-y text-sm">
              <Field label="Storage">
                {live.storage_healthy ? (
                  <span className="text-emerald-700 dark:text-emerald-400">Healthy</span>
                ) : (
                  <span className="text-destructive">Unhealthy</span>
                )}
              </Field>
              {live.storage_error && (
                <Field label="Storage error">
                  <span className="text-destructive">{live.storage_error}</span>
                </Field>
              )}
              <Field label="Free">{formatBytes(live.storage_free_bytes)}</Field>
              <Field label="Kopia server">{live.server_running ? "Running" : "Stopped"}</Field>
              <Field label="Initialized">{live.initialized ? "Yes" : "No"}</Field>
              <Field label="Kopia version">{dash(live.kopia_version)}</Field>
            </dl>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/** Largest first, with a proportional bar. */
export function sortUsage(items: HostUsage[]): HostUsage[] {
  return [...items].sort((a, b) => b.latest_bytes - a.latest_bytes || a.hostname.localeCompare(b.hostname));
}

const USAGE_NOTE =
  "Logical size of each application's latest recovery point, summed per host. Deduplicated physical storage is shared between hosts and recovery points, so it cannot be attributed and these figures do not add up to the storage used.";

function UsageCard({ repo }: { repo: Repository }) {
  const me = useCurrentUser();
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const items = sortUsage(repo.usage_by_host);
  const max = Math.max(1, ...items.map((u) => u.latest_bytes));
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2 className="inline-flex items-center gap-1.5">
            Usage by host
            <span title={USAGE_NOTE} className="text-muted-foreground">
              <InfoIcon aria-hidden="true" className="size-4" />
              <span className="sr-only">About these figures</span>
            </span>
          </h2>
        </CardTitle>
        <CardDescription>{USAGE_NOTE}</CardDescription>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No recovery points in this Repository yet.</p>
        ) : (
          <ul className="space-y-3" aria-label="Usage by host">
            {items.map((u) => (
              <li key={u.host_id} className="space-y-1">
                <div className="flex flex-wrap items-baseline justify-between gap-2 text-sm">
                  <span>
                    {canHosts ? (
                      <Link href={`/hosts/${u.host_id}`} className="font-medium underline-offset-4 hover:underline">
                        {u.hostname}
                      </Link>
                    ) : (
                      <span className="font-medium">{u.hostname}</span>
                    )}{" "}
                    <span className="text-xs text-muted-foreground">
                      {u.applications} application{u.applications === 1 ? "" : "s"}
                    </span>
                  </span>
                  <span className="tabular-nums" title="Logical size, before deduplication">
                    {formatBytes(u.latest_bytes)}
                  </span>
                </div>
                <div aria-hidden="true" className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary/70"
                    style={{ width: `${Math.max(2, Math.round((u.latest_bytes / max) * 100))}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function EscrowCard({ repo }: { repo: Repository }) {
  return (
    <Card id="escrow" className="scroll-mt-4 border-amber-500/60">
      <CardHeader>
        <CardTitle>
          <h2 className="inline-flex items-center gap-2">
            <KeyRoundIcon aria-hidden="true" className="size-4 text-amber-600" /> Confirm key escrow
          </h2>
        </CardTitle>
        <CardDescription>
          This Repository is not usable until the escrow confirmation code is entered.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <EscrowInstructions />
        <DownloadEscrowButton repo={repo} size="sm" />
        <EscrowConfirmForm repo={repo} />
      </CardContent>
    </Card>
  );
}

function RepositoryDetailBody({ id }: { id: string }) {
  const me = useCurrentUser();
  const repo = useRepository(id);
  const canManage = hasPermission(me, PERMISSION_REPOSITORY_MANAGE);
  const canBackups = hasPermission(me, PERMISSION_BACKUP_READ);

  if (repo.isPending) return <RowsSkeleton label="Loading Repository…" />;
  if (repo.isError && !repo.data) {
    if (isApiError(repo.error) && repo.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Repository not found</AlertTitle>
          <AlertDescription>It may have been removed.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the Repository" error={repo.error} onRetry={() => void repo.refetch()} />;
  }
  const r = repo.data;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-semibold tracking-tight">{r.name}</h1>
          {r.description && <p className="text-sm text-muted-foreground">{r.description}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <RepositoryStatusBadge status={r.status} />
            {r.is_default && <DefaultBadge />}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canBackups && (
            <Link href="/recovery-points" className={buttonVariants({ variant: "outline", size: "sm" })}>
              Recovery points
            </Link>
          )}
          {canManage && (
            <>
              <ReindexButton repo={r} size="sm" />
              {r.status !== "awaiting_escrow" && <DownloadEscrowButton repo={r} size="sm" />}
            </>
          )}
        </div>
      </div>
      {r.status === "awaiting_escrow" &&
        (canManage ? (
          <EscrowCard repo={r} />
        ) : (
          <Alert>
            <KeyRoundIcon aria-hidden="true" className="text-amber-600" />
            <AlertTitle>Escrow not confirmed</AlertTitle>
            <AlertDescription>
              This Repository is not usable until someone with <code>{PERMISSION_REPOSITORY_MANAGE}</code>{" "}
              enters the escrow confirmation code.
            </AlertDescription>
          </Alert>
        ))}
      <div className="grid gap-6 lg:grid-cols-2">
        <RepositoryCard repo={r} />
        <div className="space-y-6">
          <LiveCard repo={r} />
          <UsageCard repo={r} />
        </div>
      </div>
    </div>
  );
}

export function RepositoryDetail({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link href="/repositories" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> All Repositories
      </Link>
      {hasPermission(me, PERMISSION_REPOSITORY_READ) ? (
        <RepositoryDetailBody id={id} />
      ) : (
        <AccessDenied what="Viewing Repositories" permission={PERMISSION_REPOSITORY_READ} />
      )}
    </div>
  );
}

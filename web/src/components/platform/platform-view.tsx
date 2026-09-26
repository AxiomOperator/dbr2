// SPDX-License-Identifier: Apache-2.0
"use client";

// System → Platform protection (Phase 9, ADR-0008): the platform self-backup
// (age-encrypted Platform Recovery Bundle) runs, "Run now", the System
// Repository designation and where the recovery runbook lives.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  BookOpenIcon,
  CircleCheckIcon,
  CircleDashedIcon,
  CircleXIcon,
  PlayIcon,
  ServerCogIcon,
  TriangleAlertIcon,
} from "lucide-react";
import Link from "next/link";
import { z } from "zod";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { CopyButton } from "@/components/common/copy-button";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { DesignateSystemButton } from "@/components/repositories/repository-actions";
import { RepositoryStatusBadge, SystemBadge, truncateFingerprint } from "@/components/repositories/repository-badges";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  PERMISSION_REPOSITORY_MANAGE,
  PLATFORM_RUNBOOK_PATH,
  type PlatformBackup,
  type PlatformBackupState,
} from "@/lib/api/protection-schemas";
import { usePlatformBackups, useRepositories } from "@/lib/api/hooks";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";
import { formatDuration } from "@/lib/restore";

const STATE_LABEL: Record<PlatformBackupState, string> = {
  running: "Running",
  succeeded: "Succeeded",
  partial: "Partial",
  failed: "Failed",
};

export function PlatformBackupStateBadge({ state }: { state: PlatformBackupState }) {
  switch (state) {
    case "succeeded":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> {STATE_LABEL.succeeded}
        </Badge>
      );
    case "running":
      return (
        <Badge variant="outline">
          <CircleDashedIcon aria-hidden="true" className="motion-safe:animate-spin" /> {STATE_LABEL.running}
        </Badge>
      );
    case "partial":
      return (
        <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
          <TriangleAlertIcon aria-hidden="true" /> {STATE_LABEL.partial}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> {STATE_LABEL.failed}
        </Badge>
      );
  }
}

/** The parts of the bundle's plaintext manifest.json the console summarises (never secrets). */
const BundleManifestSchema = z
  .object({
    platform_version: z.string().optional(),
    tables: z.array(z.object({ name: z.string(), rows: z.number().optional() })).nullish(),
    repositories: z
      .array(z.object({ name: z.string().optional(), id: z.string().optional(), missing: z.boolean().optional(), error: z.string().optional() }))
      .nullish(),
  })
  .partial();

/** "platform 0.1.0.0 · 38 tables, 12,480 rows · 3 reposervers (1 missing)", or null. */
export function summarizeBundle(manifest: unknown): string | null {
  const parsed = BundleManifestSchema.safeParse(manifest);
  if (!parsed.success || manifest == null) return null;
  const m = parsed.data;
  const parts: string[] = [];
  if (m.platform_version) parts.push(`platform ${m.platform_version}`);
  if (m.tables?.length) {
    const rows = m.tables.reduce((n, t) => n + (t.rows ?? 0), 0);
    parts.push(`${m.tables.length} tables, ${rows.toLocaleString("en-US")} rows`);
  }
  if (m.repositories) {
    const missing = m.repositories.filter((r) => r.missing).length;
    parts.push(`${m.repositories.length} reposerver${m.repositories.length === 1 ? "" : "s"}${missing ? ` (${missing} missing)` : ""}`);
  }
  return parts.length ? parts.join(" · ") : null;
}

function RunNowButton({ running }: { running: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const run = useMutation({
    mutationFn: api.startPlatformBackup,
    onSuccess: async (res) => {
      toast({
        title: "Platform self-backup started",
        description: (
          <>
            Workflow <code className="font-mono text-xs break-all">{res.workflow_id}</code>.
          </>
        ),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.platformBackups });
    },
    onError: (err) =>
      toast({ title: "Could not start the platform self-backup", description: actionErrorMessage(err), variant: "destructive" }),
  });
  return (
    <Button onClick={() => run.mutate()} disabled={run.isPending || running}>
      <PlayIcon aria-hidden="true" /> {run.isPending ? "Starting…" : running ? "Running…" : "Run now"}
    </Button>
  );
}

function BackupsTable({ items, repoName }: { items: PlatformBackup[]; repoName: (id: string | null) => string }) {
  if (items.length === 0) {
    return <p className="text-sm text-muted-foreground">No platform self-backup has run yet. Use “Run now”.</p>;
  }
  return (
    <div className="rounded-lg border">
      <Table aria-label="Platform backups">
        <TableHeader>
          <TableRow>
            {["Started", "State", "Bundle", "Written to", "Duration", "Contents"].map((h) => (
              <TableHead key={h} scope="col">
                {h}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((b) => {
            const summary = summarizeBundle(b.manifest);
            return (
              <TableRow key={b.id} data-state={b.state}>
                <TableCell className="align-top whitespace-nowrap">
                  <time dateTime={b.started_at} title={formatDateTime(b.started_at)}>
                    {formatRelative(b.started_at)}
                  </time>
                  <div className="text-xs text-muted-foreground">{b.trigger || "—"}</div>
                </TableCell>
                <TableCell className="align-top">
                  <PlatformBackupStateBadge state={b.state} />
                  {b.error && <p className="mt-1 max-w-72 text-xs break-words whitespace-normal text-destructive">{b.error}</p>}
                </TableCell>
                <TableCell className="align-top text-xs">
                  {b.file_name ? <div className="font-mono break-all">{b.file_name}</div> : <span className="text-muted-foreground">—</span>}
                  {b.size_bytes > 0 && <div className="tabular-nums">{formatBytes(b.size_bytes)}</div>}
                  {b.sha256 && (
                    <div className="mt-1 inline-flex items-center gap-1.5">
                      <code className="font-mono" title={b.sha256}>
                        sha256 {truncateFingerprint(b.sha256)}
                      </code>
                      <CopyButton value={b.sha256} label={`SHA-256 of ${b.file_name ?? "the bundle"}`} />
                    </div>
                  )}
                </TableCell>
                <TableCell className="max-w-64 align-top text-xs whitespace-normal">
                  <div>
                    System Repository:{" "}
                    {b.snapshot_id ? (
                      <>
                        {repoName(b.repository_id)} ·{" "}
                        <code className="font-mono" title={b.snapshot_id}>
                          {b.snapshot_id.length > 13 ? `${b.snapshot_id.slice(0, 12)}…` : b.snapshot_id}
                        </code>
                      </>
                    ) : (
                      <span className="text-destructive">not written</span>
                    )}
                  </div>
                  <div className="break-all">
                    Bundle directory:{" "}
                    {b.bundle_path ? <code className="font-mono">{b.bundle_path}</code> : <span className="text-destructive">not written</span>}
                  </div>
                </TableCell>
                <TableCell className="align-top whitespace-nowrap">{formatDuration(b.started_at, b.finished_at) ?? "—"}</TableCell>
                <TableCell className="max-w-56 align-top text-xs whitespace-normal text-muted-foreground">{summary ?? "—"}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

function Body() {
  const backups = usePlatformBackups();
  const repos = useRepositories();
  const repoList = repos.data ?? [];
  const system = repoList.find((r) => r.is_system) ?? null;
  const candidates = repoList.filter((r) => !r.is_system && r.status === "ready");
  const repoName = (id: string | null) => (id ? (repoList.find((r) => r.id === id)?.name ?? id) : "—");
  const items = backups.data ?? [];
  const running = items.some((b) => b.state === "running");
  const latest = items.find((b) => b.state !== "running");

  return (
    <div className="space-y-6">
      <Alert>
        <BookOpenIcon aria-hidden="true" />
        <AlertTitle>What this protects, and how to recover</AlertTitle>
        <AlertDescription>
          <p>
            The Platform Recovery Bundle holds the platform database, <code>DBR2_SECRET_KEY</code>, the internal token
            and every reposerver&apos;s state, encrypted to the escrow recipients. It is written to the System
            Repository and to a bundle directory outside every Repository, so losing the DBR² server never means losing
            the recovery points.
          </p>
          <p className="mt-1">
            Recovery (<code>dbr2 admin restore-platform</code>, with an escrow identity from the safe) is described in
            the runbook <code className="font-mono">{PLATFORM_RUNBOOK_PATH}</code>. Keep a printed copy with the escrow
            identities.
          </p>
        </AlertDescription>
      </Alert>

      {latest && latest.state !== "succeeded" && (
        <Alert variant={latest.state === "failed" ? "destructive" : "default"} className={latest.state === "partial" ? "border-amber-500/60" : undefined}>
          <AlertTitle>The latest platform self-backup {latest.state === "failed" ? "failed" : "is partial"}</AlertTitle>
          <AlertDescription>{latest.error ?? "One target was not written."}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardTitle>
            <h2 className="flex items-center gap-2">
              <ServerCogIcon aria-hidden="true" className="size-4" /> System Repository
            </h2>
          </CardTitle>
          <CardDescription>
            The Repository that receives the platform self-backup. At most one Repository has this role.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          {repos.isPending ? (
            <RowsSkeleton label="Loading Repositories…" rows={2} />
          ) : system ? (
            <div className="flex flex-wrap items-center gap-2" data-testid="system-repository">
              <Link href={`/repositories/${system.id}`} className="font-medium underline-offset-4 hover:underline">
                {system.name}
              </Link>
              <SystemBadge />
              <RepositoryStatusBadge status={system.status} />
            </div>
          ) : (
            <Alert variant="destructive">
              <AlertTitle>No System Repository</AlertTitle>
              <AlertDescription>
                Platform self-backups are only written to the bundle directory. Designate a ready Repository below.
              </AlertDescription>
            </Alert>
          )}
          {candidates.length > 0 && (
            <ul className="space-y-1.5" aria-label="Other ready Repositories">
              {candidates.map((r) => (
                <li key={r.id} className="flex flex-wrap items-center gap-2">
                  <span>{r.name}</span>
                  <DesignateSystemButton repo={r} />
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>
              <h2>Platform backups</h2>
            </CardTitle>
            <CardDescription>
              Newest first. Partial means one target failed or a reposerver&apos;s state could not be exported; one run at
              a time.
            </CardDescription>
          </div>
          <RunNowButton running={running} />
        </CardHeader>
        <CardContent>
          {backups.isPending ? (
            <RowsSkeleton label="Loading platform backups…" rows={3} />
          ) : backups.isError && !backups.data ? (
            <QueryError title="Could not load the platform backups" error={backups.error} onRetry={() => void backups.refetch()} />
          ) : (
            <BackupsTable items={items} repoName={repoName} />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export function PlatformView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Platform protection</h1>
        <p className="text-sm text-muted-foreground">
          DBR² backs itself up: the platform self-backup and its recovery (ADR-0008).
        </p>
      </div>
      {hasPermission(me, PERMISSION_REPOSITORY_MANAGE) ? (
        <Body />
      ) : (
        <AccessDenied what="Viewing platform protection" permission={PERMISSION_REPOSITORY_MANAGE} />
      )}
    </div>
  );
}

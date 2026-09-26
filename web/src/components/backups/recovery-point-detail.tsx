// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon, ChevronRightIcon, HistoryIcon } from "lucide-react";
import Link from "next/link";
import { useState, type ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import {
  ComponentStatusBadge,
  ModeCell,
  RecoveryPointStateBadge,
  RecoveryPointStatusBadge,
} from "@/components/backups/backup-badges";
import { DatabasesCard, describeDatabase, nestComponents, TopologyCard } from "@/components/backups/manifest-sections";
import { CopyButton } from "@/components/common/copy-button";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { isApiError } from "@/lib/api/client";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import {
  ManifestSchema,
  PERMISSION_BACKUP_READ,
  PERMISSION_REPOSITORY_READ,
  type ManifestComponent,
  type RecoveryPoint,
} from "@/lib/api/protection-schemas";
import { useRecoveryPoint } from "@/lib/api/hooks";
import { PERMISSION_RESTORE_EXECUTE } from "@/lib/api/restore-schemas";
import { formatBytes, formatDateTime } from "@/lib/format";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,11rem)_1fr] gap-3 py-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

const mono = (s: string) => <span className="font-mono text-xs break-all">{s}</span>;

const VERIFICATION_LABEL: Record<string, string> = {
  unverified: "Not verified yet",
  verified: "Verified",
  verification_failed: "Verification failed",
};

/** "uid 999 / gid 999 · 0700", or "—". */
export function formatOwner(c: Pick<ManifestComponent, "owner_uid" | "owner_gid" | "mode">): string {
  const parts: string[] = [];
  if (c.owner_uid !== undefined || c.owner_gid !== undefined) {
    parts.push(`${c.owner_uid ?? "?"}:${c.owner_gid ?? "?"}`);
  }
  if (c.mode) parts.push(c.mode);
  return parts.length ? parts.join(" · ") : "—";
}

function SummaryCard({ rp }: { rp: RecoveryPoint }) {
  const me = useCurrentUser();
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const canRepos = hasPermission(me, PERMISSION_REPOSITORY_READ);
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Summary</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-x-8 text-sm lg:grid-cols-2">
          <div className="divide-y">
            <Field label="Application">
              {canApps ? (
                <Link href={`/applications/${rp.application_id}`} className="underline-offset-4 hover:underline">
                  {rp.application_name}
                </Link>
              ) : (
                rp.application_name
              )}
            </Field>
            <Field label="Host">
              {canHosts ? (
                <Link href={`/hosts/${rp.host_id}`} className="underline-offset-4 hover:underline">
                  {rp.hostname}
                </Link>
              ) : (
                rp.hostname
              )}
            </Field>
            <Field label="Repository">
              {canRepos ? (
                <Link href={`/repositories/${rp.repository_id}`} className="font-mono text-xs underline-offset-4 hover:underline">
                  {rp.repository_id}
                </Link>
              ) : (
                mono(rp.repository_id)
              )}
            </Field>
            <Field label="State">
              <span className="inline-flex flex-wrap gap-1.5">
                <RecoveryPointStateBadge state={rp.state} />
                <RecoveryPointStatusBadge status={rp.status} />
              </span>
            </Field>
            <Field label="Verification">{VERIFICATION_LABEL[rp.verification] ?? rp.verification}</Field>
            <Field label="Consistency">
              <ModeCell mode={rp.consistency_mode} crashConsistent={rp.crash_consistent_only} />
            </Field>
          </div>
          <div className="divide-y">
            <Field label="Consistency point">{formatDateTime(rp.consistency_point)}</Field>
            <Field label="Created">{formatDateTime(rp.created_at)}</Field>
            <Field label="Committed">{formatDateTime(rp.committed_at)}</Field>
            <Field label="Size">
              {formatBytes(rp.size_bytes)} · {rp.component_count} component{rp.component_count === 1 ? "" : "s"}
            </Field>
            <Field label="Trigger">{rp.trigger || "—"}</Field>
            <Field label="Workflow">{mono(rp.workflow_id)}</Field>
          </div>
        </dl>
      </CardContent>
    </Card>
  );
}

function ComponentsCard({ components }: { components: ManifestComponent[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>
            Components <span className="font-normal text-muted-foreground">({components.length})</span>
          </h2>
        </CardTitle>
        <CardDescription>
          What the recovery manifest records for each captured component; file-system metadata (fsmeta) is shown
          under the component it belongs to.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {components.length === 0 ? (
          <p className="text-sm text-muted-foreground">The manifest lists no components.</p>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Components">
              <TableHeader>
                <TableRow>
                  {["Name", "Kind", "Required", "Status", "Size", "Files", "Snapshot", "Source", "SELinux context", "Owner"].map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {nestComponents(components).map(({ component: c, depth }) => (
                  <TableRow key={c.name} data-component-status={c.status} data-depth={depth}>
                    <TableCell className="min-w-44 align-top whitespace-normal">
                      <div className={depth ? "flex gap-1.5 pl-4" : undefined}>
                        {depth === 1 && (
                          <span aria-hidden="true" className="text-muted-foreground">
                            └
                          </span>
                        )}
                        <div className="min-w-0">
                          <div className="font-mono text-xs break-all">{c.name}</div>
                          {c.parent && (
                            <div className="text-xs text-muted-foreground">
                              {c.kind === "fsmeta" ? "Ownership, modes and SELinux labels of " : "Part of "}
                              {c.parent}
                            </div>
                          )}
                          {c.database && <div className="text-xs text-muted-foreground">{describeDatabase(c.database)}</div>}
                          {c.error && <div className="mt-1 max-w-64 text-xs break-words text-destructive">{c.error}</div>}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge variant="outline">{c.kind}</Badge>
                    </TableCell>
                    <TableCell className="align-top">{c.required ? "Required" : "Optional"}</TableCell>
                    <TableCell className="align-top">
                      <ComponentStatusBadge status={c.status} />
                    </TableCell>
                    <TableCell className="align-top tabular-nums">{formatBytes(c.size_bytes)}</TableCell>
                    <TableCell className="align-top tabular-nums">{c.files ?? "—"}</TableCell>
                    <TableCell className="align-top">
                      {c.snapshot_id ? (
                        <code className="font-mono text-xs" title={c.snapshot_id}>
                          {c.snapshot_id.length > 13 ? `${c.snapshot_id.slice(0, 12)}…` : c.snapshot_id}
                        </code>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell className="min-w-64 align-top whitespace-normal">
                      {c.snapshot_source ? mono(c.snapshot_source) : c.path ? mono(c.path) : "—"}
                    </TableCell>
                    <TableCell className="min-w-48 align-top whitespace-normal">
                      {c.selinux_context ? mono(c.selinux_context) : "—"}
                    </TableCell>
                    <TableCell className="align-top font-mono text-xs whitespace-nowrap">{formatOwner(c)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ManifestJson({ manifest }: { manifest: unknown }) {
  const [open, setOpen] = useState(false);
  const json = JSON.stringify(manifest, null, 2);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <CollapsibleTrigger asChild>
          <Button variant="ghost" className="-ml-2.5 gap-1.5">
            <ChevronRightIcon
              aria-hidden="true"
              className={open ? "rotate-90 transition-transform" : "transition-transform"}
            />
            Raw manifest (JSON)
          </Button>
        </CollapsibleTrigger>
        {open && <CopyButton value={json} label="manifest JSON" />}
      </div>
      <CollapsibleContent>
        <pre
          data-testid="manifest-json"
          className="max-h-[32rem] overflow-auto rounded-lg border bg-muted/50 p-4 font-mono text-xs"
        >
          {json}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function DetailBody({ id }: { id: string }) {
  const me = useCurrentUser();
  const rp = useRecoveryPoint(id);

  if (rp.isPending) return <RowsSkeleton label="Loading recovery point…" />;
  if (rp.isError) {
    if (isApiError(rp.error) && rp.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Recovery point not found</AlertTitle>
          <AlertDescription>It may have been deleted, or the index was rebuilt.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the recovery point" error={rp.error} onRetry={() => void rp.refetch()} />;
  }

  const d = rp.data;
  const manifest = d.manifest === undefined || d.manifest === null ? null : ManifestSchema.safeParse(d.manifest);
  const manifestFailed =
    manifest?.success === true
      ? manifest.data.components.filter((c) => c.status !== "succeeded" && c.kind !== "fsmeta").map((c) => c.name)
      : [];

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1.5">
          <h1 className="flex flex-wrap items-center gap-2 text-2xl font-semibold tracking-tight">
            Recovery point
            {d.status && (
              <span className="text-base font-normal" data-testid="rp-status">
                <RecoveryPointStatusBadge status={d.status} />
              </span>
            )}
          </h1>
          <p className="inline-flex flex-wrap items-center gap-2 font-mono text-sm text-muted-foreground">
            {d.id}
            <CopyButton value={d.id} label="recovery point ID" />
          </p>
        </div>
        {d.state === "committed" && hasPermission(me, PERMISSION_RESTORE_EXECUTE) && (
          <Link href={`/recovery-points/${d.id}/restore`} className={buttonVariants({ size: "sm" })}>
            <HistoryIcon aria-hidden="true" /> Restore…
          </Link>
        )}
      </div>
      {d.error && (
        <Alert variant="destructive">
          <AlertTitle>{d.state === "failed" ? "Backup failed" : "Errors"}</AlertTitle>
          <AlertDescription>{d.error}</AlertDescription>
        </Alert>
      )}
      {d.status === "partial" && (
        <Alert className="border-amber-500/60">
          <AlertTitle>Partial recovery point</AlertTitle>
          <AlertDescription>
            <p>One or more optional components failed. Every required component was captured.</p>
            {manifestFailed.length > 0 && (
              <p>
                Not captured: <span className="font-mono text-xs">{manifestFailed.join(", ")}</span>
              </p>
            )}
          </AlertDescription>
        </Alert>
      )}
      {d.status === "complete" && (
        <Alert role="status" className="border-emerald-500/50">
          <AlertTitle>Complete recovery point</AlertTitle>
          <AlertDescription>Every component of the backup plan was captured.</AlertDescription>
        </Alert>
      )}
      <SummaryCard rp={d} />
      {manifest === null ? (
        <p className="text-sm text-muted-foreground">
          No manifest is available{d.state === "failed" ? ": the backup failed before it was committed." : "."}
        </p>
      ) : manifest.success ? (
        <>
          <ComponentsCard components={manifest.data.components} />
          <TopologyCard manifest={manifest.data} />
          <DatabasesCard components={manifest.data.components} />
        </>
      ) : (
        <Alert>
          <AlertTitle>Manifest not understood</AlertTitle>
          <AlertDescription>
            The manifest does not match the schema this console knows; the raw document is shown below.
          </AlertDescription>
        </Alert>
      )}
      {d.manifest != null && <ManifestJson manifest={d.manifest} />}
    </div>
  );
}

export function RecoveryPointDetail({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link href="/recovery-points" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> All recovery points
      </Link>
      {hasPermission(me, PERMISSION_BACKUP_READ) ? (
        <DetailBody id={id} />
      ) : (
        <AccessDenied what="Viewing recovery points" permission={PERMISSION_BACKUP_READ} />
      )}
    </div>
  );
}

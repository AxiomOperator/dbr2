// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel } from "@/components/backups/backup-badges";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { DeletePolicyButton, NextRun } from "@/components/policies/policies-view";
import { PolicyEnabledBadge } from "@/components/policies/policy-badges";
import { PolicyFormDialog } from "@/components/policies/policy-form";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { isApiError } from "@/lib/api/client";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import { PERMISSION_POLICY_MANAGE, PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { useAgents, usePolicy, useRepositories } from "@/lib/api/hooks";
import { formatDateTime } from "@/lib/format";
import { describeCron, GFS_EXPLANATION, RETENTION_FIELDS, retentionHorizon, retentionSummary } from "@/lib/schedule";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,11rem)_1fr] gap-3 py-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

function Body({ id }: { id: string }) {
  const me = useCurrentUser();
  const router = useRouter();
  const policy = usePolicy(id);
  const repos = useRepositories();
  const agents = useAgents({ enabled: hasPermission(me, PERMISSION_HOST_READ) });
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);

  if (policy.isPending) return <RowsSkeleton label="Loading policy…" />;
  if (policy.isError && !policy.data) {
    if (isApiError(policy.error) && policy.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Policy not found</AlertTitle>
          <AlertDescription>It may have been deleted.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the policy" error={policy.error} onRetry={() => void policy.refetch()} />;
  }
  const p = policy.data;
  const d = describeCron(p.schedule);
  const repoName = p.repository_id
    ? (repos.data?.find((r) => r.id === p.repository_id)?.name ?? p.repository_id)
    : "Default Repository";
  const hostname = (hostId: string) => agents.data?.find((a) => a.id === hostId)?.hostname ?? hostId;
  const horizon = retentionHorizon(p.retention);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-semibold tracking-tight">{p.name}</h1>
          {p.description && <p className="text-sm text-muted-foreground">{p.description}</p>}
          <PolicyEnabledBadge enabled={p.enabled} />
        </div>
        {canManage && (
          <div className="flex flex-wrap gap-2">
            <PolicyFormDialog policy={p} size="sm" />
            <DeletePolicyButton policy={p} size="sm" onDeleted={() => router.push("/policies")} />
          </div>
        )}
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>
              <h2>Schedule</h2>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="divide-y text-sm">
              <Field label="When">{d.ok ? d.text : "Custom schedule"}</Field>
              <Field label="Cron">
                <code className="font-mono text-xs">{p.schedule}</code>
              </Field>
              <Field label="Timezone">{p.timezone}</Field>
              <Field label="Next run">
                <NextRun policy={p} />
                {p.next_run && <span className="block text-xs text-muted-foreground">{formatDateTime(p.next_run)}</span>}
              </Field>
              <Field label="Consistency mode">
                {p.consistency_mode ? modeLabel(p.consistency_mode) : "Application setting"}
              </Field>
              <Field label="Repository">{repoName}</Field>
              <Field label="Changed">{formatDateTime(p.updated_at)}</Field>
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              <h2>Retention</h2>
            </CardTitle>
            <CardDescription>{GFS_EXPLANATION}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <p>
              <strong>{retentionSummary(p.retention)}</strong>
              {horizon ? `, reaching back ${horizon}` : ""}.
            </p>
            <dl className="divide-y">
              {RETENTION_FIELDS.map((f) => (
                <Field key={f.key} label={f.label}>
                  <span className="tabular-nums">{p.retention[f.key]}</span>{" "}
                  <span className="text-muted-foreground">{f.unit}</span>
                </Field>
              ))}
            </dl>
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>
            <h2>
              Assigned applications <span className="font-normal text-muted-foreground">({p.assigned_applications.length})</span>
            </h2>
          </CardTitle>
          <CardDescription>
            Assign or remove an application in its Backup settings tab. Each application follows at most one policy.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {p.assigned_applications.length === 0 ? (
            <p className="text-sm text-muted-foreground">No application is assigned: this policy schedules nothing.</p>
          ) : (
            <div className="rounded-lg border">
              <Table aria-label="Assigned applications">
                <TableHeader>
                  <TableRow>
                    <TableHead scope="col">Application</TableHead>
                    <TableHead scope="col">Host</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {p.assigned_applications.map((a) => (
                    <TableRow key={a.id}>
                      <TableCell>
                        {canApps ? (
                          <Link href={`/applications/${a.id}`} className="font-medium underline-offset-4 hover:underline">
                            {a.name}
                          </Link>
                        ) : (
                          a.name
                        )}
                      </TableCell>
                      <TableCell>{hostname(a.host_id)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export function PolicyDetail({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link href="/policies" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> All policies
      </Link>
      {hasPermission(me, PERMISSION_POLICY_READ) ? (
        <Body id={id} />
      ) : (
        <AccessDenied what="Viewing protection policies" permission={PERMISSION_POLICY_READ} />
      )}
    </div>
  );
}

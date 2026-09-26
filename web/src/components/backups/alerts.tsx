// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useId, useState } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { SeverityBadge, SEVERITY_LABEL } from "@/components/backups/backup-badges";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { actionErrorMessage, errorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import {
  ALERT_SEVERITIES,
  PERMISSION_BACKUP_EXECUTE,
  PERMISSION_BACKUP_READ,
  PERMISSION_REPOSITORY_READ,
  type Alert,
  type AlertSeverity,
} from "@/lib/api/protection-schemas";
import { BACKUPS_REFRESH_MS, useAlerts } from "@/lib/api/hooks";
import type { Me } from "@/lib/api/schemas";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

const SEVERITY_RANK: Record<AlertSeverity, number> = { critical: 0, warning: 1, info: 2 };

/** Open (unacknowledged) alerts per severity. */
export function countBySeverity(alerts: Alert[]): Record<AlertSeverity, number> {
  const out: Record<AlertSeverity, number> = { critical: 0, warning: 0, info: 0 };
  for (const a of alerts) if (!a.acknowledged_at) out[a.severity]++;
  return out;
}

/** Most severe first, then newest first. */
export function sortAlerts(alerts: Alert[]): Alert[] {
  return [...alerts].sort(
    (a, b) =>
      SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity] ||
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
}

function recoveryPointOf(a: Alert): string | null {
  const d = a.details;
  if (d && typeof d === "object" && "recovery_point_id" in d) {
    const v = (d as Record<string, unknown>).recovery_point_id;
    if (typeof v === "string" && v) return v;
  }
  return null;
}

/** Link to the alert's target, when the user may open it. */
export function alertTargetHref(a: Alert, me: Me): string | null {
  const rp = recoveryPointOf(a);
  if (rp && hasPermission(me, PERMISSION_BACKUP_READ)) return `/recovery-points/${encodeURIComponent(rp)}`;
  if (!a.target_id) return null;
  const id = encodeURIComponent(a.target_id);
  switch (a.target_type) {
    case "application":
      return hasPermission(me, PERMISSION_APPLICATION_READ) ? `/applications/${id}` : null;
    case "repository":
      return hasPermission(me, PERMISSION_REPOSITORY_READ) ? `/repositories/${id}` : null;
    case "agent":
    case "host":
      return hasPermission(me, PERMISSION_HOST_READ) ? `/hosts/${id}` : null;
    default:
      return null;
  }
}

function AcknowledgeButton({ alert }: { alert: Alert }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const ack = useMutation({
    mutationFn: () => api.acknowledgeAlert(alert.id),
    onSuccess: async () => {
      toast({ title: "Alert acknowledged" });
      await queryClient.invalidateQueries({ queryKey: queryKeys.alertsAll });
    },
    onError: (err) =>
      toast({ title: "Could not acknowledge the alert", description: actionErrorMessage(err), variant: "destructive" }),
  });
  return (
    <Button
      size="xs"
      variant="outline"
      onClick={() => ack.mutate()}
      disabled={ack.isPending}
      aria-label={`Acknowledge alert: ${alert.message}`}
    >
      {ack.isPending ? "Acknowledging…" : "Acknowledge"}
    </Button>
  );
}

function AlertsTable({ showAcknowledged }: { showAcknowledged: boolean }) {
  const me = useCurrentUser();
  const canAck = hasPermission(me, PERMISSION_BACKUP_EXECUTE);
  const alerts = useAlerts(showAcknowledged);

  if (alerts.isPending) return <RowsSkeleton label="Loading alerts…" rows={4} />;
  if (alerts.isError && !alerts.data) {
    return <QueryError title="Could not load alerts" error={alerts.error} onRetry={() => void alerts.refetch()} />;
  }
  const items = sortAlerts(alerts.data);

  return (
    <div className="space-y-3">
      <div className="rounded-lg border">
        <Table aria-label="Alerts">
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Severity</TableHead>
              <TableHead scope="col">Alert</TableHead>
              <TableHead scope="col">Raised</TableHead>
              <TableHead scope="col">Acknowledged</TableHead>
              {canAck && (
                <TableHead scope="col">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.length === 0 ? (
              <TableRow>
                <TableCell colSpan={canAck ? 5 : 4} className="h-20 text-center text-muted-foreground">
                  {showAcknowledged ? "No alerts." : "No open alerts."}
                </TableCell>
              </TableRow>
            ) : (
              items.map((a) => {
                const href = alertTargetHref(a, me);
                return (
                  <TableRow key={a.id} data-severity={a.severity} className={cn(a.acknowledged_at && "opacity-70")}>
                    <TableCell className="align-top">
                      <SeverityBadge severity={a.severity} />
                    </TableCell>
                    <TableCell className="align-top whitespace-normal">
                      <div className="max-w-xl">{a.message}</div>
                      <div className="text-xs text-muted-foreground">
                        <span className="font-mono">{a.type}</span>
                        {href && (
                          <>
                            {" · "}
                            <Link href={href} className="underline underline-offset-4">
                              View {recoveryPointOf(a) ? "recovery point" : a.target_type}
                            </Link>
                          </>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="align-top whitespace-nowrap">
                      <time dateTime={a.created_at} title={formatDateTime(a.created_at)}>
                        {formatRelative(a.created_at)}
                      </time>
                    </TableCell>
                    <TableCell className="align-top whitespace-nowrap">
                      {a.acknowledged_at ? formatDateTime(a.acknowledged_at) : <span className="text-muted-foreground">Open</span>}
                    </TableCell>
                    {canAck && (
                      <TableCell className="text-right align-top">
                        {!a.acknowledged_at && <AcknowledgeButton alert={a} />}
                      </TableCell>
                    )}
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        Refreshes every {BACKUPS_REFRESH_MS / 1000} s.
        {alerts.isError && " The last refresh failed; showing earlier data."}
      </p>
    </div>
  );
}

/** The alert list with the "show acknowledged" switch (Notifications → Alerts tab). */
export function AlertsPanel() {
  const id = useId();
  const me = useCurrentUser();
  const [showAcknowledged, setShowAcknowledged] = useState(false);
  if (!hasPermission(me, PERMISSION_BACKUP_READ)) {
    return <AccessDenied what="Viewing alerts" permission={PERMISSION_BACKUP_READ} />;
  }
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Alerts raised by DBR²: backup failures, partial recovery points, violated Recovery Contracts, verification
        failures, escrow and platform self-backup problems, quiesce warnings and agent auto-resumes. New alerts also
        pop up live while the console is open.
      </p>
      <div className="flex items-center gap-2">
        <Checkbox
          id={`${id}-all`}
          checked={showAcknowledged}
          onCheckedChange={(v) => setShowAcknowledged(v === true)}
        />
        <Label htmlFor={`${id}-all`} className="font-normal">
          Show acknowledged alerts
        </Label>
      </div>
      <AlertsTable showAcknowledged={showAcknowledged} />
    </div>
  );
}

/** Alerts on their own page (the Notifications page adds channels and SMTP as tabs). */
export function AlertsView() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Notifications</h1>
      </div>
      <AlertsPanel />
    </div>
  );
}

/** Dashboard card: open alerts by severity and the latest few (requires `backup.read`). */
export function AlertsCard() {
  const alerts = useAlerts(false);
  const open = alerts.data?.filter((a) => !a.acknowledged_at) ?? [];
  const counts = countBySeverity(open);
  const latest = [...open]
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, 3);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Alerts</CardTitle>
        <CardDescription>Open backup and agent alerts.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {alerts.isPending ? (
          <div className="space-y-2" aria-busy="true">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : alerts.isError ? (
          <p className="text-sm text-destructive" role="alert">
            Alerts unavailable: {errorMessage(alerts.error)}
          </p>
        ) : (
          <>
            <ul className="grid grid-cols-3 gap-2" aria-label="Open alerts by severity">
              {ALERT_SEVERITIES.map((s) => (
                <li
                  key={s}
                  className={cn(
                    "rounded-md border p-2 text-center",
                    s === "critical" && counts.critical > 0 && "border-destructive/60 bg-destructive/5",
                  )}
                >
                  <div
                    className={cn(
                      "text-xl font-semibold tabular-nums",
                      s === "critical" && counts.critical > 0 && "text-destructive",
                      s === "warning" && counts.warning > 0 && "text-amber-700 dark:text-amber-400",
                    )}
                  >
                    {counts[s]}
                  </div>
                  <div className="text-xs text-muted-foreground">{SEVERITY_LABEL[s]}</div>
                </li>
              ))}
            </ul>
            {latest.length === 0 ? (
              <p className="text-sm text-muted-foreground">No open alerts.</p>
            ) : (
              <ul className="space-y-2" aria-label="Latest alerts">
                {latest.map((a) => (
                  <li key={a.id} className="flex items-start gap-2 text-sm">
                    <SeverityBadge severity={a.severity} />
                    <span className="min-w-0 flex-1">
                      <span className="line-clamp-2">{a.message}</span>
                      <span className="text-xs text-muted-foreground">{formatRelative(a.created_at)}</span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
            <Link href="/notifications" className={buttonVariants({ variant: "outline", size: "sm" })}>
              View all alerts
            </Link>
          </>
        )}
      </CardContent>
    </Card>
  );
}

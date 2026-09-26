// SPDX-License-Identifier: Apache-2.0
"use client";

import { CircleCheckIcon, CircleMinusIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel, RecoveryPointStatusBadge } from "@/components/backups/backup-badges";
import { ApplicationJobProgress } from "@/components/live/job-progress";
import {
  CoverageText,
  PROTECTION_STATUS_DESCRIPTION,
  ProtectionStatusBadge,
  RunningBadge,
} from "@/components/protection/protection-badges";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ComponentCoverage, Protection } from "@/lib/api/fleet-schemas";
import { PERMISSION_BACKUP_READ } from "@/lib/api/protection-schemas";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";

const KIND_LABEL: Record<ComponentCoverage["kind"], string> = {
  config: "Configuration",
  volume: "Volume",
  bind_mount: "Bind mount",
  database: "Database",
};

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

function When({ iso }: { iso: string | null | undefined }) {
  if (!iso) return <span className="text-muted-foreground">Never</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)}>
      {formatRelative(iso)}
    </time>
  );
}

/** Coverage of each component the application has now by the latest recovery point. */
export function CoverageTable({ components }: { components: ComponentCoverage[] }) {
  if (components.length === 0) {
    return <p className="text-sm text-muted-foreground">No backup components (the application is not in the latest inventory).</p>;
  }
  return (
    <div className="rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead scope="col">Component</TableHead>
            <TableHead scope="col">Kind</TableHead>
            <TableHead scope="col">Required</TableHead>
            <TableHead scope="col">In latest recovery point</TableHead>
            <TableHead scope="col" className="text-right">
              Last size
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {components.map((c) => (
            <TableRow key={c.name} data-protected={c.protected}>
              <TableCell className="font-mono text-xs break-all whitespace-normal">{c.name}</TableCell>
              <TableCell>{KIND_LABEL[c.kind] ?? c.kind}</TableCell>
              <TableCell>{c.required ? "Required" : <Badge variant="outline">Optional</Badge>}</TableCell>
              <TableCell>
                {c.protected ? (
                  <span className="inline-flex items-center gap-1.5 text-emerald-700 dark:text-emerald-400">
                    <CircleCheckIcon aria-hidden="true" className="size-4" /> Protected
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
                    <CircleMinusIcon aria-hidden="true" className="size-4" /> Not protected
                  </span>
                )}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {c.protected ? formatBytes(c.last_size_bytes) : <span className="text-muted-foreground">—</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/** Application detail: protection status, reasons, last backup and coverage (API-computed). */
export function ProtectionCard({ applicationId, protection }: { applicationId: string; protection: Protection }) {
  const me = useCurrentUser();
  const canBackups = hasPermission(me, PERMISSION_BACKUP_READ);
  const p = protection;
  const rpLink = (id: string, label: ReactNode) =>
    canBackups ? (
      <Link href={`/recovery-points/${id}`} className="underline-offset-4 hover:underline">
        {label}
      </Link>
    ) : (
      label
    );

  return (
    <Card data-testid="protection-card">
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          Protection <ProtectionStatusBadge status={p.status} />
          <RunningBadge running={p.running} />
        </CardTitle>
        <CardDescription>{PROTECTION_STATUS_DESCRIPTION[p.status]}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {(p.running === "backup" || p.running === "restore") && (
          <div className="rounded-lg border border-sky-500/40 p-3">
            <ApplicationJobProgress applicationId={applicationId} running={p.running} />
          </div>
        )}
        {p.reasons.length > 0 && (
          <ul className="list-inside list-disc space-y-1 text-sm" aria-label="Reasons">
            {p.reasons.map((r) => (
              <li key={r}>{r.charAt(0).toUpperCase() + r.slice(1)}</li>
            ))}
          </ul>
        )}
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Meta label="Last backup">
            {p.last_backup_at && p.last_recovery_point_id ? rpLink(p.last_recovery_point_id, <When iso={p.last_backup_at} />) : <When iso={p.last_backup_at} />}
          </Meta>
          <Meta label="Latest recovery point">
            {p.last_status ? (
              <span className="inline-flex flex-wrap items-center gap-1.5">
                <RecoveryPointStatusBadge status={p.last_status} />
                {p.last_mode && <span className="text-muted-foreground">{modeLabel(p.last_mode)}</span>}
              </span>
            ) : (
              <span className="text-muted-foreground">None</span>
            )}
          </Meta>
          <Meta label="Last attempt">
            <span className="inline-flex flex-wrap items-center gap-1.5">
              <When iso={p.last_attempt_at} />
              {p.last_attempt_state && <span className="text-muted-foreground">({p.last_attempt_state})</span>}
            </span>
          </Meta>
          <Meta label="Coverage">
            <CoverageText protection={p} className="text-sm" />
          </Meta>
        </dl>
        {p.last_error && (
          <p className="rounded-md border border-destructive/40 p-2 text-sm text-destructive">
            <span className="font-medium">Last error:</span> {p.last_error}
          </p>
        )}
        <CoverageTable components={p.components} />
        {p.unresolved_dependencies > 0 && (
          <p className="text-sm text-amber-700 dark:text-amber-400">
            {p.unresolved_dependencies} external{" "}
            {p.unresolved_dependencies === 1 ? "dependency is" : "dependencies are"} not protected by DBR² (see
            Dependencies under Resources): restoring needs them to exist.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

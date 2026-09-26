// SPDX-License-Identifier: Apache-2.0
"use client";

import { CalendarClockIcon, FlaskConicalIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied } from "@/components/common/states";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_READ } from "@/lib/api/restore-schemas";

/** An honest "not available yet" page: what arrives, in which phase, and what works today. */
function ComingSoon({
  title,
  lead,
  phase,
  icon,
  arrives,
  today,
}: {
  title: string;
  lead: string;
  phase: string;
  icon: ReactNode;
  arrives: string[];
  today: ReactNode;
}) {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="text-sm text-muted-foreground">{lead}</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {icon} Not available yet: arrives in {phase}
          </CardTitle>
          <CardDescription>
            The DBR² API does not offer this yet, so there is nothing to show here. This page lists what is planned
            (see the roadmap) instead of sample data.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4 text-sm">
          <div>
            <h2 className="mb-1.5 font-medium">Planned</h2>
            <ul className="list-inside list-disc space-y-1 text-muted-foreground">
              {arrives.map((a) => (
                <li key={a}>{a}</li>
              ))}
            </ul>
          </div>
          <div>
            <h2 className="mb-1.5 font-medium">Available today</h2>
            <div className="text-muted-foreground">{today}</div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

const link = "underline underline-offset-4 text-foreground";

/** Protection → Policies (Phase 7). */
export function PoliciesPlaceholder() {
  const me = useCurrentUser();
  if (!hasPermission(me, PERMISSION_POLICY_READ)) {
    return <AccessDenied what="Viewing protection policies" permission={PERMISSION_POLICY_READ} />;
  }
  return (
    <ComingSoon
      title="Policies"
      lead="Protection Policies: when applications are backed up and how long recovery points are kept."
      phase="Phase 7 (scheduling, retention & notifications)"
      icon={<CalendarClockIcon aria-hidden="true" className="size-5" />}
      arrives={[
        "Schedules (cron, hourly, daily, weekly, monthly) run as Temporal schedules; overlapping runs are skipped and recorded",
        "Retention at recovery-point level, with a deletion grace period (default 7 days) for manual deletions",
        "Consistency mode and target Repositories per policy",
        "Recovery Contract: maximum RPO and required components, reported as Satisfied or Violated",
        "Notifications by email and webhook (backup failed or missed, restore finished, agent offline, RPO violated…)",
      ]}
      today={
        <>
          Back up on demand with “Back up now” on an{" "}
          <Link className={link} href="/applications">
            application
          </Link>
          ; its consistency mode, hooks and optional / excluded components are under the application&apos;s Backup
          settings tab, and each host&apos;s backup window and concurrency under{" "}
          <Link className={link} href="/hosts">
            Hosts
          </Link>
          .
        </>
      }
    />
  );
}

/** Recovery → Restore Testing (Phase 9). */
export function RestoreTestingPlaceholder() {
  const me = useCurrentUser();
  if (!hasPermission(me, PERMISSION_RESTORE_READ)) {
    return <AccessDenied what="Viewing restore testing" permission={PERMISSION_RESTORE_READ} />;
  }
  return (
    <ComingSoon
      title="Restore Testing"
      lead="Proving that recovery points can actually be restored."
      phase="Phase 9 (verification & platform self-protection)"
      icon={<FlaskConicalIcon aria-hidden="true" className="size-5" />}
      arrives={[
        "Repository integrity verification (repository/{id}/verify)",
        "Recovery point verification state: Unverified, Verified, Verification Failed",
        "Escrow health checks and the annual escrow drill reminder",
        "Platform self-backup and a tested platform recovery (dbr2 admin restore-platform)",
      ]}
      today={
        <>
          Test a recovery by{" "}
          <Link className={link} href="/restores/new">
            restoring a recovery point
          </Link>{" "}
          to a spare host (for example a disaster-recovery standby): alternate-host restores never touch the source
          application. Results appear under{" "}
          <Link className={link} href="/restores">
            Restore
          </Link>
          .
        </>
      }
    />
  );
}

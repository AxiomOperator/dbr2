// SPDX-License-Identifier: Apache-2.0
"use client";

import { FlaskConicalIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied } from "@/components/common/states";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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

/** Recovery → Restore Testing (roadmap: later). */
export function RestoreTestingPlaceholder() {
  const me = useCurrentUser();
  if (!hasPermission(me, PERMISSION_RESTORE_READ)) {
    return <AccessDenied what="Viewing restore testing" permission={PERMISSION_RESTORE_READ} />;
  }
  return (
    <ComingSoon
      title="Restore Testing"
      lead="Proving that recovery points can actually be restored."
      phase="a later release (see the roadmap)"
      icon={<FlaskConicalIcon aria-hidden="true" className="size-5" />}
      arrives={[
        "Automatic, scheduled restore tests into an isolated sandbox host, destroyed afterwards",
        "Evidence per test: health-check output and the measured restore time (RTO)",
        "Results update the recovery point's verification state and the Recovery Contract",
      ]}
      today={
        <>
          Every Repository is verified weekly (objects present, a share of the files read back and hash-checked);
          start one with “Verify now” on{" "}
          <Link className={link} href="/repositories">
            Repositories
          </Link>
          , and see each recovery point&apos;s verification state under{" "}
          <Link className={link} href="/recovery-points">
            Recovery Points
          </Link>
          . Escrow drills live on the Repositories page too. To test a full recovery,{" "}
          <Link className={link} href="/restores/new">
            restore a recovery point
          </Link>{" "}
          to a spare host: alternate-host restores never touch the source application.
        </>
      }
    />
  );
}

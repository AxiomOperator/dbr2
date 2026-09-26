// SPDX-License-Identifier: Apache-2.0
"use client";

import { ExternalLinkIcon } from "lucide-react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AlertsCard } from "@/components/backups/alerts";
import { PlatformStatusCard } from "@/components/dashboard/platform-status-card";
import { ProtectionOverviewCard } from "@/components/dashboard/protection-overview-card";
import { RecentRestoresCard } from "@/components/restores/restores-view";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import { PERMISSION_BACKUP_READ } from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_READ } from "@/lib/api/restore-schemas";
import { API_DOCS_PATH } from "@/lib/version";

export function Dashboard() {
  const me = useCurrentUser();

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Welcome, {me.display_name}</h1>
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <span>Roles:</span>
          {me.roles.length === 0 ? (
            <span>none assigned</span>
          ) : (
            <ul className="flex flex-wrap gap-1.5" aria-label="Your roles">
              {me.roles.map((role) => (
                <li key={role}>
                  <Badge variant="secondary">{role}</Badge>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Getting started</CardTitle>
            <CardDescription>Enroll hosts, review what discovery found, then back up.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <p>
              Add your Docker hosts under <strong>Hosts</strong>, approve them, and DBR² discovers
              their applications, including data that no volume protects. Then create a
              Repository (with two escrow recipients), back applications up, and restore a
              recovery point in place or to another host.
            </p>
            <a
              href={API_DOCS_PATH}
              target="_blank"
              rel="noopener noreferrer"
              className={buttonVariants({ variant: "outline" })}
            >
              Open API docs <ExternalLinkIcon aria-hidden="true" />
              <span className="sr-only">(opens in a new tab)</span>
            </a>
          </CardContent>
        </Card>
        <PlatformStatusCard />
        {hasPermission(me, PERMISSION_APPLICATION_READ) && <ProtectionOverviewCard />}
        {hasPermission(me, PERMISSION_BACKUP_READ) && <AlertsCard />}
        {hasPermission(me, PERMISSION_RESTORE_READ) && <RecentRestoresCard />}
      </div>
    </div>
  );
}

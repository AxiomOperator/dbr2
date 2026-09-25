// SPDX-License-Identifier: Apache-2.0
"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api/client";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import { useAgents, useApplications } from "@/lib/api/hooks";
import { cn } from "@/lib/utils";

function Stat({
  href,
  value,
  label,
  tone = "default",
}: {
  href: string;
  value: ReactNode;
  label: string;
  tone?: "default" | "warning" | "danger";
}) {
  return (
    <li>
      <Link
        href={href}
        className="flex items-baseline justify-between gap-3 rounded-md px-2 py-2 hover:bg-muted"
      >
        <span className="text-sm">{label}</span>
        <span
          className={cn(
            "text-lg font-semibold tabular-nums",
            tone === "danger" && "text-destructive",
            tone === "warning" && "text-amber-700 dark:text-amber-400",
          )}
        >
          {value}
        </span>
      </Link>
    </li>
  );
}

/** Dashboard summary of discovery results (requires `application.read`). */
export function ProtectionOverviewCard() {
  const me = useCurrentUser();
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const apps = useApplications({ enabled: hasPermission(me, PERMISSION_APPLICATION_READ) });
  const agents = useAgents({ enabled: canHosts });

  const list = apps.data ?? [];
  const unprotected = list.filter((a) => a.unprotected_high > 0).length;
  const reconstructed = list.filter((a) => a.source === "reconstructed").length;
  const pending = (agents.data ?? []).filter((a) => a.status === "pending").length;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Protection overview</CardTitle>
        <CardDescription>What discovery found on your Docker hosts.</CardDescription>
      </CardHeader>
      <CardContent>
        {apps.isPending ? (
          <div className="space-y-2" aria-busy="true">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : apps.isError ? (
          <p className="text-sm text-destructive" role="alert">
            Overview unavailable: {errorMessage(apps.error)}
          </p>
        ) : (
          <ul className="-mx-2 divide-y">
            <Stat href="/applications" label="Applications" value={list.length} />
            <Stat
              href="/applications?unprotected=1"
              label="With unprotected data (high severity)"
              value={unprotected}
              tone={unprotected > 0 ? "danger" : "default"}
            />
            <Stat href="/applications" label="Reconstructed Compose definitions" value={reconstructed} />
            {canHosts && (
              <Stat
                href="/hosts"
                label="Hosts pending approval"
                value={agents.isSuccess ? pending : "…"}
                tone={pending > 0 ? "warning" : "default"}
              />
            )}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

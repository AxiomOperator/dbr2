// SPDX-License-Identifier: Apache-2.0
"use client";

import { CircleAlertIcon, CircleCheckIcon, CircleHelpIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api/client";
import { useReadiness } from "@/lib/api/hooks";

function StatusBadge({ status }: { status: string }) {
  if (status === "ok") {
    return (
      <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
        <CircleCheckIcon aria-hidden="true" /> ok
      </Badge>
    );
  }
  if (status === "degraded" || status === "error" || status === "down" || status === "fail") {
    return (
      <Badge variant="destructive">
        <CircleAlertIcon aria-hidden="true" /> {status}
      </Badge>
    );
  }
  return (
    <Badge variant="outline">
      <CircleHelpIcon aria-hidden="true" /> {status}
    </Badge>
  );
}

export function PlatformStatusCard() {
  const ready = useReadiness();

  return (
    <Card>
      <CardHeader>
        <CardTitle>Platform status</CardTitle>
        <CardDescription>Control-plane dependencies (refreshes every 30 s).</CardDescription>
        {ready.isSuccess && (
          <CardAction>
            <StatusBadge status={ready.data.status} />
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        {ready.isPending && (
          <div className="space-y-2" aria-busy="true">
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-full" />
            <Skeleton className="h-5 w-full" />
          </div>
        )}
        {ready.isError && (
          <p className="text-sm text-destructive" role="alert">
            Status unavailable: {errorMessage(ready.error)}
          </p>
        )}
        {ready.isSuccess && Object.keys(ready.data.checks).length === 0 && (
          <p className="text-sm text-muted-foreground">No checks reported.</p>
        )}
        {ready.isSuccess && Object.keys(ready.data.checks).length > 0 && (
          <dl className="divide-y text-sm">
            {Object.entries(ready.data.checks).map(([name, status]) => (
              <div key={name} className="flex items-center justify-between py-2">
                <dt className="capitalize">{name}</dt>
                <dd>
                  <StatusBadge status={status} />
                </dd>
              </div>
            ))}
          </dl>
        )}
      </CardContent>
    </Card>
  );
}

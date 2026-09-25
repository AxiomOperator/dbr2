// SPDX-License-Identifier: Apache-2.0
"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api/client";

/** "Access denied" notice for a page the user lacks the permission for. */
export function AccessDenied({ what, permission }: { what: string; permission: string }) {
  return (
    <Alert>
      <AlertTitle>Access denied</AlertTitle>
      <AlertDescription>
        {what} requires the <code>{permission}</code> permission.
      </AlertDescription>
    </Alert>
  );
}

/** Error alert with a Retry button for a failed query. */
export function QueryError({
  title,
  error,
  onRetry,
}: {
  title: string;
  error: unknown;
  onRetry?: () => void;
}) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <p>{errorMessage(error)}</p>
        {onRetry && (
          <Button className="mt-3" variant="outline" onClick={onRetry}>
            Retry
          </Button>
        )}
      </AlertDescription>
    </Alert>
  );
}

export function RowsSkeleton({ label, rows = 6 }: { label: string; rows?: number }) {
  return (
    <div className="space-y-2" aria-busy="true">
      <span className="sr-only">{label}</span>
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-8 w-full" />
      ))}
    </div>
  );
}

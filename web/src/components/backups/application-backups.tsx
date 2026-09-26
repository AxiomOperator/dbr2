// SPDX-License-Identifier: Apache-2.0
"use client";

import Link from "next/link";
import { RecoveryPointsTable } from "@/components/backups/recovery-points-table";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { BACKUPS_REFRESH_MS, RECOVERY_POINTS_LIMIT, useRecoveryPoints } from "@/lib/api/hooks";
import { PERMISSION_RESTORE_EXECUTE } from "@/lib/api/restore-schemas";

/** The recovery points of one application (requires `backup.read`). */
export function ApplicationBackups({ applicationId }: { applicationId: string }) {
  const me = useCurrentUser();
  const rps = useRecoveryPoints({ applicationId });
  if (rps.isPending) return <RowsSkeleton label="Loading recovery points…" rows={4} />;
  if (rps.isError && !rps.data) {
    return <QueryError title="Could not load recovery points" error={rps.error} onRetry={() => void rps.refetch()} />;
  }
  return (
    <div className="space-y-3">
      <RecoveryPointsTable
        items={rps.data}
        label="Recovery points of this application"
        canRestore={hasPermission(me, PERMISSION_RESTORE_EXECUTE)}
        emptyText="No recovery points yet. Use “Back up now” to create the first one."
      />
      <p className="text-xs text-muted-foreground">
        {rps.data.length >= RECOVERY_POINTS_LIMIT ? `Latest ${RECOVERY_POINTS_LIMIT} shown. ` : ""}
        Refreshes every {BACKUPS_REFRESH_MS / 1000} s.{" "}
        <Link
          href={`/recovery-points?application=${encodeURIComponent(applicationId)}`}
          className="underline underline-offset-4"
        >
          Open in Recovery points
        </Link>
      </p>
    </div>
  );
}

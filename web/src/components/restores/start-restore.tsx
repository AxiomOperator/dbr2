// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon } from "lucide-react";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { RecoveryPointsTable } from "@/components/backups/recovery-points-table";
import { ALL, FilterSelect, useUrlFilters } from "@/components/common/filters";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { buttonVariants } from "@/components/ui/button";
import { useRecoveryPoints } from "@/lib/api/hooks";
import { PERMISSION_BACKUP_READ } from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_EXECUTE } from "@/lib/api/restore-schemas";

/**
 * Recovery → Restore → "Start a restore": pick a committed recovery point,
 * then the restore wizard (target, preview, confirmation) takes over.
 */
function Picker() {
  const [filters, setFilters] = useUrlFilters({ application: ALL });
  const all = useRecoveryPoints({ state: "committed" });
  const items = useMemo(
    () => (all.data ?? []).filter((r) => filters.application === ALL || r.application_id === filters.application),
    [all.data, filters.application],
  );
  const apps = useMemo(() => {
    const m = new Map<string, string>();
    for (const r of all.data ?? []) m.set(r.application_id, `${r.application_name} (${r.hostname})`);
    return [...m.entries()].map(([value, label]) => ({ value, label })).sort((a, b) => a.label.localeCompare(b.label));
  }, [all.data]);

  if (all.isPending) return <RowsSkeleton label="Loading recovery points…" />;
  if (all.isError && !all.data) {
    return <QueryError title="Could not load recovery points" error={all.error} onRetry={() => void all.refetch()} />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <FilterSelect
          label="Application"
          value={filters.application}
          onChange={(v) => setFilters({ application: v })}
          options={apps}
          allLabel="All applications"
          className="min-w-56"
        />
      </div>
      <RecoveryPointsTable
        items={items}
        showApplication
        canRestore
        label="Committed recovery points"
        emptyText="No committed recovery points: back an application up first."
      />
      <p className="text-xs text-muted-foreground">
        Only committed recovery points can be restored. Choose “Restore…” to pick the target host and components
        and review the impact preview before anything changes.
      </p>
    </div>
  );
}

export function StartRestoreView() {
  const me = useCurrentUser();
  const allowed = hasPermission(me, PERMISSION_RESTORE_EXECUTE) && hasPermission(me, PERMISSION_BACKUP_READ);
  return (
    <div className="space-y-4">
      <Link href="/restores" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> Restore history
      </Link>
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Start a restore</h1>
        <p className="text-sm text-muted-foreground">
          Choose the recovery point to restore; then pick the target, review the impact preview and confirm.
        </p>
      </div>
      {allowed ? (
        <Picker />
      ) : (
        <AccessDenied
          what="Starting a restore"
          permission={hasPermission(me, PERMISSION_RESTORE_EXECUTE) ? PERMISSION_BACKUP_READ : PERMISSION_RESTORE_EXECUTE}
        />
      )}
    </div>
  );
}

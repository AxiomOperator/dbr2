// SPDX-License-Identifier: Apache-2.0
"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useId, useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { STATE_LABEL } from "@/components/backups/backup-badges";
import { RecoveryPointsTable } from "@/components/backups/recovery-points-table";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import {
  PERMISSION_BACKUP_READ,
  RECOVERY_POINT_STATES,
  type RecoveryPointState,
} from "@/lib/api/protection-schemas";
import { BACKUPS_REFRESH_MS, RECOVERY_POINTS_LIMIT, useApplications, useRecoveryPoints } from "@/lib/api/hooks";

const ALL = "all";

export interface RecoveryPointFilters {
  application: string;
  state: RecoveryPointState | typeof ALL;
}

/** Reads the filters from the URL (`?application=&state=`); unknown states are ignored. */
export function parseRecoveryPointFilters(params: URLSearchParams): RecoveryPointFilters {
  const state = params.get("state") ?? "";
  return {
    application: params.get("application") || ALL,
    state: (RECOVERY_POINT_STATES as readonly string[]).includes(state) ? (state as RecoveryPointState) : ALL,
  };
}

function useFilters(): [RecoveryPointFilters, (next: Partial<RecoveryPointFilters>) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const filters = parseRecoveryPointFilters(new URLSearchParams(params.toString()));
  const update = (next: Partial<RecoveryPointFilters>) => {
    const merged = { ...filters, ...next };
    const qs = new URLSearchParams();
    if (merged.application !== ALL) qs.set("application", merged.application);
    if (merged.state !== ALL) qs.set("state", merged.state);
    const s = qs.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };
  return [filters, update];
}

function RecoveryPointsList() {
  const id = useId();
  const me = useCurrentUser();
  const [filters, setFilters] = useFilters();
  const rps = useRecoveryPoints({
    applicationId: filters.application === ALL ? null : filters.application,
    state: filters.state === ALL ? null : filters.state,
  });
  const apps = useApplications({ enabled: hasPermission(me, PERMISSION_APPLICATION_READ) });

  const appOptions = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of apps.data ?? []) m.set(a.id, `${a.display_name || a.name} (${a.hostname})`);
    for (const rp of rps.data ?? []) if (!m.has(rp.application_id)) m.set(rp.application_id, `${rp.application_name} (${rp.hostname})`);
    if (filters.application !== ALL && !m.has(filters.application)) m.set(filters.application, "Unknown application");
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [apps.data, rps.data, filters.application]);

  const items = rps.data ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-app`}>Application</Label>
          <Select value={filters.application} onValueChange={(v) => setFilters({ application: v })}>
            <SelectTrigger id={`${id}-app`} className="min-w-48">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All applications</SelectItem>
              {appOptions.map(([aid, label]) => (
                <SelectItem key={aid} value={aid}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-state`}>State</Label>
          <Select value={filters.state} onValueChange={(v) => setFilters({ state: v as RecoveryPointFilters["state"] })}>
            <SelectTrigger id={`${id}-state`} className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All states</SelectItem>
              {RECOVERY_POINT_STATES.map((s) => (
                <SelectItem key={s} value={s}>
                  {STATE_LABEL[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
      {rps.isPending ? (
        <RowsSkeleton label="Loading recovery points…" />
      ) : rps.isError && !rps.data ? (
        <QueryError title="Could not load recovery points" error={rps.error} onRetry={() => void rps.refetch()} />
      ) : (
        <>
          <RecoveryPointsTable
            items={items}
            showApplication
            emptyText={
              filters.application === ALL && filters.state === ALL
                ? "No recovery points yet. Use “Back up now” on an application."
                : "No recovery points match the filters."
            }
          />
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {items.length} recovery point{items.length === 1 ? "" : "s"}, newest first
            {items.length >= RECOVERY_POINTS_LIMIT ? ` (only the latest ${RECOVERY_POINTS_LIMIT} are shown)` : ""}.
            Refreshes every {BACKUPS_REFRESH_MS / 1000} s.
            {rps.isError && " The last refresh failed; showing earlier data."}
          </p>
        </>
      )}
    </div>
  );
}

export function RecoveryPointsView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_BACKUP_READ);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Recovery points</h1>
        <p className="text-sm text-muted-foreground">
          Every backup across all applications. A recovery point exists if and only if its manifest
          exists in the Repository.
        </p>
      </div>
      {canRead ? (
        <RecoveryPointsList />
      ) : (
        <AccessDenied what="Viewing recovery points" permission={PERMISSION_BACKUP_READ} />
      )}
    </div>
  );
}

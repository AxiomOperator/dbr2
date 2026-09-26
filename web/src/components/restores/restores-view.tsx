// SPDX-License-Identifier: Apache-2.0
"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useId, useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { RESTORE_STATE_LABEL, RestoreStateBadge } from "@/components/restores/restore-badges";
import { RestoresTable } from "@/components/restores/restores-table";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api/client";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import { RESTORES_LIMIT, RESTORES_REFRESH_MS, RESTORE_POLL_MS, useApplications, useRestores } from "@/lib/api/hooks";
import {
  isActiveRestore,
  PERMISSION_RESTORE_READ,
  RESTORE_STATES,
  type RestoreRun,
  type RestoreState,
} from "@/lib/api/restore-schemas";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

const ALL = "all";

export interface RestoreFilters {
  application: string;
  state: RestoreState | typeof ALL;
}

/** Reads the filters from the URL (`?application=&state=`); unknown states are ignored. */
export function parseRestoreFilters(params: URLSearchParams): RestoreFilters {
  const state = params.get("state") ?? "";
  return {
    application: params.get("application") || ALL,
    state: (RESTORE_STATES as readonly string[]).includes(state) ? (state as RestoreState) : ALL,
  };
}

function useFilters(): [RestoreFilters, (next: Partial<RestoreFilters>) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const filters = parseRestoreFilters(new URLSearchParams(params.toString()));
  const update = (next: Partial<RestoreFilters>) => {
    const merged = { ...filters, ...next };
    const qs = new URLSearchParams();
    if (merged.application !== ALL) qs.set("application", merged.application);
    if (merged.state !== ALL) qs.set("state", merged.state);
    const s = qs.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };
  return [filters, update];
}

const refreshNote = (items: RestoreRun[]) =>
  items.some((r) => isActiveRestore(r.state))
    ? `Refreshes every ${RESTORE_POLL_MS / 1000} s while a restore runs.`
    : `Refreshes every ${RESTORES_REFRESH_MS / 1000} s.`;

function RestoresList() {
  const id = useId();
  const me = useCurrentUser();
  const [filters, setFilters] = useFilters();
  const restores = useRestores({
    applicationId: filters.application === ALL ? null : filters.application,
    state: filters.state === ALL ? null : filters.state,
  });
  const apps = useApplications({ enabled: hasPermission(me, PERMISSION_APPLICATION_READ) });

  const appOptions = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of apps.data ?? []) m.set(a.id, `${a.display_name || a.name} (${a.hostname})`);
    for (const r of restores.data ?? []) if (!m.has(r.application_id)) m.set(r.application_id, r.application_name);
    if (filters.application !== ALL && !m.has(filters.application)) m.set(filters.application, "Unknown application");
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [apps.data, restores.data, filters.application]);

  const items = restores.data ?? [];

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
          <Select value={filters.state} onValueChange={(v) => setFilters({ state: v as RestoreFilters["state"] })}>
            <SelectTrigger id={`${id}-state`} className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All states</SelectItem>
              {RESTORE_STATES.map((s) => (
                <SelectItem key={s} value={s}>
                  {RESTORE_STATE_LABEL[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
      {restores.isPending ? (
        <RowsSkeleton label="Loading restores…" />
      ) : restores.isError && !restores.data ? (
        <QueryError title="Could not load restores" error={restores.error} onRetry={() => void restores.refetch()} />
      ) : (
        <>
          <RestoresTable
            items={items}
            showApplication
            emptyText={
              filters.application === ALL && filters.state === ALL
                ? "No restores yet. Start one from a committed recovery point."
                : "No restores match the filters."
            }
          />
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {items.length} restore{items.length === 1 ? "" : "s"}, newest first
            {items.length >= RESTORES_LIMIT ? ` (only the latest ${RESTORES_LIMIT} are shown)` : ""}.{" "}
            {refreshNote(items)}
            {restores.isError && " The last refresh failed; showing earlier data."}
          </p>
        </>
      )}
    </div>
  );
}

export function RestoresView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Restores</h1>
        <p className="text-sm text-muted-foreground">
          Recovery history: every restore attempt, including failures and rollbacks.
        </p>
      </div>
      {hasPermission(me, PERMISSION_RESTORE_READ) ? (
        <RestoresList />
      ) : (
        <AccessDenied what="Viewing restores" permission={PERMISSION_RESTORE_READ} />
      )}
    </div>
  );
}

/** The restores of one application (requires `restore.read`). */
export function ApplicationRestores({ applicationId }: { applicationId: string }) {
  const restores = useRestores({ applicationId });
  if (restores.isPending) return <RowsSkeleton label="Loading restores…" rows={4} />;
  if (restores.isError && !restores.data) {
    return <QueryError title="Could not load restores" error={restores.error} onRetry={() => void restores.refetch()} />;
  }
  return (
    <div className="space-y-3">
      <RestoresTable
        items={restores.data}
        label="Restores of this application"
        emptyText="This application has not been restored yet."
      />
      <p className="text-xs text-muted-foreground">
        {refreshNote(restores.data)}{" "}
        <Link href={`/restores?application=${encodeURIComponent(applicationId)}`} className="underline underline-offset-4">
          Open in Restores
        </Link>
      </p>
    </div>
  );
}

/** Dashboard card: running and failed restores and the latest few (requires `restore.read`). */
export function RecentRestoresCard() {
  const restores = useRestores({}, { limit: 50 });
  const items = restores.data ?? [];
  const running = items.filter((r) => isActiveRestore(r.state)).length;
  const failed = items.filter((r) => r.state === "failed" || r.state === "rolled_back").length;
  const latest = items.slice(0, 3);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent restores</CardTitle>
        <CardDescription>Running restores and recent failures or rollbacks.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {restores.isPending ? (
          <div className="space-y-2" aria-busy="true">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : restores.isError ? (
          <p className="text-sm text-destructive" role="alert">
            Restores unavailable: {errorMessage(restores.error)}
          </p>
        ) : (
          <>
            <ul className="grid grid-cols-2 gap-2" aria-label="Restore counts">
              <li className={cn("rounded-md border p-2 text-center", running > 0 && "border-sky-500/60 bg-sky-500/5")}>
                <div className={cn("text-xl font-semibold tabular-nums", running > 0 && "text-sky-700 dark:text-sky-400")}>
                  {running}
                </div>
                <div className="text-xs text-muted-foreground">Running</div>
              </li>
              <li className={cn("rounded-md border p-2 text-center", failed > 0 && "border-destructive/60 bg-destructive/5")}>
                <div className={cn("text-xl font-semibold tabular-nums", failed > 0 && "text-destructive")}>{failed}</div>
                <div className="text-xs text-muted-foreground">Failed or rolled back</div>
              </li>
            </ul>
            {latest.length === 0 ? (
              <p className="text-sm text-muted-foreground">No restores yet.</p>
            ) : (
              <ul className="space-y-2" aria-label="Latest restores">
                {latest.map((r) => (
                  <li key={r.id} className="flex items-start gap-2 text-sm">
                    <RestoreStateBadge state={r.state} step={r.step} />
                    <span className="min-w-0 flex-1">
                      <Link href={`/restores/${r.id}`} className="line-clamp-1 underline-offset-4 hover:underline">
                        {r.application_name} to {r.target_hostname}
                      </Link>
                      <span className="text-xs text-muted-foreground">{formatRelative(r.created_at)}</span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
            <Link href="/restores" className={buttonVariants({ variant: "outline", size: "sm" })}>
              View all restores
            </Link>
          </>
        )}
      </CardContent>
    </Card>
  );
}

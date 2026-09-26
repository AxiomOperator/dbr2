// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import { Trash2Icon } from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel } from "@/components/backups/backup-badges";
import { DataTable } from "@/components/common/data-table";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { PolicyEnabledBadge } from "@/components/policies/policy-badges";
import { PolicyFormDialog } from "@/components/policies/policy-form";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { Policy } from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_MANAGE, PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { POLICIES_REFRESH_MS, usePolicies, useRepositories } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";
import { describeCron, retentionHorizon, retentionSummary } from "@/lib/schedule";

const EMPTY: Policy[] = [];
const features = tableFeatures({});
const col = createColumnHelper<typeof features, Policy>();

/** Schedule in words over the cron expression and timezone. */
export function ScheduleCell({ schedule, timezone }: { schedule: string; timezone: string }) {
  const d = describeCron(schedule);
  return (
    <div className="min-w-40 space-y-0.5">
      <div>{d.ok ? d.text : "Custom schedule"}</div>
      <div className="text-xs text-muted-foreground">
        <code className="font-mono">{schedule}</code> · {timezone}
      </div>
    </div>
  );
}

export function NextRun({ policy }: { policy: Pick<Policy, "enabled" | "next_run" | "applications"> }) {
  if (!policy.enabled) return <span className="text-muted-foreground">Disabled</span>;
  if (!policy.next_run) return <span className="text-muted-foreground">—</span>;
  return (
    <time dateTime={policy.next_run} title={formatDateTime(policy.next_run)} className="whitespace-nowrap">
      {formatRelative(policy.next_run)}
      {policy.applications === 0 && <span className="block text-xs text-muted-foreground">no applications assigned</span>}
    </time>
  );
}

/** Deletes a policy after a confirmation; assigned applications stop being scheduled. */
export function DeletePolicyButton({
  policy,
  size = "xs",
  onDeleted,
}: {
  policy: Pick<Policy, "id" | "name" | "applications">;
  size?: "xs" | "sm";
  onDeleted?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const queryClient = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.deletePolicy(policy.id),
    onSuccess: async () => {
      setOpen(false);
      toast({ title: `Policy ${policy.name} deleted` });
      queryClient.removeQueries({ queryKey: queryKeys.policy(policy.id) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.policies });
      onDeleted?.();
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size={size} variant="destructive" aria-label={`Delete policy ${policy.name}`}>
          <Trash2Icon aria-hidden="true" /> Delete
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete policy {policy.name}?</DialogTitle>
          <DialogDescription>
            {policy.applications > 0
              ? `${policy.applications} assigned application${policy.applications === 1 ? " is" : "s are"} no longer backed up on a schedule. `
              : ""}
            Existing recovery points are kept; no data is deleted.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={del.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => del.mutate()} disabled={del.isPending}>
            {del.isPending ? "Deleting…" : "Delete policy"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function buildColumns(canManage: boolean, repoName: (id: string | null) => string) {
  const base = [
    col.accessor("name", {
      header: "Policy",
      cell: ({ row }) => (
        <div className="min-w-36 space-y-1">
          <Link href={`/policies/${row.original.id}`} className="font-medium underline-offset-4 hover:underline">
            {row.original.name}
          </Link>
          {row.original.description && (
            <div className="max-w-56 text-xs whitespace-normal text-muted-foreground">{row.original.description}</div>
          )}
        </div>
      ),
    }),
    col.accessor("schedule", {
      header: "Schedule",
      cell: ({ row }) => <ScheduleCell schedule={row.original.schedule} timezone={row.original.timezone} />,
    }),
    col.accessor("next_run", {
      header: "Next run",
      cell: ({ row }) => <NextRun policy={row.original} />,
    }),
    col.accessor("retention", {
      header: "Retention",
      cell: ({ row }) => {
        const horizon = retentionHorizon(row.original.retention);
        return (
          <div className="min-w-40 space-y-0.5">
            <div>{retentionSummary(row.original.retention)}</div>
            {horizon && <div className="text-xs text-muted-foreground">Reaches back {horizon}</div>}
          </div>
        );
      },
    }),
    col.display({
      id: "defaults",
      header: "Mode / Repository",
      cell: ({ row }) => (
        <div className="text-xs">
          <div>{row.original.consistency_mode ? modeLabel(row.original.consistency_mode) : "Application setting"}</div>
          <div className="text-muted-foreground">{repoName(row.original.repository_id)}</div>
        </div>
      ),
    }),
    col.accessor("applications", {
      header: "Applications",
      cell: (info) => <span className="tabular-nums">{info.getValue()}</span>,
    }),
    col.accessor("enabled", {
      header: "State",
      cell: (info) => <PolicyEnabledBadge enabled={info.getValue()} />,
    }),
  ];
  if (!canManage) return col.columns(base);
  return col.columns([
    ...base,
    col.display({
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) => (
        <div className="flex flex-col items-stretch gap-1">
          <PolicyFormDialog policy={row.original} size="xs" />
          <DeletePolicyButton policy={row.original} />
        </div>
      ),
    }),
  ]);
}

function PoliciesTable({ canManage }: { canManage: boolean }) {
  const policies = usePolicies();
  const repos = useRepositories();
  const repoName = useMemo(() => {
    const m = new Map((repos.data ?? []).map((r) => [r.id, r.name]));
    return (id: string | null) => (id ? (m.get(id) ?? id) : "Default Repository");
  }, [repos.data]);
  const columns = useMemo(() => buildColumns(canManage, repoName), [canManage, repoName]);
  const data = policies.data ?? EMPTY;
  const table = useTable({ features, columns, data, getRowId: (row) => row.id });

  if (policies.isPending) return <RowsSkeleton label="Loading policies…" rows={3} />;
  if (policies.isError && !policies.data) {
    return <QueryError title="Could not load policies" error={policies.error} onRetry={() => void policies.refetch()} />;
  }
  return (
    <div className="space-y-3">
      <DataTable
        table={table}
        columns={columns.length}
        label="Policies"
        empty={
          <>
            No policies yet: applications are only backed up on demand.
            {canManage ? " Use “Create policy”, then assign applications in their Backup settings tab." : ""}
          </>
        }
        rowProps={(p) => ({ "data-enabled": String(p.enabled) })}
      />
      <p className="text-xs text-muted-foreground">
        Schedules run as Temporal schedules; next-run times refresh every {POLICIES_REFRESH_MS / 1000} s.
        {policies.isError && " The last refresh failed; showing earlier data."}
      </p>
    </div>
  );
}

export function PoliciesView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_POLICY_READ);
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Policies</h1>
          <p className="text-sm text-muted-foreground">
            Protection Policies: when assigned applications are backed up and how long their recovery points are kept.
            Each application follows at most one policy.
          </p>
        </div>
        {canRead && canManage && <PolicyFormDialog />}
      </div>
      {canRead ? (
        <>
          <PoliciesTable canManage={canManage} />
          <p className="text-sm text-muted-foreground">
            Recovery Contracts (maximum RPO, required components) are set per application; see{" "}
            <Link href="/contracts" className="text-foreground underline underline-offset-4">
              Contracts
            </Link>
            .
          </p>
        </>
      ) : (
        <AccessDenied what="Viewing protection policies" permission={PERMISSION_POLICY_READ} />
      )}
    </div>
  );
}

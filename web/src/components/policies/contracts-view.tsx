// SPDX-License-Identifier: Apache-2.0
"use client";

import { createColumnHelper, tableFeatures, useTable } from "@tanstack/react-table";
import { FileWarningIcon } from "lucide-react";
import Link from "next/link";
import { useMemo } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { DataTable } from "@/components/common/data-table";
import { ALL, FilterSelect, useUrlFilters } from "@/components/common/filters";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { CONTRACT_STATE_LABEL, ContractStateBadge } from "@/components/policies/policy-badges";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PERMISSION_APPLICATION_READ } from "@/lib/api/fleet-schemas";
import { CONTRACT_STATES, formatMinutes, type Contract } from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { CONTRACTS_REFRESH_MS, useContracts } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

const EMPTY: Contract[] = [];
const features = tableFeatures({});
const col = createColumnHelper<typeof features, Contract>();

const STATE_ORDER: Record<Contract["state"], number> = { violated: 0, unknown: 1, satisfied: 2 };

/** Violated first (longest-violated first), then unknown, then satisfied; by name within. */
export function sortContracts(items: Contract[]): Contract[] {
  return [...items].sort(
    (a, b) =>
      STATE_ORDER[a.state] - STATE_ORDER[b.state] ||
      (a.violated_since ?? "").localeCompare(b.violated_since ?? "") ||
      (a.application_name ?? "").localeCompare(b.application_name ?? ""),
  );
}

function When({ iso }: { iso: string | null }) {
  if (!iso) return <span className="text-muted-foreground">—</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className="whitespace-nowrap">
      {formatRelative(iso)}
    </time>
  );
}

function buildColumns(canApps: boolean) {
  return col.columns([
    col.accessor("application_name", {
      header: "Application",
      cell: ({ row }) => {
        const name = row.original.application_name || row.original.application_id;
        return canApps ? (
          <Link href={`/applications/${row.original.application_id}`} className="font-medium underline-offset-4 hover:underline">
            {name}
          </Link>
        ) : (
          <span className="font-medium">{name}</span>
        );
      },
    }),
    col.accessor("state", {
      header: "State",
      cell: (info) => <ContractStateBadge state={info.getValue()} />,
    }),
    col.accessor("state_reasons", {
      header: "Reasons",
      cell: (info) =>
        info.getValue().length === 0 ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <ul className="max-w-80 list-disc space-y-0.5 pl-4 text-xs whitespace-normal">
            {info.getValue().map((r) => (
              <li key={r}>{r}</li>
            ))}
          </ul>
        ),
    }),
    col.accessor("violated_since", {
      header: "Violated since",
      cell: (info) => <When iso={info.getValue()} />,
    }),
    col.accessor("max_rpo_minutes", {
      header: "Max RPO",
      cell: (info) => (info.getValue() ? formatMinutes(info.getValue()!) : <span className="text-muted-foreground">None</span>),
    }),
    col.accessor("required_components", {
      header: "Required components",
      cell: (info) =>
        info.getValue().length === 0 ? (
          <span className="text-muted-foreground">None</span>
        ) : (
          <ul className="font-mono text-xs">
            {info.getValue().map((c) => (
              <li key={c} className="break-all">
                {c}
              </li>
            ))}
          </ul>
        ),
    }),
    col.accessor("evaluated_at", {
      header: "Evaluated",
      cell: (info) => <When iso={info.getValue()} />,
    }),
  ]);
}

function ContractsTable() {
  const me = useCurrentUser();
  const contracts = useContracts();
  const [filters, setFilters] = useUrlFilters({ state: ALL }, { state: [ALL, ...CONTRACT_STATES] });
  const columns = useMemo(() => buildColumns(hasPermission(me, PERMISSION_APPLICATION_READ)), [me]);
  const all = contracts.data ?? EMPTY;
  const data = useMemo(
    () => sortContracts(all.filter((c) => filters.state === ALL || c.state === filters.state)),
    [all, filters.state],
  );
  const table = useTable({ features, columns, data, getRowId: (row) => row.application_id });

  if (contracts.isPending) return <RowsSkeleton label="Loading contracts…" rows={3} />;
  if (contracts.isError && !contracts.data) {
    return <QueryError title="Could not load contracts" error={contracts.error} onRetry={() => void contracts.refetch()} />;
  }
  const violated = all.filter((c) => c.state === "violated").length;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <FilterSelect
          label="State"
          value={filters.state}
          onChange={(v) => setFilters({ state: v })}
          allLabel="All states"
          options={CONTRACT_STATES.map((s) => ({ value: s, label: CONTRACT_STATE_LABEL[s] }))}
        />
      </div>
      <DataTable
        table={table}
        columns={columns.length}
        label="Recovery Contracts"
        empty={
          all.length === 0
            ? "No Recovery Contracts yet. Set one in an application's Backup settings tab."
            : "No contracts match the filter."
        }
        rowProps={(c) => ({ "data-state": c.state })}
      />
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {all.length} contract{all.length === 1 ? "" : "s"}, {violated} violated. Evaluated every 5 minutes and after
        each change; refreshes every {CONTRACTS_REFRESH_MS / 1000} s.
      </p>
    </div>
  );
}

export function ContractsView() {
  const me = useCurrentUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Recovery Contracts</h1>
        <p className="text-sm text-muted-foreground">
          What each application promises: a maximum RPO (age of the latest recovery point) and components every
          recovery point must contain. Violations raise critical alerts.
        </p>
      </div>
      {hasPermission(me, PERMISSION_POLICY_READ) ? (
        <ContractsTable />
      ) : (
        <AccessDenied what="Viewing Recovery Contracts" permission={PERMISSION_POLICY_READ} />
      )}
    </div>
  );
}

/** Dashboard: violated contracts (hidden without `policy.read`). */
export function ViolatedContractsCard() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_POLICY_READ);
  const contracts = useContracts({ enabled: canRead });
  if (!canRead) return null;
  const violated = sortContracts((contracts.data ?? []).filter((c) => c.state === "violated"));
  return (
    <Card data-testid="violated-contracts">
      <CardHeader>
        <CardTitle>
          <h2 className="flex items-center gap-2">
            <FileWarningIcon aria-hidden="true" className="size-4" /> Recovery Contracts
          </h2>
        </CardTitle>
        <CardDescription>
          {contracts.isPending
            ? "Loading…"
            : contracts.isError && !contracts.data
              ? "Could not load the contracts."
              : violated.length === 0
                ? `${contracts.data?.length ?? 0} contract${contracts.data?.length === 1 ? "" : "s"}, none violated.`
                : `${violated.length} of ${contracts.data?.length ?? 0} violated.`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {violated.length > 0 && (
          <ul className="space-y-2" aria-label="Violated contracts">
            {violated.slice(0, 5).map((c) => (
              <li key={c.application_id} className="space-y-0.5">
                <div className="flex flex-wrap items-center gap-2">
                  <Link href={`/applications/${c.application_id}`} className="font-medium underline-offset-4 hover:underline">
                    {c.application_name || c.application_id}
                  </Link>
                  {c.violated_since && (
                    <span className="text-xs text-muted-foreground">since {formatRelative(c.violated_since)}</span>
                  )}
                </div>
                {c.state_reasons[0] && <p className="text-xs text-muted-foreground">{c.state_reasons[0]}</p>}
              </li>
            ))}
          </ul>
        )}
        <Link href="/contracts" className="inline-block text-sm font-medium underline underline-offset-4">
          All contracts
        </Link>
      </CardContent>
    </Card>
  );
}

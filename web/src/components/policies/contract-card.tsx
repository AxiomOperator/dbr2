// SPDX-License-Identifier: Apache-2.0
"use client";

// An application's Recovery Contract (Phase 7): maximum RPO and required
// components (picked from the application's planned components), with the
// current state and reasons. View with policy.read, edit with policy.manage.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { ContractStateBadge } from "@/components/policies/policy-badges";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { ApplicationDetail } from "@/lib/api/fleet-schemas";
import { ContractRequestSchema, formatMinutes, type Contract, type ContractRequest } from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_MANAGE } from "@/lib/api/protection-schemas";
import { useContract } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";

export const RPO_UNITS = { minutes: 1, hours: 60, days: 1440 } as const;
export type RpoUnit = keyof typeof RPO_UNITS;

export interface ContractDraft {
  rpoEnabled: boolean;
  rpoValue: string;
  rpoUnit: RpoUnit;
  required: string[];
}

/** Largest unit that divides the minutes evenly (1440 → 1 day). */
export function contractToDraft(c: Contract | null): ContractDraft {
  const m = c?.max_rpo_minutes ?? null;
  if (!m) return { rpoEnabled: false, rpoValue: "24", rpoUnit: "hours", required: c?.required_components ?? [] };
  const unit: RpoUnit = m % 1440 === 0 ? "days" : m % 60 === 0 ? "hours" : "minutes";
  return { rpoEnabled: true, rpoValue: String(m / RPO_UNITS[unit]), rpoUnit: unit, required: c?.required_components ?? [] };
}

export function contractDraftToRequest(d: ContractDraft): { ok: true; value: ContractRequest } | { ok: false; error: string } {
  let minutes: number | undefined;
  if (d.rpoEnabled) {
    const n = Number(d.rpoValue);
    if (d.rpoValue.trim() === "" || !Number.isFinite(n) || n <= 0) {
      return { ok: false, error: "Enter the maximum RPO as a positive number." };
    }
    minutes = n * RPO_UNITS[d.rpoUnit];
    if (!Number.isInteger(minutes)) return { ok: false, error: "The maximum RPO must be a whole number of minutes." };
  }
  if (minutes === undefined && d.required.length === 0) {
    return { ok: false, error: "Set a maximum RPO or require at least one component (or remove the contract)." };
  }
  const parsed = ContractRequestSchema.safeParse({ max_rpo_minutes: minutes, required_components: [...new Set(d.required)] });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

/** The components a contract can require: the plan's, plus required names the plan no longer has. */
export function contractComponentOptions(app: ApplicationDetail, required: string[]): { name: string; kind: string; missing: boolean }[] {
  const plan: { name: string; kind: string; missing: boolean }[] = (app.protection?.components ?? []).map((c) => ({
    name: c.name,
    kind: c.kind,
    missing: false,
  }));
  const known = new Set(plan.map((c) => c.name));
  if (!known.has("config")) plan.unshift({ name: "config", kind: "config", missing: false });
  for (const r of required) if (!known.has(r) && r !== "config") plan.push({ name: r, kind: "unknown", missing: true });
  return plan;
}

function ContractForm({
  app,
  contract,
  canManage,
}: {
  app: ApplicationDetail;
  contract: Contract | null;
  canManage: boolean;
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<ContractDraft>(() => contractToDraft(contract));
  const [error, setError] = useState<string | null>(null);
  const set = (patch: Partial<ContractDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.contract(app.id) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.contractsAll }),
    ]);
  };
  const save = useMutation({
    mutationFn: (req: ContractRequest) => api.putContract(app.id, req),
    onSuccess: async (saved) => {
      queryClient.setQueryData(queryKeys.contract(app.id), saved);
      toast({ title: "Recovery Contract saved", description: `State: ${saved.state}.` });
      await refresh();
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteContract(app.id),
    onSuccess: async () => {
      queryClient.setQueryData(queryKeys.contract(app.id), null);
      toast({ title: "Recovery Contract removed" });
      await refresh();
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const pending = save.isPending || remove.isPending;
  const disabled = !canManage || pending;
  const options = contractComponentOptions(app, draft.required);
  const required = new Set(draft.required);
  const rpoMinutes = Number(draft.rpoValue) * RPO_UNITS[draft.rpoUnit];

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const res = contractDraftToRequest(draft);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    save.mutate(res.value);
  }

  return (
    <form onSubmit={submit} noValidate>
      <CardContent className="space-y-5">
        {contract && (
          <div className="space-y-2 rounded-lg border p-3 text-sm" data-testid="contract-state">
            <div className="flex flex-wrap items-center gap-2">
              <ContractStateBadge state={contract.state} />
              {contract.violated_since && (
                <span className="text-muted-foreground">
                  since <time dateTime={contract.violated_since}>{formatDateTime(contract.violated_since)}</time>
                </span>
              )}
              {contract.evaluated_at && (
                <span className="text-xs text-muted-foreground">Evaluated {formatRelative(contract.evaluated_at)}</span>
              )}
            </div>
            {contract.state_reasons.length > 0 && (
              <ul className="list-disc space-y-0.5 pl-5" aria-label="Reasons">
                {contract.state_reasons.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Maximum RPO</legend>
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${id}-rpo-on`}
              checked={draft.rpoEnabled}
              onCheckedChange={(v) => set({ rpoEnabled: v === true })}
              disabled={disabled}
            />
            <Label htmlFor={`${id}-rpo-on`} className="font-normal">
              The latest recovery point must never be older than
            </Label>
          </div>
          {draft.rpoEnabled && (
            <div className="flex flex-wrap items-center gap-2">
              <Input
                id={`${id}-rpo`}
                type="number"
                inputMode="numeric"
                min={1}
                value={draft.rpoValue}
                onChange={(e) => set({ rpoValue: e.target.value })}
                className="w-24"
                aria-label="Maximum RPO"
                disabled={disabled}
              />
              <Select value={draft.rpoUnit} onValueChange={(v) => set({ rpoUnit: v as RpoUnit })} disabled={disabled}>
                <SelectTrigger className="w-32" aria-label="Maximum RPO unit">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="minutes">minutes</SelectItem>
                  <SelectItem value="hours">hours</SelectItem>
                  <SelectItem value="days">days</SelectItem>
                </SelectContent>
              </Select>
              {Number.isFinite(rpoMinutes) && rpoMinutes > 0 && (
                <span className="text-xs text-muted-foreground">= {formatMinutes(rpoMinutes)}</span>
              )}
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            Pick a value comfortably above the policy&apos;s interval (e.g. 26 hours for a daily schedule).
          </p>
        </fieldset>
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Required components</legend>
          <p className="text-xs text-muted-foreground">
            Every recovery point must contain these; a recovery point without one violates the contract.
          </p>
          <ul className="grid gap-1.5 sm:grid-cols-2" aria-label="Components">
            {options.map((c) => (
              <li key={c.name} className="flex items-center gap-2">
                <Checkbox
                  id={`${id}-c-${c.name}`}
                  checked={required.has(c.name)}
                  onCheckedChange={(v) =>
                    set({ required: v === true ? [...draft.required, c.name] : draft.required.filter((x) => x !== c.name) })
                  }
                  disabled={disabled}
                />
                <Label htmlFor={`${id}-c-${c.name}`} className="font-mono text-xs font-normal break-all">
                  {c.name}
                  {c.missing && <span className="font-sans text-amber-700 dark:text-amber-400"> (no longer planned)</span>}
                </Label>
              </li>
            ))}
          </ul>
        </fieldset>
      </CardContent>
      <CardFooter className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {contract ? "Evaluated every 5 minutes and after each change." : "This application has no contract yet."}
        </p>
        {canManage && (
          <div className="flex gap-2">
            {contract && (
              <Button type="button" variant="outline" disabled={pending} onClick={() => remove.mutate()}>
                {remove.isPending ? "Removing…" : "Remove contract"}
              </Button>
            )}
            <Button type="submit" disabled={pending}>
              {save.isPending ? "Saving…" : contract ? "Save contract" : "Create contract"}
            </Button>
          </div>
        )}
      </CardFooter>
    </form>
  );
}

export function ContractCard({ app }: { app: ApplicationDetail }) {
  const me = useCurrentUser();
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const contract = useContract(app.id);
  return (
    <Card data-testid="contract-card">
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-2">
            Recovery Contract
            {contract.isSuccess && <ContractStateBadge state={contract.data?.state ?? "none"} />}
          </h2>
        </CardTitle>
        <CardDescription>
          What this application promises: a maximum RPO and the components every recovery point must contain.
          {!canManage && (
            <>
              {" "}
              Read-only: editing requires <code>{PERMISSION_POLICY_MANAGE}</code>.
            </>
          )}
        </CardDescription>
      </CardHeader>
      {contract.isPending ? (
        <CardContent>
          <RowsSkeleton label="Loading the Recovery Contract…" rows={3} />
        </CardContent>
      ) : contract.isError ? (
        <CardContent>
          <QueryError title="Could not load the Recovery Contract" error={contract.error} onRetry={() => void contract.refetch()} />
        </CardContent>
      ) : (
        <ContractForm
          key={`${contract.data?.required_components.join(",") ?? "none"}-${contract.data?.max_rpo_minutes ?? ""}`}
          app={app}
          contract={contract.data}
          canManage={canManage}
        />
      )}
    </Card>
  );
}

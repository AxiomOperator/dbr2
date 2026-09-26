// SPDX-License-Identifier: Apache-2.0
"use client";

// Assigns an application to a Protection Policy (Phase 7). The application
// DTO does not carry its policy, so the current assignment is read from the
// policies' assigned applications (useApplicationPolicy).

import { useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useId, useState } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { RowsSkeleton } from "@/components/common/states";
import { NextRun } from "@/components/policies/policies-view";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { Policy } from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_MANAGE } from "@/lib/api/protection-schemas";
import { useApplicationPolicy } from "@/lib/api/hooks";
import { retentionSummary, scheduleSummary } from "@/lib/schedule";

const NONE = "__none__";

function Assignment({
  applicationId,
  applicationName,
  current,
  policies,
  canManage,
}: {
  applicationId: string;
  applicationName: string;
  current: Policy | null;
  policies: Policy[];
  canManage: boolean;
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState(current?.id ?? NONE);
  const [error, setError] = useState<string | null>(null);
  const assign = useMutation({
    mutationFn: (policyId: string | null) => api.assignPolicy(applicationId, policyId),
    onSuccess: async (_, policyId) => {
      const p = policies.find((x) => x.id === policyId);
      toast({
        title: p ? `${applicationName} follows ${p.name}` : `${applicationName} has no policy`,
        description: p ? scheduleSummary(p.schedule, p.timezone) : "It is only backed up on demand now.",
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.policies });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const shown = policies.find((p) => p.id === selected) ?? null;
  const changed = selected !== (current?.id ?? NONE);

  return (
    <CardContent className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-2">
          <Label htmlFor={`${id}-policy`}>Protection policy</Label>
          <Select
            value={selected}
            onValueChange={(v) => {
              setSelected(v);
              setError(null);
            }}
            disabled={!canManage || assign.isPending}
          >
            <SelectTrigger id={`${id}-policy`} className="w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>No policy (on demand only)</SelectItem>
              {policies.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                  {p.enabled ? "" : " (disabled)"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {canManage && (
          <Button
            type="button"
            disabled={!changed || assign.isPending}
            onClick={() => {
              setError(null);
              assign.mutate(selected === NONE ? null : selected);
            }}
          >
            {assign.isPending ? "Saving…" : "Save assignment"}
          </Button>
        )}
      </div>
      {shown ? (
        <dl className="grid gap-3 text-sm sm:grid-cols-3" data-testid="policy-summary">
          <div>
            <dt className="text-muted-foreground">Schedule</dt>
            <dd>{scheduleSummary(shown.schedule, shown.timezone)}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Next run</dt>
            <dd>{changed ? <span className="text-muted-foreground">After saving</span> : <NextRun policy={shown} />}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Retention</dt>
            <dd>{retentionSummary(shown.retention)}</dd>
          </div>
          <div className="sm:col-span-3">
            <Link href={`/policies/${shown.id}`} className="text-sm underline underline-offset-4">
              View policy {shown.name}
            </Link>
          </div>
        </dl>
      ) : (
        <p className="text-sm text-muted-foreground">
          Without a policy, this application is only backed up with “Back up now” and its recovery points are never
          pruned.
        </p>
      )}
    </CardContent>
  );
}

export function PolicyAssignmentCard({ applicationId, applicationName }: { applicationId: string; applicationName: string }) {
  const me = useCurrentUser();
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const { policies, policy, isPending, isError } = useApplicationPolicy(applicationId);
  return (
    <Card data-testid="policy-assignment">
      <CardHeader>
        <CardTitle>
          <h2>Protection policy</h2>
        </CardTitle>
        <CardDescription>
          The schedule and retention this application follows.
          {!canManage && (
            <>
              {" "}
              Read-only: assigning requires <code>{PERMISSION_POLICY_MANAGE}</code>.
            </>
          )}
        </CardDescription>
      </CardHeader>
      {isPending ? (
        <CardContent>
          <RowsSkeleton label="Loading policies…" rows={2} />
        </CardContent>
      ) : isError && !policies.data ? (
        <CardContent>
          <Alert variant="destructive">
            <AlertDescription>Could not load the policies.</AlertDescription>
          </Alert>
        </CardContent>
      ) : (
        <Assignment
          key={policy?.id ?? NONE}
          applicationId={applicationId}
          applicationName={applicationName}
          current={policy}
          policies={policies.data ?? []}
          canManage={canManage}
        />
      )}
    </Card>
  );
}

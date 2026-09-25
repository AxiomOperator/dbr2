// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PERMISSION_HOST_MANAGE } from "@/lib/api/fleet-schemas";
import {
  MAX_CONCURRENT_JOBS,
  UpdateHostSettingsRequestSchema,
  type HostSettings,
  type UpdateHostSettingsRequest,
} from "@/lib/api/protection-schemas";
import { useHostSettings } from "@/lib/api/hooks";
import { describeWindow, hhmmToMinutes, isValidTimeZone, minutesToHHMM, timeZones } from "@/lib/protection";

export interface LimitsDraft {
  jobs: string;
  noWindow: boolean;
  start: string;
  end: string;
  timezone: string;
}

export function limitsToDraft(s: HostSettings): LimitsDraft {
  const noWindow = s.backup_window_start === null || s.backup_window_end === null;
  return {
    jobs: String(s.max_concurrent_jobs),
    noWindow,
    start: noWindow ? "22:00" : minutesToHHMM(s.backup_window_start),
    end: noWindow ? "06:00" : minutesToHHMM(s.backup_window_end),
    timezone: s.backup_window_timezone || "UTC",
  };
}

/** Validates the form and builds the PUT body (window times as minutes of the day). */
export function draftToLimits(
  d: LimitsDraft,
): { ok: true; value: UpdateHostSettingsRequest } | { ok: false; error: string } {
  const jobs = d.jobs.trim() === "" ? Number.NaN : Number(d.jobs);
  let start: number | null = null;
  let end: number | null = null;
  if (!d.noWindow) {
    start = hhmmToMinutes(d.start);
    end = hhmmToMinutes(d.end);
    if (start === null || end === null) return { ok: false, error: "Enter the window start and end as HH:MM." };
  }
  if (!isValidTimeZone(d.timezone)) {
    return { ok: false, error: `“${d.timezone}” is not a known IANA timezone (e.g. America/Chicago).` };
  }
  const parsed = UpdateHostSettingsRequestSchema.safeParse({
    max_concurrent_jobs: jobs,
    backup_window_start: start,
    backup_window_end: end,
    backup_window_timezone: d.timezone.trim(),
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

function LimitsForm({
  agentId,
  hostname,
  initial,
  canManage,
}: {
  agentId: string;
  hostname: string;
  initial: HostSettings;
  canManage: boolean;
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<LimitsDraft>(() => limitsToDraft(initial));
  const [error, setError] = useState<string | null>(null);
  const zones = useMemo(() => timeZones(), []);
  const set = (patch: Partial<LimitsDraft>) => setDraft((d) => ({ ...d, ...patch }));

  const save = useMutation({
    mutationFn: (req: UpdateHostSettingsRequest) => api.updateHostSettings(agentId, req),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.hostSettings(agentId), updated);
      setDraft(limitsToDraft(updated));
      toast({
        title: `Limits of ${hostname} saved`,
        description: "The agent reconnects to apply the new concurrency limit.",
      });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const disabled = !canManage || save.isPending;

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const res = draftToLimits(draft);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    save.mutate(res.value);
  }

  const preview = draftToLimits(draft);

  return (
    <form onSubmit={submit} noValidate>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="space-y-2">
          <Label htmlFor={`${id}-jobs`}>Maximum concurrent jobs</Label>
          <Input
            id={`${id}-jobs`}
            type="number"
            inputMode="numeric"
            min={1}
            max={MAX_CONCURRENT_JOBS}
            value={draft.jobs}
            onChange={(e) => set({ jobs: e.target.value })}
            className="w-28"
            aria-describedby={`${id}-jobs-help`}
            disabled={disabled}
          />
          <p id={`${id}-jobs-help`} className="text-xs text-muted-foreground">
            Snapshot and restore jobs the agent runs at once (1–{MAX_CONCURRENT_JOBS}).
          </p>
        </div>
        <fieldset className="space-y-3">
          <legend className="text-sm font-medium">Backup window</legend>
          <div className="flex items-center gap-2">
            <Checkbox
              id={`${id}-nowindow`}
              checked={draft.noWindow}
              onCheckedChange={(v) => set({ noWindow: v === true })}
              disabled={disabled}
            />
            <Label htmlFor={`${id}-nowindow`} className="font-normal">
              No window: scheduled backups may start at any time
            </Label>
          </div>
          {!draft.noWindow && (
            <div className="flex flex-wrap items-end gap-4">
              <div className="space-y-1.5">
                <Label htmlFor={`${id}-start`}>Starts</Label>
                <Input
                  id={`${id}-start`}
                  type="time"
                  step={60}
                  value={draft.start}
                  onChange={(e) => set({ start: e.target.value })}
                  className="w-32"
                  disabled={disabled}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={`${id}-end`}>Ends</Label>
                <Input
                  id={`${id}-end`}
                  type="time"
                  step={60}
                  value={draft.end}
                  onChange={(e) => set({ end: e.target.value })}
                  className="w-32"
                  disabled={disabled}
                />
              </div>
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-tz`}>Timezone</Label>
            <Input
              id={`${id}-tz`}
              list={`${id}-zones`}
              value={draft.timezone}
              onChange={(e) => set({ timezone: e.target.value })}
              className="w-64"
              spellCheck={false}
              disabled={disabled}
            />
            <datalist id={`${id}-zones`}>
              {zones.map((z) => (
                <option key={z} value={z} />
              ))}
            </datalist>
          </div>
          <p className="text-xs text-muted-foreground" data-testid="window-summary">
            {preview.ok
              ? `${describeWindow(preview.value.backup_window_start, preview.value.backup_window_end)}${
                  preview.value.backup_window_start !== null ? ` (${preview.value.backup_window_timezone})` : ""
                }.`
              : null}{" "}
            Scheduled backups wait for the window (e.g. outside other backup jobs); “Back up now” does not.
          </p>
        </fieldset>
      </CardContent>
      {canManage && (
        <CardFooter className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">Saving makes the agent reconnect.</p>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save limits"}
          </Button>
        </CardFooter>
      )}
    </form>
  );
}

/** Host limits: concurrency and backup window (view with host.read, edit with host.manage). */
export function HostLimitsCard({ agentId, hostname }: { agentId: string; hostname: string }) {
  const me = useCurrentUser();
  const canManage = hasPermission(me, PERMISSION_HOST_MANAGE);
  const settings = useHostSettings(agentId);
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Limits</h2>
        </CardTitle>
        <CardDescription>Concurrent jobs and the window scheduled backups may start in.</CardDescription>
      </CardHeader>
      {settings.isPending ? (
        <CardContent>
          <RowsSkeleton label="Loading limits…" rows={3} />
        </CardContent>
      ) : settings.isError ? (
        <CardContent>
          <QueryError title="Could not load the limits" error={settings.error} onRetry={() => void settings.refetch()} />
        </CardContent>
      ) : (
        <LimitsForm agentId={agentId} hostname={hostname} initial={settings.data} canManage={canManage} />
      )}
    </Card>
  );
}

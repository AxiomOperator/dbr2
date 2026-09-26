// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PencilIcon, PlusIcon } from "lucide-react";
import { useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel } from "@/components/backups/backup-badges";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PolicyRequestSchema, type Policy, type PolicyRequest } from "@/lib/api/policy-schemas";
import { CONSISTENCY_MODES, PERMISSION_REPOSITORY_READ, type ConsistencyMode } from "@/lib/api/protection-schemas";
import { useRepositories } from "@/lib/api/hooks";
import { isValidTimeZone, timeZones } from "@/lib/protection";
import {
  DEFAULT_RETENTION,
  describeCron,
  GFS_EXPLANATION,
  parseCron,
  PRESET_LABEL,
  presetOf,
  RETENTION_FIELDS,
  retentionHorizon,
  retentionSummary,
  SCHEDULE_PRESET_NAMES,
  SCHEDULE_PRESETS,
  type Retention,
  type SchedulePreset,
} from "@/lib/schedule";

const CUSTOM = "custom";
const NONE = "__none__";

// ---------------------------------------------------------------------------
// Draft <-> request (pure, tested)
// ---------------------------------------------------------------------------

export interface PolicyDraft {
  name: string;
  description: string;
  schedule: SchedulePreset | typeof CUSTOM;
  cron: string;
  timezone: string;
  enabled: boolean;
  mode: ConsistencyMode | typeof NONE;
  repositoryId: string;
  retention: Record<keyof Retention, string>;
}

const retentionDraft = (r: Retention): PolicyDraft["retention"] => ({
  keep_last: String(r.keep_last),
  keep_hourly: String(r.keep_hourly),
  keep_daily: String(r.keep_daily),
  keep_weekly: String(r.keep_weekly),
  keep_monthly: String(r.keep_monthly),
  keep_yearly: String(r.keep_yearly),
});

/** A new policy: daily at 01:00 in the browser's timezone, the server's default retention. */
export function newPolicyDraft(): PolicyDraft {
  let tz = "UTC";
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    // keep UTC
  }
  return {
    name: "",
    description: "",
    schedule: "daily",
    cron: SCHEDULE_PRESETS.daily,
    timezone: tz,
    enabled: true,
    mode: NONE,
    repositoryId: NONE,
    retention: retentionDraft(DEFAULT_RETENTION),
  };
}

export function policyToDraft(p: Policy): PolicyDraft {
  const preset = presetOf(p.schedule);
  return {
    name: p.name,
    description: p.description,
    schedule: preset ?? CUSTOM,
    cron: p.schedule,
    timezone: p.timezone,
    enabled: p.enabled,
    mode: (CONSISTENCY_MODES as readonly string[]).includes(p.consistency_mode ?? "") ? (p.consistency_mode as ConsistencyMode) : NONE,
    repositoryId: p.repository_id ?? NONE,
    retention: retentionDraft(p.retention),
  };
}

/** The cron expression a draft schedules (a preset's expression, or the custom one). */
export const draftCron = (d: Pick<PolicyDraft, "schedule" | "cron">): string =>
  d.schedule === CUSTOM ? d.cron.trim() : SCHEDULE_PRESETS[d.schedule];

/**
 * Builds the request body. Custom schedules are checked client-side first
 * (the server's parser stays authoritative and its message is shown when it
 * disagrees); presets are sent by name.
 */
export function policyDraftToRequest(d: PolicyDraft): { ok: true; value: PolicyRequest } | { ok: false; error: string } {
  if (d.schedule === CUSTOM) {
    const parsed = parseCron(d.cron);
    if (!parsed.ok) return { ok: false, error: `Schedule: ${parsed.error}` };
  }
  if (!isValidTimeZone(d.timezone)) {
    return { ok: false, error: `“${d.timezone}” is not a known IANA timezone (e.g. America/Chicago).` };
  }
  const retention = {} as Record<keyof Retention, number>;
  for (const f of RETENTION_FIELDS) {
    const raw = d.retention[f.key].trim();
    const n = raw === "" ? NaN : Number(raw);
    retention[f.key] = n;
  }
  const parsed = PolicyRequestSchema.safeParse({
    name: d.name,
    description: d.description.trim() || undefined,
    schedule: d.schedule === CUSTOM ? d.cron.trim().replace(/\s+/g, " ") : d.schedule,
    timezone: d.timezone.trim(),
    enabled: d.enabled,
    consistency_mode: d.mode === NONE ? undefined : d.mode,
    repository_id: d.repositoryId === NONE ? undefined : d.repositoryId,
    retention,
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

/** Numeric retention of a draft, or null while a field is invalid (for the live summary). */
export function draftRetention(d: PolicyDraft): Retention | null {
  const out = {} as Retention;
  for (const f of RETENTION_FIELDS) {
    const n = Number(d.retention[f.key]);
    if (d.retention[f.key].trim() === "" || !Number.isInteger(n) || n < 0) return null;
    out[f.key] = n;
  }
  return out.keep_last >= 1 ? out : null;
}

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

/** "every day at 01:00" for a preset. */
function presetText(p: SchedulePreset): string {
  const d = describeCron(SCHEDULE_PRESETS[p]);
  return d.ok ? d.text.charAt(0).toLowerCase() + d.text.slice(1) : SCHEDULE_PRESETS[p];
}

/** Live schedule preview: the description, or why the expression is invalid. */
export function SchedulePreview({ cron, timezone, id }: { cron: string; timezone: string; id?: string }) {
  const d = describeCron(cron);
  return (
    <p id={id} className={d.ok ? "text-xs text-muted-foreground" : "text-xs text-destructive"} aria-live="polite" data-testid="schedule-preview">
      {d.ok ? (
        <>
          <span className="font-medium text-foreground">{d.text}</span> ({timezone || "UTC"}) ·{" "}
          <code className="font-mono">{cron}</code>
        </>
      ) : (
        d.error
      )}
    </p>
  );
}

function Section({ title, children, description }: { title: string; description?: ReactNode; children: ReactNode }) {
  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{title}</legend>
      {description && <div className="text-xs text-muted-foreground">{description}</div>}
      {children}
    </fieldset>
  );
}

function PolicyForm({
  initial,
  submitLabel,
  onSubmit,
  pending,
  error,
  onCancel,
}: {
  initial: PolicyDraft;
  submitLabel: string;
  onSubmit: (req: PolicyRequest) => void;
  pending: boolean;
  error: string | null;
  onCancel: () => void;
}) {
  const id = useId();
  const me = useCurrentUser();
  const [draft, setDraft] = useState<PolicyDraft>(initial);
  const [localError, setLocalError] = useState<string | null>(null);
  const zones = useMemo(() => timeZones(), []);
  const repos = useRepositories({ enabled: hasPermission(me, PERMISSION_REPOSITORY_READ) });
  const errorRef = useRef<HTMLDivElement>(null);
  const set = (patch: Partial<PolicyDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const cron = draftCron(draft);
  const retention = draftRetention(draft);
  const horizon = retention ? retentionHorizon(retention) : null;
  const shownError = localError ?? error;

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setLocalError(null);
    const res = policyDraftToRequest(draft);
    if (!res.ok) {
      setLocalError(res.error);
      errorRef.current?.scrollIntoView?.({ block: "nearest" });
      return;
    }
    onSubmit(res.value);
  }

  const repoOptions = repos.data ?? [];
  const repoKnown = draft.repositoryId === NONE || repoOptions.some((r) => r.id === draft.repositoryId);

  return (
    <form onSubmit={submit} noValidate className="space-y-5">
      <div ref={errorRef}>
        {shownError && (
          <Alert variant="destructive">
            <AlertDescription>{shownError}</AlertDescription>
          </Alert>
        )}
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor={`${id}-name`}>Name</Label>
          <Input
            id={`${id}-name`}
            value={draft.name}
            onChange={(e) => set({ name: e.target.value })}
            placeholder="e.g. Nightly production"
            maxLength={100}
            aria-required="true"
            disabled={pending}
          />
        </div>
        <div className="flex items-end gap-2 pb-1">
          <Checkbox
            id={`${id}-enabled`}
            checked={draft.enabled}
            onCheckedChange={(v) => set({ enabled: v === true })}
            disabled={pending}
          />
          <Label htmlFor={`${id}-enabled`} className="font-normal">
            Enabled (assigned applications are backed up on schedule)
          </Label>
        </div>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-desc`}>Description (optional)</Label>
        <Textarea
          id={`${id}-desc`}
          value={draft.description}
          onChange={(e) => set({ description: e.target.value })}
          maxLength={500}
          rows={2}
          disabled={pending}
        />
      </div>

      <Section title="Schedule" description="Overlapping runs are skipped and recorded (backup.skipped).">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor={`${id}-schedule`}>Frequency</Label>
            <Select
              value={draft.schedule}
              onValueChange={(v) =>
                set(
                  v === CUSTOM
                    ? { schedule: CUSTOM, cron: draftCron(draft) }
                    : { schedule: v as SchedulePreset, cron: SCHEDULE_PRESETS[v as SchedulePreset] },
                )
              }
              disabled={pending}
            >
              <SelectTrigger id={`${id}-schedule`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SCHEDULE_PRESET_NAMES.map((p) => (
                  <SelectItem key={p} value={p}>
                    {PRESET_LABEL[p]} ({presetText(p)})
                  </SelectItem>
                ))}
                <SelectItem value={CUSTOM}>Custom (cron expression)</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-tz`}>Timezone</Label>
            <Input
              id={`${id}-tz`}
              list={`${id}-zones`}
              value={draft.timezone}
              onChange={(e) => set({ timezone: e.target.value })}
              placeholder="UTC"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={draft.timezone !== "" && !isValidTimeZone(draft.timezone) ? true : undefined}
              disabled={pending}
            />
            <datalist id={`${id}-zones`}>
              {zones.map((z) => (
                <option key={z} value={z} />
              ))}
            </datalist>
          </div>
        </div>
        {draft.schedule === CUSTOM && (
          <div className="space-y-2">
            <Label htmlFor={`${id}-cron`}>Cron expression</Label>
            <Input
              id={`${id}-cron`}
              value={draft.cron}
              onChange={(e) => set({ cron: e.target.value })}
              placeholder="minute hour day-of-month month weekday, e.g. 30 22 * * 1-5"
              className="font-mono text-xs"
              spellCheck={false}
              autoComplete="off"
              aria-describedby={`${id}-cron-preview`}
              aria-invalid={!parseCron(draft.cron).ok ? true : undefined}
              disabled={pending}
            />
          </div>
        )}
        <SchedulePreview id={`${id}-cron-preview`} cron={cron} timezone={draft.timezone} />
      </Section>

      <Section title="Retention" description={GFS_EXPLANATION}>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {RETENTION_FIELDS.map((f) => (
            <div key={f.key} className="space-y-1.5">
              <Label htmlFor={`${id}-${f.key}`}>
                {f.label} <span className="font-normal text-muted-foreground">({f.unit})</span>
              </Label>
              <Input
                id={`${id}-${f.key}`}
                type="number"
                inputMode="numeric"
                min={f.key === "keep_last" ? 1 : 0}
                value={draft.retention[f.key]}
                onChange={(e) => set({ retention: { ...draft.retention, [f.key]: e.target.value } })}
                title={f.help}
                disabled={pending}
              />
            </div>
          ))}
        </div>
        <p className="text-xs" aria-live="polite" data-testid="retention-summary">
          {retention ? (
            <>
              Keeps: <strong>{retentionSummary(retention)}</strong>
              {horizon ? `, reaching back ${horizon}` : ""}. The latest recovery point is never deleted.
            </>
          ) : (
            <span className="text-destructive">Keep last is at least 1; the other counts are whole numbers ≥ 0.</span>
          )}
        </p>
      </Section>

      <Section
        title="Defaults for assigned applications"
        description="Used when an application's own backup settings leave the consistency mode or Repository unset."
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor={`${id}-mode`}>Consistency mode</Label>
            <Select value={draft.mode} onValueChange={(v) => set({ mode: v as PolicyDraft["mode"] })} disabled={pending}>
              <SelectTrigger id={`${id}-mode`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Application setting (automatic)</SelectItem>
                {CONSISTENCY_MODES.map((m) => (
                  <SelectItem key={m} value={m}>
                    {modeLabel(m)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-repo`}>Repository</Label>
            <Select value={draft.repositoryId} onValueChange={(v) => set({ repositoryId: v })} disabled={pending}>
              <SelectTrigger id={`${id}-repo`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Default Repository</SelectItem>
                {repoOptions.map((r) => (
                  <SelectItem key={r.id} value={r.id} disabled={r.status !== "ready" && r.id !== draft.repositoryId}>
                    {r.name}
                    {r.status !== "ready" ? ` (${r.status.replace("_", " ")})` : ""}
                  </SelectItem>
                ))}
                {!repoKnown && <SelectItem value={draft.repositoryId}>{draft.repositoryId}</SelectItem>}
              </SelectContent>
            </Select>
          </div>
        </div>
      </Section>

      <DialogFooter>
        <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Create (no `policy`) or edit a Protection Policy (requires `policy.manage`). */
export function PolicyFormDialog({ policy, size = "default" }: { policy?: Policy; size?: "xs" | "sm" | "default" }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formKey, setFormKey] = useState(0);
  const save = useMutation({
    mutationFn: (req: PolicyRequest) => (policy ? api.updatePolicy(policy.id, req) : api.createPolicy(req)),
    onSuccess: async (saved) => {
      setOpen(false);
      toast({
        title: policy ? `Policy ${saved.name} saved` : `Policy ${saved.name} created`,
        description: policy
          ? "Schedules of assigned applications are updated."
          : "Assign applications in their Backup settings tab.",
      });
      queryClient.setQueryData(queryKeys.policy(saved.id), (old: object | undefined) => (old ? { ...old, ...saved } : old));
      await queryClient.invalidateQueries({ queryKey: queryKeys.policies });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const initial = useMemo(() => (policy ? policyToDraft(policy) : newPolicyDraft()), [policy]);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setError(null);
          save.reset();
          setFormKey((k) => k + 1);
        }
      }}
    >
      <DialogTrigger asChild>
        {policy ? (
          <Button size={size} variant="outline" aria-label={`Edit policy ${policy.name}`}>
            <PencilIcon aria-hidden="true" /> Edit
          </Button>
        ) : (
          <Button size={size}>
            <PlusIcon aria-hidden="true" /> Create policy
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{policy ? `Edit ${policy.name}` : "Create policy"}</DialogTitle>
          <DialogDescription>
            When assigned applications are backed up, and how long their recovery points are kept.
          </DialogDescription>
        </DialogHeader>
        <PolicyForm
          key={formKey}
          initial={initial}
          submitLabel={policy ? "Save policy" : "Create policy"}
          onSubmit={(req) => {
            setError(null);
            save.mutate(req);
          }}
          pending={save.isPending}
          error={error}
          onCancel={() => setOpen(false)}
        />
      </DialogContent>
    </Dialog>
  );
}

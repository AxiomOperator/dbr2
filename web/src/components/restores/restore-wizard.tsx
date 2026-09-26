// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, PlusIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon, WandSparklesIcon } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useId, useMemo, useState } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { RestoreModeBadge } from "@/components/restores/restore-badges";
import { RestorePreviewView } from "@/components/restores/restore-preview";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage, isApiError } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import { useAgents, useRecoveryPoint, useRestorePreview } from "@/lib/api/hooks";
import {
  ManifestSchema,
  PERMISSION_BACKUP_READ,
  type ManifestComponent,
  type RecoveryPoint,
} from "@/lib/api/protection-schemas";
import {
  MAX_REASON,
  PERMISSION_RESTORE_EXECUTE,
  PERMISSION_RESTORE_PRODUCTION,
  type Preview,
  type RestoreBody,
} from "@/lib/api/restore-schemas";
import { formatBytes, formatDateTime } from "@/lib/format";
import {
  checkStart,
  defaultSelection,
  remappablePaths,
  selectableComponents,
  suggestRemaps,
  validateRemaps,
  type RemapRow,
} from "@/lib/restore";
import { cn } from "@/lib/utils";

type WizardStep = 1 | 2 | 3;
const STEP_TITLES: Record<WizardStep, string> = { 1: "Target", 2: "Impact preview", 3: "Confirm" };

/** Message for a failed start: 409 blocked / busy, 403 production permission, 400 safeguards. */
export function startRestoreErrorMessage(err: unknown): string {
  if (isApiError(err) && err.status === 409) {
    return `${err.detail ? `${err.detail}. ` : ""}The restore is blocked by a collision, or another backup or restore is running for this application. Review the preview and try again.`;
  }
  if (isApiError(err) && err.status === 403) {
    return `You do not have permission to start this restore.${err.detail ? ` (${err.detail})` : ""}`;
  }
  return actionErrorMessage(err);
}

function workingDirOf(manifest: unknown): string | undefined {
  if (manifest && typeof manifest === "object" && "application" in manifest) {
    const app = (manifest as { application?: unknown }).application;
    if (app && typeof app === "object" && "working_dir" in app) {
      const wd = (app as { working_dir?: unknown }).working_dir;
      if (typeof wd === "string" && wd) return wd;
    }
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// Remap editor
// ---------------------------------------------------------------------------

/** Editable from → to rows for bind-mount and config paths. */
export function RemapEditor({
  rows,
  onChange,
  errors = {},
  suggestions = [],
}: {
  rows: RemapRow[];
  onChange: (rows: RemapRow[]) => void;
  errors?: Record<number, string>;
  suggestions?: RemapRow[];
}) {
  const id = useId();
  const set = (i: number, patch: Partial<RemapRow>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">Path remaps</legend>
      <p className="text-xs text-muted-foreground">
        Restore bind-mount and project paths somewhere else on the target: every path starting with “from” is
        written below “to” instead. Both must be absolute paths.
      </p>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No remaps: paths are restored where they were captured.</p>
      ) : (
        <ul className="space-y-2" aria-label="Path remaps">
          {rows.map((r, i) => (
            <li key={i} className="space-y-1">
              <div className="grid grid-cols-[1fr_auto_1fr_auto] items-center gap-2">
                <Input
                  id={`${id}-from-${i}`}
                  aria-label={`Remap ${i + 1}: from`}
                  placeholder="/srv/app"
                  value={r.from}
                  aria-invalid={errors[i] ? true : undefined}
                  onChange={(e) => set(i, { from: e.target.value })}
                  className="font-mono text-xs"
                />
                <span aria-hidden="true" className="text-muted-foreground">
                  →
                </span>
                <Input
                  aria-label={`Remap ${i + 1}: to`}
                  placeholder="/srv/app-restored"
                  value={r.to}
                  aria-invalid={errors[i] ? true : undefined}
                  onChange={(e) => set(i, { to: e.target.value })}
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Remove remap ${i + 1}`}
                  onClick={() => onChange(rows.filter((_, j) => j !== i))}
                >
                  <Trash2Icon aria-hidden="true" />
                </Button>
              </div>
              {errors[i] && (
                <p className="text-xs text-destructive" role="alert">
                  Remap {i + 1}: {errors[i]}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => onChange([...rows, { from: "", to: "" }])}>
          <PlusIcon aria-hidden="true" /> Add remap
        </Button>
        {suggestions.length > 0 && (
          <Button type="button" variant="outline" size="sm" onClick={() => onChange(suggestions)}>
            <WandSparklesIcon aria-hidden="true" /> Use suggested remaps
          </Button>
        )}
      </div>
    </fieldset>
  );
}

// ---------------------------------------------------------------------------
// Step 1: target
// ---------------------------------------------------------------------------

interface TargetState {
  hostId: string;
  selected: string[];
  remaps: RemapRow[];
  /** The user edited the remaps (stop prefilling suggestions). */
  remapsTouched: boolean;
}

function TargetStep({
  rp,
  components,
  workingDir,
  value,
  onChange,
  remapErrors,
}: {
  rp: RecoveryPoint;
  components: ManifestComponent[];
  workingDir?: string;
  value: TargetState;
  onChange: (v: TargetState) => void;
  remapErrors: Record<number, string>;
}) {
  const id = useId();
  const me = useCurrentUser();
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const agents = useAgents({ enabled: canHosts });
  const hosts = useMemo(() => {
    const active = (agents.data ?? []).filter((a) => a.status === "active").map((a) => ({ id: a.id, hostname: a.hostname }));
    if (!active.some((a) => a.id === rp.host_id)) active.unshift({ id: rp.host_id, hostname: rp.hostname });
    return active.sort((a, b) => (a.id === rp.host_id ? -1 : b.id === rp.host_id ? 1 : a.hostname.localeCompare(b.hostname)));
  }, [agents.data, rp.host_id, rp.hostname]);
  const selectable = selectableComponents(components);
  const suggestions = suggestRemaps(remappablePaths(components, value.selected, workingDir));

  const setHost = (hostId: string) => {
    let remaps = value.remaps;
    if (!value.remapsTouched) remaps = hostId === rp.host_id ? [] : suggestRemaps(remappablePaths(components, value.selected, workingDir));
    onChange({ ...value, hostId, remaps });
  };
  const toggle = (name: string, on: boolean) => {
    const selected = on ? [...value.selected, name] : value.selected.filter((n) => n !== name);
    // Keep manifest order.
    const ordered = selectable.map((s) => s.component.name).filter((n) => selected.includes(n));
    onChange({ ...value, selected: ordered });
  };

  return (
    <div className="space-y-6">
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-host`}>Target host</Label>
        <Select value={value.hostId} onValueChange={setHost}>
          <SelectTrigger id={`${id}-host`} className="w-full max-w-md">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {hosts.map((h) => (
              <SelectItem key={h.id} value={h.id}>
                {h.hostname}
                {h.id === rp.host_id ? " (source host: restore in place)" : ""}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">
          {canHosts
            ? "Active hosts only. Another host restores a copy of the application there (alternate host)."
            : `Only the source host is offered: listing other hosts requires the ${PERMISSION_HOST_READ} permission.`}
        </p>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Components</legend>
        <p className="text-xs text-muted-foreground">
          All captured components are selected. Filesystem metadata (ownership, modes, SELinux labels) follows its
          volume or bind mount automatically.
        </p>
        <ul className="divide-y rounded-lg border" aria-label="Components to restore">
          {selectable.map(({ component: c, restorable, fsmeta }) => {
            const cid = `${id}-c-${c.name}`;
            return (
              <li key={c.name} className="flex items-start gap-3 p-3" data-restorable={restorable ? "true" : "false"}>
                <Checkbox
                  id={cid}
                  aria-label={`Restore ${c.name}`}
                  checked={value.selected.includes(c.name)}
                  disabled={!restorable}
                  onCheckedChange={(v) => toggle(c.name, v === true)}
                  className="mt-0.5"
                />
                <div className="min-w-0 flex-1 space-y-0.5">
                  <label htmlFor={cid} className={cn("flex flex-wrap items-center gap-2", !restorable && "opacity-60")}>
                    <span className="font-mono text-xs break-all">{c.name}</span>
                    <Badge variant="outline">{c.kind}</Badge>
                    <span className="text-xs text-muted-foreground tabular-nums">{formatBytes(c.size_bytes)}</span>
                  </label>
                  {!restorable && (
                    <p className="text-xs text-muted-foreground">Not restorable: the component was not captured ({c.status}).</p>
                  )}
                  {restorable && fsmeta && (
                    <p className="text-xs text-muted-foreground">
                      + <span className="font-mono">{fsmeta.name}</span> included automatically
                    </p>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
        {value.selected.length === 0 && (
          <p className="text-xs text-destructive" role="alert">
            Select at least one component.
          </p>
        )}
      </fieldset>

      <RemapEditor
        rows={value.remaps}
        onChange={(remaps) => onChange({ ...value, remaps, remapsTouched: true })}
        errors={remapErrors}
        suggestions={value.hostId !== rp.host_id ? suggestions : []}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 3: confirm
// ---------------------------------------------------------------------------

function ConfirmStep({
  preview,
  reason,
  confirmation,
  onReason,
  onConfirmation,
}: {
  preview: Preview;
  reason: string;
  confirmation: string;
  onReason: (v: string) => void;
  onConfirmation: (v: string) => void;
}) {
  const id = useId();
  const me = useCurrentUser();
  const canProduction = hasPermission(me, PERMISSION_RESTORE_PRODUCTION);
  const check = checkStart({ preview, reason, confirmation, canProduction });

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <RestoreModeBadge mode={preview.mode} />
        <span>
          Restore <strong>{preview.application_name}</strong> to <strong>{preview.target_hostname}</strong>
          {" · "}
          {preview.components.length} component{preview.components.length === 1 ? "" : "s"}
        </span>
        {preview.production && <Badge variant="destructive">Production</Badge>}
      </div>

      <Alert>
        <ShieldCheckIcon aria-hidden="true" />
        <AlertTitle>How the restore protects your data</AlertTitle>
        <AlertDescription>
          Data is restored to a staging location first and verified, then swapped in; the previous data is kept
          until the application is healthy. Any failure rolls the restore back automatically.
        </AlertDescription>
      </Alert>

      {preview.production && !canProduction && (
        <Alert variant="destructive">
          <AlertTitle>You cannot start this production restore</AlertTitle>
          <AlertDescription>
            {preview.production_reasons.length > 0 ? `It is a production restore (${preview.production_reasons.join("; ")}). ` : ""}
            Production restores require the <code>{PERMISSION_RESTORE_PRODUCTION}</code> permission. Ask a Backup
            Administrator, or restore to a non-production host.
          </AlertDescription>
        </Alert>
      )}

      <div className="space-y-1.5">
        <Label htmlFor={`${id}-reason`}>
          Reason{" "}
          <span className="font-normal text-muted-foreground">{check.reasonRequired ? "(required)" : "(optional)"}</span>
        </Label>
        <Textarea
          id={`${id}-reason`}
          value={reason}
          maxLength={MAX_REASON}
          placeholder="Change ticket or why, e.g. INC-48391: restore after failed migration"
          onChange={(e) => onReason(e.target.value)}
          aria-required={check.reasonRequired || undefined}
        />
        <p className="text-xs text-muted-foreground">Stored in the audit log.</p>
      </div>

      {check.confirmationRequired && (
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-confirm`}>
            Type <code className="font-mono">{preview.application_name}</code> to confirm
          </Label>
          <Input
            id={`${id}-confirm`}
            value={confirmation}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => onConfirmation(e.target.value)}
            aria-invalid={confirmation !== "" && !check.confirmationMatches ? true : undefined}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">Must match the application name exactly (case-sensitive).</p>
        </div>
      )}

      {check.blockers.length > 0 && (
        <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground" aria-label="Before you can start" aria-live="polite">
          {check.blockers.map((b) => (
            <li key={b}>{b}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Wizard
// ---------------------------------------------------------------------------

function StepHeader({ step }: { step: WizardStep }) {
  return (
    <ol aria-label="Restore wizard steps" className="flex flex-wrap items-center gap-2 text-sm">
      {([1, 2, 3] as WizardStep[]).map((s) => (
        <li
          key={s}
          aria-current={s === step ? "step" : undefined}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-full border px-3 py-1",
            s === step ? "border-primary font-medium" : s < step ? "text-foreground" : "text-muted-foreground",
          )}
        >
          <span className="tabular-nums">{s}.</span> {STEP_TITLES[s]}
        </li>
      ))}
    </ol>
  );
}

function WizardBody({ rp, components, workingDir }: { rp: RecoveryPoint; components: ManifestComponent[]; workingDir?: string }) {
  const router = useRouter();
  const toast = useToast();
  const queryClient = useQueryClient();
  const me = useCurrentUser();
  const [step, setStep] = useState<WizardStep>(1);
  const [target, setTarget] = useState<TargetState>(() => ({
    hostId: rp.host_id,
    selected: defaultSelection(components),
    remaps: [],
    remapsTouched: false,
  }));
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [startError, setStartError] = useState<string | null>(null);

  const remapCheck = useMemo(() => validateRemaps(target.remaps), [target.remaps]);
  const body = useMemo<RestoreBody>(() => {
    const b: RestoreBody = { target_host_id: target.hostId, components: target.selected };
    if (remapCheck.ok && remapCheck.remaps.length > 0) b.path_remaps = remapCheck.remaps;
    return b;
  }, [target.hostId, target.selected, remapCheck]);
  const targetValid = target.selected.length > 0 && remapCheck.ok;

  const preview = useRestorePreview(rp.id, body, { enabled: step >= 2 && targetValid });
  const p = preview.data;

  const start = useMutation({
    mutationFn: () =>
      api.startRestore(rp.id, {
        ...body,
        reason: reason.trim() || undefined,
        confirmation: p?.production ? confirmation.trim() : undefined,
      }),
    onSuccess: async (run) => {
      toast({ title: `Restore of ${run.application_name} started`, description: `Restoring to ${run.target_hostname}.` });
      await queryClient.invalidateQueries({ queryKey: queryKeys.restoresAll });
      router.push(`/restores/${run.id}`);
    },
    onError: (err) => {
      setStartError(startRestoreErrorMessage(err));
      if (isApiError(err) && err.status === 409) void preview.refetch();
    },
  });

  const canStart =
    p !== undefined &&
    checkStart({ preview: p, reason, confirmation, canProduction: hasPermission(me, PERMISSION_RESTORE_PRODUCTION) })
      .canStart;

  return (
    <div className="space-y-6">
      <StepHeader step={step} />
      <Card>
        <CardHeader>
          <CardTitle>
            <h2>
              {step}. {STEP_TITLES[step]}
            </h2>
          </CardTitle>
          <CardDescription>
            {step === 1 && "Where to restore and what."}
            {step === 2 && "What the restore will change on the target host. Nothing has been changed yet."}
            {step === 3 && "Review the safeguards and start the restore."}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {step === 1 && (
            <TargetStep
              rp={rp}
              components={components}
              workingDir={workingDir}
              value={target}
              onChange={setTarget}
              remapErrors={remapCheck.ok ? {} : remapCheck.errors}
            />
          )}
          {step >= 2 &&
            (preview.isPending ? (
              <RowsSkeleton label="Computing the impact preview…" rows={5} />
            ) : preview.isError && !p ? (
              <QueryError title="Could not compute the preview" error={preview.error} onRetry={() => void preview.refetch()} />
            ) : p ? (
              step === 2 ? (
                <RestorePreviewView preview={p} />
              ) : (
                <ConfirmStep
                  preview={p}
                  reason={reason}
                  confirmation={confirmation}
                  onReason={setReason}
                  onConfirmation={setConfirmation}
                />
              )
            ) : null)}
          {step === 3 && startError && (
            <Alert variant="destructive">
              <AlertTitle>Could not start the restore</AlertTitle>
              <AlertDescription>{startError}</AlertDescription>
            </Alert>
          )}
        </CardContent>
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          {step > 1 && (
            <Button
              variant="outline"
              onClick={() => {
                setStartError(null);
                setStep((s) => (s - 1) as WizardStep);
              }}
              disabled={start.isPending}
            >
              Back
            </Button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {step === 2 && (
            <Button variant="ghost" onClick={() => void preview.refetch()} disabled={preview.isFetching}>
              <RefreshCwIcon aria-hidden="true" className={preview.isFetching ? "motion-safe:animate-spin" : ""} />
              Refresh preview
            </Button>
          )}
          {step === 1 && (
            <Button onClick={() => setStep(2)} disabled={!targetValid}>
              Preview impact
            </Button>
          )}
          {step === 2 && (
            <>
              {p?.blocked && <span className="text-sm text-destructive">Blocked: go back and change the target or remaps.</span>}
              <Button onClick={() => setStep(3)} disabled={!p || p.blocked || preview.isFetching}>
                Continue
              </Button>
            </>
          )}
          {step === 3 && (
            <Button
              variant={p?.production ? "destructive" : "default"}
              onClick={() => {
                setStartError(null);
                start.mutate();
              }}
              disabled={!canStart || start.isPending || preview.isFetching}
            >
              {start.isPending ? "Starting…" : "Start restore"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function WizardLoader({ recoveryPointId }: { recoveryPointId: string }) {
  const rp = useRecoveryPoint(recoveryPointId);
  if (rp.isPending) return <RowsSkeleton label="Loading recovery point…" />;
  if (rp.isError) {
    if (isApiError(rp.error) && rp.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Recovery point not found</AlertTitle>
          <AlertDescription>It may have been deleted, or the index was rebuilt.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the recovery point" error={rp.error} onRetry={() => void rp.refetch()} />;
  }
  const d = rp.data;
  const manifest = d.manifest == null ? null : ManifestSchema.safeParse(d.manifest);
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {d.application_name} on {d.hostname} · captured {formatDateTime(d.consistency_point ?? d.created_at)} ·{" "}
        <Link href={`/recovery-points/${d.id}`} className="font-mono text-xs underline underline-offset-4">
          {d.id}
        </Link>
      </p>
      {d.state !== "committed" ? (
        <Alert>
          <AlertTitle>This recovery point cannot be restored</AlertTitle>
          <AlertDescription>Only committed recovery points can be restored.</AlertDescription>
        </Alert>
      ) : !manifest?.success ? (
        <Alert variant="destructive">
          <AlertTitle>Manifest unavailable</AlertTitle>
          <AlertDescription>The recovery point&apos;s manifest is missing or not understood, so its components cannot be listed.</AlertDescription>
        </Alert>
      ) : (
        <WizardBody rp={d} components={manifest.data.components} workingDir={workingDirOf(d.manifest)} />
      )}
    </div>
  );
}

/** The restore wizard for one recovery point (requires `restore.execute` and `backup.read`). */
export function RestoreWizard({ recoveryPointId }: { recoveryPointId: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link
        href={`/recovery-points/${recoveryPointId}`}
        className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}
      >
        <ArrowLeftIcon aria-hidden="true" /> Recovery point
      </Link>
      <h1 className="text-2xl font-semibold tracking-tight">Restore</h1>
      {!hasPermission(me, PERMISSION_RESTORE_EXECUTE) ? (
        <AccessDenied what="Restoring" permission={PERMISSION_RESTORE_EXECUTE} />
      ) : !hasPermission(me, PERMISSION_BACKUP_READ) ? (
        <AccessDenied what="Reading the recovery point to restore" permission={PERMISSION_BACKUP_READ} />
      ) : (
        <WizardLoader recoveryPointId={recoveryPointId} />
      )}
    </div>
  );
}

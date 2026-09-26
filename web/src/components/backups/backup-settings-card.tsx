// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useId, useMemo, useRef, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { modeLabel } from "@/components/backups/backup-badges";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { ApplicationDetail } from "@/lib/api/fleet-schemas";
import {
  CONSISTENCY_MODES,
  HOOK_MAX_TIMEOUT_SECONDS,
  MAX_QUIESCE_MAX_MINUTES,
  MAX_QUIESCE_MIN_MINUTES,
  PERMISSION_POLICY_MANAGE,
  PERMISSION_REPOSITORY_READ,
  UpdateBackupSettingsRequestSchema,
  type BackupSettings,
  type ConsistencyMode,
  type Hook,
  type UpdateBackupSettingsRequest,
} from "@/lib/api/protection-schemas";
import { useBackupSettings, useRepositories } from "@/lib/api/hooks";
import { formatDateTime } from "@/lib/format";
import {
  componentDetail,
  componentKind,
  hookTargets,
  joinCommand,
  splitCommand,
  type BackupComponent,
} from "@/lib/protection";

const DEFAULT_REPO = "__default__";
const AUTO = "automatic";

export const MODE_HELP: Record<ConsistencyMode | typeof AUTO, string> = {
  automatic:
    "Quiesced when pre- or post-hooks are defined, otherwise Live (crash-consistent). Recommended for minimal downtime.",
  live: "Captured while the application runs. Recovery points are crash-consistent only, like pulling the plug.",
  quiesced:
    "Runs the pre-hooks (e.g. flush and lock), captures, then resumes and runs the post-hooks. Bounded by the maximum quiesce time; if the control plane is lost the agent resumes the application on its own.",
  offline: "Stops the application's containers for the capture and starts them again afterwards: fully consistent, with downtime.",
};

// ---------------------------------------------------------------------------
// Draft <-> request (pure, tested)
// ---------------------------------------------------------------------------

export interface HookDraft {
  key: number;
  container: string;
  command: string;
  timeout: string;
  optional: boolean;
}

export interface SettingsDraft {
  repositoryId: string;
  mode: ConsistencyMode | typeof AUTO;
  quiesceMinutes: string;
  preHooks: HookDraft[];
  postHooks: HookDraft[];
  optional: string[];
  excluded: string[];
}

let hookKey = 1;

export function hookToDraft(h: Hook): HookDraft {
  return {
    key: hookKey++,
    container: h.container,
    command: joinCommand(h.command),
    timeout: h.timeout_seconds ? String(h.timeout_seconds) : "",
    optional: h.optional ?? false,
  };
}

export function settingsToDraft(s: BackupSettings): SettingsDraft {
  return {
    repositoryId: s.repository_id ?? DEFAULT_REPO,
    mode: s.consistency_mode ?? AUTO,
    quiesceMinutes: String(Math.max(1, Math.round(s.max_quiesce_seconds / 60))),
    preHooks: s.pre_hooks.map(hookToDraft),
    postHooks: s.post_hooks.map(hookToDraft),
    optional: [...s.optional_components],
    excluded: [...s.excluded_components],
  };
}

function hooksToRequest(hooks: HookDraft[], label: string): UpdateBackupSettingsRequest["pre_hooks"] {
  return hooks.map((h, i) => {
    const where = `${label} ${i + 1}`;
    if (!h.container.trim()) throw new Error(`${where}: choose the container (service name, container name or ID).`);
    let argv: string[];
    try {
      argv = splitCommand(h.command);
    } catch (err) {
      throw new Error(`${where}: ${(err as Error).message}`);
    }
    if (argv.length === 0) throw new Error(`${where}: enter the command.`);
    const out: UpdateBackupSettingsRequest["pre_hooks"][number] = { container: h.container.trim(), command: argv };
    if (h.timeout.trim() !== "") {
      const t = Number(h.timeout);
      if (!Number.isInteger(t) || t < 0 || t > HOOK_MAX_TIMEOUT_SECONDS) {
        throw new Error(`${where}: the timeout is 0–${HOOK_MAX_TIMEOUT_SECONDS} whole seconds.`);
      }
      out.timeout_seconds = t;
    }
    if (h.optional) out.optional = true;
    return out;
  });
}

/**
 * Builds the PUT body. The maximum quiesce is edited in minutes and stored in
 * seconds; when the minutes are unchanged the original seconds are kept.
 */
export function draftToRequest(
  draft: SettingsDraft,
  initial: BackupSettings,
): { ok: true; value: UpdateBackupSettingsRequest } | { ok: false; error: string } {
  const minutes = Number(draft.quiesceMinutes);
  if (
    draft.quiesceMinutes.trim() === "" ||
    !Number.isInteger(minutes) ||
    minutes < MAX_QUIESCE_MIN_MINUTES ||
    minutes > MAX_QUIESCE_MAX_MINUTES
  ) {
    return {
      ok: false,
      error: `The maximum quiesce time is ${MAX_QUIESCE_MIN_MINUTES}–${MAX_QUIESCE_MAX_MINUTES} whole minutes.`,
    };
  }
  const initialMinutes = Math.max(1, Math.round(initial.max_quiesce_seconds / 60));
  const seconds = minutes === initialMinutes ? initial.max_quiesce_seconds : minutes * 60;
  let pre, post;
  try {
    pre = hooksToRequest(draft.preHooks, "Pre-hook");
    post = hooksToRequest(draft.postHooks, "Post-hook");
  } catch (err) {
    return { ok: false, error: (err as Error).message };
  }
  const excluded = new Set(draft.excluded);
  const parsed = UpdateBackupSettingsRequestSchema.safeParse({
    repository_id: draft.repositoryId === DEFAULT_REPO ? null : draft.repositoryId,
    consistency_mode: draft.mode === AUTO ? null : draft.mode,
    max_quiesce_seconds: seconds,
    pre_hooks: pre,
    post_hooks: post,
    // A component cannot be both optional and excluded; config is always required.
    optional_components: draft.optional.filter((c) => !excluded.has(c) && c !== "config"),
    excluded_components: draft.excluded,
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

// ---------------------------------------------------------------------------
// Hooks editor
// ---------------------------------------------------------------------------

function ArgvPreview({ command }: { command: string }) {
  let argv: string[] | null = null;
  let error: string | null = null;
  try {
    argv = splitCommand(command);
  } catch (err) {
    error = (err as Error).message;
  }
  if (error) return <p className="text-xs text-destructive">{error}</p>;
  if (!argv || argv.length === 0) return null;
  return (
    <ol className="flex flex-wrap gap-1" aria-label="Arguments">
      {argv.map((a, i) => (
        <li key={i}>
          <Badge variant="secondary" className="font-mono whitespace-pre">
            {a === "" ? "''" : a}
          </Badge>
        </li>
      ))}
    </ol>
  );
}

function HooksEditor({
  title,
  description,
  hooks,
  onChange,
  targets,
  disabled,
}: {
  title: string;
  description: string;
  hooks: HookDraft[];
  onChange: (next: HookDraft[]) => void;
  targets: string[];
  disabled: boolean;
}) {
  const id = useId();
  const update = (key: number, patch: Partial<HookDraft>) =>
    onChange(hooks.map((h) => (h.key === key ? { ...h, ...patch } : h)));
  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{title}</legend>
      <p className="text-xs text-muted-foreground">{description}</p>
      <datalist id={`${id}-targets`}>
        {targets.map((t) => (
          <option key={t} value={t} />
        ))}
      </datalist>
      {hooks.length === 0 && <p className="text-sm text-muted-foreground">None.</p>}
      {hooks.map((h, i) => (
        <div key={h.key} className="space-y-2 rounded-lg border p-3" data-testid="hook-row">
          <div className="grid gap-3 sm:grid-cols-[12rem_1fr]">
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-${h.key}-container`}>
                {title.replace(/s$/, "")} {i + 1}: container
              </Label>
              <Input
                id={`${id}-${h.key}-container`}
                list={`${id}-targets`}
                value={h.container}
                onChange={(e) => update(h.key, { container: e.target.value })}
                placeholder="service or container"
                disabled={disabled}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-${h.key}-command`}>
                {title.replace(/s$/, "")} {i + 1}: command
              </Label>
              <Input
                id={`${id}-${h.key}-command`}
                value={h.command}
                onChange={(e) => update(h.key, { command: e.target.value })}
                placeholder={`e.g. sh -c 'psql -U app -c "CHECKPOINT"'`}
                className="font-mono text-xs"
                spellCheck={false}
                disabled={disabled}
              />
              <ArgvPreview command={h.command} />
            </div>
          </div>
          <div className="flex flex-wrap items-end gap-4">
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-${h.key}-timeout`}>Timeout (seconds)</Label>
              <Input
                id={`${id}-${h.key}-timeout`}
                type="number"
                inputMode="numeric"
                min={0}
                max={HOOK_MAX_TIMEOUT_SECONDS}
                value={h.timeout}
                onChange={(e) => update(h.key, { timeout: e.target.value })}
                placeholder="default"
                className="w-28"
                disabled={disabled}
              />
            </div>
            <div className="flex h-8 items-center gap-2">
              <Checkbox
                id={`${id}-${h.key}-optional`}
                checked={h.optional}
                onCheckedChange={(v) => update(h.key, { optional: v === true })}
                disabled={disabled}
              />
              <Label htmlFor={`${id}-${h.key}-optional`} className="font-normal">
                Optional (a failure does not fail the backup)
              </Label>
            </div>
            {!disabled && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="ml-auto"
                onClick={() => onChange(hooks.filter((x) => x.key !== h.key))}
                aria-label={`Remove ${title.replace(/s$/, "").toLowerCase()} ${i + 1}`}
              >
                <Trash2Icon aria-hidden="true" /> Remove
              </Button>
            )}
          </div>
        </div>
      ))}
      {!disabled && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...hooks, { key: hookKey++, container: "", command: "", timeout: "", optional: false }])}
        >
          <PlusIcon aria-hidden="true" /> Add {title.replace(/s$/, "").toLowerCase()}
        </Button>
      )}
    </fieldset>
  );
}

// ---------------------------------------------------------------------------
// Components
// ---------------------------------------------------------------------------

function ComponentsEditor({
  components,
  optional,
  excluded,
  onChange,
  disabled,
}: {
  components: BackupComponent[];
  optional: string[];
  excluded: string[];
  onChange: (next: { optional: string[]; excluded: string[] }) => void;
  disabled: boolean;
}) {
  const id = useId();
  const opt = new Set(optional);
  const exc = new Set(excluded);
  const toggle = (list: Set<string>, name: string, on: boolean) => {
    const next = new Set(list);
    if (on) next.add(name);
    else next.delete(name);
    return [...next];
  };
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">Components</legend>
      <p className="text-xs text-muted-foreground">
        Optional components are best-effort: if one fails the recovery point is committed as Partial.
        Excluded components are not captured at all. The configuration is always required.
      </p>
      <div className="rounded-lg border">
        <Table aria-label="Backup components">
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Component</TableHead>
              <TableHead scope="col">Source</TableHead>
              <TableHead scope="col">Optional</TableHead>
              <TableHead scope="col">Excluded</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {components.map((c, i) => (
              <TableRow key={c.name}>
                <TableCell className="font-mono text-xs break-all whitespace-normal">{c.name}</TableCell>
                <TableCell className="text-xs break-all whitespace-normal text-muted-foreground">{c.detail}</TableCell>
                <TableCell>
                  <Checkbox
                    id={`${id}-opt-${i}`}
                    aria-label={`${c.name} optional`}
                    checked={opt.has(c.name) && !exc.has(c.name)}
                    disabled={disabled || c.name === "config" || exc.has(c.name)}
                    onCheckedChange={(v) => onChange({ optional: toggle(opt, c.name, v === true), excluded })}
                  />
                </TableCell>
                <TableCell>
                  <Checkbox
                    id={`${id}-exc-${i}`}
                    aria-label={`${c.name} excluded`}
                    checked={exc.has(c.name)}
                    disabled={disabled}
                    onCheckedChange={(v) =>
                      onChange({
                        excluded: toggle(exc, c.name, v === true),
                        optional: v === true ? toggle(opt, c.name, false) : optional,
                      })
                    }
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </fieldset>
  );
}

/**
 * The application's backup components as the server plans them
 * (`protection.components`), plus configured optional / excluded names the
 * plan does not list (excluded components are left out of the plan).
 */
export function componentRows(app: ApplicationDetail, s: Pick<SettingsDraft, "optional" | "excluded">): BackupComponent[] {
  const rows: BackupComponent[] = (app.protection?.components ?? []).map((c) => ({
    name: c.name,
    kind: c.kind,
    detail: componentDetail(app.analysis, c.name) ?? "Not in the latest analysis",
  }));
  if (!rows.some((r) => r.name === "config")) {
    rows.unshift({ name: "config", kind: "config", detail: componentDetail(app.analysis, "config")! });
  }
  const known = new Set(rows.map((r) => r.name));
  for (const name of [...s.optional, ...s.excluded]) {
    if (known.has(name)) continue;
    known.add(name);
    rows.push({ name, kind: componentKind(name), detail: componentDetail(app.analysis, name) ?? "Not in the latest analysis" });
  }
  return rows;
}

// ---------------------------------------------------------------------------
// Card
// ---------------------------------------------------------------------------

function SettingsForm({
  app,
  initial,
  canManage,
}: {
  app: ApplicationDetail;
  initial: BackupSettings;
  canManage: boolean;
}) {
  const id = useId();
  const me = useCurrentUser();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<SettingsDraft>(() => settingsToDraft(initial));
  const [error, setError] = useState<string | null>(null);
  const repos = useRepositories({ enabled: hasPermission(me, PERMISSION_REPOSITORY_READ) });
  const targets = useMemo(() => hookTargets(app.analysis), [app.analysis]);
  const rows = componentRows(app, draft);
  const errorRef = useRef<HTMLDivElement>(null);

  const save = useMutation({
    mutationFn: (req: UpdateBackupSettingsRequest) => api.updateBackupSettings(app.id, req),
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.backupSettings(app.id), updated);
      toast({ title: "Backup settings saved" });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const disabled = !canManage || save.isPending;
  const set = (patch: Partial<SettingsDraft>) => setDraft((d) => ({ ...d, ...patch }));

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const res = draftToRequest(draft, initial);
    if (!res.ok) {
      setError(res.error);
      errorRef.current?.scrollIntoView?.({ block: "nearest" });
      return;
    }
    save.mutate(res.value);
  }

  const repoOptions = repos.data ?? [];
  const selectedKnown = draft.repositoryId === DEFAULT_REPO || repoOptions.some((r) => r.id === draft.repositoryId);

  return (
    <form onSubmit={submit} noValidate>
      <CardContent className="space-y-6">
        <div ref={errorRef}>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor={`${id}-repo`}>Repository</Label>
            <Select value={draft.repositoryId} onValueChange={(v) => set({ repositoryId: v })} disabled={disabled}>
              <SelectTrigger id={`${id}-repo`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={DEFAULT_REPO}>Default Repository</SelectItem>
                {repoOptions.map((r) => (
                  <SelectItem key={r.id} value={r.id} disabled={r.status !== "ready" && r.id !== draft.repositoryId}>
                    {r.name}
                    {r.status !== "ready" ? ` (${r.status.replace("_", " ")})` : ""}
                  </SelectItem>
                ))}
                {!selectedKnown && <SelectItem value={draft.repositoryId}>{draft.repositoryId}</SelectItem>}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-quiesce`}>Maximum quiesce (minutes)</Label>
            <Input
              id={`${id}-quiesce`}
              type="number"
              inputMode="numeric"
              min={MAX_QUIESCE_MIN_MINUTES}
              max={MAX_QUIESCE_MAX_MINUTES}
              value={draft.quiesceMinutes}
              onChange={(e) => set({ quiesceMinutes: e.target.value })}
              aria-describedby={`${id}-quiesce-help`}
              disabled={disabled}
            />
            <p id={`${id}-quiesce-help`} className="text-xs text-muted-foreground">
              {MAX_QUIESCE_MIN_MINUTES}–{MAX_QUIESCE_MAX_MINUTES} minutes. The agent&apos;s dead-man switch
              resumes the application 10 minutes after this.
            </p>
          </div>
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-mode`}>Consistency mode</Label>
          <Select
            value={draft.mode}
            onValueChange={(v) => set({ mode: v as SettingsDraft["mode"] })}
            disabled={disabled}
          >
            <SelectTrigger id={`${id}-mode`} className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={AUTO}>Automatic</SelectItem>
              {CONSISTENCY_MODES.map((m) => (
                <SelectItem key={m} value={m}>
                  {modeLabel(m)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground" data-testid="mode-help">
            {MODE_HELP[draft.mode]}
          </p>
          {initial.effective_mode && (
            <p className="text-xs">
              Effective mode (saved settings): <strong>{modeLabel(initial.effective_mode)}</strong>
            </p>
          )}
        </div>
        <HooksEditor
          title="Pre-hooks"
          description="Run in a container before the capture (e.g. flush or lock), with docker exec: no shell unless the command starts one. Quoting works like a shell; nothing is expanded."
          hooks={draft.preHooks}
          onChange={(preHooks) => set({ preHooks })}
          targets={targets}
          disabled={disabled}
        />
        <HooksEditor
          title="Post-hooks"
          description="Run after the capture, also when it fails (e.g. unlock)."
          hooks={draft.postHooks}
          onChange={(postHooks) => set({ postHooks })}
          targets={targets}
          disabled={disabled}
        />
        <ComponentsEditor
          components={rows}
          optional={draft.optional}
          excluded={draft.excluded}
          onChange={(next) => set(next)}
          disabled={disabled}
        />
      </CardContent>
      <CardFooter className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {initial.updated_at ? `Last changed ${formatDateTime(initial.updated_at)}. ` : "Defaults. "}
          Changes are audited.
        </p>
        {canManage && (
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={save.isPending}
              onClick={() => {
                setDraft(settingsToDraft(initial));
                setError(null);
              }}
            >
              Reset
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save settings"}
            </Button>
          </div>
        )}
      </CardFooter>
    </form>
  );
}

/** Backup settings of an application: view with `policy.read`, edit with `policy.manage`. */
export function BackupSettingsCard({ app }: { app: ApplicationDetail }) {
  const me = useCurrentUser();
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const settings = useBackupSettings(app.id);

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Backup settings</h2>
        </CardTitle>
        <CardDescription>
          How this application is captured.
          {!canManage && (
            <>
              {" "}
              Read-only: editing requires <code>{PERMISSION_POLICY_MANAGE}</code>.
            </>
          )}
        </CardDescription>
      </CardHeader>
      {settings.isPending ? (
        <CardContent>
          <RowsSkeleton label="Loading backup settings…" rows={4} />
        </CardContent>
      ) : settings.isError ? (
        <CardContent>
          <QueryError
            title="Could not load the backup settings"
            error={settings.error}
            onRetry={() => void settings.refetch()}
          />
        </CardContent>
      ) : (
        // Re-mount (fresh draft) whenever the saved settings change.
        <SettingsForm key={settings.data.updated_at ?? "defaults"} app={app} initial={settings.data} canManage={canManage} />
      )}
    </Card>
  );
}

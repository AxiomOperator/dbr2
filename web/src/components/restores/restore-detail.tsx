// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  ArrowLeftIcon,
  CheckIcon,
  ChevronRightIcon,
  CircleIcon,
  LoaderCircleIcon,
  XIcon,
} from "lucide-react";
import Link from "next/link";
import { useState, type ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { CopyButton } from "@/components/common/copy-button";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import {
  OutcomeBadge,
  ProductionBadge,
  RestoreModeBadge,
  RestoreStateBadge,
} from "@/components/restores/restore-badges";
import { RestorePreviewView } from "@/components/restores/restore-preview";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { isApiError } from "@/lib/api/client";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import { RESTORE_POLL_MS, useAgents, useRestore } from "@/lib/api/hooks";
import { PERMISSION_BACKUP_READ } from "@/lib/api/protection-schemas";
import {
  isActiveRestore,
  PERMISSION_RESTORE_READ,
  runPreview,
  runResult,
  type ContainerHealth,
  type RestoreResult,
  type RestoreRun,
} from "@/lib/api/restore-schemas";
import { formatBytes, formatDateTime } from "@/lib/format";
import { formatDuration, restoreSteps, type StepView } from "@/lib/restore";
import { cn } from "@/lib/utils";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,10rem)_1fr] gap-3 py-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

const mono = (s: string) => <span className="font-mono text-xs break-all">{s}</span>;

const STEP_STATUS_TEXT: Record<StepView["status"], string> = {
  done: "done",
  current: "in progress",
  pending: "not reached",
  failed: "failed here",
};

/** The workflow's steps with done / current / failed markers. */
export function StepProgress({ steps }: { steps: StepView[] }) {
  return (
    <ol aria-label="Restore progress" className="flex flex-wrap items-center gap-x-1 gap-y-2 text-xs">
      {steps.map((s, i) => (
        <li
          key={s.step}
          data-step={s.step}
          data-status={s.status}
          aria-current={s.status === "current" ? "step" : undefined}
          className="flex items-center gap-1"
        >
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-full border px-2 py-0.5",
              s.status === "done" && "border-emerald-500/50 text-emerald-700 dark:text-emerald-400",
              s.status === "current" && "border-sky-500/60 bg-sky-500/10 font-medium text-sky-700 dark:text-sky-400",
              s.status === "failed" && "border-destructive/60 bg-destructive/10 font-medium text-destructive",
              s.status === "pending" && "text-muted-foreground",
            )}
          >
            {s.status === "done" && <CheckIcon aria-hidden="true" className="size-3" />}
            {s.status === "current" && <LoaderCircleIcon aria-hidden="true" className="size-3 motion-safe:animate-spin" />}
            {s.status === "failed" && <XIcon aria-hidden="true" className="size-3" />}
            {s.status === "pending" && <CircleIcon aria-hidden="true" className="size-3" />}
            {s.label}
            <span className="sr-only"> ({STEP_STATUS_TEXT[s.status]})</span>
          </span>
          {i < steps.length - 1 && <ChevronRightIcon aria-hidden="true" className="size-3 text-muted-foreground" />}
        </li>
      ))}
    </ol>
  );
}

function SummaryCard({ run }: { run: RestoreRun }) {
  const me = useCurrentUser();
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const canRps = hasPermission(me, PERMISSION_BACKUP_READ);
  const crossHost = run.source_host_id !== run.target_host_id;
  // The run carries only the source host's ID: resolve its name when allowed.
  const agents = useAgents({ enabled: canHosts && crossHost });
  const sourceName = crossHost
    ? (agents.data?.find((a) => a.id === run.source_host_id)?.hostname ?? run.source_host_id)
    : run.target_hostname;
  const host = (id: string, name: string) =>
    canHosts ? (
      <Link href={`/hosts/${id}`} className="underline-offset-4 hover:underline">
        {name}
      </Link>
    ) : (
      name
    );
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Summary</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-x-8 text-sm lg:grid-cols-2">
          <div className="divide-y">
            <Field label="Application">
              {canApps ? (
                <Link href={`/applications/${run.application_id}`} className="underline-offset-4 hover:underline">
                  {run.application_name}
                </Link>
              ) : (
                run.application_name
              )}
            </Field>
            <Field label="Recovery point">
              {canRps ? (
                <Link href={`/recovery-points/${run.recovery_point_id}`} className="font-mono text-xs underline-offset-4 hover:underline">
                  {run.recovery_point_id}
                </Link>
              ) : (
                mono(run.recovery_point_id)
              )}
            </Field>
            <Field label="Source host">
              {host(run.source_host_id, sourceName)}
            </Field>
            <Field label="Target host">{host(run.target_host_id, run.target_hostname)}</Field>
            <Field label="Components">
              {run.components.length === 0 ? (
                "—"
              ) : (
                <span className="flex flex-wrap gap-1">
                  {run.components.map((c) => (
                    <Badge key={c} variant="outline" className="font-mono">
                      {c}
                    </Badge>
                  ))}
                </span>
              )}
            </Field>
            <Field label="Path remaps">
              {run.path_remaps.length === 0 ? (
                "None"
              ) : (
                <ul className="space-y-0.5">
                  {run.path_remaps.map((r) => (
                    <li key={r.from} className="font-mono text-xs break-all">
                      {r.from} → {r.to}
                    </li>
                  ))}
                </ul>
              )}
            </Field>
          </div>
          <div className="divide-y">
            <Field label="Requested by">{run.requested_by || "—"}</Field>
            <Field label="Reason">{run.reason ? <span className="whitespace-pre-wrap">{run.reason}</span> : "—"}</Field>
            <Field label="Created">{formatDateTime(run.created_at)}</Field>
            <Field label="Started">{formatDateTime(run.started_at)}</Field>
            <Field label="Finished">{formatDateTime(run.finished_at)}</Field>
            <Field label="Duration">{formatDuration(run.started_at, run.finished_at) ?? "—"}</Field>
            <Field label="Workflow">{run.workflow_id ? mono(run.workflow_id) : "—"}</Field>
          </div>
        </dl>
      </CardContent>
    </Card>
  );
}

function LogTail({ c }: { c: ContainerHealth }) {
  const [open, setOpen] = useState(false);
  if (!c.log_tail) return null;
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <Button variant="ghost" size="xs" className="-ml-2">
          <ChevronRightIcon aria-hidden="true" className={open ? "rotate-90 transition-transform" : "transition-transform"} />
          Log tail of {c.name}
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre
          data-testid={`log-tail-${c.name}`}
          className="mt-1 max-h-72 overflow-auto rounded-md border bg-muted/50 p-3 font-mono text-xs whitespace-pre-wrap"
        >
          {c.log_tail}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ResultCard({ result, state }: { result: RestoreResult; state: RestoreRun["state"] }) {
  const h = result.health;
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Result</h2>
        </CardTitle>
        <CardDescription>What the agent reported for each component, container and health check.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        {(result.rolled_back || result.rollback_error) && (
          <Alert
            variant={result.rollback_error ? "destructive" : "default"}
            className={cn(!result.rollback_error && "border-amber-500/60")}
          >
            <AlertTitle>{result.rollback_error ? "Rollback failed" : "Rolled back"}</AlertTitle>
            <AlertDescription>
              {result.rollback_error
                ? `The automatic rollback did not complete: ${result.rollback_error}. The previous data is kept at the "previous path" of each component; check the target host.`
                : "The previous data and containers were put back automatically; the application runs as before the restore."}
            </AlertDescription>
          </Alert>
        )}

        <section aria-label="Restored components" className="space-y-2">
          <h3 className="text-sm font-medium">Components</h3>
          {result.components.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {state === "failed" ? "No data was touched." : "No component results."}
            </p>
          ) : (
            <div className="rounded-lg border">
              <Table aria-label="Component results">
                <TableHeader>
                  <TableRow>
                    {["Component", "Status", "Target", "Data", "Metadata", "Previous content"].map((x) => (
                      <TableHead key={x} scope="col">
                        {x}
                      </TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {result.components.map((c) => (
                    <TableRow key={c.name} data-component-status={c.status}>
                      <TableCell className="align-top whitespace-normal">
                        <div className="font-mono text-xs break-all">{c.name}</div>
                        {c.error && <div className="mt-1 max-w-64 text-xs break-words text-destructive">{c.error}</div>}
                      </TableCell>
                      <TableCell className="align-top">
                        <OutcomeBadge status={c.status} />
                      </TableCell>
                      <TableCell className="min-w-48 align-top font-mono text-xs break-all whitespace-normal">
                        {c.target_path || "—"}
                      </TableCell>
                      <TableCell className="align-top text-xs whitespace-nowrap tabular-nums">
                        {formatBytes(c.bytes)} · {c.files} file{c.files === 1 ? "" : "s"}
                      </TableCell>
                      <TableCell className="align-top text-xs whitespace-nowrap">
                        {c.metadata_applied} applied
                        {c.verify_mismatches > 0 ? (
                          <span className="block text-destructive">{c.verify_mismatches} verify mismatches</span>
                        ) : (
                          <span className="block text-muted-foreground">verified</span>
                        )}
                      </TableCell>
                      <TableCell className="align-top font-mono text-xs break-all whitespace-normal">
                        {c.created_volume ? (
                          <span className="font-sans">Volume created</span>
                        ) : c.previous_path ? (
                          c.previous_path
                        ) : (
                          <span className="font-sans text-muted-foreground">Target did not exist</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </section>

        {result.containers.length > 0 && (
          <section aria-label="Re-created containers" className="space-y-2">
            <h3 className="text-sm font-medium">Containers</h3>
            <ul className="space-y-1 text-sm">
              {result.containers.map((c) => (
                <li key={c.name} className="flex flex-wrap items-center gap-2">
                  <code className="font-mono text-xs">{c.name}</code>
                  <OutcomeBadge status={c.status} />
                  {c.container_id && <span className="font-mono text-xs text-muted-foreground">{c.container_id.slice(0, 12)}</span>}
                  {c.was_running && <span className="text-xs text-muted-foreground">was running at capture</span>}
                  {c.error && <span className="text-xs text-destructive">{c.error}</span>}
                </li>
              ))}
            </ul>
          </section>
        )}

        {result.images.length > 0 && (
          <section aria-label="Image results" className="space-y-2">
            <h3 className="text-sm font-medium">Images</h3>
            <ul className="space-y-1 text-sm">
              {result.images.map((im) => (
                <li key={`${im.ref}-${im.digest}`} className="flex flex-wrap items-center gap-2">
                  <code className="font-mono text-xs break-all">{im.ref}</code>
                  <OutcomeBadge status={im.status} />
                  {im.error && <span className="text-xs text-destructive">{im.error}</span>}
                </li>
              ))}
            </ul>
          </section>
        )}

        {result.databases.length > 0 && (
          <p className="text-sm">
            Databases loaded: <span className="font-mono text-xs">{result.databases.join(", ")}</span>
          </p>
        )}

        {h && (
          <section aria-label="Health check" className="space-y-2">
            <h3 className="flex items-center gap-2 text-sm font-medium">
              Health check
              {h.ok ? <OutcomeBadge status="restored" /> : <Badge variant="destructive">Unhealthy</Badge>}
            </h3>
            <ul className="space-y-2 text-sm">
              {h.containers.map((c) => (
                <li key={c.container_id || c.name} data-healthy={c.ok ? "true" : "false"}>
                  <div className="flex flex-wrap items-center gap-2">
                    <code className="font-mono text-xs">{c.name}</code>
                    <Badge variant={c.ok ? "secondary" : "destructive"}>{c.ok ? "OK" : "Not OK"}</Badge>
                    <span className="text-xs text-muted-foreground">
                      {c.state}
                      {c.health && c.health !== "none" ? ` · ${c.health}` : ""}
                      {!c.ok && c.exit_code ? ` · exit code ${c.exit_code}` : ""}
                    </span>
                  </div>
                  <LogTail c={c} />
                </li>
              ))}
            </ul>
          </section>
        )}
      </CardContent>
    </Card>
  );
}

function DetailBody({ id }: { id: string }) {
  const q = useRestore(id);

  if (q.isPending) return <RowsSkeleton label="Loading restore…" />;
  if (q.isError && !q.data) {
    if (isApiError(q.error) && q.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Restore not found</AlertTitle>
          <AlertDescription>Check the link; restores are never deleted from the history.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the restore" error={q.error} onRetry={() => void q.refetch()} />;
  }

  const run = q.data;
  const preview = runPreview(run);
  const result = runResult(run);
  const steps = restoreSteps(run, preview);
  const active = isActiveRestore(run.state);

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-semibold tracking-tight">
            Restore of {run.application_name} to {run.target_hostname}
          </h1>
          <p className="inline-flex flex-wrap items-center gap-2 font-mono text-sm text-muted-foreground">
            {run.id}
            <CopyButton value={run.id} label="restore ID" />
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <RestoreStateBadge state={run.state} step={run.step} />
            <RestoreModeBadge mode={run.mode} />
            {run.production && <ProductionBadge />}
          </div>
        </div>
        <StepProgress steps={steps} />
        {active && (
          <p className="text-xs text-muted-foreground" aria-live="polite">
            Refreshes every {RESTORE_POLL_MS / 1000} s while the restore runs.
            {q.isError && " The last refresh failed; showing earlier data."}
          </p>
        )}
      </div>

      {run.state === "failed" && (
        <Alert variant="destructive">
          <AlertTitle>Restore failed</AlertTitle>
          <AlertDescription>{run.error || "The workflow failed without an error message."}</AlertDescription>
        </Alert>
      )}
      {run.state === "rolled_back" && (
        <Alert className="border-amber-500/60">
          <AlertTitle>Restore rolled back</AlertTitle>
          <AlertDescription>
            <p>{run.error || "The restore did not complete."}</p>
            <p>The previous data was kept and has been swapped back automatically.</p>
          </AlertDescription>
        </Alert>
      )}
      {run.state === "succeeded" && (
        <Alert role="status" className="border-emerald-500/50">
          <AlertTitle>Restore succeeded</AlertTitle>
          <AlertDescription>The restored data was verified and the application is healthy.</AlertDescription>
        </Alert>
      )}

      <SummaryCard run={run} />
      {result ? (
        <ResultCard result={result} state={run.state} />
      ) : (
        !active &&
        run.result != null && (
          <Alert>
            <AlertTitle>Result not understood</AlertTitle>
            <AlertDescription>The result document does not match the schema this console knows.</AlertDescription>
          </Alert>
        )
      )}

      <Card>
        <CardHeader>
          <CardTitle>
            <h2>Impact preview at request time</h2>
          </CardTitle>
          <CardDescription>What the restore was expected to change when it was requested.</CardDescription>
        </CardHeader>
        <CardContent>
          {preview ? (
            <RestorePreviewView preview={preview} recorded />
          ) : (
            <p className="text-sm text-muted-foreground">No preview was recorded with this restore.</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export function RestoreDetail({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link href="/restores" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> All restores
      </Link>
      {hasPermission(me, PERMISSION_RESTORE_READ) ? (
        <DetailBody id={id} />
      ) : (
        <AccessDenied what="Viewing restores" permission={PERMISSION_RESTORE_READ} />
      )}
    </div>
  );
}

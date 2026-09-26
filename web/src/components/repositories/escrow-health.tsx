// SPDX-License-Identifier: Apache-2.0
"use client";

// Escrow health and drills (Phase 9, ADR-0008): the problems the hourly check
// reports, each with its fix (regenerate the package → download → confirm
// code; re-confirm; add recipients; run a drill), and the annual escrow drill
// (start → download the drill package → decrypt offline → enter the code).

import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  CircleAlertIcon,
  CircleCheckIcon,
  DownloadIcon,
  FlaskConicalIcon,
  KeyRoundIcon,
  RotateCwIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  TriangleAlertIcon,
} from "lucide-react";
import Link from "next/link";
import { useId, useState, type FormEvent } from "react";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { ESCROW_SECTION_ID } from "@/components/repositories/escrow-recipients";
import {
  DownloadEscrowButton,
  EscrowConfirmForm,
  EscrowInstructions,
} from "@/components/repositories/repository-actions";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  CompleteDrillRequestSchema,
  type Drill,
  type EscrowProblem,
  type RegeneratedEscrow,
  type Repository,
} from "@/lib/api/protection-schemas";
import { useEscrowDrills, useEscrowHealth } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";
import { downloadTextFile } from "@/lib/protection";

export const DRILLS_SECTION_ID = "escrow-drills";

export const PROBLEM_LABEL: Record<EscrowProblem["code"], string> = {
  too_few_recipients: "Too few escrow recipients",
  not_confirmed: "Escrow not confirmed",
  recipients_changed: "Recipients changed: regenerate the package",
  reconfirm_due: "Re-confirmation due",
  drill_due: "Escrow drill due",
};

/** Critical problems first, then by message. */
export function sortProblems(problems: EscrowProblem[]): EscrowProblem[] {
  const rank = (p: EscrowProblem) => (p.severity === "critical" ? 0 : 1);
  return [...problems].sort((a, b) => rank(a) - rank(b) || a.message.localeCompare(b.message));
}

// ---------------------------------------------------------------------------
// Regenerate → download → confirm (reuses the create wizard's pieces)
// ---------------------------------------------------------------------------

type RegenStep = "intro" | "package" | "confirm" | "done";

/** Re-seals the Repository password to the current recipients and confirms the new package. */
export function RegenerateEscrowDialog({ repo, size = "xs" }: { repo: Pick<Repository, "id" | "name">; size?: "xs" | "sm" }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<RegenStep>("intro");
  const [pkg, setPkg] = useState<RegeneratedEscrow | null>(null);
  const [downloaded, setDownloaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const regenerate = useMutation({
    mutationFn: () => api.regenerateEscrow(repo.id),
    onSuccess: async (res) => {
      setPkg(res);
      setStep("package");
      queryClient.setQueryData(queryKeys.repository(repo.id), res.repository);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.repositories }),
        queryClient.invalidateQueries({ queryKey: queryKeys.escrowHealth }),
      ]);
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    if (next) {
      setStep("intro");
      setPkg(null);
      setDownloaded(false);
      setError(null);
      regenerate.reset();
    } else if (pkg && step !== "done") {
      toast({
        title: `Escrow of ${repo.name} is not confirmed yet`,
        description: "Enter the new package's confirmation code to confirm it (escrow health keeps reporting it until then).",
        variant: "warning",
      });
    }
    setOpen(next);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size={size} variant="outline" aria-label={`Regenerate the escrow package of ${repo.name}`}>
          <RotateCwIcon aria-hidden="true" /> Regenerate package
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Regenerate the escrow package of {repo.name}</DialogTitle>
          <DialogDescription>
            Step {step === "intro" ? 1 : step === "package" ? 2 : 3} of 3 ·{" "}
            {step === "intro" ? "Regenerate" : step === "package" ? "Store the new package" : step === "confirm" ? "Confirm key escrow" : "Confirmed"}
          </DialogDescription>
        </DialogHeader>
        {step === "intro" && (
          <div className="space-y-4">
            <p className="text-sm">
              The Repository password is read from the reposerver and sealed again to the <strong>current</strong>{" "}
              escrow recipients. The Repository stays usable; its escrow counts as unconfirmed until you enter the new
              package&apos;s confirmation code. Old packages keep working for the recipients they were sealed to: destroy
              them once the new one is stored.
            </p>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={regenerate.isPending}>
                  Cancel
                </Button>
              </DialogClose>
              <Button onClick={() => regenerate.mutate()} disabled={regenerate.isPending}>
                {regenerate.isPending ? "Regenerating…" : "Regenerate package"}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "package" && pkg && (
          <div className="space-y-4">
            <EscrowInstructions filename={pkg.escrow_filename} />
            <div className="flex flex-wrap items-center gap-3">
              <Button
                type="button"
                onClick={() => {
                  downloadTextFile(pkg.escrow_filename, pkg.escrow_package);
                  setDownloaded(true);
                }}
              >
                <DownloadIcon aria-hidden="true" /> Download {pkg.escrow_filename}
              </Button>
              {downloaded && (
                <span className="inline-flex items-center gap-1 text-sm text-emerald-700 dark:text-emerald-400" role="status">
                  <CircleCheckIcon aria-hidden="true" className="size-4" /> Downloaded
                </span>
              )}
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  Confirm later
                </Button>
              </DialogClose>
              <Button type="button" onClick={() => setStep("confirm")} disabled={!downloaded}>
                I stored it offline: next
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "confirm" && pkg && (
          <div className="space-y-4">
            <p className="text-sm">
              Decrypt <code className="font-mono text-xs break-all">{pkg.escrow_filename}</code> with{" "}
              <code className="font-mono text-xs">age -d -i &lt;identity-file&gt; {pkg.escrow_filename}</code> and enter
              the <code className="font-mono text-xs">confirmation_code</code> it contains.
            </p>
            <EscrowConfirmForm
              repo={pkg.repository}
              onConfirmed={() => {
                setStep("done");
                void queryClient.invalidateQueries({ queryKey: queryKeys.escrowHealth });
              }}
            />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setStep("package")}>
                Back
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "done" && (
          <div className="space-y-4">
            <Alert role="status">
              <CircleCheckIcon aria-hidden="true" className="text-emerald-600" />
              <AlertTitle>Escrow of {repo.name} is confirmed</AlertTitle>
              <AlertDescription>The new package is sealed to every current recipient.</AlertDescription>
            </Alert>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button">Done</Button>
              </DialogClose>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Re-confirm: decrypt the stored package again and enter its code (every 90 days). */
export function ReconfirmEscrowDialog({ repo, size = "xs" }: { repo: Pick<Repository, "id" | "name">; size?: "xs" | "sm" }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [done, setDone] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) setDone(false);
      }}
    >
      <DialogTrigger asChild>
        <Button size={size} variant="outline" aria-label={`Confirm the escrow of ${repo.name}`}>
          <KeyRoundIcon aria-hidden="true" /> Confirm escrow
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Confirm the escrow of {repo.name}</DialogTitle>
          <DialogDescription>
            Prove that the stored escrow package can still be decrypted: decrypt it with an identity from the safe and
            enter its confirmation code.
          </DialogDescription>
        </DialogHeader>
        {done ? (
          <Alert role="status">
            <CircleCheckIcon aria-hidden="true" className="text-emerald-600" />
            <AlertTitle>Escrow confirmed</AlertTitle>
            <AlertDescription>The next re-confirmation is due in 90 days.</AlertDescription>
          </Alert>
        ) : (
          <div className="space-y-4">
            <EscrowInstructions />
            <DownloadEscrowButton repo={repo} size="sm" />
            <EscrowConfirmForm
              repo={repo}
              onConfirmed={() => {
                setDone(true);
                void queryClient.invalidateQueries({ queryKey: queryKeys.escrowHealth });
              }}
            />
          </div>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant={done ? "default" : "outline"}>
              {done ? "Done" : "Close"}
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Health panel
// ---------------------------------------------------------------------------

function ProblemFix({
  problem,
  repo,
  canManage,
}: {
  problem: EscrowProblem;
  repo: Repository | undefined;
  canManage: boolean;
}) {
  if (!canManage) return null;
  switch (problem.code) {
    case "too_few_recipients":
      return (
        <Link href={`#${ESCROW_SECTION_ID}`} className="text-sm font-medium underline underline-offset-4">
          Add escrow recipients
        </Link>
      );
    case "recipients_changed":
      return repo ? <RegenerateEscrowDialog repo={repo} /> : null;
    case "not_confirmed":
    case "reconfirm_due":
      return repo ? <ReconfirmEscrowDialog repo={repo} /> : null;
    case "drill_due":
      return (
        <Link href={`#${DRILLS_SECTION_ID}`} className="text-sm font-medium underline underline-offset-4">
          Run an escrow drill
        </Link>
      );
  }
}

/** Escrow health (checked hourly by the server) with a fix per problem. */
export function EscrowHealthPanel({ repositories, canManage }: { repositories: Repository[]; canManage: boolean }) {
  const health = useEscrowHealth();
  const byId = new Map(repositories.map((r) => [r.id, r]));
  return (
    <Card data-testid="escrow-health">
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-2">
            Escrow health
            {health.data &&
              (health.data.healthy ? (
                <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
                  <ShieldCheckIcon aria-hidden="true" /> Healthy
                </Badge>
              ) : (
                <Badge variant="destructive">
                  <ShieldAlertIcon aria-hidden="true" /> {health.data.problems.length} problem
                  {health.data.problems.length === 1 ? "" : "s"}
                </Badge>
              ))}
          </h2>
        </CardTitle>
        <CardDescription>
          Recipients configured, packages confirmed and sealed to the current recipients, re-confirmation within 90 days
          and an escrow drill within 12 months (ADR-0008). Problems raise an “escrow needs attention” alert.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {health.isPending ? (
          <RowsSkeleton label="Checking escrow health…" rows={2} />
        ) : health.isError && !health.data ? (
          <QueryError title="Could not load the escrow health" error={health.error} onRetry={() => void health.refetch()} />
        ) : (
          <>
            <dl className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-3">
              <div>
                <dt className="text-muted-foreground">Recipients</dt>
                <dd className="tabular-nums">{health.data.recipients}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Last drill</dt>
                <dd>
                  {health.data.last_drill_at ? (
                    <time dateTime={health.data.last_drill_at} title={formatDateTime(health.data.last_drill_at)}>
                      {formatRelative(health.data.last_drill_at)}
                    </time>
                  ) : (
                    "Never"
                  )}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Checked</dt>
                <dd>{formatRelative(health.data.checked_at)}</dd>
              </div>
            </dl>
            {health.data.problems.length === 0 ? (
              <p className="inline-flex items-center gap-1.5 text-sm text-emerald-700 dark:text-emerald-400" role="status">
                <CircleCheckIcon aria-hidden="true" className="size-4" /> No problems: every package is confirmed and
                current.
              </p>
            ) : (
              <ul className="divide-y rounded-lg border" aria-label="Escrow problems">
                {sortProblems(health.data.problems).map((p) => {
                  const repo = p.repository_id ? byId.get(p.repository_id) : undefined;
                  return (
                    <li
                      key={`${p.code}-${p.repository_id ?? ""}`}
                      className="flex flex-wrap items-start justify-between gap-3 p-3"
                      data-problem={p.code}
                    >
                      <div className="flex min-w-0 items-start gap-2">
                        {p.severity === "critical" ? (
                          <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
                        ) : (
                          <TriangleAlertIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                        )}
                        <div className="min-w-0 space-y-0.5 text-sm">
                          <p className="font-medium">
                            <span className="sr-only">{p.severity === "critical" ? "Critical: " : "Warning: "}</span>
                            {PROBLEM_LABEL[p.code]}
                            {repo && (
                              <>
                                {" · "}
                                <Link href={`/repositories/${repo.id}`} className="underline-offset-4 hover:underline">
                                  {repo.name}
                                </Link>
                              </>
                            )}
                          </p>
                          <p className="text-muted-foreground">{p.message}</p>
                        </div>
                      </div>
                      <ProblemFix problem={p} repo={repo} canManage={canManage} />
                    </li>
                  );
                })}
              </ul>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Drills
// ---------------------------------------------------------------------------

/** The drill package file name. */
export const drillFilename = (d: Pick<Drill, "id" | "created_at">): string =>
  `dbr2-escrow-drill-${d.created_at.slice(0, 10)}-${d.id.slice(0, 8)}.age`;

/** Confirmation-code form for a drill. */
export function DrillCodeForm({ drill, onCompleted }: { drill: Pick<Drill, "id">; onCompleted?: (d: Drill) => void }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const complete = useMutation({
    mutationFn: (c: string) => api.completeEscrowDrill(drill.id, c),
    onSuccess: async (d) => {
      toast({ title: "Escrow drill passed", description: "The escrow identities can still decrypt DBR² packages." });
      onCompleted?.(d);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.escrowDrills }),
        queryClient.invalidateQueries({ queryKey: queryKeys.escrowHealth }),
      ]);
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = CompleteDrillRequestSchema.safeParse({ confirmation_code: code });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Enter the confirmation code.");
      return;
    }
    complete.mutate(parsed.data.confirmation_code);
  }
  return (
    <form onSubmit={submit} noValidate className="space-y-3">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-2">
        <Label htmlFor={`${id}-code`}>Drill confirmation code</Label>
        <div className="flex flex-wrap gap-2">
          <Input
            id={`${id}-code`}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="XXXX-XXXX-XXXX-XXXX"
            className="max-w-64 font-mono uppercase"
            autoComplete="off"
            spellCheck={false}
            maxLength={40}
            aria-required="true"
            disabled={complete.isPending}
          />
          <Button type="submit" disabled={complete.isPending || code.trim() === ""}>
            {complete.isPending ? "Checking…" : "Complete drill"}
          </Button>
        </div>
      </div>
    </form>
  );
}

type DrillStep = "intro" | "package" | "code" | "done";

/** Start drill → download the package → decrypt offline → enter the code. */
export function StartDrillDialog() {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<DrillStep>("intro");
  const [drill, setDrill] = useState<Drill | null>(null);
  const [downloaded, setDownloaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const start = useMutation({
    mutationFn: api.startEscrowDrill,
    onSuccess: async (d) => {
      setDrill(d);
      setStep("package");
      await queryClient.invalidateQueries({ queryKey: queryKeys.escrowDrills });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const filename = drill ? drillFilename(drill) : "";
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setStep("intro");
          setDrill(null);
          setDownloaded(false);
          setError(null);
          start.reset();
        }
        setOpen(next);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <FlaskConicalIcon aria-hidden="true" /> Start escrow drill
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Escrow drill</DialogTitle>
          <DialogDescription>
            Step {step === "intro" ? 1 : step === "package" ? 2 : 3} of 3 ·{" "}
            {step === "intro" ? "Start" : step === "package" ? "Download the drill package" : step === "code" ? "Enter the code" : "Passed"}
          </DialogDescription>
        </DialogHeader>
        {step === "intro" && (
          <div className="space-y-4 text-sm">
            <p>
              A drill package holds a random secret (no real key) sealed to every escrow recipient. An escrow holder
              takes their identity from the safe, decrypts the package on an offline machine and enters its confirmation
              code here, proving the identities still work. Run one at least once a year.
            </p>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={start.isPending}>
                  Cancel
                </Button>
              </DialogClose>
              <Button onClick={() => start.mutate()} disabled={start.isPending}>
                {start.isPending ? "Creating…" : "Create drill package"}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "package" && drill && (
          <div className="space-y-4 text-sm">
            {drill.package ? (
              <>
                <p>
                  Sealed to {drill.recipients} recipient{drill.recipients === 1 ? "" : "s"}. The package is shown only
                  now: download it before you continue.
                </p>
                <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs">
                  age -d -i identity.txt {filename}
                </pre>
                <div className="flex flex-wrap items-center gap-3">
                  <Button
                    type="button"
                    onClick={() => {
                      downloadTextFile(filename, drill.package!);
                      setDownloaded(true);
                    }}
                  >
                    <DownloadIcon aria-hidden="true" /> Download {filename}
                  </Button>
                  {downloaded && (
                    <span className="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400" role="status">
                      <CircleCheckIcon aria-hidden="true" className="size-4" /> Downloaded
                    </span>
                  )}
                </div>
              </>
            ) : (
              <Alert variant="destructive">
                <AlertDescription>The server returned no drill package. Start a new drill.</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  Finish later
                </Button>
              </DialogClose>
              <Button type="button" onClick={() => setStep("code")} disabled={!downloaded}>
                Next: enter the code
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "code" && drill && (
          <div className="space-y-4">
            <DrillCodeForm drill={drill} onCompleted={() => setStep("done")} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setStep("package")}>
                Back
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === "done" && (
          <div className="space-y-4">
            <Alert role="status">
              <CircleCheckIcon aria-hidden="true" className="text-emerald-600" />
              <AlertTitle>Drill passed</AlertTitle>
              <AlertDescription>The next drill is due in 12 months. Delete the drill package.</AlertDescription>
            </Alert>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button">Done</Button>
              </DialogClose>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Enter the code of a drill started earlier (the package was downloaded then). */
function CompleteDrillDialog({ drill }: { drill: Drill }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="xs" variant="outline" aria-label={`Enter the code of the drill started ${formatDateTime(drill.created_at)}`}>
          Enter code
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Complete the escrow drill</DialogTitle>
          <DialogDescription>
            Started {formatDateTime(drill.created_at)}. Enter the confirmation code from the decrypted drill package.
          </DialogDescription>
        </DialogHeader>
        <DrillCodeForm drill={drill} onCompleted={() => setOpen(false)} />
      </DialogContent>
    </Dialog>
  );
}

export function EscrowDrills({ canManage }: { canManage: boolean }) {
  const drills = useEscrowDrills();
  const items = drills.data ?? [];
  return (
    <Card id={DRILLS_SECTION_ID} className="scroll-mt-4">
      <CardHeader className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle>
            <h2>Escrow drills</h2>
          </CardTitle>
          <CardDescription>
            Proof, at least once a year, that the escrow identities in the safe can still decrypt DBR² packages.
          </CardDescription>
        </div>
        {canManage && <StartDrillDialog />}
      </CardHeader>
      <CardContent>
        {drills.isPending ? (
          <RowsSkeleton label="Loading escrow drills…" rows={2} />
        ) : drills.isError && !drills.data ? (
          <QueryError title="Could not load the escrow drills" error={drills.error} onRetry={() => void drills.refetch()} />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No escrow drill yet.</p>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Escrow drills">
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Started</TableHead>
                  <TableHead scope="col">Recipients</TableHead>
                  <TableHead scope="col">Result</TableHead>
                  {canManage && (
                    <TableHead scope="col">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((d) => (
                  <TableRow key={d.id}>
                    <TableCell>{formatDateTime(d.created_at)}</TableCell>
                    <TableCell className="tabular-nums">{d.recipients}</TableCell>
                    <TableCell>
                      {d.completed_at ? (
                        <span className="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400">
                          <CircleCheckIcon aria-hidden="true" className="size-3.5" /> Passed {formatDateTime(d.completed_at)}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">Not completed</span>
                      )}
                    </TableCell>
                    {canManage && <TableCell>{!d.completed_at && <CompleteDrillDialog drill={d} />}</TableCell>}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

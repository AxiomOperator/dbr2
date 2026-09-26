// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArchiveIcon, LoaderCircleIcon } from "lucide-react";
import { useId, useState } from "react";
import { ApplicationJobProgress } from "@/components/live/job-progress";
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
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { actionErrorMessage, isApiError } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { CONSISTENCY_MODES, type ConsistencyMode } from "@/lib/api/protection-schemas";
import { MODE_LABEL } from "@/components/backups/backup-badges";

const SETTING = "__setting__";

/** Message for a failed "Back up now": 409 means an operation is already running. */
export function startBackupErrorMessage(err: unknown): string {
  if (isApiError(err) && err.status === 409) {
    return "An operation is already running for this application. Wait for it to finish, then try again.";
  }
  return actionErrorMessage(err);
}

/**
 * "Back up now" with an optional one-off consistency mode (requires
 * `backup.execute`). After the start, and whenever a backup already runs, the
 * dialog shows the live per-component progress (`job.progress` over SSE).
 */
export function BackUpNowButton({
  applicationId,
  name,
  effectiveMode,
  running,
}: {
  applicationId: string;
  name: string;
  /** The application's effective mode, when known (policy.read). */
  effectiveMode?: string;
  /** `protection.running`: an operation already runs for the application. */
  running?: string;
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<string>(SETTING);
  const [error, setError] = useState<string | null>(null);
  const [workflowId, setWorkflowId] = useState<string | null>(null);
  const showProgress = workflowId !== null || running === "backup";

  const start = useMutation({
    mutationFn: () =>
      api.startBackup(applicationId, mode === SETTING ? {} : { consistency_mode: mode as ConsistencyMode }),
    onSuccess: async (res) => {
      setWorkflowId(res.workflow_id);
      toast({
        title: `Backup of ${name} started`,
        description: (
          <>
            Workflow <code className="font-mono text-xs break-all">{res.workflow_id}</code>. The recovery
            point appears under Backups.
          </>
        ),
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.recoveryPointsAll }),
        queryClient.invalidateQueries({ queryKey: queryKeys.application(applicationId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.jobsAll }),
      ]);
    },
    onError: (err) => setError(startBackupErrorMessage(err)),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setMode(SETTING);
          setError(null);
          setWorkflowId(null);
          start.reset();
        }
        setOpen(next);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant={running === "backup" ? "outline" : "default"}>
          {running === "backup" ? (
            <>
              <LoaderCircleIcon aria-hidden="true" className="motion-safe:animate-spin" /> Backup running
            </>
          ) : (
            <>
              <ArchiveIcon aria-hidden="true" /> Back up now
            </>
          )}
        </Button>
      </DialogTrigger>
      <DialogContent>
        {showProgress ? (
          <>
            <DialogHeader>
              <DialogTitle>Backing up {name}</DialogTitle>
              <DialogDescription>
                Live progress from the agent. You can close this dialog: the backup continues and its
                progress also shows on the application&apos;s Protection card.
              </DialogDescription>
            </DialogHeader>
            <div className="rounded-lg border p-3" aria-live="polite">
              <ApplicationJobProgress applicationId={applicationId} running="backup" />
            </div>
            {workflowId && (
              <p className="text-xs text-muted-foreground">
                Workflow <code className="font-mono break-all">{workflowId}</code>
              </p>
            )}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  Close
                </Button>
              </DialogClose>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Back up {name} now</DialogTitle>
              <DialogDescription>
                Starts a backup to the application&apos;s Repository. Only one operation runs per
                application at a time.
              </DialogDescription>
            </DialogHeader>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-2">
              <Label htmlFor={`${id}-mode`}>Consistency mode for this run</Label>
              <Select value={mode} onValueChange={setMode} disabled={start.isPending}>
                <SelectTrigger id={`${id}-mode`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={SETTING}>
                    Use the backup settings{effectiveMode ? ` (${MODE_LABEL[effectiveMode] ?? effectiveMode})` : ""}
                  </SelectItem>
                  {CONSISTENCY_MODES.map((m) => (
                    <SelectItem key={m} value={m}>
                      {MODE_LABEL[m]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {mode === "offline" && (
                <p className="text-xs text-amber-700 dark:text-amber-400">
                  Offline stops the application&apos;s containers for the duration of the capture.
                </p>
              )}
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={start.isPending}>
                  Cancel
                </Button>
              </DialogClose>
              <Button onClick={() => start.mutate()} disabled={start.isPending}>
                {start.isPending ? "Starting…" : "Start backup"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

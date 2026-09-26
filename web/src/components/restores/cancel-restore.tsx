// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { OctagonXIcon } from "lucide-react";
import { useState } from "react";
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
import { actionErrorMessage, isApiError } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { RestoreRun } from "@/lib/api/restore-schemas";

/** Cancels a running restore (`restore.execute`); compensation puts the previous state back. */
export function CancelRestoreButton({ run }: { run: RestoreRun }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const cancel = useMutation({
    mutationFn: () => api.cancelRestore(run.id),
    onSuccess: async () => {
      setOpen(false);
      toast({
        title: "Cancellation requested",
        description: "The restore rolls back: the previous data and containers are put back.",
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.restore(run.id) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.restoresAll }),
        queryClient.invalidateQueries({ queryKey: queryKeys.jobsAll }),
      ]);
    },
  });
  const error =
    cancel.error && isApiError(cancel.error) && cancel.error.status === 409
      ? "The restore is no longer running: it finished in the meantime."
      : cancel.error
        ? actionErrorMessage(cancel.error)
        : null;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (o) cancel.reset();
        setOpen(o);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <OctagonXIcon aria-hidden="true" /> Cancel restore
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cancel this restore?</DialogTitle>
          <DialogDescription>
            Compensation runs: the previous data and containers of {run.application_name} on {run.target_hostname}{" "}
            are put back and the restore ends as rolled back (or failed, if nothing had changed yet). This cannot be
            undone; start a new restore to try again.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={cancel.isPending}>
              Keep running
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
            {cancel.isPending ? "Cancelling…" : "Cancel restore"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

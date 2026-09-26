// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { DownloadIcon, RefreshCwIcon, ServerCogIcon, ShieldCheckIcon, Undo2Icon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { DeleteWithGraceDialog, ScheduledDeletionBanner } from "@/components/common/deletion";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  ConfirmEscrowRequestSchema,
  DEFAULT_VERIFY_READ_PERCENT,
  DELETION_GRACE_DAYS,
  VerifyRepositoryRequestSchema,
  type DeleteRequest,
  type Repository,
} from "@/lib/api/protection-schemas";
import { downloadTextFile } from "@/lib/protection";

type Size = "xs" | "sm" | "default";

/** Replaces the cached Repository and refreshes the lists after an action. */
function useRepositoryRefresh() {
  const queryClient = useQueryClient();
  return async (updated: Repository) => {
    queryClient.setQueryData(queryKeys.repository(updated.id), updated);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.repositories }),
      queryClient.invalidateQueries({ queryKey: queryKeys.escrowHealth }),
    ]);
  };
}

/** Repositories that can be scheduled for deletion. */
export const canDeleteRepository = (r: Pick<Repository, "status">): boolean =>
  r.status === "ready" || r.status === "awaiting_escrow" || r.status === "unavailable";

/** "Delete…" for a Repository (typed name + reason; retired after the grace period). */
export function DeleteRepositoryButton({ repo, size = "sm" }: { repo: Repository; size?: Size }) {
  const toast = useToast();
  const refresh = useRepositoryRefresh();
  const del = useMutation({
    mutationFn: (req: DeleteRequest) => api.deleteRepository(repo.id, repo.name, req),
    onSuccess: async (updated) => {
      toast({
        title: `${repo.name} is pending deletion`,
        description: `It is retired after ${updated.delete_after ? new Date(updated.delete_after).toLocaleString() : `${DELETION_GRACE_DAYS} days`}; undo it until then.`,
      });
      await refresh(updated);
    },
  });
  return (
    <DeleteWithGraceDialog
      subject={`Repository ${repo.name}`}
      name={repo.name}
      nameKind="Repository name"
      size={size}
      onDelete={(req) => del.mutateAsync(req)}
    >
      <p>
        DBR² stops using <strong>{repo.name}</strong> now: no new backups go to it
        {repo.is_default ? " and it is no longer the default Repository" : ""}. After {DELETION_GRACE_DAYS} days it is
        retired.
      </p>
      <p className="text-muted-foreground">
        DBR² never erases the stored data: remove the NAS share yourself once the Repository is retired. Until then
        you can undelete it.
        {repo.is_system ? " It is the System Repository: designate another one for the platform self-backup." : ""}
      </p>
    </DeleteWithGraceDialog>
  );
}

/** "Pending deletion" banner with Undelete (requires `repository.manage` for the action). */
export function RepositoryDeletionBanner({ repo, canManage }: { repo: Repository; canManage: boolean }) {
  const toast = useToast();
  const refresh = useRepositoryRefresh();
  const undelete = useMutation({
    mutationFn: () => api.undeleteRepository(repo.id),
    onSuccess: async (updated) => {
      toast({ title: `${repo.name} is kept`, description: "The deletion was cancelled." });
      await refresh(updated);
    },
  });
  if (repo.status !== "pending_deletion" || !repo.delete_after) return null;
  return (
    <ScheduledDeletionBanner
      deleteAfter={repo.delete_after}
      undoLabel="Undelete Repository"
      onUndelete={canManage ? () => undelete.mutateAsync() : undefined}
    >
      <p>No new backups use this Repository meanwhile; its recovery points can still be restored.</p>
    </ScheduledDeletionBanner>
  );
}

/** Compact Undelete for tables (the detail page shows the full banner). */
export function RepositoryUndeleteButton({ repo, size = "xs" }: { repo: Repository; size?: Size }) {
  const toast = useToast();
  const refresh = useRepositoryRefresh();
  const undelete = useMutation({
    mutationFn: () => api.undeleteRepository(repo.id),
    onSuccess: async (updated) => {
      toast({ title: `${repo.name} is kept`, description: "The deletion was cancelled." });
      await refresh(updated);
    },
    onError: (err) =>
      toast({ title: `Could not undelete ${repo.name}`, description: actionErrorMessage(err), variant: "destructive" }),
  });
  return (
    <Button
      size={size}
      variant="outline"
      onClick={() => undelete.mutate()}
      disabled={undelete.isPending}
      aria-label={`Undelete ${repo.name}`}
    >
      <Undo2Icon aria-hidden="true" /> {undelete.isPending ? "Undeleting…" : "Undelete"}
    </Button>
  );
}

/** Makes the Repository the System Repository (platform self-backup target). */
export function DesignateSystemButton({ repo, size = "xs" }: { repo: Repository; size?: Size }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const refresh = useRepositoryRefresh();
  const queryClient = useQueryClient();
  const designate = useMutation({
    mutationFn: () => api.designateSystemRepository(repo.id),
    onSuccess: async (updated) => {
      setOpen(false);
      toast({ title: `${repo.name} is now the System Repository` });
      await refresh(updated);
      await queryClient.invalidateQueries({ queryKey: queryKeys.platformBackups });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  if (repo.is_system || repo.status === "retired" || repo.status === "pending_deletion") return null;
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size={size} variant="outline" aria-label={`Make ${repo.name} the System Repository`}>
          <ServerCogIcon aria-hidden="true" /> Make System Repository
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Make {repo.name} the System Repository?</DialogTitle>
          <DialogDescription>
            The platform self-backup (Platform Recovery Bundle: the platform database, DBR2_SECRET_KEY, the internal
            token and every reposerver&apos;s state, encrypted to the escrow recipients) is written to this Repository
            from the next run. The previous System Repository loses the role; the bundle is also written to a directory
            outside every Repository, so the System Repository is never the only copy.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={designate.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button onClick={() => designate.mutate()} disabled={designate.isPending}>
            {designate.isPending ? "Saving…" : "Make System Repository"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}


/** Starts the reindex workflow after a confirmation (requires `repository.manage`). */
export function ReindexButton({ repo, size = "xs" }: { repo: Repository; size?: Size }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const queryClient = useQueryClient();
  const reindex = useMutation({
    mutationFn: () => api.reindexRepository(repo.id),
    onSuccess: async (res) => {
      setOpen(false);
      toast({
        title: `Reindex of ${repo.name} started`,
        description: (
          <>
            Workflow <code className="font-mono text-xs break-all">{res.workflow_id}</code>. Recovery
            points update when it completes.
          </>
        ),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.repositories });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size={size} variant="outline" aria-label={`Reindex ${repo.name}`}>
          <RefreshCwIcon aria-hidden="true" /> Reindex
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Reindex {repo.name}?</DialogTitle>
          <DialogDescription>
            Every recovery manifest is read back from the Repository and the recovery point index is
            replaced: the Repository is authoritative (ADR-0003). Index entries without a manifest
            are marked missing. This can take a while for large Repositories.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={reindex.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button onClick={() => reindex.mutate()} disabled={reindex.isPending}>
            {reindex.isPending ? "Starting…" : "Start reindex"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Starts repository/{id}/verify for one recovery point (requires `repository.manage`). */
export function VerifyButton({
  repositoryId,
  recoveryPointId,
  label,
  size = "sm",
}: {
  repositoryId: string;
  recoveryPointId?: string;
  /** Accessible name of the target, e.g. "nas01-backups". */
  label: string;
  size?: "xs" | "sm" | "default";
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [percent, setPercent] = useState(String(DEFAULT_VERIFY_READ_PERCENT));
  const [error, setError] = useState<string | null>(null);
  const verify = useMutation({
    mutationFn: (readPercent: number) =>
      api.verifyRepository(repositoryId, { read_percent: readPercent, ...(recoveryPointId ? { recovery_point_id: recoveryPointId } : {}) }),
    onSuccess: async (res) => {
      setOpen(false);
      toast({
        title: `Verification of ${label} started`,
        description: (
          <>
            Workflow <code className="font-mono text-xs break-all">{res.workflow_id}</code>. Results appear on the
            recovery points when it finishes.
          </>
        ),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.repositories });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function start() {
    setError(null);
    const parsed = VerifyRepositoryRequestSchema.safeParse({ read_percent: percent.trim() === "" ? undefined : Number(percent) });
    if (!parsed.success || Number.isNaN(Number(percent))) {
      setError(parsed.success ? "The read share is 0–100 %." : (parsed.error.issues[0]?.message ?? "Check the form."));
      return;
    }
    verify.mutate(parsed.data.read_percent ?? DEFAULT_VERIFY_READ_PERCENT);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size={size} variant="outline" aria-label={`Verify ${label}`}>
          <ShieldCheckIcon aria-hidden="true" /> Verify now
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Verify {label}?</DialogTitle>
          <DialogDescription>
            {recoveryPointId
              ? "Checks that every object of this recovery point is present in the Repository and reads a share of its files back, verifying their hashes."
              : "Checks the least recently verified recovery points of this Repository: every object present, and a share of the files read back and hash-verified. A weekly schedule also verifies every Repository."}{" "}
            A failure raises a critical alert.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-pct`}>Files to read back (%)</Label>
          <Input
            id={`${id}-pct`}
            type="number"
            inputMode="decimal"
            min={0}
            max={100}
            value={percent}
            onChange={(e) => setPercent(e.target.value)}
            className="w-28"
            aria-describedby={`${id}-pct-help`}
          />
          <p id={`${id}-pct-help`} className="text-xs text-muted-foreground">
            0 checks presence only; 100 reads everything (slow on large Repositories). Default {DEFAULT_VERIFY_READ_PERCENT} %.
          </p>
        </div>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={verify.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button onClick={start} disabled={verify.isPending}>
            {verify.isPending ? "Starting…" : "Start verification"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Downloads the (encrypted) escrow package; the download is audited. */
export function DownloadEscrowButton({ repo, size = "xs" }: { repo: Pick<Repository, "id" | "name">; size?: Size }) {
  const toast = useToast();
  const download = useMutation({
    mutationFn: () => api.repositoryEscrowPackage(repo.id),
    onSuccess: (pkg) => {
      downloadTextFile(pkg.filename, pkg.package);
      toast({ title: `Escrow package ${pkg.filename} downloaded`, description: "Store it offline." });
    },
    onError: (err) =>
      toast({
        title: `Could not download the escrow package of ${repo.name}`,
        description: actionErrorMessage(err),
        variant: "destructive",
      }),
  });
  return (
    <Button
      size={size}
      variant="outline"
      onClick={() => download.mutate()}
      disabled={download.isPending}
      aria-label={`Download escrow package of ${repo.name}`}
    >
      <DownloadIcon aria-hidden="true" /> {download.isPending ? "Downloading…" : "Escrow package"}
    </Button>
  );
}

/** How to store and decrypt the escrow package. */
export function EscrowInstructions({ filename = "<escrow-package-file>" }: { filename?: string }) {
  return (
    <ol className="list-decimal space-y-2 pl-5 text-sm">
      <li>
        Store <code className="font-mono text-xs break-all">{filename}</code> <strong>offline</strong>{" "}
        (for example on two encrypted USB drives in separate safes). It is the only copy of the
        Repository password: DBR² does not keep it.
      </li>
      <li>
        A key holder decrypts it on an offline machine with their identity:
        <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs">
          age -d -i &lt;identity-file&gt; {filename}
        </pre>
      </li>
      <li>
        Copy the <code className="font-mono text-xs">confirmation_code</code> from the decrypted JSON
        and enter it below. This proves the package can be decrypted.
      </li>
    </ol>
  );
}

/**
 * Confirmation-code form (`XXXX-XXXX-XXXX-XXXX`, case- and space-insensitive).
 * On success the Repository becomes ready.
 */
export function EscrowConfirmForm({
  repo,
  onConfirmed,
}: {
  repo: Pick<Repository, "id" | "name">;
  onConfirmed?: (updated: Repository) => void;
}) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const confirm = useMutation({
    mutationFn: (c: string) => api.confirmRepositoryEscrow(repo.id, { confirmation_code: c }),
    onSuccess: async (updated) => {
      queryClient.setQueryData(queryKeys.repository(repo.id), updated);
      toast({ title: `${repo.name} is ready`, description: "Key escrow confirmed." });
      onConfirmed?.(updated);
      await queryClient.invalidateQueries({ queryKey: queryKeys.repositories });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = ConfirmEscrowRequestSchema.safeParse({ confirmation_code: code });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Enter the confirmation code.");
      return;
    }
    confirm.mutate(parsed.data.confirmation_code);
  }

  return (
    <form onSubmit={submit} noValidate className="space-y-3">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-2">
        <Label htmlFor={`${id}-code`}>Confirmation code</Label>
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
            required
            aria-required="true"
            disabled={confirm.isPending}
          />
          <Button type="submit" disabled={confirm.isPending || code.trim() === ""}>
            {confirm.isPending ? "Confirming…" : "Confirm escrow"}
          </Button>
        </div>
      </div>
    </form>
  );
}

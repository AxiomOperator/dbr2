// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { DownloadIcon, RefreshCwIcon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
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
import { ConfirmEscrowRequestSchema, type Repository } from "@/lib/api/protection-schemas";
import { downloadTextFile } from "@/lib/protection";

type Size = "xs" | "sm" | "default";

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

/** Downloads the (encrypted) escrow package; the download is audited. */
export function DownloadEscrowButton({ repo, size = "xs" }: { repo: Repository; size?: Size }) {
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

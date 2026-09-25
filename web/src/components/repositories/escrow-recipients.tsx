// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, ShieldAlertIcon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  AddEscrowRecipientRequestSchema,
  looksLikePrivateKey,
  MIN_ESCROW_RECIPIENTS,
  type EscrowRecipient,
} from "@/lib/api/protection-schemas";
import { useEscrowRecipients } from "@/lib/api/hooks";
import { formatDateTime } from "@/lib/format";

export const ESCROW_SECTION_ID = "escrow-recipients";

/** "age1qyqs…x7k2" / "ssh-ed25519 AAAAC3…Zm9v" for display. */
export function shortKey(key: string): string {
  const k = key.trim();
  if (k.startsWith("ssh-")) {
    const [type, body = ""] = k.split(/\s+/);
    return body.length > 16 ? `${type} ${body.slice(0, 10)}…${body.slice(-6)}` : k;
  }
  return k.length > 24 ? `${k.slice(0, 12)}…${k.slice(-6)}` : k;
}

const PRIVATE_KEY_WARNING =
  "That was a PRIVATE identity. It was not sent and the field was cleared. Never paste private keys or age identities (AGE-SECRET-KEY-…) into DBR²: keep them offline and paste only the public key (age1… or ssh-…). If this identity was exposed anywhere, generate a new one.";

function AddRecipientDialog() {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [privateWarning, setPrivateWarning] = useState(false);

  const add = useMutation({
    mutationFn: api.addEscrowRecipient,
    onSuccess: async (r) => {
      toast({ title: `Escrow recipient ${r.name} added` });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.escrowRecipients });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    if (next) {
      setName("");
      setPublicKey("");
      setError(null);
      setPrivateWarning(false);
      add.reset();
    }
    setOpen(next);
  }

  function onKeyChange(value: string) {
    // A pasted private identity is dropped immediately: it never stays in
    // the form, let alone leaves the browser.
    if (looksLikePrivateKey(value)) {
      setPublicKey("");
      setPrivateWarning(true);
      return;
    }
    setPublicKey(value);
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = AddEscrowRecipientRequestSchema.safeParse({ name, public_key: publicKey });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    add.mutate(parsed.data);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <PlusIcon aria-hidden="true" /> Add recipient
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} noValidate className="space-y-4">
          <DialogHeader>
            <DialogTitle>Add escrow recipient</DialogTitle>
            <DialogDescription>
              Every new Repository password is sealed to all escrow recipients. Register the{" "}
              <strong>public</strong> key of each key holder; their private identity stays offline.
            </DialogDescription>
          </DialogHeader>
          {privateWarning && (
            <Alert variant="destructive" data-testid="private-key-warning">
              <ShieldAlertIcon aria-hidden="true" />
              <AlertTitle>Private key rejected</AlertTitle>
              <AlertDescription>{PRIVATE_KEY_WARNING}</AlertDescription>
            </Alert>
          )}
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-name`}>Key holder</Label>
            <Input
              id={`${id}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Ada Lovelace (IT security)"
              maxLength={100}
              required
              aria-required="true"
              disabled={add.isPending}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-key`}>Public key</Label>
            <Textarea
              id={`${id}-key`}
              value={publicKey}
              onChange={(e) => onKeyChange(e.target.value)}
              placeholder="age1… or ssh-ed25519 AAAA…"
              className="font-mono text-xs"
              rows={3}
              maxLength={2000}
              spellCheck={false}
              autoComplete="off"
              required
              aria-required="true"
              aria-describedby={`${id}-key-help`}
              disabled={add.isPending}
            />
            <p id={`${id}-key-help`} className="text-xs text-muted-foreground">
              An age X25519 public key (<code>age-keygen -y identity.txt</code> prints it) or an SSH
              ed25519 / RSA public key. Never paste a private identity.
            </p>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={add.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={add.isPending}>
              {add.isPending ? "Adding…" : "Add recipient"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RemoveRecipientButton({ recipient, remaining }: { recipient: EscrowRecipient; remaining: number }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: () => api.removeEscrowRecipient(recipient.id),
    onSuccess: async () => {
      toast({ title: `Escrow recipient ${recipient.name} removed` });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.escrowRecipients });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const dropsBelow = remaining - 1 < MIN_ESCROW_RECIPIENTS;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size="xs" variant="destructive" aria-label={`Remove escrow recipient ${recipient.name}`}>
          Remove
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove {recipient.name}?</DialogTitle>
          <DialogDescription>
            New escrow packages are no longer encrypted to this key. Packages that already exist are
            unchanged and can still be decrypted with it.
          </DialogDescription>
        </DialogHeader>
        {dropsBelow && (
          <Alert>
            <AlertDescription>
              Fewer than {MIN_ESCROW_RECIPIENTS} recipients will remain: new Repositories cannot be
              created until another recipient is added.
            </AlertDescription>
          </Alert>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={remove.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => remove.mutate()} disabled={remove.isPending}>
            {remove.isPending ? "Removing…" : "Remove recipient"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Escrow recipients (ADR-0008): list for `repository.read`, add/remove for `repository.manage`. */
export function EscrowRecipients({ canManage }: { canManage: boolean }) {
  const recipients = useEscrowRecipients();
  const items = recipients.data ?? [];

  let body;
  if (recipients.isPending) {
    body = <RowsSkeleton label="Loading escrow recipients…" rows={2} />;
  } else if (recipients.isError) {
    body = (
      <QueryError
        title="Could not load escrow recipients"
        error={recipients.error}
        onRetry={() => void recipients.refetch()}
      />
    );
  } else {
    body = (
      <div className="space-y-3">
        {items.length < MIN_ESCROW_RECIPIENTS && (
          <Alert>
            <ShieldAlertIcon aria-hidden="true" className="text-amber-600" />
            <AlertTitle>
              {items.length} of {MIN_ESCROW_RECIPIENTS} required recipients
            </AlertTitle>
            <AlertDescription>
              Register at least {MIN_ESCROW_RECIPIENTS} public keys held by two different people
              before creating a Repository.
            </AlertDescription>
          </Alert>
        )}
        <div className="rounded-lg border">
          <Table aria-label="Escrow recipients">
            <TableHeader>
              <TableRow>
                <TableHead scope="col">Key holder</TableHead>
                <TableHead scope="col">Public key</TableHead>
                <TableHead scope="col">Added</TableHead>
                {canManage && (
                  <TableHead scope="col">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={canManage ? 4 : 3} className="h-16 text-center text-muted-foreground">
                    No escrow recipients registered.
                  </TableCell>
                </TableRow>
              ) : (
                items.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="font-medium">{r.name}</TableCell>
                    <TableCell>
                      <span className="inline-flex items-center gap-1.5">
                        <Badge variant="outline">{r.public_key.startsWith("ssh-") ? "SSH" : "age"}</Badge>
                        <code className="font-mono text-xs" title={r.public_key}>
                          {shortKey(r.public_key)}
                        </code>
                      </span>
                    </TableCell>
                    <TableCell className="whitespace-nowrap">{formatDateTime(r.created_at)}</TableCell>
                    {canManage && (
                      <TableCell className="text-right">
                        <RemoveRecipientButton recipient={r} remaining={items.length} />
                      </TableCell>
                    )}
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>
    );
  }

  return (
    <Card id={ESCROW_SECTION_ID} className="scroll-mt-4">
      <CardHeader>
        <CardTitle>
          <h2>Escrow recipients</h2>
        </CardTitle>
        <CardDescription>
          Public keys every Repository password is sealed to (ADR-0008). DBR² never stores the
          Repository password: the escrow package is the only way to recover it.
        </CardDescription>
        {canManage && (
          <CardAction>
            <AddRecipientDialog />
          </CardAction>
        )}
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  );
}

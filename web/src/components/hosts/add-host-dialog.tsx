// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, TriangleAlertIcon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { CopyButton } from "@/components/common/copy-button";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
  CreateRegistrationTokenRequestSchema,
  TOKEN_DEFAULT_HOURS,
  TOKEN_MAX_HOURS,
  type CreateRegistrationTokenResponse,
} from "@/lib/api/fleet-schemas";
import { formatDateTime } from "@/lib/format";

/** Where the RPM and install steps are documented (repository-relative). */
export const AGENT_INSTALL_DOC = "deployments/packaging/README.md";

function JoinInstructions({ result }: { result: CreateRegistrationTokenResponse }) {
  return (
    <div className="space-y-4 text-sm">
      <Alert className="border-amber-500/60">
        <TriangleAlertIcon aria-hidden="true" className="text-amber-600" />
        <AlertTitle>Shown only once</AlertTitle>
        <AlertDescription>
          Copy the join command now. The registration token is never shown again; if you lose it,
          revoke it and create a new one.
        </AlertDescription>
      </Alert>

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="font-medium" id="join-command-label">
            Join command
          </h3>
          <CopyButton value={result.join_command} label="join command" />
        </div>
        <pre
          aria-labelledby="join-command-label"
          data-testid="join-command"
          className="max-h-40 overflow-auto rounded-md bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap"
        >
          {result.join_command}
        </pre>
      </div>

      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
        <dt className="text-muted-foreground">CA fingerprint (SHA-256)</dt>
        <dd className="font-mono text-xs break-all">{result.ca_sha256}</dd>
        <dt className="text-muted-foreground">Gateway address</dt>
        <dd className="font-mono text-xs break-all">{result.gateway_address}</dd>
        <dt className="text-muted-foreground">Token</dt>
        <dd>
          <span className="font-mono text-xs">{result.registration_token.prefix}…</span>{" "}
          <span className="text-muted-foreground">({result.registration_token.description})</span>
        </dd>
        <dt className="text-muted-foreground">Expires</dt>
        <dd>{formatDateTime(result.registration_token.expires_at)}</dd>
      </dl>

      <ol className="list-decimal space-y-1.5 pl-5">
        <li>
          Install the <code>dbr2-agent</code> RPM on the host (Rocky Linux 9 or Fedora):{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            sudo dnf install ./dbr2-agent-&lt;VERSION&gt;-&lt;BUILD&gt;.x86_64.rpm
          </code>{" "}
          — see <code>{AGENT_INSTALL_DOC}</code>.
        </li>
        <li>Run the join command above as root, then <code>sudo systemctl enable --now dbr2-agent</code>.</li>
        <li>
          The host appears on this page as <strong>Pending approval</strong>. Check its hostname,
          then approve it.
        </li>
      </ol>
    </div>
  );
}

/**
 * "Add host": creates a single-use registration token and shows the join
 * command exactly once. Closing the dialog discards it from memory.
 */
export function AddHostDialog() {
  const id = useId();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [description, setDescription] = useState("");
  const [hours, setHours] = useState(String(TOKEN_DEFAULT_HOURS));
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CreateRegistrationTokenResponse | null>(null);

  const create = useMutation({
    mutationFn: api.createRegistrationToken,
    onSuccess: (data) => {
      setResult(data);
      void queryClient.invalidateQueries({ queryKey: queryKeys.registrationTokens });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    setOpen(next);
    if (!next) {
      // Forget the token as soon as the dialog closes: it is shown only once.
      setResult(null);
      setDescription("");
      setHours(String(TOKEN_DEFAULT_HOURS));
      setError(null);
      create.reset();
    }
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = CreateRegistrationTokenRequestSchema.safeParse({
      description,
      expires_in_hours: hours.trim() === "" ? Number.NaN : Number(hours),
    });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    create.mutate(parsed.data);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button>
          <PlusIcon aria-hidden="true" /> Add host
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add host</DialogTitle>
          <DialogDescription>
            {result
              ? "Enroll the host with this command, then approve it here."
              : "Create a single-use, expiring registration token for one host."}
          </DialogDescription>
        </DialogHeader>
        {result ? (
          <>
            <JoinInstructions result={result} />
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button">Done</Button>
              </DialogClose>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} noValidate className="space-y-4">
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-2">
              <Label htmlFor={`${id}-description`}>Description</Label>
              <Input
                id={`${id}-description`}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="e.g. docker-prod-01 (rack 3)"
                maxLength={200}
                required
                disabled={create.isPending}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-hours`}>Valid for (hours, 1–{TOKEN_MAX_HOURS})</Label>
              <Input
                id={`${id}-hours`}
                type="number"
                inputMode="numeric"
                min={1}
                max={TOKEN_MAX_HOURS}
                step={1}
                className="max-w-32"
                value={hours}
                onChange={(e) => setHours(e.target.value)}
                required
                disabled={create.isPending}
              />
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={create.isPending}>
                  Cancel
                </Button>
              </DialogClose>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending ? "Creating…" : "Create token"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

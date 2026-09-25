// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleCheckIcon, DownloadIcon, KeyRoundIcon, PlusIcon, TriangleAlertIcon } from "lucide-react";
import Link from "next/link";
import { useId, useState, type FormEvent } from "react";
import { ESCROW_SECTION_ID } from "@/components/repositories/escrow-recipients";
import { EscrowConfirmForm, EscrowInstructions } from "@/components/repositories/repository-actions";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  CreateRepositoryRequestSchema,
  DEFAULT_MANAGEMENT_URL,
  INTERNAL_SERVER_URL_PLACEHOLDER,
  MIN_ESCROW_RECIPIENTS,
  type CreateRepositoryResponse,
  type RepositoryBackend,
} from "@/lib/api/protection-schemas";
import { useEscrowRecipients } from "@/lib/api/hooks";
import { downloadTextFile } from "@/lib/protection";

type Step = "form" | "package" | "confirm" | "done";

const STEP_NUMBER: Record<Step, number> = { form: 1, package: 2, confirm: 3, done: 3 };

interface FormState {
  name: string;
  description: string;
  backend: RepositoryBackend;
  managementUrl: string;
  serverUrl: string;
  internalServerUrl: string;
  isDefault: boolean;
}

const EMPTY_FORM: FormState = {
  name: "",
  description: "",
  backend: "nfs",
  managementUrl: DEFAULT_MANAGEMENT_URL,
  serverUrl: "",
  internalServerUrl: "",
  isDefault: false,
};

function RecipientsNotice({ count, onNavigate }: { count: number; onNavigate: () => void }) {
  return (
    <Alert data-testid="recipients-notice">
      <TriangleAlertIcon aria-hidden="true" className="text-amber-600" />
      <AlertTitle>At least {MIN_ESCROW_RECIPIENTS} escrow recipients are required</AlertTitle>
      <AlertDescription>
        <p>
          {count === 0 ? "No escrow recipient is" : `Only ${count} escrow recipient is`} registered. The
          Repository password is sealed to every recipient, and ADR-0008 requires two key holders.
        </p>
        <Link
          href={`/repositories#${ESCROW_SECTION_ID}`}
          onClick={onNavigate}
          className="mt-2 inline-block font-medium text-foreground underline underline-offset-4"
        >
          Add escrow recipients
        </Link>
      </AlertDescription>
    </Alert>
  );
}

/**
 * Create Repository wizard (requires `repository.manage`):
 * 1. details → the server initialises the Kopia repository and returns the
 *    escrow package; 2. download it and store it offline; 3. enter the
 *    confirmation code from the decrypted package → the Repository is ready.
 * The package is only held in component state and dropped when the dialog
 * closes (it can be downloaded again from the Repository).
 */
export function CreateRepositoryDialog() {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const recipients = useEscrowRecipients();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<Step>("form");
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreateRepositoryResponse | null>(null);
  const [downloaded, setDownloaded] = useState(false);

  const recipientCount = recipients.data?.length ?? 0;
  const blocked = recipients.isSuccess && recipientCount < MIN_ESCROW_RECIPIENTS;

  const create = useMutation({
    mutationFn: api.createRepository,
    onSuccess: async (res) => {
      setCreated(res);
      setStep("package");
      await queryClient.invalidateQueries({ queryKey: queryKeys.repositories });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    if (next) {
      setForm(EMPTY_FORM);
      setStep("form");
      setError(null);
      setCreated(null);
      setDownloaded(false);
      create.reset();
    } else if (created && step !== "done") {
      toast({
        title: `${created.repository.name} is not usable yet`,
        description: "Confirm the escrow code from the Repository's page to make it ready.",
      });
    }
    if (!next) setCreated(null);
    setOpen(next);
  }

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }));

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = CreateRepositoryRequestSchema.safeParse({
      name: form.name,
      description: form.description.trim() || undefined,
      backend: form.backend,
      management_url: form.managementUrl,
      server_url: form.serverUrl,
      internal_server_url: form.internalServerUrl.trim() || undefined,
      default: form.isDefault,
    });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    create.mutate(parsed.data);
  }

  function download() {
    if (!created) return;
    downloadTextFile(created.escrow_filename, created.escrow_package);
    setDownloaded(true);
  }

  const pending = create.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button>
          <PlusIcon aria-hidden="true" /> Create Repository
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Create Repository</DialogTitle>
          <DialogDescription>
            Step {STEP_NUMBER[step]} of 3 ·{" "}
            {step === "form"
              ? "Details"
              : step === "package"
                ? "Store the escrow package"
                : step === "confirm"
                  ? "Confirm key escrow"
                  : "Ready"}
          </DialogDescription>
        </DialogHeader>

        {step === "form" && (
          <form onSubmit={submit} noValidate className="space-y-4">
            {blocked && <RecipientsNotice count={recipientCount} onNavigate={() => onOpenChange(false)} />}
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-2">
              <Label htmlFor={`${id}-name`}>Name</Label>
              <Input
                id={`${id}-name`}
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                placeholder="e.g. nas01-backups"
                maxLength={100}
                required
                aria-required="true"
                disabled={pending}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-desc`}>Description (optional)</Label>
              <Textarea
                id={`${id}-desc`}
                value={form.description}
                onChange={(e) => set("description", e.target.value)}
                maxLength={500}
                rows={2}
                disabled={pending}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-backend`}>Storage backend</Label>
              <Select
                value={form.backend}
                onValueChange={(v) => set("backend", v as RepositoryBackend)}
                disabled={pending}
              >
                <SelectTrigger id={`${id}-backend`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="nfs">NFS</SelectItem>
                  <SelectItem value="filesystem">Filesystem</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-mgmt`}>Management URL</Label>
              <Input
                id={`${id}-mgmt`}
                value={form.managementUrl}
                onChange={(e) => set("managementUrl", e.target.value)}
                className="font-mono text-xs"
                spellCheck={false}
                required
                aria-required="true"
                aria-describedby={`${id}-mgmt-help`}
                disabled={pending}
              />
              <p id={`${id}-mgmt-help`} className="text-xs text-muted-foreground">
                The reposerver management API; its storage must be empty.
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-server`}>Server URL</Label>
              <Input
                id={`${id}-server`}
                value={form.serverUrl}
                onChange={(e) => set("serverUrl", e.target.value)}
                placeholder="https://backup.example.lan:51515"
                className="font-mono text-xs"
                spellCheck={false}
                required
                aria-required="true"
                aria-describedby={`${id}-server-help`}
                disabled={pending}
              />
              <p id={`${id}-server-help`} className="text-xs text-muted-foreground">
                The Kopia repository server URL the agents connect to.
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-internal`}>Internal server URL (optional)</Label>
              <Input
                id={`${id}-internal`}
                value={form.internalServerUrl}
                onChange={(e) => set("internalServerUrl", e.target.value)}
                placeholder={INTERNAL_SERVER_URL_PLACEHOLDER}
                className="font-mono text-xs"
                spellCheck={false}
                aria-describedby={`${id}-internal-help`}
                disabled={pending}
              />
              <p id={`${id}-internal-help`} className="text-xs text-muted-foreground">
                The Kopia server URL dbr2-worker uses on the deployment network, e.g.{" "}
                <code>{INTERNAL_SERVER_URL_PLACEHOLDER}</code>. Leave empty to use the server URL.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                id={`${id}-default`}
                checked={form.isDefault}
                onCheckedChange={(v) => set("isDefault", v === true)}
                disabled={pending}
              />
              <Label htmlFor={`${id}-default`} className="font-normal">
                Default Repository (used by applications without an explicit Repository)
              </Label>
            </div>
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={pending}>
                  Cancel
                </Button>
              </DialogClose>
              <Button type="submit" disabled={pending || blocked}>
                {pending ? "Initializing…" : "Create Repository"}
              </Button>
            </DialogFooter>
          </form>
        )}

        {step === "package" && created && (
          <div className="space-y-4">
            <Alert>
              <KeyRoundIcon aria-hidden="true" className="text-amber-600" />
              <AlertTitle>{created.repository.name} is not usable yet</AlertTitle>
              <AlertDescription>
                The Repository stays <strong>awaiting escrow</strong> and no backup can use it until
                you confirm that the escrow package can be decrypted.
              </AlertDescription>
            </Alert>
            <EscrowInstructions filename={created.escrow_filename} />
            <div className="flex flex-wrap items-center gap-3">
              <Button type="button" onClick={download}>
                <DownloadIcon aria-hidden="true" /> Download {created.escrow_filename}
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

        {step === "confirm" && created && (
          <div className="space-y-4">
            <p className="text-sm">
              Decrypt <code className="font-mono text-xs break-all">{created.escrow_filename}</code> with{" "}
              <code className="font-mono text-xs">age -d -i &lt;identity-file&gt; {created.escrow_filename}</code>{" "}
              and enter the <code className="font-mono text-xs">confirmation_code</code> it contains.
            </p>
            <EscrowConfirmForm repo={created.repository} onConfirmed={() => setStep("done")} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setStep("package")}>
                Back
              </Button>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  Confirm later
                </Button>
              </DialogClose>
            </DialogFooter>
          </div>
        )}

        {step === "done" && created && (
          <div className="space-y-4">
            <Alert role="status">
              <CircleCheckIcon aria-hidden="true" className="text-emerald-600" />
              <AlertTitle>{created.repository.name} is ready</AlertTitle>
              <AlertDescription>
                Key escrow is confirmed. Applications can now back up to this Repository.
              </AlertDescription>
            </Alert>
            <DialogFooter>
              <Link
                href={`/repositories/${created.repository.id}`}
                className={buttonVariants({ variant: "outline" })}
                onClick={() => onOpenChange(false)}
              >
                View Repository
              </Link>
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

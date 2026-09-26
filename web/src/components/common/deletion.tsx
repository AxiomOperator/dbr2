// SPDX-License-Identifier: Apache-2.0
"use client";

// Deletion with a grace period (Phase 7): a typed-confirmation dialog (type
// the name exactly + a reason, like a production restore) and the banner
// shown while the deletion is scheduled, with Undelete.

import { CalendarClockIcon, Trash2Icon, Undo2Icon } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
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
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import {
  DELETION_GRACE_DAYS,
  MAX_DELETE_REASON,
  MIN_DELETE_REASON,
  deleteRequestSchema,
  type DeleteRequest,
} from "@/lib/api/protection-schemas";
import { daysUntil, formatDateTime } from "@/lib/format";

/** What still blocks the Delete button (empty when it can be pressed). */
export function deleteBlockers(name: string, confirmation: string, reason: string): string[] {
  const out: string[] = [];
  if (confirmation.trim() !== name) out.push(`Type ${name} exactly to confirm.`);
  if (reason.trim().length < MIN_DELETE_REASON) out.push(`Enter a reason (at least ${MIN_DELETE_REASON} characters).`);
  return out;
}

/**
 * "Delete…" with a typed confirmation and a reason. `onDelete` performs the
 * request; its error is shown in the dialog.
 */
export function DeleteWithGraceDialog({
  subject,
  name,
  nameKind,
  children,
  onDelete,
  size = "sm",
  triggerLabel = "Delete…",
}: {
  /** e.g. "recovery point rp_…" (dialog title: "Delete <subject>?"). */
  subject: string;
  /** The exact text to type. */
  name: string;
  /** What `name` is ("application name", "Repository name"). */
  nameKind: string;
  /** Explanation of what happens (grace period, restorability…). */
  children: ReactNode;
  onDelete: (req: DeleteRequest) => Promise<unknown>;
  size?: "xs" | "sm" | "default";
  triggerLabel?: string;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const blockers = deleteBlockers(name, confirmation, reason);
  const matches = confirmation.trim() === name;

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = deleteRequestSchema(name).safeParse({ confirmation, reason });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    setPending(true);
    try {
      await onDelete(parsed.data);
      setOpen(false);
    } catch (err) {
      setError(actionErrorMessage(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setConfirmation("");
          setReason("");
          setError(null);
        }
      }}
    >
      <DialogTrigger asChild>
        <Button variant="destructive" size={size} aria-label={`Delete ${subject}`}>
          <Trash2Icon aria-hidden="true" /> {triggerLabel}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Delete {subject}?</DialogTitle>
          <DialogDescription>
            Deletion is scheduled {DELETION_GRACE_DAYS} days from now and can be undone until then.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate className="space-y-4">
          <div className="space-y-2 text-sm">{children}</div>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-reason`}>Reason (required)</Label>
            <Textarea
              id={`${id}-reason`}
              value={reason}
              maxLength={MAX_DELETE_REASON}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Why, e.g. CHG-1182: test data, not needed"
              aria-required="true"
              disabled={pending}
            />
            <p className="text-xs text-muted-foreground">Stored in the audit log.</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-confirm`}>
              Type <code className="font-mono">{name}</code> to confirm
            </Label>
            <Input
              id={`${id}-confirm`}
              value={confirmation}
              onChange={(e) => setConfirmation(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={confirmation !== "" && !matches ? true : undefined}
              aria-describedby={`${id}-confirm-help`}
              disabled={pending}
            />
            <p id={`${id}-confirm-help`} className="text-xs text-muted-foreground">
              The {nameKind}, exactly (case-sensitive).
            </p>
          </div>
          {blockers.length > 0 && (
            <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground" aria-label="Before you can delete" aria-live="polite">
              {blockers.map((b) => (
                <li key={b}>{b}</li>
              ))}
            </ul>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={pending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" variant="destructive" disabled={pending || blockers.length > 0}>
              {pending ? "Scheduling…" : `Schedule deletion`}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** "in 4 days" / "tomorrow" / "today" for a deletion date. */
export function graceRemaining(deleteAfter: string, now: Date = new Date()): string {
  const days = daysUntil(deleteAfter, now);
  if (days === null) return "";
  if (days <= 0) return "within a day";
  if (days === 1) return "in 1 day";
  return `in ${days} days`;
}

/** Banner while a deletion is scheduled, with Undelete when allowed. */
export function ScheduledDeletionBanner({
  deleteAfter,
  reason,
  children,
  onUndelete,
  undoLabel,
}: {
  deleteAfter: string;
  reason?: string | null;
  /** What still works meanwhile (e.g. "It can still be restored"). */
  children?: ReactNode;
  /** Omit when the user may not undo it. */
  onUndelete?: () => Promise<unknown>;
  undoLabel: string;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Alert className="border-amber-500/60" data-testid="scheduled-deletion">
      <CalendarClockIcon aria-hidden="true" className="text-amber-600" />
      <AlertTitle>
        Scheduled for deletion on <time dateTime={deleteAfter}>{formatDateTime(deleteAfter)}</time> (
        {graceRemaining(deleteAfter)})
      </AlertTitle>
      <AlertDescription>
        <div className="space-y-2">
          {reason && (
            <p>
              Reason: <span className="text-foreground">{reason}</span>
            </p>
          )}
          {children}
          {error && <p className="text-destructive">{error}</p>}
          {onUndelete && (
            <Button
              size="sm"
              variant="outline"
              disabled={pending}
              onClick={async () => {
                setError(null);
                setPending(true);
                try {
                  await onUndelete();
                } catch (err) {
                  setError(actionErrorMessage(err));
                } finally {
                  setPending(false);
                }
              }}
            >
              <Undo2Icon aria-hidden="true" /> {pending ? "Undeleting…" : undoLabel}
            </Button>
          )}
        </div>
      </AlertDescription>
    </Alert>
  );
}

// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
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
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys, type AgentAction } from "@/lib/api/endpoints";
import { ReasonRequestSchema, type Agent } from "@/lib/api/fleet-schemas";

interface ActionCopy {
  label: string;
  title: (host: string) => string;
  description: string;
  submit: string;
  pending: string;
  done: (host: string) => string;
  destructive?: boolean;
}

export const ACTION_COPY: Record<AgentAction, ActionCopy> = {
  approve: {
    label: "Approve",
    title: (h) => `Approve ${h}`,
    description:
      "The agent becomes active: its sessions are accepted and DBR² can discover and protect this host.",
    submit: "Approve host",
    pending: "Approving…",
    done: (h) => `${h} approved`,
  },
  suspend: {
    label: "Suspend",
    title: (h) => `Suspend ${h}`,
    description:
      "Ends the agent's session and refuses new ones until the host is resumed. No data is deleted.",
    submit: "Suspend host",
    pending: "Suspending…",
    done: (h) => `${h} suspended`,
  },
  resume: {
    label: "Resume",
    title: (h) => `Resume ${h}`,
    description: "Allows the suspended agent to connect again.",
    submit: "Resume host",
    pending: "Resuming…",
    done: (h) => `${h} resumed`,
  },
  revoke: {
    label: "Revoke",
    title: (h) => `Revoke ${h}`,
    description:
      "Permanently revokes the agent and all of its certificates. This cannot be undone: the host must enroll again with a new registration token.",
    submit: "Revoke host",
    pending: "Revoking…",
    done: (h) => `${h} revoked`,
    destructive: true,
  },
};

/** The status transitions offered for a host in a given status. */
export function actionsFor(status: Agent["status"]): AgentAction[] {
  switch (status) {
    case "pending":
      return ["approve", "revoke"];
    case "active":
      return ["suspend", "revoke"];
    case "suspended":
      return ["resume", "revoke"];
    case "revoked":
      return [];
  }
}

/**
 * Dialog for a status transition. Every transition requires a reason (stored
 * in the audit log). Revoking additionally requires typing the hostname, the
 * ADR-0014 safeguard against a single operator's slip.
 */
export function AgentActionDialog({
  agent,
  action,
  open,
  onOpenChange,
}: {
  agent: Agent;
  action: AgentAction;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const id = useId();
  const copy = ACTION_COPY[action];
  const queryClient = useQueryClient();
  const toast = useToast();
  const [reason, setReason] = useState("");
  const [confirmName, setConfirmName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: (r: string) => api.agentAction(agent.id, action, { reason: r }),
    onSuccess: async (updated) => {
      queryClient.setQueryData(queryKeys.agent(agent.id), updated);
      toast({ title: copy.done(agent.hostname) });
      close(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.agents });
      await queryClient.invalidateQueries({ queryKey: queryKeys.applications });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  const needsConfirm = action === "revoke";
  const confirmed = !needsConfirm || confirmName === agent.hostname;

  function close(next: boolean) {
    if (!next) {
      setReason("");
      setConfirmName("");
      setError(null);
      mutation.reset();
    }
    onOpenChange(next);
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = ReasonRequestSchema.safeParse({ reason });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Enter a reason.");
      return;
    }
    if (!confirmed) {
      setError(`Type the hostname "${agent.hostname}" to confirm.`);
      return;
    }
    mutation.mutate(parsed.data.reason);
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="space-y-4">
          <DialogHeader>
            <DialogTitle>{copy.title(agent.hostname)}</DialogTitle>
            <DialogDescription>{copy.description}</DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-reason`}>Reason (stored in the audit log)</Label>
            <Textarea
              id={`${id}-reason`}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={500}
              required
              aria-required="true"
              disabled={mutation.isPending}
            />
          </div>
          {needsConfirm && (
            <div className="space-y-2">
              <Label htmlFor={`${id}-confirm`}>
                Type <span className="font-mono font-semibold">{agent.hostname}</span> to confirm
              </Label>
              <Input
                id={`${id}-confirm`}
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                required
                aria-required="true"
                disabled={mutation.isPending}
              />
            </div>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={mutation.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button
              type="submit"
              variant={copy.destructive ? "destructive" : "default"}
              disabled={mutation.isPending || reason.trim() === "" || !confirmed}
            >
              {mutation.isPending ? copy.pending : copy.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** "Run discovery" button (active hosts): starts the DiscoverHost workflow. */
export function DiscoverButton({ agent, size = "xs" }: { agent: Agent; size?: "xs" | "sm" | "default" }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const discover = useMutation({
    mutationFn: () => api.discoverAgent(agent.id),
    onSuccess: (res) => {
      toast({
        title: `Discovery started on ${agent.hostname}`,
        description: (
          <>
            Workflow <code className="font-mono text-xs break-all">{res.workflow_id}</code>. Applications
            update when it completes.
          </>
        ),
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.agentInventory(agent.id) });
    },
    onError: (err) =>
      toast({
        title: `Could not start discovery on ${agent.hostname}`,
        description: actionErrorMessage(err),
        variant: "destructive",
      }),
  });
  return (
    <Button
      type="button"
      variant="outline"
      size={size}
      onClick={() => discover.mutate()}
      disabled={discover.isPending}
      aria-label={`Run discovery on ${agent.hostname}`}
    >
      {discover.isPending ? "Starting…" : "Run discovery"}
    </Button>
  );
}

/** Row / header actions for one host (requires `host.manage`). */
export function HostActions({ agent, size = "xs" }: { agent: Agent; size?: "xs" | "sm" | "default" }) {
  const [open, setOpen] = useState<AgentAction | null>(null);
  const actions = actionsFor(agent.status);
  if (actions.length === 0) return <span className="text-muted-foreground">—</span>;

  return (
    <div className="flex flex-wrap items-center gap-1">
      {actions.map((action) => (
        <Button
          key={action}
          type="button"
          size={size}
          variant={ACTION_COPY[action].destructive ? "destructive" : action === "approve" ? "default" : "outline"}
          onClick={() => setOpen(action)}
          aria-label={`${ACTION_COPY[action].label} ${agent.hostname}`}
        >
          {ACTION_COPY[action].label}
        </Button>
      ))}
      {agent.status === "active" && <DiscoverButton agent={agent} size={size} />}
      {open && (
        <AgentActionDialog
          agent={agent}
          action={open}
          open
          onOpenChange={(next) => {
            if (!next) setOpen(null);
          }}
        />
      )}
    </div>
  );
}

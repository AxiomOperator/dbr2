// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PencilIcon } from "lucide-react";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  CRITICALITIES,
  ENVIRONMENTS,
  UpdateApplicationRequestSchema,
  type ApplicationDetail,
} from "@/lib/api/fleet-schemas";

const NONE = "__none__";
const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** Select value for a stored metadata value (unknown values are kept as-is). */
function toSelect(v: string | null): string {
  return v ? v : NONE;
}

export function EditMetadataDialog({ app }: { app: ApplicationDetail }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [displayName, setDisplayName] = useState("");
  const [owner, setOwner] = useState("");
  const [environment, setEnvironment] = useState(NONE);
  const [criticality, setCriticality] = useState(NONE);
  const [error, setError] = useState<string | null>(null);

  const update = useMutation({
    mutationFn: (req: Parameters<typeof api.updateApplication>[1]) => api.updateApplication(app.id, req),
    onSuccess: async () => {
      toast({ title: "Application metadata saved" });
      setOpen(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.application(app.id), exact: true }),
        queryClient.invalidateQueries({ queryKey: queryKeys.applications, exact: true }),
      ]);
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    if (next) {
      setDisplayName(app.display_name ?? "");
      setOwner(app.owner ?? "");
      setEnvironment(toSelect(app.environment));
      setCriticality(toSelect(app.criticality));
      setError(null);
      update.reset();
    }
    setOpen(next);
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    // PATCH semantics: only changed fields are sent; an empty string clears one.
    const next = {
      display_name: displayName.trim(),
      owner: owner.trim(),
      environment: environment === NONE ? "" : environment,
      criticality: criticality === NONE ? "" : criticality,
    };
    const current = {
      display_name: app.display_name ?? "",
      owner: app.owner ?? "",
      environment: app.environment ?? "",
      criticality: app.criticality ?? "",
    };
    const changed = Object.fromEntries(
      (Object.keys(next) as (keyof typeof next)[])
        .filter((k) => next[k] !== current[k])
        .map((k) => [k, next[k]]),
    );
    if (Object.keys(changed).length === 0) {
      setOpen(false);
      return;
    }
    const parsed = UpdateApplicationRequestSchema.safeParse(changed);
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    update.mutate(parsed.data);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <PencilIcon aria-hidden="true" /> Edit metadata
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} noValidate className="space-y-4">
          <DialogHeader>
            <DialogTitle>Edit metadata</DialogTitle>
            <DialogDescription>
              Ownership information for <span className="font-mono">{app.name}</span>. Leave a field
              empty to clear it.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-display`}>Display name</Label>
            <Input
              id={`${id}-display`}
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder={app.name}
              maxLength={200}
              disabled={update.isPending}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-owner`}>Owner</Label>
            <Input
              id={`${id}-owner`}
              value={owner}
              onChange={(e) => setOwner(e.target.value)}
              placeholder="Team or person"
              maxLength={200}
              disabled={update.isPending}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={`${id}-env`}>Environment</Label>
              <Select value={environment} onValueChange={setEnvironment} disabled={update.isPending}>
                <SelectTrigger id={`${id}-env`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Not set</SelectItem>
                  {ENVIRONMENTS.map((v) => (
                    <SelectItem key={v} value={v}>
                      {cap(v)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-crit`}>Criticality</Label>
              <Select value={criticality} onValueChange={setCriticality} disabled={update.isPending}>
                <SelectTrigger id={`${id}-crit`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Not set</SelectItem>
                  {CRITICALITIES.map((v) => (
                    <SelectItem key={v} value={v}>
                      {cap(v)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={update.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={update.isPending}>
              {update.isPending ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

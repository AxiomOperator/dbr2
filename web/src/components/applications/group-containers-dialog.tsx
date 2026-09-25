// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BoxesIcon } from "lucide-react";
import { useRouter } from "next/navigation";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { CreateApplicationRequestSchema, type ApplicationSummary } from "@/lib/api/fleet-schemas";

/**
 * "Group containers": builds a manual application from standalone containers
 * of one host. Standalone containers are the `container`-kind applications
 * (one per container; the application name is the container name).
 */
export function GroupContainersDialog({ applications }: { applications: ApplicationSummary[] }) {
  const id = useId();
  const router = useRouter();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [hostId, setHostId] = useState("");
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const hosts = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of applications) {
      if (a.kind === "container" && !a.missing_since) m.set(a.host_id, a.hostname);
    }
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [applications]);

  const standalone = useMemo(
    () =>
      applications
        .filter((a) => a.kind === "container" && a.host_id === hostId && !a.missing_since)
        .sort((a, b) => a.name.localeCompare(b.name)),
    [applications, hostId],
  );

  const create = useMutation({
    mutationFn: api.createApplication,
    onSuccess: async (res, req) => {
      toast({ title: `Application ${req.name} created` });
      onOpenChange(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.applications });
      router.push(`/applications/${encodeURIComponent(res.id)}`);
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function onOpenChange(next: boolean) {
    setOpen(next);
    if (!next) {
      setHostId("");
      setName("");
      setPicked([]);
      setError(null);
      create.reset();
    }
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const parsed = CreateApplicationRequestSchema.safeParse({ name, host_id: hostId, containers: picked });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    create.mutate(parsed.data);
  }

  const toggle = (container: string, on: boolean) =>
    setPicked((cur) => (on ? [...cur, container] : cur.filter((c) => c !== container)));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline">
          <BoxesIcon aria-hidden="true" /> Group containers
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} noValidate className="space-y-4">
          <DialogHeader>
            <DialogTitle>Group containers</DialogTitle>
            <DialogDescription>
              Combine standalone (non-Compose) containers of one host into a manual application,
              protected and restored together.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-host`}>Host</Label>
            <Select
              value={hostId}
              onValueChange={(v) => {
                setHostId(v);
                setPicked([]);
              }}
              disabled={create.isPending}
            >
              <SelectTrigger id={`${id}-host`} className="w-full">
                <SelectValue placeholder="Choose a host" />
              </SelectTrigger>
              <SelectContent>
                {hosts.map(([hid, hostname]) => (
                  <SelectItem key={hid} value={hid}>
                    {hostname}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {hosts.length === 0 && (
              <p className="text-sm text-muted-foreground">No host has standalone containers.</p>
            )}
          </div>
          {hostId && (
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">Containers</legend>
              {standalone.length === 0 ? (
                <p className="text-sm text-muted-foreground">No standalone containers on this host.</p>
              ) : (
                <ul className="max-h-56 space-y-1.5 overflow-y-auto rounded-md border p-2">
                  {standalone.map((a) => {
                    const cid = `${id}-c-${a.id}`;
                    return (
                      <li key={a.id} className="flex items-center gap-2">
                        <Checkbox
                          id={cid}
                          checked={picked.includes(a.name)}
                          onCheckedChange={(v) => toggle(a.name, v === true)}
                          disabled={create.isPending}
                        />
                        <Label htmlFor={cid} className="font-mono text-xs font-normal">
                          {a.name}
                        </Label>
                      </li>
                    );
                  })}
                </ul>
              )}
            </fieldset>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-name`}>Application name</Label>
            <Input
              id={`${id}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={200}
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
              {create.isPending ? "Creating…" : "Create application"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

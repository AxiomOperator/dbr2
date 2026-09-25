// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronRightIcon } from "lucide-react";
import { useState } from "react";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import type { RegistrationToken } from "@/lib/api/fleet-schemas";
import { useRegistrationTokens } from "@/lib/api/hooks";
import { formatDateTime } from "@/lib/format";

type TokenState = "used" | "revoked" | "expired" | "unused";

export function tokenState(t: RegistrationToken, now: Date = new Date()): TokenState {
  if (t.revoked_at) return "revoked";
  if (t.used_at) return "used";
  if (new Date(t.expires_at).getTime() <= now.getTime()) return "expired";
  return "unused";
}

function TokenStateBadge({ state }: { state: TokenState }) {
  const label = { used: "Used", revoked: "Revoked", expired: "Expired", unused: "Unused" }[state];
  return (
    <Badge variant={state === "unused" ? "default" : state === "revoked" ? "destructive" : "outline"}>
      {label}
    </Badge>
  );
}

function TokenRows() {
  const tokens = useRegistrationTokens();
  const queryClient = useQueryClient();
  const toast = useToast();
  const revoke = useMutation({
    mutationFn: (t: RegistrationToken) => api.revokeRegistrationToken(t.id),
    onSuccess: async (_d, t) => {
      toast({ title: `Token ${t.prefix}… revoked` });
      await queryClient.invalidateQueries({ queryKey: queryKeys.registrationTokens });
    },
    onError: (err) =>
      toast({ title: "Could not revoke the token", description: actionErrorMessage(err), variant: "destructive" }),
  });

  if (tokens.isPending) return <RowsSkeleton label="Loading registration tokens…" rows={3} />;
  if (tokens.isError) {
    return (
      <QueryError
        title="Could not load registration tokens"
        error={tokens.error}
        onRetry={() => void tokens.refetch()}
      />
    );
  }
  return (
    <div className="rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead scope="col">Token</TableHead>
            <TableHead scope="col">Description</TableHead>
            <TableHead scope="col">State</TableHead>
            <TableHead scope="col">Created</TableHead>
            <TableHead scope="col">Expires</TableHead>
            <TableHead scope="col">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tokens.data.length === 0 ? (
            <TableRow>
              <TableCell colSpan={6} className="h-16 text-center text-muted-foreground">
                No registration tokens.
              </TableCell>
            </TableRow>
          ) : (
            tokens.data.map((t) => {
              const state = tokenState(t);
              return (
                <TableRow key={t.id}>
                  <TableCell className="font-mono text-xs">{t.prefix}…</TableCell>
                  <TableCell>{t.description}</TableCell>
                  <TableCell>
                    <TokenStateBadge state={state} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap">{formatDateTime(t.created_at)}</TableCell>
                  <TableCell className="whitespace-nowrap">{formatDateTime(t.expires_at)}</TableCell>
                  <TableCell className="text-right">
                    {state === "unused" && (
                      <Button
                        size="xs"
                        variant="destructive"
                        disabled={revoke.isPending}
                        onClick={() => revoke.mutate(t)}
                        aria-label={`Revoke token ${t.prefix}`}
                      >
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </div>
  );
}

/** Collapsible list of registration tokens (requires `host.manage`). */
export function RegistrationTokens() {
  const [open, setOpen] = useState(false);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="space-y-3">
      <CollapsibleTrigger asChild>
        <Button variant="ghost" className="-ml-2.5 gap-1.5">
          <ChevronRightIcon
            aria-hidden="true"
            className={open ? "rotate-90 transition-transform" : "transition-transform"}
          />
          Registration tokens
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>{open && <TokenRows />}</CollapsibleContent>
    </Collapsible>
  );
}

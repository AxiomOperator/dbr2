// SPDX-License-Identifier: Apache-2.0
"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { EyeIcon, EyeOffIcon, LockIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { SourceBadge } from "@/components/applications/app-badges";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { QueryError, RowsSkeleton } from "@/components/common/states";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { isApiError } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { PERMISSION_SECRETS_READ, type Compose, type ComposeFile } from "@/lib/api/fleet-schemas";
import { useApplicationCompose } from "@/lib/api/hooks";

function FileBlock({ file, revealed }: { file: ComposeFile; revealed: boolean }) {
  return (
    <figure className="space-y-1.5">
      <figcaption className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-mono text-xs break-all">{file.path}</span>
        {file.masked && (
          <Badge variant="outline">
            <LockIcon aria-hidden="true" /> Secrets masked
          </Badge>
        )}
        {revealed && (
          <Badge variant="destructive">
            <EyeIcon aria-hidden="true" /> Secrets revealed
          </Badge>
        )}
      </figcaption>
      {file.error && (
        <p className="text-sm text-destructive" role="alert">
          {file.error}
        </p>
      )}
      <pre
        tabIndex={0}
        aria-label={`Contents of ${file.path}`}
        className="max-h-[28rem] overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed"
      >
        {file.content}
      </pre>
    </figure>
  );
}

function SecretsTable({ secrets }: { secrets: Record<string, string> }) {
  const entries = Object.entries(secrets).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return null;
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Placeholder values</h3>
      <div className="rounded-lg border">
        <Table aria-label="Secret placeholder values">
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Variable</TableHead>
              <TableHead scope="col">Value</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map(([k, v]) => (
              <TableRow key={k}>
                <TableCell className="font-mono text-xs">{k}</TableCell>
                <TableCell className="font-mono text-xs break-all whitespace-normal">{v}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}

function ComposeContent({ compose }: { compose: Compose }) {
  const revealed = compose.revealed;
  if (compose.source === "reconstructed") {
    return (
      <div className="space-y-4">
        <Alert className="border-violet-500/60">
          <TriangleAlertIcon aria-hidden="true" className="text-violet-600" />
          <AlertTitle>Reconstructed definition</AlertTitle>
          <AlertDescription>
            DBR² generated this from Docker runtime metadata; it is not the original Compose file.
            Review it before use. Sensitive values are <code>{"${VAR}"}</code> placeholders.
          </AlertDescription>
        </Alert>
        {compose.reconstructed ? (
          <pre
            tabIndex={0}
            aria-label="Reconstructed Compose YAML"
            className="max-h-[32rem] overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed"
          >
            {compose.reconstructed}
          </pre>
        ) : (
          <p className="text-sm text-muted-foreground">No reconstructed definition is available.</p>
        )}
        {revealed && compose.secrets && <SecretsTable secrets={compose.secrets} />}
      </div>
    );
  }
  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <h3 className="text-sm font-medium">Compose files ({compose.config_files.length})</h3>
        {compose.config_files.length === 0 ? (
          <p className="text-sm text-muted-foreground">No Compose files were collected.</p>
        ) : (
          compose.config_files.map((f) => <FileBlock key={f.path} file={f} revealed={revealed} />)
        )}
      </section>
      <section className="space-y-3">
        <h3 className="text-sm font-medium">Environment files ({compose.env_files.length})</h3>
        {compose.env_files.length === 0 ? (
          <p className="text-sm text-muted-foreground">No .env files.</p>
        ) : (
          compose.env_files.map((f) => <FileBlock key={f.path} file={f} revealed={revealed} />)
        )}
      </section>
      {revealed && compose.secrets && <SecretsTable secrets={compose.secrets} />}
    </div>
  );
}

function RevealConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Reveal secrets?</DialogTitle>
          <DialogDescription>
            This action is audited: your name, the time and this application are recorded in the
            audit log. Secret values are shown on this page only and are not kept after you leave
            it.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline">
              Cancel
            </Button>
          </DialogClose>
          <Button type="button" variant="destructive" onClick={onConfirm}>
            <EyeIcon aria-hidden="true" /> Reveal secrets
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The application's Compose definition (masked). Users with `secrets.read`
 * can reveal secret values after an explicit, audited confirmation. Revealed
 * content lives under its own query key with `gcTime: 0`, is never refetched
 * automatically (each fetch is an audited reveal) and is removed from the
 * cache when hidden or when this view unmounts.
 */
export function ComposeView({ applicationId }: { applicationId: string }) {
  const me = useCurrentUser();
  const canReveal = hasPermission(me, PERMISSION_SECRETS_READ);
  const queryClient = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [reveal, setReveal] = useState(false);
  const revealKey = queryKeys.applicationComposeRevealed(applicationId);

  const masked = useApplicationCompose(applicationId);
  const revealed = useQuery({
    queryKey: revealKey,
    queryFn: ({ signal }) => api.applicationCompose(applicationId, true, signal),
    enabled: reveal && canReveal,
    gcTime: 0,
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchOnMount: false,
  });

  useEffect(() => {
    const key = queryKeys.applicationComposeRevealed(applicationId);
    return () => {
      queryClient.removeQueries({ queryKey: key, exact: true });
    };
  }, [queryClient, applicationId]);

  function hide() {
    setReveal(false);
    queryClient.removeQueries({ queryKey: revealKey, exact: true });
  }

  if (masked.isPending) return <RowsSkeleton label="Loading the Compose definition…" rows={4} />;
  if (masked.isError) {
    if (isApiError(masked.error) && (masked.error.status === 404 || masked.error.status === 409)) {
      return (
        <p className="text-sm text-muted-foreground">
          No Compose definition is available: {masked.error.detail ?? "the application is not in the latest inventory"}.
        </p>
      );
    }
    return (
      <QueryError
        title="Could not load the Compose definition"
        error={masked.error}
        onRetry={() => void masked.refetch()}
      />
    );
  }

  const showRevealed = reveal && revealed.isSuccess;
  const compose = showRevealed ? revealed.data : masked.data;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <SourceBadge source={masked.data.source} />
          {masked.data.reason && <span className="text-muted-foreground">{masked.data.reason}</span>}
        </div>
        {canReveal &&
          (reveal ? (
            <Button variant="outline" size="sm" onClick={hide}>
              <EyeOffIcon aria-hidden="true" /> Hide secrets
            </Button>
          ) : (
            <Button variant="destructive" size="sm" onClick={() => setConfirmOpen(true)}>
              <EyeIcon aria-hidden="true" /> Reveal secrets
            </Button>
          ))}
      </div>
      {reveal && revealed.isPending && <RowsSkeleton label="Revealing secrets…" rows={2} />}
      {reveal && revealed.isError && (
        <QueryError title="Could not reveal secrets" error={revealed.error} />
      )}
      {showRevealed && (
        <Alert variant="destructive">
          <EyeIcon aria-hidden="true" />
          <AlertTitle>Secrets are visible</AlertTitle>
          <AlertDescription>This reveal was recorded in the audit log. Hide them when you are done.</AlertDescription>
        </Alert>
      )}
      <ComposeContent compose={compose} />
      <RevealConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        onConfirm={() => {
          setConfirmOpen(false);
          setReveal(true);
        }}
      />
    </div>
  );
}

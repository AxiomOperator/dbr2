// SPDX-License-Identifier: Apache-2.0
"use client";

// Recovery point detail sections added in Phases 7–9: deletion with a grace
// period (Delete… / scheduled banner / Undelete), verification results and
// the Recovery Contract evaluated at capture time.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleCheckIcon, CircleXIcon } from "lucide-react";
import Link from "next/link";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { VerificationBadge } from "@/components/backups/backup-badges";
import { DeleteWithGraceDialog, ScheduledDeletionBanner } from "@/components/common/deletion";
import { VerifyButton } from "@/components/repositories/repository-actions";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, queryKeys } from "@/lib/api/endpoints";
import { formatMinutes } from "@/lib/api/policy-schemas";
import {
  DELETION_GRACE_DAYS,
  PERMISSION_BACKUP_DELETE,
  PERMISSION_POLICY_READ,
  PERMISSION_REPOSITORY_MANAGE,
  VerificationDetailsSchema,
  type ManifestContract,
  type RecoveryPoint,
} from "@/lib/api/protection-schemas";
import { formatBytes, formatDateTime } from "@/lib/format";

// ---------------------------------------------------------------------------
// Deletion
// ---------------------------------------------------------------------------

/** Recovery points that can be scheduled for deletion. */
export const canScheduleDeletion = (rp: Pick<RecoveryPoint, "state" | "delete_after">): boolean =>
  (rp.state === "committed" || rp.state === "missing") && !rp.delete_after;

function useRecoveryPointMutations(rp: RecoveryPoint) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const refresh = async (updated: RecoveryPoint) => {
    queryClient.setQueryData(queryKeys.recoveryPoint(rp.id), (old: RecoveryPoint | undefined) =>
      // The action returns the list shape (no manifest / details): keep them.
      old ? { ...old, ...updated, manifest: old.manifest, verification_details: old.verification_details } : updated,
    );
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.recoveryPointsAll }),
      queryClient.invalidateQueries({ queryKey: queryKeys.jobsAll }),
    ]);
  };
  const del = useMutation({
    mutationFn: (req: { confirmation: string; reason: string }) => api.deleteRecoveryPoint(rp.id, rp.application_name, req),
    onSuccess: async (updated) => {
      toast({
        title: "Deletion scheduled",
        description: `The recovery point is deleted after ${formatDateTime(updated.delete_after)}; undo it until then.`,
      });
      await refresh(updated);
    },
  });
  const undelete = useMutation({
    mutationFn: () => api.undeleteRecoveryPoint(rp.id),
    onSuccess: async (updated) => {
      toast({ title: "Deletion cancelled", description: "The recovery point is kept." });
      await refresh(updated);
    },
  });
  return { del, undelete };
}

/** "Delete…" for a recovery point (requires `backup.delete`). */
export function DeleteRecoveryPointButton({ rp }: { rp: RecoveryPoint }) {
  const { del } = useRecoveryPointMutations(rp);
  return (
    <DeleteWithGraceDialog
      subject="this recovery point"
      name={rp.application_name}
      nameKind="application name"
      onDelete={(req) => del.mutateAsync(req)}
    >
      <p>
        Recovery point <code className="font-mono text-xs break-all">{rp.id}</code> of{" "}
        <strong>{rp.application_name}</strong> is deleted from the Repository after a {DELETION_GRACE_DAYS}-day grace
        period.
      </p>
      <p className="text-muted-foreground">
        Until then it stays listed and <strong className="text-foreground">can still be restored</strong>, and you can
        undelete it. Retention may still prune it earlier if the policy says so; the latest recovery point of an
        application is never deleted.
      </p>
    </DeleteWithGraceDialog>
  );
}

/** Deletion state of a recovery point: scheduled banner (with Undelete) or deleted notice. */
export function RecoveryPointDeletionState({ rp }: { rp: RecoveryPoint }) {
  const me = useCurrentUser();
  const { undelete } = useRecoveryPointMutations(rp);
  const canDelete = hasPermission(me, PERMISSION_BACKUP_DELETE);
  if (rp.state === "deleted") {
    return (
      <Alert>
        <AlertTitle>Deleted {rp.deleted_at ? formatDateTime(rp.deleted_at) : ""}</AlertTitle>
        <AlertDescription>
          The recovery point was removed from the Repository and can no longer be restored.
          {rp.delete_reason ? ` Reason: ${rp.delete_reason}` : ""}
        </AlertDescription>
      </Alert>
    );
  }
  if (rp.state === "deleting") {
    return (
      <Alert>
        <AlertTitle>Being deleted</AlertTitle>
        <AlertDescription>
          The grace period is over and the recovery point is being removed from the Repository.
          {rp.delete_reason ? ` Reason: ${rp.delete_reason}` : ""}
        </AlertDescription>
      </Alert>
    );
  }
  if (!rp.delete_after) return null;
  return (
    <ScheduledDeletionBanner
      deleteAfter={rp.delete_after}
      reason={rp.delete_reason}
      undoLabel="Undelete"
      onUndelete={canDelete ? () => undelete.mutateAsync() : undefined}
    >
      <p>Until then the recovery point stays restorable.</p>
      {!canDelete && (
        <p>
          Undeleting requires the <code>{PERMISSION_BACKUP_DELETE}</code> permission.
        </p>
      )}
    </ScheduledDeletionBanner>
  );
}

// ---------------------------------------------------------------------------
// Verification (Phase 9)
// ---------------------------------------------------------------------------

/** Verification state and per-component results of a recovery point. */
export function VerificationCard({ rp }: { rp: RecoveryPoint }) {
  const me = useCurrentUser();
  const details =
    rp.verification_details === undefined || rp.verification_details === null
      ? null
      : VerificationDetailsSchema.safeParse(rp.verification_details);
  const canVerify = hasPermission(me, PERMISSION_REPOSITORY_MANAGE) && rp.state === "committed";
  const components = details?.success ? details.data.components : [];
  return (
    <Card data-testid="verification-card">
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-2">
            Verification <VerificationBadge state={rp.verification} verifiedAt={rp.verified_at} />
          </h2>
        </CardTitle>
        <CardDescription>
          {rp.verification === "unverified"
            ? "Not verified yet: the weekly Repository verification, or “Verify now”, checks it."
            : `Last checked ${formatDateTime(rp.verified_at)}${
                details?.success && details.data.read_percent !== undefined ? `, reading ${details.data.read_percent} % of the files back` : ""
              }.`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {canVerify && <VerifyButton repositoryId={rp.repository_id} recoveryPointId={rp.id} label="this recovery point" />}
        {details && !details.success && (
          <p className="text-sm text-muted-foreground">The verification details are in a format this console does not know.</p>
        )}
        {components.length > 0 && (
          <div className="rounded-lg border">
            <Table aria-label="Verification results">
              <TableHeader>
                <TableRow>
                  {["Component", "Result", "Files", "Directories", "Files read", "Bytes read"].map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {components.map((c) => (
                  <TableRow key={c.name} data-verify-ok={c.errors.length === 0 ? "true" : "false"}>
                    <TableCell className="align-top">
                      <div className="font-mono text-xs break-all">{c.name}</div>
                      {c.errors.length > 0 && (
                        <ul className="mt-1 max-w-md list-disc space-y-0.5 pl-4 text-xs break-words whitespace-normal text-destructive">
                          {c.errors.map((e) => (
                            <li key={e}>{e}</li>
                          ))}
                        </ul>
                      )}
                    </TableCell>
                    <TableCell className="align-top">
                      {c.errors.length === 0 ? (
                        <span className="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400">
                          <CircleCheckIcon aria-hidden="true" className="size-3.5" /> OK
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-destructive">
                          <CircleXIcon aria-hidden="true" className="size-3.5" /> {c.errors.length} error
                          {c.errors.length === 1 ? "" : "s"}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="align-top tabular-nums">{c.files}</TableCell>
                    <TableCell className="align-top tabular-nums">{c.dirs}</TableCell>
                    <TableCell className="align-top tabular-nums">{c.files_read}</TableCell>
                    <TableCell className="align-top tabular-nums">{formatBytes(c.bytes_read)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Recovery Contract at capture time (Phase 7)
// ---------------------------------------------------------------------------

export function ContractAtCaptureCard({
  contract,
  applicationId,
}: {
  contract: ManifestContract | null;
  applicationId: string;
}) {
  const me = useCurrentUser();
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-2">
            Recovery Contract at capture
            {contract &&
              (contract.satisfied ? (
                <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
                  <CircleCheckIcon aria-hidden="true" /> Satisfied
                </Badge>
              ) : (
                <Badge variant="destructive">
                  <CircleXIcon aria-hidden="true" /> Not satisfied
                </Badge>
              ))}
          </h2>
        </CardTitle>
        <CardDescription>
          The application&apos;s contract as evaluated when this recovery point was captured (the RPO is met by
          definition at capture time).
        </CardDescription>
      </CardHeader>
      <CardContent className="text-sm">
        {!contract ? (
          <p className="text-muted-foreground">
            No Recovery Contract applied when this recovery point was captured.
            {hasPermission(me, PERMISSION_POLICY_READ) && (
              <>
                {" "}
                Contracts are set on the{" "}
                <Link href={`/applications/${applicationId}`} className="text-foreground underline underline-offset-4">
                  application
                </Link>{" "}
                (Backup settings tab).
              </>
            )}
          </p>
        ) : (
          <dl className="grid gap-3 sm:grid-cols-3">
            <div>
              <dt className="text-muted-foreground">Maximum RPO</dt>
              <dd>{contract.details.max_rpo_minutes ? formatMinutes(contract.details.max_rpo_minutes) : "None"}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Required components</dt>
              <dd>
                {contract.details.required_components.length === 0 ? (
                  "None"
                ) : (
                  <ul className="font-mono text-xs">
                    {contract.details.required_components.map((c) => (
                      <li key={c} className="break-all">
                        {c}
                      </li>
                    ))}
                  </ul>
                )}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Missing</dt>
              <dd>
                {contract.details.missing_components.length === 0 ? (
                  "Nothing"
                ) : (
                  <ul className="font-mono text-xs text-destructive">
                    {contract.details.missing_components.map((c) => (
                      <li key={c} className="break-all">
                        {c}
                      </li>
                    ))}
                  </ul>
                )}
              </dd>
            </div>
          </dl>
        )}
      </CardContent>
    </Card>
  );
}

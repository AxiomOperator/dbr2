// SPDX-License-Identifier: Apache-2.0
"use client";

import { ShieldAlertIcon } from "lucide-react";
import { SeverityBadge } from "@/components/applications/app-badges";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Unprotected } from "@/lib/api/fleet-schemas";

const RANK: Record<string, number> = { high: 0, low: 1 };

/** High severity first, then by container and path (stable, non-mutating). */
export function sortUnprotected(items: Unprotected[]): Unprotected[] {
  return [...items].sort(
    (a, b) =>
      (RANK[a.severity] ?? 2) - (RANK[b.severity] ?? 2) ||
      a.container.localeCompare(b.container) ||
      a.path.localeCompare(b.path),
  );
}

/**
 * Writable container paths that no volume or bind mount backs: they are lost
 * when the container is recreated. This is the core Phase 3 finding.
 */
export function UnprotectedData({ items }: { items: Unprotected[] }) {
  if (items.length === 0) return null;
  const sorted = sortUnprotected(items);
  const high = sorted.filter((u) => u.severity === "high").length;

  return (
    <Card className={high > 0 ? "border-destructive/50 ring-1 ring-destructive/30" : undefined}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldAlertIcon aria-hidden="true" className={high > 0 ? "text-destructive" : "text-muted-foreground"} />
          <h2>Unprotected data</h2>
        </CardTitle>
        <CardDescription>
          {high > 0 ? (
            <>
              <strong className="text-destructive">
                {high} high-severity path{high === 1 ? "" : "s"}
              </strong>{" "}
              written inside the container filesystem, not backed by a volume or bind mount.
              Container-level backups will not include {high === 1 ? "it" : "them"}, and recreating
              the container loses the data. Move {high === 1 ? "it" : "them"} to a volume.
            </>
          ) : (
            "Low-severity writable paths (logs, caches, runtime config): usually regenerated, but lost when the container is recreated."
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="rounded-lg border">
          <Table aria-label="Unprotected paths">
            <TableHeader>
              <TableRow>
                <TableHead scope="col">Severity</TableHead>
                <TableHead scope="col">Container</TableHead>
                <TableHead scope="col">Path</TableHead>
                <TableHead scope="col" className="text-right">
                  Files
                </TableHead>
                <TableHead scope="col">Reason</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.map((u) => (
                <TableRow key={`${u.container}:${u.path}`} data-severity={u.severity}>
                  <TableCell>
                    <SeverityBadge severity={u.severity} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{u.container}</TableCell>
                  <TableCell className="font-mono text-xs break-all whitespace-normal">{u.path}</TableCell>
                  <TableCell className="text-right tabular-nums">{u.files}</TableCell>
                  <TableCell className="text-xs whitespace-normal text-muted-foreground">{u.reason}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  );
}

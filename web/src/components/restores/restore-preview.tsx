// SPDX-License-Identifier: Apache-2.0
"use client";

import { OctagonXIcon, ShieldAlertIcon, TriangleAlertIcon } from "lucide-react";
import { useId, type ReactNode } from "react";
import { ComponentActionBadge, RestoreModeBadge } from "@/components/restores/restore-badges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Preview } from "@/lib/api/restore-schemas";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

const COLLISION_KIND_LABEL: Record<string, string> = {
  container_name: "Container name",
  port: "Port",
  network: "Network",
  volume: "Volume",
  bind_path: "Bind path",
  dependency: "Dependency",
};

const NETWORK_ACTION_LABEL: Record<string, string> = {
  exists: "Exists",
  create: "Create",
  missing_external: "Missing (external)",
};

function Section({
  title,
  count,
  description,
  children,
}: {
  title: string;
  count?: number;
  description?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="space-y-2">
      <h3 id={id} className="text-sm font-medium">
        {title}
        {count !== undefined && <span className="font-normal text-muted-foreground"> ({count})</span>}
      </h3>
      {description && <p className="text-xs text-muted-foreground">{description}</p>}
      {children}
    </section>
  );
}

const None = ({ children }: { children: ReactNode }) => <p className="text-sm text-muted-foreground">{children}</p>;

function Digest({ value }: { value: string }) {
  if (!value) return <span className="text-muted-foreground">No registry digest</span>;
  const at = value.indexOf("@");
  const d = at >= 0 ? value.slice(at + 1) : value;
  return (
    <code className="font-mono text-xs" title={value}>
      {d.length > 19 ? `${d.slice(0, 19)}…` : d}
    </code>
  );
}

/**
 * The restore impact preview (ADR-0014). Read-only: used by the wizard and
 * for the preview recorded with a restore.
 */
export function RestorePreviewView({ preview: p, recorded = false }: { preview: Preview; recorded?: boolean }) {
  return (
    <div className="space-y-6" data-testid="restore-preview">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <RestoreModeBadge mode={p.mode} />
        <span>
          <strong>{p.application_name}</strong> to <strong>{p.target_hostname}</strong>
        </span>
        {p.blocked ? (
          <Badge variant="destructive">
            <OctagonXIcon aria-hidden="true" /> Blocked
          </Badge>
        ) : (
          !recorded && <Badge variant="secondary">No collisions</Badge>
        )}
      </div>

      {p.production && (
        <Alert variant="destructive" className="border-destructive/60 bg-destructive/5" data-testid="production-banner">
          <ShieldAlertIcon aria-hidden="true" />
          <AlertTitle>Production restore</AlertTitle>
          <AlertDescription>
            <ul className="list-disc space-y-0.5 pl-4">
              {p.production_reasons.length > 0 ? (
                p.production_reasons.map((r) => <li key={r}>{r}</li>)
              ) : (
                <li>The server classified this restore as production.</li>
              )}
            </ul>
            <p className="mt-1">
              Requires the <code>restore.production</code> permission, a reason and typing the application name.
            </p>
          </AlertDescription>
        </Alert>
      )}

      {p.collisions.length > 0 && (
        <section aria-label="Collisions">
          <Alert variant="destructive" className="border-destructive/60">
            <OctagonXIcon aria-hidden="true" />
            <AlertTitle>
              {p.collisions.length} collision{p.collisions.length === 1 ? "" : "s"}: the restore is blocked
            </AlertTitle>
            <AlertDescription>
              <p>The target host has something that belongs to someone else. Resolve these, choose another host or remap paths.</p>
              <ul className="mt-2 space-y-1.5">
                {p.collisions.map((c, i) => (
                  <li key={`${c.kind}-${c.name}-${i}`} data-collision-kind={c.kind} className="text-sm">
                    <Badge variant="destructive" className="mr-1.5">
                      {COLLISION_KIND_LABEL[c.kind] ?? c.kind}
                    </Badge>
                    <code className="font-mono text-xs break-all">{c.name}</code>
                    <span className="block text-xs">{c.detail}</span>
                  </li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        </section>
      )}

      {p.warnings.length > 0 && (
        <section aria-label="Warnings">
          <Alert className="border-amber-500/60 text-amber-800 dark:text-amber-300">
            <TriangleAlertIcon aria-hidden="true" />
            <AlertTitle>Warnings</AlertTitle>
            <AlertDescription className="text-amber-800 dark:text-amber-300">
              <ul className="list-disc space-y-0.5 pl-4">
                {p.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        </section>
      )}

      <Section title="Data" count={p.components.length}>
        {p.components.length === 0 ? (
          <None>No data components are restored.</None>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Data">
              <TableHeader>
                <TableRow>
                  {["Component", "Action", "Target", "Size"].map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {p.components.map((c) => (
                  <TableRow key={c.name} data-action={c.action}>
                    <TableCell className="align-top whitespace-normal">
                      <div className="font-mono text-xs break-all">{c.name}</div>
                      <div className="text-xs text-muted-foreground">{c.kind}</div>
                    </TableCell>
                    <TableCell className="align-top">
                      <ComponentActionBadge action={c.action} />
                    </TableCell>
                    <TableCell className="min-w-48 align-top font-mono text-xs break-all whitespace-normal">
                      {c.target || "—"}
                    </TableCell>
                    <TableCell className="align-top tabular-nums">{formatBytes(c.size_bytes)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Section>

      <div className="grid gap-6 md:grid-cols-2">
        <Section
          title="Containers to stop"
          count={p.stop_containers.length}
          description="Stopped before the data is swapped in, started again afterwards."
        >
          {p.stop_containers.length === 0 ? (
            <None>None.</None>
          ) : (
            <ul className="space-y-1 text-sm" aria-label="Containers to stop">
              {p.stop_containers.map((c) => (
                <li key={c.id || c.name} className="flex items-center gap-2">
                  <code className="font-mono text-xs">{c.name}</code>
                  <Badge variant="outline" className={cn(c.state === "running" && "text-sky-700 dark:text-sky-400")}>
                    {c.state}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section
          title="Containers to re-create"
          count={p.create_containers.length}
          description="Re-created from the recovery point's configuration."
        >
          {p.create_containers.length === 0 ? (
            <None>None.</None>
          ) : (
            <ul className="flex flex-wrap gap-1.5" aria-label="Containers to re-create">
              {p.create_containers.map((c) => (
                <li key={c}>
                  <Badge variant="outline" className="font-mono">
                    {c}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section title="Networks" count={p.networks.length}>
          {p.networks.length === 0 ? (
            <None>No networks.</None>
          ) : (
            <ul className="space-y-1 text-sm" aria-label="Networks">
              {p.networks.map((n) => (
                <li key={n.name} className="flex items-center gap-2">
                  <code className="font-mono text-xs">{n.name}</code>
                  <Badge
                    variant={n.action === "missing_external" ? "destructive" : "outline"}
                    className={cn(n.action === "create" && "text-emerald-700 dark:text-emerald-400")}
                  >
                    {NETWORK_ACTION_LABEL[n.action] ?? n.action}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section title="Published ports" count={p.ports.length}>
          {p.ports.length === 0 ? (
            <None>No published ports.</None>
          ) : (
            <ul className="flex flex-wrap gap-1.5" aria-label="Published ports">
              {p.ports.map((port) => (
                <li key={port}>
                  <Badge variant="outline" className="font-mono">
                    {port}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Section>
      </div>

      <Section title="Images" count={p.images.length} description="Missing images are pulled by digest, so the restored containers run the captured image.">
        {p.images.length === 0 ? (
          <None>No images recorded.</None>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Images">
              <TableHeader>
                <TableRow>
                  {["Image", "Digest", "Action"].map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {p.images.map((im) => (
                  <TableRow key={`${im.ref}-${im.digest}`}>
                    <TableCell className="font-mono text-xs break-all whitespace-normal">{im.ref}</TableCell>
                    <TableCell>
                      <Digest value={im.digest ?? ""} />
                    </TableCell>
                    <TableCell>
                      {im.action === "pull" ? (
                        <Badge variant="outline">{im.digest ? "Pull by digest" : "Pull"}</Badge>
                      ) : im.action === "present" ? (
                        <Badge variant="secondary">Present</Badge>
                      ) : (
                        <Badge variant="outline">{im.action}</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Section>
    </div>
  );
}

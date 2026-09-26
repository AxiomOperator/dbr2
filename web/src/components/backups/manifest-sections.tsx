// SPDX-License-Identifier: Apache-2.0
"use client";

import { DatabaseIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Manifest, ManifestComponent, ManifestTopology } from "@/lib/api/protection-schemas";
import { formatBytes } from "@/lib/format";

export interface NestedComponent {
  component: ManifestComponent;
  /** 1 for fsmeta records shown under their parent component. */
  depth: 0 | 1;
}

/**
 * Manifest components with every fsmeta record directly under the component
 * it describes (`parent`); records whose parent is missing stay at the end.
 */
export function nestComponents(components: ManifestComponent[]): NestedComponent[] {
  const byParent = new Map<string, ManifestComponent[]>();
  const names = new Set(components.map((c) => c.name));
  for (const c of components) {
    if (c.parent && names.has(c.parent)) {
      byParent.set(c.parent, [...(byParent.get(c.parent) ?? []), c]);
    }
  }
  const out: NestedComponent[] = [];
  for (const c of components) {
    if (c.parent && names.has(c.parent)) continue;
    out.push({ component: c, depth: 0 });
    for (const child of byParent.get(c.name) ?? []) out.push({ component: child, depth: 1 });
  }
  return out;
}

const ENGINE_LABEL: Record<string, string> = { postgresql: "PostgreSQL", redis: "Redis" };

/** "PostgreSQL dump (pg_dumpall-sql-zstd) of service db, container shop-db-1". */
export function describeDatabase(d: NonNullable<ManifestComponent["database"]>): string {
  const where = [d.service && `service ${d.service}`, d.container && `container ${d.container}`].filter(Boolean).join(", ");
  return `${ENGINE_LABEL[d.engine] ?? d.engine} dump (${d.format})${where ? ` of ${where}` : ""}`;
}

export const engineLabel = (engine: string): string => ENGINE_LABEL[engine] ?? engine;

/** Database dump components of the manifest (logical backups, Phase 8). */
export function DatabasesCard({ components }: { components: ManifestComponent[] }) {
  const dbs = components.filter((c) => c.database || c.kind === "database");
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Databases</h2>
        </CardTitle>
        <CardDescription>
          Logical database dumps recorded in the manifest (PostgreSQL <code>pg_dumpall</code>, Redis RDB), taken while
          the database runs and validated before the recovery point is committed.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {dbs.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No database dumps: databases in this recovery point are captured with their volumes (crash-consistent
            unless quiesced). Detected PostgreSQL and Redis containers are dumped when the application&apos;s database
            strategy is “logical” or “both”.
          </p>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Database dumps">
              <TableHeader>
                <TableRow>
                  {["Component", "Engine", "Format", "Taken from", "Validation", "Status", "Size"].map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {dbs.map((c) => (
                  <TableRow key={c.name}>
                    <TableCell className="align-top">
                      <span className="inline-flex items-center gap-1.5">
                        <DatabaseIcon aria-hidden="true" className="size-4 text-muted-foreground" />
                        <span className="font-mono text-xs break-all">{c.name}</span>
                      </span>
                      {c.file_name && <div className="font-mono text-xs text-muted-foreground">{c.file_name}</div>}
                      {c.error && <div className="mt-1 max-w-64 text-xs break-words whitespace-normal text-destructive">{c.error}</div>}
                    </TableCell>
                    <TableCell className="align-top">{c.database ? engineLabel(c.database.engine) : "—"}</TableCell>
                    <TableCell className="align-top font-mono text-xs">{c.database?.format ?? "—"}</TableCell>
                    <TableCell className="align-top text-xs whitespace-normal">
                      {c.database?.service && <div>service {c.database.service}</div>}
                      {c.database?.container && <div className="font-mono">{c.database.container}</div>}
                      {!c.database?.service && !c.database?.container && "—"}
                    </TableCell>
                    <TableCell className="max-w-72 align-top text-xs break-words whitespace-normal">
                      {c.validation || <span className="text-muted-foreground">Not recorded</span>}
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge variant={c.status === "succeeded" ? "secondary" : c.status === "failed" ? "destructive" : "outline"}>
                        {c.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="align-top tabular-nums">{formatBytes(c.size_bytes)}</TableCell>
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

function ports(c: ManifestTopology["containers"][number]): string {
  return (
    c.ports
      .map((p) => `${p.host_port ? `${p.host_port}→` : ""}${p.container_port}/${p.protocol || "tcp"}`)
      .join(", ") || "—"
  );
}

/** The application's shape at capture time (containers, networks, volumes, images). */
export function TopologyCard({ manifest }: { manifest: Manifest }) {
  const t = manifest.topology;
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Topology at capture time</h2>
        </CardTitle>
        <CardDescription>
          Containers, networks and volumes recorded in the manifest: what a restore re-creates and checks for
          collisions.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {!t ? (
          <p className="text-sm text-muted-foreground">
            This manifest records no topology (it was written before Phase 5).
          </p>
        ) : (
          <>
            <div className="rounded-lg border">
              <Table aria-label="Containers at capture time">
                <TableHeader>
                  <TableRow>
                    <TableHead scope="col">Container</TableHead>
                    <TableHead scope="col">Image</TableHead>
                    <TableHead scope="col">State</TableHead>
                    <TableHead scope="col">Ports</TableHead>
                    <TableHead scope="col">Mounts</TableHead>
                    <TableHead scope="col">Networks</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {t.containers.map((c) => (
                    <TableRow key={c.name}>
                      <TableCell className="align-top">
                        <div className="font-mono text-xs">{c.name}</div>
                        {c.service && <div className="text-xs text-muted-foreground">service {c.service}</div>}
                      </TableCell>
                      <TableCell className="align-top font-mono text-xs break-all whitespace-normal">{c.image}</TableCell>
                      <TableCell className="align-top">{c.state || "—"}</TableCell>
                      <TableCell className="align-top font-mono text-xs">{ports(c)}</TableCell>
                      <TableCell className="align-top whitespace-normal">
                        {c.mounts.length === 0 ? (
                          "—"
                        ) : (
                          <ul className="font-mono text-xs">
                            {c.mounts.map((m) => (
                              <li key={`${m.type}-${m.destination}`} className="break-all">
                                {m.type} {m.name || m.source || ""} → {m.destination} ({m.rw ? "rw" : "ro"})
                              </li>
                            ))}
                          </ul>
                        )}
                      </TableCell>
                      <TableCell className="align-top text-xs">{c.networks.join(", ") || "—"}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <dl className="grid gap-4 text-sm sm:grid-cols-2">
              <div>
                <dt className="mb-1 font-medium">Networks</dt>
                <dd>
                  {t.networks.length === 0 ? (
                    <span className="text-muted-foreground">None recorded</span>
                  ) : (
                    <ul className="space-y-0.5">
                      {t.networks.map((n) => (
                        <li key={n.name}>
                          <span className="font-mono text-xs">{n.name}</span>{" "}
                          <span className="text-muted-foreground">
                            {n.driver}
                            {n.external ? " · external (must exist before a restore)" : ""}
                            {n.internal ? " · internal" : ""}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                </dd>
              </div>
              <div>
                <dt className="mb-1 font-medium">Volumes</dt>
                <dd>
                  {t.volumes.length === 0 ? (
                    <span className="text-muted-foreground">None recorded</span>
                  ) : (
                    <ul className="space-y-0.5">
                      {t.volumes.map((v) => (
                        <li key={v.name}>
                          <span className="font-mono text-xs break-all">{v.name}</span>{" "}
                          <span className="text-muted-foreground">
                            {v.driver}
                            {v.external ? " · external" : ""}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                </dd>
              </div>
            </dl>
          </>
        )}
        {manifest.images.length > 0 && (
          <div className="text-sm">
            <h3 className="mb-1 font-medium">Images</h3>
            <ul className="space-y-0.5">
              {manifest.images.map((im) => (
                <li key={`${im.service}-${im.ref}`} className="font-mono text-xs break-all">
                  {im.service && <span className="text-muted-foreground">{im.service}: </span>}
                  {im.ref}
                  {im.digest ? (
                    <span className="text-muted-foreground"> · {im.digest.split("@").pop()?.slice(0, 19)}…</span>
                  ) : (
                    <span className="text-muted-foreground"> · no registry digest (locally built)</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

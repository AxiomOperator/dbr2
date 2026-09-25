// SPDX-License-Identifier: Apache-2.0
"use client";

import { LockIcon } from "lucide-react";
import type { ReactNode } from "react";
import { VolumeClassBadge } from "@/components/applications/app-badges";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Analysis, ContainerDetail } from "@/lib/api/fleet-schemas";

function Section({
  title,
  description,
  count,
  children,
}: {
  title: string;
  description?: ReactNode;
  count: number;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>
            {title} <span className="font-normal text-muted-foreground">({count})</span>
          </h2>
        </CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

function SimpleTable({ label, head, rows }: { label: string; head: string[]; rows: ReactNode[][] }) {
  return (
    <div className="rounded-lg border">
      <Table aria-label={label}>
        <TableHeader>
          <TableRow>
            {head.map((h) => (
              <TableHead key={h} scope="col">
                {h}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((cells, i) => (
            <TableRow key={i}>
              {cells.map((c, j) => (
                <TableCell key={j} className="align-top whitespace-normal">
                  {c}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

const mono = (s: string) => <span className="font-mono text-xs break-all">{s}</span>;

function StateBadge({ state }: { state: string }) {
  return <Badge variant={state === "running" ? "secondary" : "outline"}>{state}</Badge>;
}

export function ServicesSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section title="Services & containers" count={analysis.services.length}>
      {analysis.services.length === 0 ? (
        <Empty>No services.</Empty>
      ) : (
        <SimpleTable
          label="Services"
          head={["Service", "Image", "Containers"]}
          rows={analysis.services.map((s) => [
            <span key="n" className="font-medium">{s.name}</span>,
            mono(s.image),
            <ul key="c" className="space-y-1">
              {s.containers.map((c) => (
                <li key={c.id} className="flex flex-wrap items-center gap-1.5">
                  {mono(c.name)} <StateBadge state={c.state} />
                </li>
              ))}
            </ul>,
          ])}
        />
      )}
    </Section>
  );
}

export function VolumesSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section
      title="Volumes"
      count={analysis.volumes.length}
      description="Local volumes are protected by default; External (driver- or network-backed) volumes belong to another storage system and are not; Ephemeral ones hold throwaway data (ADR-0006)."
    >
      {analysis.volumes.length === 0 ? (
        <Empty>No named or anonymous volumes.</Empty>
      ) : (
        <SimpleTable
          label="Volumes"
          head={["Name", "Class", "Protected by default", "Flags", "Used by"]}
          rows={analysis.volumes.map((v) => [
            <div key="n" className="space-y-0.5">
              {mono(v.name)}
              <div className="text-xs text-muted-foreground">driver {v.driver}</div>
            </div>,
            <div key="c" className="space-y-1">
              <VolumeClassBadge cls={v.class} />
              {v.reasons.length > 0 && (
                <ul className="list-disc pl-4 text-xs text-muted-foreground">
                  {v.reasons.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              )}
            </div>,
            v.protected_by_default ? (
              <Badge key="p" variant="secondary">Yes</Badge>
            ) : (
              <Badge key="p" variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
                No
              </Badge>
            ),
            <div key="f" className="flex flex-wrap gap-1">
              {v.anonymous && <Badge variant="outline">Anonymous</Badge>}
              {v.external && <Badge variant="outline">External</Badge>}
              {!v.anonymous && !v.external && <span className="text-muted-foreground">—</span>}
            </div>,
            <ul key="u" className="space-y-0.5">
              {v.used_by.map((u) => (
                <li key={u}>{mono(u)}</li>
              ))}
            </ul>,
          ])}
        />
      )}
    </Section>
  );
}

export function BindMountsSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section title="Bind mounts" count={analysis.bind_mounts.length}>
      {analysis.bind_mounts.length === 0 ? (
        <Empty>No bind mounts.</Empty>
      ) : (
        <SimpleTable
          label="Bind mounts"
          head={["Container", "Host path", "Container path", "Mode"]}
          rows={analysis.bind_mounts.map((b) => [
            mono(b.container),
            mono(b.source),
            mono(b.destination),
            b.rw ? "Read-write" : "Read-only",
          ])}
        />
      )}
    </Section>
  );
}

export function TmpfsSection({ analysis }: { analysis: Analysis }) {
  if (analysis.tmpfs.length === 0) return null;
  return (
    <Section title="tmpfs" count={analysis.tmpfs.length} description="In-memory mounts; never backed up.">
      <SimpleTable
        label="tmpfs mounts"
        head={["Container", "Path"]}
        rows={analysis.tmpfs.map((t) => [mono(t.container), mono(t.destination)])}
      />
    </Section>
  );
}

export function NetworksSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section title="Networks" count={analysis.networks.length}>
      {analysis.networks.length === 0 ? (
        <Empty>No networks.</Empty>
      ) : (
        <SimpleTable
          label="Networks"
          head={["Name", "Driver", "External"]}
          rows={analysis.networks.map((n) => [
            mono(n.name),
            n.driver,
            n.external ? (
              <Badge key="e" variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
                External
              </Badge>
            ) : (
              "No"
            ),
          ])}
        />
      )}
    </Section>
  );
}

export function ImagesSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section title="Images" count={analysis.images.length}>
      {analysis.images.length === 0 ? (
        <Empty>No images.</Empty>
      ) : (
        <SimpleTable
          label="Images"
          head={["Reference", "Registry digests", "Platform"]}
          rows={analysis.images.map((i) => [
            <div key="r" className="space-y-0.5">
              {mono(i.reference)}
              <div className="font-mono text-[0.7rem] break-all text-muted-foreground">{i.image_id}</div>
            </div>,
            i.digests.length === 0 ? (
              <Badge key="d" variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
                No registry digest (locally built)
              </Badge>
            ) : (
              <ul key="d" className="space-y-0.5">
                {i.digests.map((d) => (
                  <li key={d}>{mono(d)}</li>
                ))}
              </ul>
            ),
            i.platform ?? "—",
          ])}
        />
      )}
    </Section>
  );
}

const DEP_LABEL: Record<string, string> = {
  external_network: "External network",
  external_volume: "External volume",
  network_storage: "Network storage",
  container_network: "Shared container network",
};

export function DependenciesSection({ analysis }: { analysis: Analysis }) {
  return (
    <Section
      title="Dependencies"
      count={analysis.dependencies.length}
      description="Things outside this application that a recovery needs."
    >
      {analysis.dependencies.length === 0 ? (
        <Empty>No external dependencies.</Empty>
      ) : (
        <SimpleTable
          label="Dependencies"
          head={["Kind", "Name", "Detail"]}
          rows={analysis.dependencies.map((d) => [
            <Badge key="k" variant="outline">
              {DEP_LABEL[d.kind] ?? d.kind}
            </Badge>,
            mono(d.name),
            <span key="d" className="text-xs text-muted-foreground">{d.detail}</span>,
          ])}
        />
      )}
    </Section>
  );
}

/** Per-container runtime detail. Env values of sensitive keys arrive masked. */
export function ContainersDetail({ containers }: { containers: ContainerDetail[] }) {
  if (containers.length === 0) {
    return <Empty>No container details in the latest inventory.</Empty>;
  }
  return (
    <div className="space-y-6">
      {containers.map((c) => (
        <Card key={c.id}>
          <CardHeader>
            <CardTitle className="flex flex-wrap items-center gap-2">
              <h3 className="font-mono text-sm">{c.name}</h3>
              <StateBadge state={c.state} />
            </CardTitle>
            <CardDescription className="font-mono text-xs break-all">{c.image}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <section className="space-y-2">
              <h4 className="text-sm font-medium">Environment ({c.env.length})</h4>
              {c.env.length === 0 ? (
                <Empty>No environment variables.</Empty>
              ) : (
                <SimpleTable
                  label={`Environment of ${c.name}`}
                  head={["Variable", "Value"]}
                  rows={c.env.map((e) => [
                    <span key="k" className="inline-flex items-center gap-1.5 font-mono text-xs">
                      {e.sensitive && (
                        <>
                          <LockIcon aria-hidden="true" className="size-3.5 text-amber-600" />
                          <span className="sr-only">Sensitive: </span>
                        </>
                      )}
                      {e.key}
                    </span>,
                    e.sensitive ? (
                      <span key="v" className="font-mono text-xs text-muted-foreground" title="Masked secret">
                        {e.value ?? "********"}
                      </span>
                    ) : (
                      mono(e.value ?? "")
                    ),
                  ])}
                />
              )}
            </section>
            <div className="grid gap-4 md:grid-cols-3">
              <section className="space-y-1.5">
                <h4 className="text-sm font-medium">Ports</h4>
                {c.ports.length === 0 ? (
                  <Empty>None published.</Empty>
                ) : (
                  <ul className="space-y-0.5 font-mono text-xs">
                    {c.ports.map((p, i) => (
                      <li key={i}>
                        {p.host_port ? `${p.host_ip ? `${p.host_ip}:` : ""}${p.host_port} → ` : ""}
                        {p.container_port}/{p.protocol}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
              <section className="space-y-1.5">
                <h4 className="text-sm font-medium">Mounts</h4>
                {c.mounts.length === 0 ? (
                  <Empty>None.</Empty>
                ) : (
                  <ul className="space-y-0.5 text-xs">
                    {c.mounts.map((m, i) => (
                      <li key={i} className="break-all">
                        <Badge variant="outline" className="mr-1">
                          {m.type}
                        </Badge>
                        <span className="font-mono">
                          {m.name ?? m.source ?? ""} → {m.destination}
                        </span>{" "}
                        <span className="text-muted-foreground">({m.rw ? "rw" : "ro"})</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
              <section className="space-y-1.5">
                <h4 className="text-sm font-medium">Networks</h4>
                {c.networks.length === 0 ? (
                  <Empty>None.</Empty>
                ) : (
                  <ul className="space-y-0.5 font-mono text-xs">
                    {c.networks.map((n) => (
                      <li key={n}>{n}</li>
                    ))}
                  </ul>
                )}
              </section>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

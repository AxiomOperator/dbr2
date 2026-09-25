// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon, TriangleAlertIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { HostActions } from "@/components/hosts/host-actions";
import { HostLimitsCard } from "@/components/hosts/host-limits-card";
import {
  AgentStatusBadge,
  CertExpiry,
  ConnectedIndicator,
  DockerState,
  OutdatedBadge,
} from "@/components/hosts/host-badges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { isApiError } from "@/lib/api/client";
import {
  PERMISSION_APPLICATION_READ,
  PERMISSION_HOST_MANAGE,
  PERMISSION_HOST_READ,
  type Agent,
} from "@/lib/api/fleet-schemas";
import { useAgent, useAgentInventory } from "@/lib/api/hooks";
import { dash, formatBytes, formatDateTime, formatRelative } from "@/lib/format";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,11rem)_1fr] gap-3 py-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

function YesNo({ value }: { value: boolean }) {
  return <span>{value ? "Yes" : "No"}</span>;
}

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Agent</h2>
        </CardTitle>
        <CardDescription>Enrollment, versions and health reported by the agent.</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="divide-y text-sm">
          <Field label="Status">
            <AgentStatusBadge status={agent.status} />
          </Field>
          <Field label="Status reason">{dash(agent.status_reason)}</Field>
          <Field label="Connection">
            <ConnectedIndicator connected={agent.connected} />
          </Field>
          <Field label="Agent version">
            <span className="inline-flex items-center gap-1.5">
              <span className="font-mono text-xs">{agent.agent_version}</span>
              {agent.outdated && <OutdatedBadge />}
            </span>
          </Field>
          <Field label="Protocol version">
            <span className="font-mono text-xs">{agent.protocol_version}</span>
          </Field>
          <Field label="Operating system">{dash(agent.os_release)}</Field>
          <Field label="Architecture">{dash(agent.architecture)}</Field>
          <Field label="Docker">
            <DockerState reachable={agent.docker_reachable} version={agent.docker_version} />
          </Field>
          <Field label="Latency">{agent.latency_ms === null ? "—" : `${agent.latency_ms} ms`}</Field>
          <Field label="Health error">
            {agent.health_error ? <span className="text-destructive">{agent.health_error}</span> : "None"}
          </Field>
          <Field label="Last seen">
            {agent.last_seen_at
              ? `${formatRelative(agent.last_seen_at)} (${formatDateTime(agent.last_seen_at)})`
              : "Never"}
          </Field>
          <Field label="Enrolled">{formatDateTime(agent.enrolled_at)}</Field>
          <Field label="Approved">{formatDateTime(agent.approved_at)}</Field>
          <Field label="Certificate expires">
            <CertExpiry iso={agent.certificate_not_after} />
          </Field>
          <Field label="Agent ID">
            <span className="font-mono text-xs">{agent.id}</span>
          </Field>
        </dl>
      </CardContent>
    </Card>
  );
}

function InventoryCard({ agentId }: { agentId: string }) {
  const inv = useAgentInventory(agentId);

  let body: ReactNode;
  if (inv.isPending) {
    body = <RowsSkeleton label="Loading inventory…" rows={5} />;
  } else if (inv.isError) {
    // 404 / 409: no inventory reported yet (pending, or discovery not run).
    body =
      isApiError(inv.error) && (inv.error.status === 404 || inv.error.status === 409) ? (
        <p className="text-sm text-muted-foreground">
          No inventory yet. It is reported once the host is approved and discovery has run.
        </p>
      ) : (
        <QueryError title="Could not load the inventory" error={inv.error} onRetry={() => void inv.refetch()} />
      );
  } else {
    const { inventory, received_at } = inv.data;
    const h = inventory.host;
    const counts: [string, number][] = [
      ["Containers", inventory.containers.length],
      ["Volumes", inventory.volumes.length],
      ["Networks", inventory.networks.length],
      ["Images", inventory.images.length],
      ["Compose projects", inventory.compose_projects.length],
    ];
    body = (
      <div className="space-y-4">
        <ul className="grid grid-cols-2 gap-2 sm:grid-cols-5" aria-label="Inventory counts">
          {counts.map(([label, n]) => (
            <li key={label} className="rounded-md border p-2 text-center">
              <div className="text-xl font-semibold tabular-nums">{n}</div>
              <div className="text-xs text-muted-foreground">{label}</div>
            </li>
          ))}
        </ul>
        <dl className="divide-y text-sm">
          <Field label="Engine">
            {h.runtime} {h.engine_version}{" "}
            <span className="text-muted-foreground">(API {h.api_version})</span>
          </Field>
          <Field label="Root directory">
            <span className="font-mono text-xs">{h.root_dir}</span>
          </Field>
          <Field label="Storage driver">{h.storage_driver}</Field>
          <Field label="SELinux">
            <YesNo value={h.selinux} />
          </Field>
          <Field label="Rootless">
            <YesNo value={h.rootless} />
          </Field>
          <Field label="cgroup version">{h.cgroup_version}</Field>
          <Field label="Kernel">{h.kernel_version}</Field>
          <Field label="CPUs / memory">
            {h.cpus} / {formatBytes(h.memory_bytes)}
          </Field>
          <Field label="Collected">{formatDateTime(inventory.collected_at)}</Field>
          <Field label="Received">{formatDateTime(received_at)}</Field>
        </dl>
        {inventory.warnings.length > 0 && (
          <Alert>
            <TriangleAlertIcon aria-hidden="true" className="text-amber-600" />
            <AlertTitle>
              {inventory.warnings.length} discovery warning{inventory.warnings.length === 1 ? "" : "s"}
            </AlertTitle>
            <AlertDescription>
              <ul className="list-disc space-y-1 pl-4 font-mono text-xs break-all">
                {inventory.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        )}
      </div>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Inventory</h2>
        </CardTitle>
        <CardDescription>Latest discovery report from this host.</CardDescription>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  );
}

function HostDetailBody({ id }: { id: string }) {
  const me = useCurrentUser();
  const agent = useAgent(id);
  const canManage = hasPermission(me, PERMISSION_HOST_MANAGE);
  const canApps = hasPermission(me, PERMISSION_APPLICATION_READ);

  if (agent.isPending) return <RowsSkeleton label="Loading host…" />;
  if (agent.isError && !agent.data) {
    if (isApiError(agent.error) && agent.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Host not found</AlertTitle>
          <AlertDescription>It may have been removed.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the host" error={agent.error} onRetry={() => void agent.refetch()} />;
  }
  const a = agent.data;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">{a.hostname}</h1>
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <AgentStatusBadge status={a.status} />
            <ConnectedIndicator connected={a.connected} />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canApps && (
            <Link href={`/applications?host=${encodeURIComponent(a.id)}`} className={buttonVariants({ variant: "outline", size: "sm" })}>
              Applications on this host
            </Link>
          )}
          {canManage && <HostActions agent={a} size="sm" />}
        </div>
      </div>
      {a.status_reason && (
        <p className="text-sm text-muted-foreground">
          Reason for the last status change:{" "}
          <span className="text-foreground">{a.status_reason}</span>
        </p>
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        <AgentCard agent={a} />
        {a.status === "pending" ? (
          <Card>
            <CardHeader>
              <CardTitle>
                <h2>Inventory</h2>
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              This host is waiting for approval. Its inventory is collected once it is approved.
            </CardContent>
          </Card>
        ) : (
          <InventoryCard agentId={a.id} />
        )}
        {a.status !== "revoked" && <HostLimitsCard agentId={a.id} hostname={a.hostname} />}
      </div>
    </div>
  );
}

export function HostDetail({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link href="/hosts" className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}>
        <ArrowLeftIcon aria-hidden="true" /> All hosts
      </Link>
      {hasPermission(me, PERMISSION_HOST_READ) ? (
        <HostDetailBody id={id} />
      ) : (
        <AccessDenied what="Viewing hosts" permission={PERMISSION_HOST_READ} />
      )}
    </div>
  );
}

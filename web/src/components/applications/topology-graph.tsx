// SPDX-License-Identifier: Apache-2.0
"use client";

import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { BoxIcon, ContainerIcon, FolderIcon, HardDriveIcon, LinkIcon, MemoryStickIcon, NetworkIcon } from "lucide-react";
import { useTheme } from "next-themes";
import { useMemo } from "react";
import type { ApplicationDetail, ComponentCoverage } from "@/lib/api/fleet-schemas";
import { buildTopology, ROW_HEIGHT, type Coverage, type TopologyKind, type TopologyNode } from "@/lib/topology";
import { cn } from "@/lib/utils";

type ResourceData = Pick<TopologyNode, "kind" | "label" | "detail" | "coverage" | "external">;
type ResourceNode = Node<ResourceData, "resource">;

const ICON: Record<TopologyKind, typeof BoxIcon> = {
  image: BoxIcon,
  container: ContainerIcon,
  volume: HardDriveIcon,
  bind: FolderIcon,
  tmpfs: MemoryStickIcon,
  network: NetworkIcon,
  dependency: LinkIcon,
};

const COVERAGE_LABEL: Record<Coverage, string | null> = {
  protected: "Protected",
  unprotected: "Not protected",
  excluded: "Not backed up",
  none: null,
};

const COVERAGE_CLASS: Record<Coverage, string> = {
  protected: "border-emerald-500 bg-emerald-50 dark:bg-emerald-950/40",
  unprotected: "border-amber-500 bg-amber-50 dark:bg-amber-950/40",
  excluded: "border-dashed border-muted-foreground/60 bg-muted/40",
  none: "border-border bg-card",
};

function ResourceNodeView({ data }: NodeProps<ResourceNode>) {
  const Icon = ICON[data.kind];
  const coverage = COVERAGE_LABEL[data.coverage];
  return (
    <div
      className={cn(
        "w-60 rounded-md border-2 px-2.5 py-1.5 text-left text-xs text-card-foreground shadow-sm",
        COVERAGE_CLASS[data.coverage],
        data.external && data.coverage === "none" && "border-dashed",
      )}
      data-kind={data.kind}
      data-coverage={data.coverage}
    >
      <Handle type="target" position={Position.Left} className="!size-1.5 !min-h-0 !min-w-0 !border-0 !bg-muted-foreground/60" />
      <div className="flex items-center gap-1.5">
        <Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono font-medium" title={data.label}>
          {data.label}
        </span>
      </div>
      {(data.detail || coverage || data.external) && (
        <div className="mt-0.5 truncate text-[11px] text-muted-foreground" title={data.detail}>
          {[coverage, data.external ? "external dependency" : null, data.detail].filter(Boolean).join(" · ")}
        </div>
      )}
      <Handle type="source" position={Position.Right} className="!size-1.5 !min-h-0 !min-w-0 !border-0 !bg-muted-foreground/60" />
    </div>
  );
}

const nodeTypes = { resource: ResourceNodeView };

const COLUMN_TITLES = ["Images", "Containers", "Storage", "Networks & dependencies"];

/**
 * Topology tab: services / containers with their images, volumes, bind
 * mounts, networks and external dependencies; storage coloured by whether
 * the latest recovery point contains it. A text version follows for screen
 * readers.
 */
export function TopologyGraph({ app, components }: { app: ApplicationDetail; components: ComponentCoverage[] }) {
  const { resolvedTheme } = useTheme();
  const topo = useMemo(() => buildTopology(app, components), [app, components]);
  const nodes: ResourceNode[] = useMemo(
    () =>
      topo.nodes.map((n) => ({
        id: n.id,
        type: "resource",
        position: { x: n.x, y: n.y },
        data: { kind: n.kind, label: n.label, detail: n.detail, coverage: n.coverage, external: n.external },
        draggable: false,
        connectable: false,
      })),
    [topo],
  );
  const edges: Edge[] = useMemo(
    () =>
      topo.edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        label: e.label,
        labelStyle: { fontSize: 10 },
        style: e.dashed ? { strokeDasharray: "4 4" } : undefined,
      })),
    [topo],
  );

  if (topo.nodes.length === 0) {
    return <p className="text-sm text-muted-foreground">No containers in the latest inventory: nothing to draw.</p>;
  }
  const height = Math.min(900, Math.max(360, 120 + topo.rows * ROW_HEIGHT));
  const byId = new Map(topo.nodes.map((n) => [n.id, n]));
  const containers = topo.nodes.filter((n) => n.kind === "container");

  return (
    <div className="space-y-3">
      <ul className="flex flex-wrap gap-3 text-xs text-muted-foreground" aria-label="Legend">
        <li className="inline-flex items-center gap-1.5">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm border-2 border-emerald-500" /> Protected (in the
          latest recovery point)
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm border-2 border-amber-500" /> Not protected
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm border-2 border-dashed border-muted-foreground/60" />{" "}
          Never backed up (external, ephemeral, tmpfs or excluded)
        </li>
      </ul>
      <div
        className="rounded-lg border"
        style={{ height }}
        role="figure"
        aria-label={`Topology of ${app.display_name || app.name}: ${containers.length} containers. A text version follows.`}
        data-testid="topology-graph"
      >
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.15 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          colorMode={resolvedTheme === "dark" ? "dark" : "light"}
          minZoom={0.2}
        >
          <Background gap={24} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      <div className="sr-only">
        <h3>Topology as text</h3>
        <p>Columns: {COLUMN_TITLES.join(", ")}.</p>
        <ul>
          {containers.map((c) => (
            <li key={c.id}>
              Container {c.label}
              {c.detail ? ` (${c.detail})` : ""}:{" "}
              {topo.edges
                .filter((e) => e.source === c.id || e.target === c.id)
                .map((e) => {
                  const other = byId.get(e.source === c.id ? e.target : e.source);
                  if (!other) return "";
                  const cov = COVERAGE_LABEL[other.coverage];
                  return `${other.kind} ${other.label}${e.label ? ` at ${e.label}` : ""}${cov ? `, ${cov.toLowerCase()}` : ""}`;
                })
                .filter(Boolean)
                .join("; ")}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

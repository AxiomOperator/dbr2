// SPDX-License-Identifier: Apache-2.0
//
// Builds the application topology graph (Topology tab): images → containers
// → storage (volumes, bind mounts, tmpfs) and networks / external
// dependencies, laid out in four fixed columns (no layout library; readable
// for 1–20 containers). Storage is coloured by the API's protection coverage
// (`protection.components`: `volume:<name>`, `bind:<source>`).

import type { ApplicationDetail, ComponentCoverage } from "@/lib/api/fleet-schemas";

export type TopologyKind = "image" | "container" | "volume" | "bind" | "tmpfs" | "network" | "dependency";

/**
 * protected: in the latest recovery point; unprotected: a backup component
 * that is not (yet) in it; excluded: never captured (external / ephemeral
 * volume, tmpfs, excluded component); none: not storage.
 */
export type Coverage = "protected" | "unprotected" | "excluded" | "none";

export interface TopologyNode {
  id: string;
  kind: TopologyKind;
  label: string;
  detail?: string;
  coverage: Coverage;
  /** An external dependency DBR² does not protect (external network, network storage…). */
  external?: boolean;
  x: number;
  y: number;
}

export interface TopologyEdge {
  id: string;
  source: string;
  target: string;
  /** Mount destination, e.g. `/var/lib/postgresql/data (rw)`. */
  label?: string;
  dashed?: boolean;
}

export interface Topology {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  /** Rows of the tallest column (for sizing the canvas). */
  rows: number;
}

export const COLUMN_X: Record<TopologyKind, number> = {
  image: 0,
  container: 300,
  volume: 620,
  bind: 620,
  tmpfs: 620,
  network: 940,
  dependency: 940,
};
export const ROW_HEIGHT = 84;

const COLUMNS: TopologyKind[][] = [["image"], ["container"], ["volume", "bind", "tmpfs"], ["network", "dependency"]];

export function buildTopology(app: ApplicationDetail, components: ComponentCoverage[] = []): Topology {
  const a = app.analysis;
  const nodes = new Map<string, Omit<TopologyNode, "x" | "y">>();
  const edges = new Map<string, TopologyEdge>();
  const coverageByName = new Map(components.map((c) => [c.name, c]));
  const coverageOf = (component: string): Coverage => {
    const c = coverageByName.get(component);
    if (!c) return "excluded";
    return c.protected ? "protected" : "unprotected";
  };
  const serviceOf = new Map<string, string>();
  for (const s of a?.services ?? []) for (const c of s.containers) serviceOf.set(c.name.replace(/^\//, ""), s.name);
  const volumeInfo = new Map((a?.volumes ?? []).map((v) => [v.name, v]));
  const networkInfo = new Map((a?.networks ?? []).map((n) => [n.name, n]));
  const deps = a?.dependencies ?? [];
  const externalNames = new Set(
    deps.filter((d) => d.kind === "external_network" || d.kind === "network_storage").map((d) => d.name),
  );

  const addNode = (n: Omit<TopologyNode, "x" | "y">) => {
    if (!nodes.has(n.id)) nodes.set(n.id, n);
  };
  const addEdge = (e: TopologyEdge) => {
    if (!edges.has(e.id)) edges.set(e.id, e);
  };

  for (const c of app.containers_detail) {
    const name = c.name.replace(/^\//, "");
    const cid = `container:${name}`;
    const svc = serviceOf.get(name);
    addNode({
      id: cid,
      kind: "container",
      label: name,
      detail: [svc && svc !== name ? `service ${svc}` : null, c.state].filter(Boolean).join(" · "),
      coverage: "none",
    });
    if (c.image) {
      const iid = `image:${c.image}`;
      addNode({ id: iid, kind: "image", label: c.image, coverage: "none" });
      addEdge({ id: `${iid}->${cid}`, source: iid, target: cid });
    }
    for (const m of c.mounts) {
      const mode = m.rw ? "rw" : "ro";
      if (m.type === "volume" && m.name) {
        const info = volumeInfo.get(m.name);
        const vid = `volume:${m.name}`;
        const cls = info?.class ?? "local";
        addNode({
          id: vid,
          kind: "volume",
          label: info?.anonymous ? `${m.name.slice(0, 12)}… (anonymous)` : m.name,
          detail: `volume · ${cls}`,
          coverage: coverageOf(vid),
          external: externalNames.has(m.name) || cls === "external",
        });
        addEdge({ id: `${cid}->${vid}`, source: cid, target: vid, label: `${m.destination} (${mode})` });
      } else if (m.type === "bind" && m.source) {
        const bid = `bind:${m.source}`;
        addNode({ id: bid, kind: "bind", label: m.source, detail: "bind mount", coverage: coverageOf(bid) });
        addEdge({ id: `${cid}->${bid}`, source: cid, target: bid, label: `${m.destination} (${mode})` });
      } else if (m.type === "tmpfs") {
        const tid = `tmpfs:${name}:${m.destination}`;
        addNode({ id: tid, kind: "tmpfs", label: m.destination, detail: "tmpfs (memory, never backed up)", coverage: "excluded" });
        addEdge({ id: `${cid}->${tid}`, source: cid, target: tid, dashed: true });
      }
    }
    for (const net of c.networks) {
      const info = networkInfo.get(net);
      const nid = `network:${net}`;
      addNode({
        id: nid,
        kind: "network",
        label: net,
        detail: `network${info?.driver ? ` · ${info.driver}` : ""}${info?.external ? " · external" : ""}`,
        coverage: "none",
        external: Boolean(info?.external) || externalNames.has(net),
      });
      addEdge({ id: `${cid}->${nid}`, source: cid, target: nid, dashed: true });
    }
  }

  // External dependencies without a node of their own (e.g. a database host).
  for (const d of deps) {
    if (nodes.has(`network:${d.name}`) || nodes.has(`volume:${d.name}`)) continue;
    addNode({
      id: `dependency:${d.kind}:${d.name}`,
      kind: "dependency",
      label: d.name,
      detail: `${d.kind.replace(/_/g, " ")}: ${d.detail}`,
      coverage: "none",
      external: true,
    });
  }

  // Layout: fixed columns, rows centred against the tallest column.
  const all = [...nodes.values()];
  const columns = COLUMNS.map((kinds) => all.filter((n) => kinds.includes(n.kind)));
  const rows = Math.max(1, ...columns.map((c) => c.length));
  const placed: TopologyNode[] = [];
  for (const col of columns) {
    const offset = ((rows - col.length) * ROW_HEIGHT) / 2;
    col.forEach((n, i) => placed.push({ ...n, x: COLUMN_X[n.kind], y: offset + i * ROW_HEIGHT }));
  }
  return { nodes: placed, edges: [...edges.values()], rows };
}

// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { ApplicationDetailSchema } from "@/lib/api/fleet-schemas";
import { buildTopology, COLUMN_X, ROW_HEIGHT } from "@/lib/topology";

const detail = ApplicationDetailSchema.parse({
  id: "app",
  name: "shop",
  display_name: null,
  kind: "compose",
  host_id: "h",
  hostname: "docker-prod-01",
  source: "original",
  services: 2,
  containers: 2,
  volumes: 2,
  bind_mounts: 1,
  unprotected_high: 0,
  dependencies: 2,
  secrets_count: 0,
  owner: null,
  environment: null,
  criticality: null,
  last_seen_at: "2026-09-25T10:00:00Z",
  missing_since: null,
  policy_id: null,
  collected_at: "2026-09-25T10:00:00Z",
  analysis: {
    key: "compose:shop",
    kind: "compose",
    name: "shop",
    source: "original",
    services: [
      { name: "web", image: "web:1", containers: [{ id: "c1", name: "shop-web-1", state: "running" }] },
      { name: "db", image: "postgres:18", containers: [{ id: "c2", name: "shop-db-1", state: "running" }] },
    ],
    volumes: [
      { name: "shop_pgdata", driver: "local", mountpoint: "/v/pg", class: "local", used_by: [], protected_by_default: true },
      { name: "shop_media", driver: "local", mountpoint: "/v/m", class: "external", used_by: [], protected_by_default: false },
    ],
    bind_mounts: [{ container: "shop-web-1", source: "/srv/shop/uploads", destination: "/app/uploads", rw: true }],
    tmpfs: null,
    networks: [
      { name: "shop_default", driver: "bridge" },
      { name: "proxy", driver: "bridge", external: true },
    ],
    images: null,
    dependencies: [
      { kind: "external_network", name: "proxy", detail: "must exist" },
      { kind: "external_service", name: "smtp.example.com", detail: "SMTP relay" },
    ],
    unprotected: null,
    secrets_count: 0,
    containers: ["shop-web-1", "shop-db-1"],
  },
  containers_detail: [
    {
      id: "c1",
      name: "shop-web-1",
      image: "web:1",
      state: "running",
      env: null,
      ports: null,
      networks: ["shop_default", "proxy"],
      mounts: [
        { type: "bind", source: "/srv/shop/uploads", destination: "/app/uploads", rw: true },
        { type: "volume", name: "shop_media", destination: "/app/media", rw: true },
        { type: "tmpfs", destination: "/tmp", rw: true },
      ],
    },
    {
      id: "c2",
      name: "shop-db-1",
      image: "postgres:18",
      state: "running",
      env: null,
      ports: null,
      networks: ["shop_default"],
      mounts: [{ type: "volume", name: "shop_pgdata", destination: "/var/lib/postgresql/data", rw: true }],
    },
  ],
});

const coverage = [
  { name: "config", kind: "config" as const, required: true, protected: true, last_size_bytes: 1 },
  { name: "volume:shop_pgdata", kind: "volume" as const, required: true, protected: true, last_size_bytes: 10 },
  { name: "bind:/srv/shop/uploads", kind: "bind_mount" as const, required: false, protected: false, last_size_bytes: 0 },
];

describe("buildTopology", () => {
  const t = buildTopology(detail, coverage);
  const node = (id: string) => t.nodes.find((n) => n.id === id)!;

  it("draws images → containers → storage and networks / dependencies", () => {
    expect(t.nodes.filter((n) => n.kind === "container").map((n) => n.label)).toEqual(["shop-web-1", "shop-db-1"]);
    expect(t.edges.map((e) => e.id)).toEqual(
      expect.arrayContaining([
        "image:web:1->container:shop-web-1",
        "container:shop-web-1->bind:/srv/shop/uploads",
        "container:shop-web-1->volume:shop_media",
        "container:shop-db-1->volume:shop_pgdata",
        "container:shop-web-1->network:proxy",
      ]),
    );
    expect(node("container:shop-db-1").detail).toBe("service db · running");
    expect(t.edges.find((e) => e.target === "volume:shop_pgdata")?.label).toBe("/var/lib/postgresql/data (rw)");
  });

  it("colours storage by the API's coverage", () => {
    expect(node("volume:shop_pgdata").coverage).toBe("protected");
    expect(node("bind:/srv/shop/uploads").coverage).toBe("unprotected");
    expect(node("volume:shop_media").coverage).toBe("excluded");
    expect(node("volume:shop_media").external).toBe(true);
    expect(t.nodes.find((n) => n.kind === "tmpfs")?.coverage).toBe("excluded");
  });

  it("marks external networks and adds dependencies without a node of their own", () => {
    expect(node("network:proxy").external).toBe(true);
    expect(node("network:shop_default").external).toBe(false);
    expect(node("dependency:external_service:smtp.example.com").detail).toContain("SMTP relay");
    expect(t.nodes.some((n) => n.id === "dependency:external_network:proxy")).toBe(false);
  });

  it("lays nodes out in four fixed columns, centred on the tallest", () => {
    expect(new Set(t.nodes.map((n) => n.x))).toEqual(new Set([COLUMN_X.image, COLUMN_X.container, COLUMN_X.volume, COLUMN_X.network]));
    const storage = t.nodes.filter((n) => n.x === COLUMN_X.volume);
    expect(t.rows).toBe(storage.length);
    expect(storage.map((n) => n.y)).toEqual(storage.map((_, i) => i * ROW_HEIGHT));
    const containers = t.nodes.filter((n) => n.kind === "container");
    expect(containers[0]!.y).toBeGreaterThan(0);
  });

  it("stays empty for an application without containers", () => {
    expect(buildTopology({ ...detail, containers_detail: [] }, []).nodes.filter((n) => n.kind !== "dependency")).toEqual([]);
  });
});

// SPDX-License-Identifier: Apache-2.0
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DatabasesCard, describeDatabase, nestComponents, TopologyCard } from "@/components/backups/manifest-sections";
import { hostUsageRows, totalCapacity } from "@/components/repositories/usage-view";
import { ManifestSchema, RepositoryListSchema } from "@/lib/api/protection-schemas";
import { REPO_AWAITING, REPO_READY, REPO_UNAVAILABLE } from "@/test/protection-fixtures";
import { renderWithQuery } from "@/test/render";

const c = (name: string, over: Record<string, unknown> = {}) => ({ name, kind: "volume", status: "succeeded", ...over });

describe("nestComponents", () => {
  it("puts each fsmeta record right under its parent and keeps orphans", () => {
    const m = ManifestSchema.parse({
      schema_version: 1,
      components: [
        c("fsmeta:volume:a", { kind: "fsmeta", parent: "volume:a" }),
        c("config", { kind: "config" }),
        c("volume:a"),
        c("bind:/srv", { kind: "bind_mount" }),
        c("fsmeta:bind:/srv", { kind: "fsmeta", parent: "bind:/srv" }),
        c("fsmeta:volume:gone", { kind: "fsmeta", parent: "volume:gone" }),
      ],
    });
    expect(nestComponents(m.components).map((n) => [n.component.name, n.depth])).toEqual([
      ["config", 0],
      ["volume:a", 0],
      ["fsmeta:volume:a", 1],
      ["bind:/srv", 0],
      ["fsmeta:bind:/srv", 1],
      ["fsmeta:volume:gone", 0],
    ]);
  });
});

describe("manifest sections", () => {
  it("describes database dumps", () => {
    expect(describeDatabase({ engine: "postgresql", format: "pg_dumpall-sql-zstd", service: "db", container: "shop-db-1" })).toBe(
      "PostgreSQL dump (pg_dumpall-sql-zstd) of service db, container shop-db-1",
    );
    const m = ManifestSchema.parse({
      schema_version: 1,
      components: [
        c("database:cache", { kind: "database", database: { engine: "redis", format: "rdb", container: "shop-cache-1" } }),
        c("database:db", {
          kind: "database",
          database: { engine: "postgresql", format: "pg_dumpall-sql-zstd", service: "db" },
          validation: "pg_dumpall exit 0; zstd frame verified",
        }),
      ],
    });
    renderWithQuery(<DatabasesCard components={m.components} />);
    const table = screen.getByRole("table", { name: "Database dumps" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(within(rows[0]!).getByText("Redis")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("shop-cache-1")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("Not recorded")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("PostgreSQL")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("pg_dumpall-sql-zstd")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("service db")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("pg_dumpall exit 0; zstd frame verified")).toBeInTheDocument();
  });

  it("explains when a recovery point has no dumps", () => {
    renderWithQuery(<DatabasesCard components={[]} />);
    expect(screen.getByText(/database strategy is “logical” or “both”/)).toBeInTheDocument();
  });

  it("shows the topology at capture time, or says it was not recorded", () => {
    const m = ManifestSchema.parse({
      schema_version: 1,
      components: [],
      images: [{ service: "web", ref: "web:1", digest: "web@sha256:0123456789abcdef0123456789abcdef" }, { ref: "worker:dev" }],
      topology: {
        containers: [
          {
            id: "c1",
            name: "shop-web-1",
            service: "web",
            image: "web:1",
            state: "running",
            ports: [{ container_port: "3000", protocol: "tcp", host_port: "8080" }],
            mounts: [{ type: "volume", name: "media", destination: "/app/media", rw: true }],
            networks: ["shop_default"],
          },
        ],
        networks: [{ name: "proxy", driver: "bridge", external: true }],
        volumes: [{ name: "media", driver: "local" }],
      },
    });
    renderWithQuery(<TopologyCard manifest={m} />);
    const table = screen.getByRole("table", { name: "Containers at capture time" });
    expect(within(table).getByText("8080→3000/tcp")).toBeInTheDocument();
    expect(within(table).getByText("volume media → /app/media (rw)")).toBeInTheDocument();
    expect(screen.getByText(/external \(must exist before a restore\)/)).toBeInTheDocument();
    expect(screen.getByText(/no registry digest/)).toBeInTheDocument();

    renderWithQuery(<TopologyCard manifest={ManifestSchema.parse({ schema_version: 1, components: [] })} />);
    expect(screen.getByText(/records no topology/)).toBeInTheDocument();
  });
});

describe("usage", () => {
  const repos = RepositoryListSchema.parse({ items: [REPO_READY, REPO_AWAITING, REPO_UNAVAILABLE] }).items;

  it("lists per-host logical usage, largest first", () => {
    const rows = hostUsageRows(repos);
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.map((r) => r.latestBytes)).toEqual([...rows.map((r) => r.latestBytes)].sort((a, b) => b - a));
    expect(rows[0]?.repositoryName).toBe(REPO_READY.name);
  });

  it("sums the capacity of reachable Repositories and counts unreachable ones", () => {
    const cap = totalCapacity(repos);
    expect(cap.unreachable).toBe(1);
    expect(cap.total).toBeGreaterThan(cap.used);
  });
});

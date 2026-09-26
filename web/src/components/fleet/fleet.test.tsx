// SPDX-License-Identifier: Apache-2.0
import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { ALL } from "@/components/common/filters";
import { ContainersView, filterContainers, NO_APPLICATION } from "@/components/fleet/containers-view";
import { filterVolumes, VolumesView } from "@/components/fleet/volumes-view";
import { hostRows, HostsView } from "@/components/hosts/hosts-view";
import { AgentSchema, FleetContainerListSchema, FleetVolumeListSchema } from "@/lib/api/fleet-schemas";
import { AGENT_ACTIVE, AGENT_PENDING, APP_SUMMARY, meWith } from "@/test/fleet-fixtures";
import { CONTAINERS, HOST_EDGE, HOST_PROD, VOLUMES } from "@/test/phase6-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const nav = vi.hoisted(() => ({ replace: vi.fn(), search: "" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace }),
  usePathname: () => "/containers",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

beforeEach(() => {
  nav.replace.mockReset();
  nav.search = "";
});

const containers = FleetContainerListSchema.parse({ items: CONTAINERS }).items;
const volumes = FleetVolumeListSchema.parse({ items: VOLUMES }).items;

describe("fleet schemas", () => {
  it("normalise null ports / networks / used_by to []", () => {
    expect(containers[1]?.ports).toEqual([]);
    expect(containers[1]?.networks).toEqual([]);
    expect(volumes[2]?.used_by).toEqual([]);
  });
});

describe("filterContainers", () => {
  const f = { host: ALL, application: ALL, state: ALL };
  it("filters by host, application (or none) and state", () => {
    expect(filterContainers(containers, f)).toHaveLength(3);
    expect(filterContainers(containers, { ...f, host: HOST_EDGE }).map((c) => c.name)).toEqual(["metrics-agent"]);
    expect(filterContainers(containers, { ...f, application: NO_APPLICATION }).map((c) => c.name)).toEqual([
      "db-migrate-oneoff",
    ]);
    expect(filterContainers(containers, { ...f, state: "running" })).toHaveLength(2);
  });
});

describe("filterVolumes", () => {
  const f = { host: ALL, application: ALL, class: ALL, protected: ALL };
  it("filters by class and protection", () => {
    expect(filterVolumes(volumes, { ...f, class: "unused" }).map((v) => v.name)).toEqual(["shop_pgdata_old"]);
    expect(filterVolumes(volumes, { ...f, protected: "yes" }).map((v) => v.name)).toEqual(["shop_pgdata"]);
    expect(filterVolumes(volumes, { ...f, protected: "no" })).toHaveLength(2);
    expect(filterVolumes(volumes, { ...f, application: NO_APPLICATION })).toHaveLength(1);
  });
});

describe("hostRows", () => {
  it("counts containers, running containers, volumes and applications per approved host", () => {
    const agents = [AgentSchema.parse({ ...AGENT_ACTIVE, id: HOST_PROD, hostname: "docker-prod-01" }), AgentSchema.parse(AGENT_PENDING)];
    const rows = hostRows(agents, containers, volumes, [{ host_id: HOST_PROD, missing_since: null }]);
    expect(rows).toHaveLength(1); // pending hosts have no inventory
    expect(rows[0]).toMatchObject({ applications: 1, containers: 2, running: 1, volumes: 3, protectedVolumes: 1 });
    expect(hostRows(agents, [], [], null)[0]?.applications).toBeNull();
  });
});

function withUser(perms: string[], ui: React.ReactNode) {
  return renderWithQuery(<CurrentUserProvider me={meWith(perms)}>{ui}</CurrentUserProvider>);
}

describe("ContainersView", () => {
  it("lists containers with links to their host and application, filtered from the URL", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/containers": () => Response.json({ items: CONTAINERS }) })));
    nav.search = "state=running";
    withUser(["host.read", "application.read"], <ContainersView />);
    const table = await screen.findByRole("table", { name: "Containers" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByRole("link", { name: "Web shop" })).toHaveAttribute("href", `/applications/${CONTAINERS[0]!.application_id}`);
    expect(within(rows[0]!).getByRole("link", { name: "docker-prod-01" })).toHaveAttribute("href", `/hosts/${HOST_PROD}`);
    expect(within(rows[0]!).getByText("8080→3000/tcp")).toBeInTheDocument();
    expect(screen.getByText(/2 of 3 containers shown/)).toBeInTheDocument();
  });

  it("requires host.read", () => {
    withUser(["application.read"], <ContainersView />);
    expect(screen.getByText("Access denied")).toBeInTheDocument();
  });
});

describe("VolumesView", () => {
  it("shows class, protection and last size", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/volumes": () => Response.json({ items: VOLUMES }) })));
    withUser(["host.read"], <VolumesView />);
    const table = await screen.findByRole("table", { name: "Volumes" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    expect(within(rows[0]!).getByText("Protected")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("12 GiB")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("External")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("Unused")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("No container")).toBeInTheDocument();
    // Without application.read the application is shown as text only.
    expect(within(rows[0]!).queryByRole("link", { name: "Web shop" })).toBeNull();
  });
});

describe("HostsView", () => {
  it("shows the host inventory with counts and links to Agents for the lifecycle", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/agents": () =>
            Response.json({ items: [{ ...AGENT_ACTIVE, id: HOST_PROD, hostname: "docker-prod-01" }, AGENT_PENDING] }),
          "GET /api/v1/containers": () => Response.json({ items: CONTAINERS }),
          "GET /api/v1/volumes": () => Response.json({ items: VOLUMES }),
          "GET /api/v1/applications": () => Response.json({ items: [{ ...APP_SUMMARY, host_id: HOST_PROD }] }),
        }),
      ),
    );
    withUser(["host.read", "application.read"], <HostsView />);
    const row = (await screen.findByRole("link", { name: "docker-prod-01" })).closest("tr")!;
    expect(within(row).getByText("29.8.1", { exact: false })).toBeInTheDocument();
    expect(await within(row).findByText("(1 running)")).toBeInTheDocument();
    expect(within(row).getByText("(1 protected)")).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "1" })).toHaveAttribute("href", `/applications?host=${HOST_PROD}`);
    expect(screen.getByText(/1 pending or revoked agent is listed under/)).toBeInTheDocument();
    expect(screen.queryByText("build-runner-03")).toBeNull();
  });
});

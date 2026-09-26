// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { RecoveryPointDetail } from "@/components/backups/recovery-point-detail";
import { RestoreStateBadge } from "@/components/restores/restore-badges";
import { RestoreDetail } from "@/components/restores/restore-detail";
import { RestorePreviewView } from "@/components/restores/restore-preview";
import { RemapEditor, RestoreWizard, startRestoreErrorMessage } from "@/components/restores/restore-wizard";
import {
  ApplicationRestores,
  parseRestoreFilters,
  RecentRestoresCard,
  RestoresView,
} from "@/components/restores/restores-view";
import { ApiError } from "@/lib/api/client";
import { PreviewSchema } from "@/lib/api/restore-schemas";
import type { RemapRow } from "@/lib/restore";
import { meWith } from "@/test/fleet-fixtures";
import { APP_ID, HOST_ID, RP_FAILED, RP_ID } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";
import {
  AGENTS,
  PREVIEW_COLLISION,
  PREVIEW_DR,
  PREVIEW_PROD,
  RP_WITH_MANIFEST,
  RS_FAILED_ID,
  RS_ID,
  RS_ROLLED_BACK_ID,
  RUN_FAILED,
  RUN_ROLLED_BACK,
  RUN_RUNNING,
  RUN_SUCCEEDED,
} from "@/test/restore-fixtures";

const nav = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn(), search: "" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: nav.push, replace: nav.replace }),
  usePathname: () => "/restores",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

beforeEach(() => {
  nav.push.mockReset();
  nav.replace.mockReset();
  nav.search = "";
});

const ALL = [
  "application.read",
  "backup.read",
  "host.read",
  "restore.read",
  "restore.execute",
  "restore.production",
];

function problem(status: number, code: string, detail: string) {
  return new Response(JSON.stringify({ title: "Error", status, detail, code }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });
}

function withUser(permissions: string[], ui: React.ReactElement) {
  return renderWithQuery(<CurrentUserProvider me={meWith(permissions)}>{ui}</CurrentUserProvider>);
}

describe("state badges", () => {
  it("colours restore states: running blue with the step, succeeded green, rolled back amber, failed red", () => {
    const { rerender } = renderWithQuery(<RestoreStateBadge state="running" step="restore-data" />);
    const running = screen.getByText(/Running/);
    expect(running).toHaveTextContent("Running: Restore data");
    expect(running).toHaveClass("text-sky-700");
    rerender(<RestoreStateBadge state="succeeded" />);
    expect(screen.getByText("Succeeded")).toHaveClass("text-emerald-700");
    rerender(<RestoreStateBadge state="rolled_back" step="health-check" />);
    expect(screen.getByText("Rolled back")).toHaveClass("text-amber-700");
    rerender(<RestoreStateBadge state="failed" />);
    expect(screen.getByText("Failed")).toHaveAttribute("data-variant", "destructive");
    rerender(<RestoreStateBadge state="requested" />);
    expect(screen.getByText("Requested")).toBeInTheDocument();
  });

  it("explains start failures", () => {
    expect(startRestoreErrorMessage(new ApiError({ status: 409, code: "conflict", detail: "blocked" }))).toMatch(
      /^blocked\. The restore is blocked by a collision, or another backup or restore is running/,
    );
    expect(startRestoreErrorMessage(new ApiError({ status: 403, code: "forbidden", detail: "needs restore.production" }))).toBe(
      "You do not have permission to start this restore. (needs restore.production)",
    );
  });
});

describe("remap editor", () => {
  function Harness({ initial, suggestions, onRows }: { initial: RemapRow[]; suggestions?: RemapRow[]; onRows: (r: RemapRow[]) => void }) {
    const [rows, setRows] = useState(initial);
    return (
      <RemapEditor
        rows={rows}
        suggestions={suggestions}
        errors={{}}
        onChange={(r) => {
          setRows(r);
          onRows(r);
        }}
      />
    );
  }

  it("adds, edits and removes rows and applies suggestions", async () => {
    const seen: RemapRow[][] = [];
    const user = userEvent.setup();
    renderWithQuery(
      <Harness initial={[]} suggestions={[{ from: "/srv/shop", to: "/srv/shop-restored" }]} onRows={(r) => seen.push(r)} />,
    );
    expect(screen.getByText(/No remaps/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add remap" }));
    await user.type(screen.getByLabelText("Remap 1: from"), "/data");
    await user.type(screen.getByLabelText("Remap 1: to"), "/mnt/data");
    expect(seen.at(-1)).toEqual([{ from: "/data", to: "/mnt/data" }]);
    await user.click(screen.getByRole("button", { name: "Use suggested remaps" }));
    expect(screen.getByLabelText("Remap 1: from")).toHaveValue("/srv/shop");
    expect(screen.getByLabelText("Remap 1: to")).toHaveValue("/srv/shop-restored");
    await user.click(screen.getByRole("button", { name: "Remove remap 1" }));
    expect(seen.at(-1)).toEqual([]);
  });

  it("shows row errors", () => {
    renderWithQuery(
      <RemapEditor rows={[{ from: "srv", to: "/x" }]} errors={{ 0: "Both paths must be absolute (start with /)." }} onChange={() => {}} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Remap 1: Both paths must be absolute");
    expect(screen.getByLabelText("Remap 1: from")).toHaveAttribute("aria-invalid", "true");
  });
});

describe("impact preview", () => {
  it("shows the production banner, mode, data actions, containers, networks, images and ports", () => {
    renderWithQuery(<RestorePreviewView preview={PreviewSchema.parse(PREVIEW_PROD)} />);
    const banner = screen.getByTestId("production-banner");
    expect(banner).toHaveTextContent("Production restore");
    expect(banner).toHaveTextContent("the target application is tagged environment: production");
    expect(screen.getByText("In place")).toBeInTheDocument();
    const data = screen.getByRole("table", { name: "Data" });
    const rows = within(data).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-action"))).toEqual(["restore_files", "overwrite"]);
    expect(within(rows[0]!).getByText("Restore files")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Overwrite")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("12 GiB")).toBeInTheDocument();
    expect(within(screen.getByRole("list", { name: "Containers to stop" })).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByRole("list", { name: "Networks" })).toHaveTextContent("shop_defaultExists");
    expect(screen.getByRole("list", { name: "Published ports" })).toHaveTextContent("8080/tcp");
    expect(within(screen.getByRole("table", { name: "Images" })).getAllByText("Present")).toHaveLength(2);
    expect(screen.queryByRole("region", { name: "Collisions" })).not.toBeInTheDocument();
  });

  it("lists collisions (blocking) and warnings for an alternate host", () => {
    renderWithQuery(<RestorePreviewView preview={PreviewSchema.parse(PREVIEW_COLLISION)} />);
    expect(screen.getByText("Alternate host")).toBeInTheDocument();
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    const collisions = screen.getByRole("region", { name: "Collisions" });
    expect(within(collisions).getByText("2 collisions: the restore is blocked")).toBeInTheDocument();
    const items = within(collisions).getAllByRole("listitem");
    expect(items.map((i) => i.getAttribute("data-collision-kind"))).toEqual(["container_name", "port"]);
    expect(items[1]).toHaveTextContent("Port8080/tcpalready published by container staging-proxy");
    expect(within(screen.getByRole("region", { name: "Warnings" })).getByText(/3 hours old/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Containers to re-create" })).toHaveTextContent("shop-db-1");
    expect(within(screen.getByRole("table", { name: "Images" })).getByText("Pull by digest")).toBeInTheDocument();
    expect(screen.queryByTestId("production-banner")).not.toBeInTheDocument();
  });
});

describe("restore wizard", () => {
  function stub(opts: {
    preview?: unknown;
    onPreview?: (body: unknown) => void;
    start?: (body: unknown) => Response;
    rp?: unknown;
  }) {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () => Response.json(opts.rp ?? RP_WITH_MANIFEST),
          "GET /api/v1/agents": () => Response.json({ items: AGENTS }),
          [`POST /api/v1/recovery-points/${RP_ID}/restore-preview`]: (init) => {
            opts.onPreview?.(JSON.parse(String(init?.body)));
            return Response.json(opts.preview ?? PREVIEW_PROD);
          },
          [`POST /api/v1/recovery-points/${RP_ID}/restores`]: (init) =>
            opts.start ? opts.start(JSON.parse(String(init?.body))) : Response.json(RUN_RUNNING, { status: 202 }),
        }),
      ),
    );
  }

  it("walks target → preview → confirm and starts a production restore only after the typed confirmation", async () => {
    const previews: unknown[] = [];
    const starts: unknown[] = [];
    stub({
      onPreview: (b) => previews.push(b),
      start: (b) => {
        starts.push(b);
        return Response.json(RUN_RUNNING, { status: 202 });
      },
    });
    const user = userEvent.setup();
    withUser(ALL, <RestoreWizard recoveryPointId={RP_ID} />);

    // Step 1: all restorable components, fsmeta automatic, the failed bind mount disabled.
    const list = await screen.findByRole("list", { name: "Components to restore" });
    expect(screen.getByRole("checkbox", { name: "Restore config" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Restore volume:shop_pgdata" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Restore bind:/srv/shop/uploads" })).toBeDisabled();
    expect(within(list).getByText(/included automatically/)).toHaveTextContent("+ fsmeta:volume:shop_pgdata included automatically");
    expect(screen.getByRole("combobox", { name: "Target host" })).toHaveTextContent("docker-prod-01 (source host: restore in place)");
    expect(screen.getByRole("listitem", { current: "step" })).toHaveTextContent("1. Target");

    // Step 2: the preview for the default selection.
    await user.click(screen.getByRole("button", { name: "Preview impact" }));
    expect(await screen.findByTestId("production-banner")).toBeInTheDocument();
    expect(previews).toEqual([{ target_host_id: HOST_ID, components: ["config", "volume:shop_pgdata"] }]);

    // Step 3: Start stays disabled until reason and exact name are given.
    await user.click(screen.getByRole("button", { name: "Continue" }));
    const start = await screen.findByRole("button", { name: "Start restore" });
    expect(screen.getByText(/restored to a staging location first and verified/)).toBeInTheDocument();
    expect(screen.getByText("(required)")).toBeInTheDocument();
    expect(start).toBeDisabled();
    await user.type(screen.getByLabelText(/^Reason/), "INC-48391");
    const confirm = screen.getByLabelText("Type Web shop to confirm");
    await user.type(confirm, "web shop");
    expect(start).toBeDisabled();
    expect(screen.getByRole("list", { name: "Before you can start" })).toHaveTextContent("Type Web shop exactly to confirm.");
    await user.clear(confirm);
    await user.type(confirm, "Web shop");
    expect(start).toBeEnabled();
    await user.click(start);

    await waitFor(() => expect(nav.push).toHaveBeenCalledWith(`/restores/${RS_ID}`));
    expect(starts).toEqual([
      {
        target_host_id: HOST_ID,
        components: ["config", "volume:shop_pgdata"],
        reason: "INC-48391",
        confirmation: "Web shop",
      },
    ]);
  });

  it("re-runs the preview when the selection changes and requires at least one component", async () => {
    const previews: { components?: string[] }[] = [];
    stub({ onPreview: (b) => previews.push(b as { components?: string[] }), preview: PREVIEW_DR });
    const user = userEvent.setup();
    withUser(ALL, <RestoreWizard recoveryPointId={RP_ID} />);
    const config = await screen.findByRole("checkbox", { name: "Restore config" });
    const volume = screen.getByRole("checkbox", { name: "Restore volume:shop_pgdata" });
    await user.click(config);
    await user.click(volume);
    expect(screen.getByText("Select at least one component.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview impact" })).toBeDisabled();
    await user.click(volume);
    await user.click(screen.getByRole("button", { name: "Preview impact" }));
    await screen.findByTestId("restore-preview");
    await user.click(screen.getByRole("button", { name: "Back" }));
    await user.click(screen.getByRole("checkbox", { name: "Restore config" }));
    await user.click(screen.getByRole("button", { name: "Preview impact" }));
    await waitFor(() => expect(previews).toHaveLength(2));
    expect(previews.map((p) => p.components)).toEqual([["volume:shop_pgdata"], ["config", "volume:shop_pgdata"]]);
  });

  it("sends valid path remaps and blocks the preview on invalid ones", async () => {
    const previews: unknown[] = [];
    stub({ onPreview: (b) => previews.push(b), preview: PREVIEW_DR });
    const user = userEvent.setup();
    withUser(ALL, <RestoreWizard recoveryPointId={RP_ID} />);
    await user.click(await screen.findByRole("button", { name: "Add remap" }));
    await user.type(screen.getByLabelText("Remap 1: from"), "/srv/shop");
    await user.type(screen.getByLabelText("Remap 1: to"), "relative");
    expect(screen.getByRole("alert")).toHaveTextContent("Both paths must be absolute");
    expect(screen.getByRole("button", { name: "Preview impact" })).toBeDisabled();
    await user.clear(screen.getByLabelText("Remap 1: to"));
    await user.type(screen.getByLabelText("Remap 1: to"), "/srv/shop-restored");
    await user.click(screen.getByRole("button", { name: "Preview impact" }));
    await screen.findByTestId("restore-preview");
    expect(previews).toEqual([
      {
        target_host_id: HOST_ID,
        components: ["config", "volume:shop_pgdata"],
        path_remaps: [{ from: "/srv/shop", to: "/srv/shop-restored" }],
      },
    ]);
  });

  it("does not continue past a preview with collisions", async () => {
    stub({ preview: PREVIEW_COLLISION });
    const user = userEvent.setup();
    withUser(ALL, <RestoreWizard recoveryPointId={RP_ID} />);
    await user.click(await screen.findByRole("button", { name: "Preview impact" }));
    expect(await screen.findByRole("region", { name: "Collisions" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    expect(screen.getByText("Blocked: go back and change the target or remaps.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start restore" })).not.toBeInTheDocument();
  });

  it("explains and disables a production restore without restore.production", async () => {
    stub({});
    const user = userEvent.setup();
    withUser(ALL.filter((p) => p !== "restore.production"), <RestoreWizard recoveryPointId={RP_ID} />);
    await user.click(await screen.findByRole("button", { name: "Preview impact" }));
    await user.click(await screen.findByRole("button", { name: "Continue" }));
    expect(await screen.findByText("You cannot start this production restore")).toBeInTheDocument();
    await user.type(screen.getByLabelText(/^Reason/), "INC-1");
    await user.type(screen.getByLabelText("Type Web shop to confirm"), "Web shop");
    expect(screen.getByRole("button", { name: "Start restore" })).toBeDisabled();
  });

  it("starts a non-production restore without reason or confirmation and shows a 409", async () => {
    stub({ preview: PREVIEW_DR, start: () => problem(409, "conflict", "an operation is already running for this application") });
    const user = userEvent.setup();
    withUser(ALL.filter((p) => p !== "restore.production"), <RestoreWizard recoveryPointId={RP_ID} />);
    await user.click(await screen.findByRole("button", { name: "Preview impact" }));
    await user.click(await screen.findByRole("button", { name: "Continue" }));
    expect(screen.getByText("(optional)")).toBeInTheDocument();
    expect(screen.queryByLabelText(/to confirm/)).not.toBeInTheDocument();
    const start = screen.getByRole("button", { name: "Start restore" });
    expect(start).toBeEnabled();
    await user.click(start);
    expect(await screen.findByText("Could not start the restore")).toBeInTheDocument();
    expect(screen.getByText(/an operation is already running for this application\. The restore is blocked/)).toBeInTheDocument();
    expect(nav.push).not.toHaveBeenCalled();
  });

  it("refuses recovery points that are not committed and users without restore.execute", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/recovery-points/${RP_FAILED.id}`]: () => Response.json(RP_FAILED) })),
    );
    const { unmount } = withUser(ALL, <RestoreWizard recoveryPointId={RP_FAILED.id} />);
    expect(await screen.findByText("This recovery point cannot be restored")).toBeInTheDocument();
    unmount();
    withUser(["backup.read", "restore.read"], <RestoreWizard recoveryPointId={RP_ID} />);
    expect(screen.getByText("Access denied")).toBeInTheDocument();
    expect(screen.getByText("restore.execute")).toBeInTheDocument();
  });
});

describe("launch points", () => {
  it("offers Restore… on a committed recovery point with restore.execute only", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/recovery-points/${RP_ID}`]: () => Response.json(RP_WITH_MANIFEST) })),
    );
    const { unmount } = withUser(["backup.read", "restore.execute"], <RecoveryPointDetail id={RP_ID} />);
    expect(await screen.findByRole("link", { name: "Restore…" })).toHaveAttribute("href", `/recovery-points/${RP_ID}/restore`);
    unmount();
    withUser(["backup.read"], <RecoveryPointDetail id={RP_ID} />);
    await screen.findByRole("table", { name: "Components" });
    expect(screen.queryByRole("link", { name: "Restore…" })).not.toBeInTheDocument();
  });
});

describe("restore detail", () => {
  it("shows a running restore's step progress, reason and recorded preview", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ [`GET /api/v1/restores/${RS_ID}`]: () => Response.json(RUN_RUNNING) })));
    withUser(ALL, <RestoreDetail id={RS_ID} />);
    const progress = await screen.findByRole("list", { name: "Restore progress" });
    const steps = within(progress).getAllByRole("listitem");
    expect(steps.map((s) => s.getAttribute("data-step"))).not.toContain("grant-access");
    const current = within(progress).getByRole("listitem", { current: "step" });
    expect(current).toHaveAttribute("data-step", "restore-data");
    expect(current).toHaveTextContent("Restore data (in progress)");
    expect(screen.getByText(/Refreshes every 3 s while the restore runs/)).toBeInTheDocument();
    expect(screen.getByText("INC-48391: orders table corrupted")).toBeInTheDocument();
    expect(screen.getByText("Running: Restore data")).toBeInTheDocument();
    expect(screen.getByTestId("production-banner")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: RP_ID })).toHaveAttribute("href", `/recovery-points/${RP_ID}`);
  });

  it("shows a rolled-back restore's result with health and an expandable log tail", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/restores/${RS_ROLLED_BACK_ID}`]: () => Response.json(RUN_ROLLED_BACK) })),
    );
    const user = userEvent.setup();
    withUser(["restore.read"], <RestoreDetail id={RS_ROLLED_BACK_ID} />);
    expect(await screen.findByText("Restore rolled back")).toBeInTheDocument();
    expect(screen.getByText(/did not become healthy: shop-db-1/)).toBeInTheDocument();
    const failedStep = within(screen.getByRole("list", { name: "Restore progress" }))
      .getAllByRole("listitem")
      .find((li) => li.getAttribute("data-status") === "failed");
    expect(failedStep).toHaveAttribute("data-step", "health-check");
    const table = screen.getByRole("table", { name: "Component results" });
    expect(within(table).getByText("/var/lib/docker/volumes/shop_pgdata/_data.dbr2-prev-4f2a")).toBeInTheDocument();
    expect(within(table).getByText("verified")).toBeInTheDocument();
    const health = screen.getByRole("region", { name: "Health check" });
    expect(within(health).getByText("Unhealthy")).toBeInTheDocument();
    expect(within(health).getByText(/exited · exit code 1/)).toBeInTheDocument();
    expect(screen.queryByTestId("log-tail-shop-db-1")).not.toBeInTheDocument();
    await user.click(within(health).getByRole("button", { name: "Log tail of shop-db-1" }));
    expect(await screen.findByTestId("log-tail-shop-db-1")).toHaveTextContent("could not locate a valid checkpoint record");
    expect(screen.getByText("Rolled back", { selector: "[data-slot=alert-title]" })).toBeInTheDocument();
    // Without application.read / backup.read there are no links to them.
    expect(screen.queryByRole("link", { name: "Web shop" })).not.toBeInTheDocument();
  });

  it("shows a failed restore's error and image results", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ [`GET /api/v1/restores/${RS_FAILED_ID}`]: () => Response.json(RUN_FAILED) })));
    withUser(["restore.read"], <RestoreDetail id={RS_FAILED_ID} />);
    expect(await screen.findByText("Restore failed")).toBeInTheDocument();
    expect(screen.getAllByText(/unauthorized: authentication required/).length).toBeGreaterThan(0);
    expect(screen.getByText("/srv/shop → /srv/shop-restored")).toBeInTheDocument();
    expect(screen.getByText("No data was touched.")).toBeInTheDocument();
    const steps = within(screen.getByRole("list", { name: "Restore progress" })).getAllByRole("listitem");
    expect(steps[0]).toHaveAttribute("data-step", "grant-access");
    expect(steps.find((s) => s.getAttribute("data-status") === "failed")).toHaveAttribute("data-step", "images");
  });

  it("requires restore.read", () => {
    withUser(["backup.read"], <RestoreDetail id={RS_ID} />);
    expect(screen.getByText("restore.read")).toBeInTheDocument();
  });
});

describe("restores lists", () => {
  // The list view carries neither the preview nor the result (detail only).
  const LIST = [RUN_RUNNING, RUN_FAILED, RUN_ROLLED_BACK, RUN_SUCCEEDED].map((r) =>
    Object.fromEntries(Object.entries(r).filter(([k]) => k !== "preview" && k !== "result")),
  );

  it("parses URL filters and ignores unknown states", () => {
    expect(parseRestoreFilters(new URLSearchParams("application=abc&state=rolled_back"))).toEqual({
      application: "abc",
      state: "rolled_back",
    });
    expect(parseRestoreFilters(new URLSearchParams("state=bogus"))).toEqual({ application: "all", state: "all" });
  });

  it("lists the restore history with the URL filters", async () => {
    nav.search = `application=${APP_ID}&state=failed`;
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        urls.push(url);
        if (url.startsWith("/api/v1/applications")) return Promise.resolve(Response.json({ items: [] }));
        return Promise.resolve(Response.json({ items: [LIST[1]] }));
      }),
    );
    withUser(ALL, <RestoresView />);
    const table = await screen.findByRole("table", { name: "Restores" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-restore-state"))).toEqual(["failed"]);
    expect(urls.some((u) => u.includes(`application_id=${APP_ID}`) && u.includes("state=failed"))).toBe(true);
    expect(within(rows[0]!).getByRole("link", { name: "Web shop" })).toHaveAttribute("href", `/applications/${APP_ID}`);
    expect(within(rows[0]!).getByText("Alternate host")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("39 s")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "State" })).toHaveTextContent("Failed");
  });

  it("lists an application's restores with state, mode, production flag and duration", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        expect(String(input)).toContain(`application_id=${APP_ID}`);
        return Promise.resolve(Response.json({ items: LIST }));
      }),
    );
    withUser(["restore.read"], <ApplicationRestores applicationId={APP_ID} />);
    const table = await screen.findByRole("table", { name: "Restores of this application" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-restore-state"))).toEqual(["running", "failed", "rolled_back", "succeeded"]);
    expect(within(rows[0]!).getByText("Running: Restore data")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("Production")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("In place")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("ada@example.com")).toBeInTheDocument();
    expect(within(rows[3]!).getByText("3 min 12 s")).toBeInTheDocument();
    expect(within(rows[0]!).getByRole("link", { name: RS_ID })).toHaveAttribute("href", `/restores/${RS_ID}`);
    // Recovery point IDs are plain text without backup.read.
    expect(within(rows[0]!).queryByRole("link", { name: RP_ID })).not.toBeInTheDocument();
    expect(screen.getByText(/Refreshes every 3 s while a restore runs/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open in Restores" })).toHaveAttribute("href", `/restores?application=${APP_ID}`);
  });

  it("summarises running and failed restores on the dashboard", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/restores": () => Response.json({ items: LIST }) })));
    withUser(["restore.read"], <RecentRestoresCard />);
    const counts = await screen.findByRole("list", { name: "Restore counts" });
    expect(within(counts).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "1Running",
      "2Failed or rolled back",
    ]);
    const latest = screen.getByRole("list", { name: "Latest restores" });
    expect(within(latest).getAllByRole("listitem")).toHaveLength(3);
    expect(within(latest).getAllByRole("link", { name: "Web shop to docker-prod-01" })).toHaveLength(2);
    expect(screen.getByRole("link", { name: "View all restores" })).toHaveAttribute("href", "/restores");
  });
});

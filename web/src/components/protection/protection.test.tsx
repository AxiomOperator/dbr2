// SPDX-License-Identifier: Apache-2.0
import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { filterApplications } from "@/components/applications/applications-view";
import { CurrentUserProvider } from "@/components/auth-guard";
import { ProtectionOverviewCard } from "@/components/dashboard/protection-overview-card";
import { ProtectionCell } from "@/components/protection/protection-badges";
import { ProtectionCard } from "@/components/protection/protection-card";
import { ApplicationListSchema } from "@/lib/api/fleet-schemas";
import { APP_SUMMARY, meWith } from "@/test/fleet-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const base = ApplicationListSchema.parse({ items: [APP_SUMMARY] }).items[0]!;
const protection = base.protection!;
const withStatus = (status: typeof protection.status, over: Partial<typeof protection> = {}) => ({
  ...base,
  id: `${status}-${Math.random()}`,
  protection: { ...protection, status, ...over },
});

describe("ProtectionCell", () => {
  it("shows the status badge, coverage and a running indicator", () => {
    renderWithQuery(<ProtectionCell protection={{ ...protection, running: "backup" }} />);
    expect(screen.getByText("At risk")).toBeInTheDocument();
    expect(screen.getByTitle("1 of 2 components are in the latest recovery point")).toHaveTextContent("1/2 components");
    expect(screen.getByText("Backing up")).toBeInTheDocument();
  });

  it("renders a dash for servers without protection data", () => {
    renderWithQuery(<ProtectionCell protection={undefined} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});

describe("filterApplications by protection status", () => {
  it("keeps only applications with the chosen status", () => {
    const apps = [withStatus("protected"), withStatus("failed"), withStatus("failed")];
    const f = { host: "all", kind: "all" as const, unprotectedOnly: false };
    expect(filterApplications(apps, { ...f, status: "failed" })).toHaveLength(2);
    expect(filterApplications(apps, { ...f, status: "all" })).toHaveLength(3);
    expect(filterApplications(apps, { ...f, status: "unprotected" })).toHaveLength(0);
  });
});

describe("ProtectionCard", () => {
  it("shows reasons, the last backup, the error and per-component coverage from the API", () => {
    renderWithQuery(
      <CurrentUserProvider me={meWith(["application.read", "backup.read"])}>
        <ProtectionCard
          applicationId={base.id}
          protection={{ ...protection, last_error: "pre-hook failed", unresolved_dependencies: 2 }}
        />
      </CurrentUserProvider>,
    );
    const card = screen.getByTestId("protection-card");
    expect(within(card).getByRole("list", { name: "Reasons" })).toHaveTextContent("1 of 2 components are not in the latest recovery point");
    expect(within(card).getByText(/pre-hook failed/)).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: /ago|now/ })).toHaveAttribute(
      "href",
      `/recovery-points/${protection.last_recovery_point_id}`,
    );
    const rows = within(within(card).getByRole("table")).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-protected"))).toEqual(["true", "false"]);
    expect(within(rows[1]!).getByText("Not protected")).toBeInTheDocument();
    expect(within(card).getByText(/2 external dependencies are not protected/)).toBeInTheDocument();
  });
});

describe("ProtectionOverviewCard", () => {
  it("counts applications by the server's protection status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/applications": () =>
            Response.json({
              items: [
                { ...APP_SUMMARY, id: "a", protection: { ...APP_SUMMARY.protection, status: "protected" } },
                { ...APP_SUMMARY, id: "b", protection: { ...APP_SUMMARY.protection, status: "failed", running: "backup" } },
                { ...APP_SUMMARY, id: "c", protection: { ...APP_SUMMARY.protection, status: "failed" } },
              ],
            }),
        }),
      ),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["application.read"])}>
        <ProtectionOverviewCard />
      </CurrentUserProvider>,
    );
    const list = await screen.findByTestId("protection-overview");
    const failed = within(list).getByRole("link", { name: /Failed/ });
    expect(failed).toHaveAttribute("href", "/applications?status=failed");
    expect(failed).toHaveTextContent("2");
    expect(within(list).getByRole("link", { name: /Protected/ })).toHaveTextContent("1");
    expect(within(list).getByRole("link", { name: /running now/ })).toHaveAttribute("href", "/jobs?state=running");
  });
});

// SPDX-License-Identifier: Apache-2.0
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AppShell, isActive, NAV_GROUPS, visibleNav } from "@/components/app-shell";
import { CurrentUserProvider } from "@/components/auth-guard";
import { meWith } from "@/test/fleet-fixtures";
import { renderWithQuery } from "@/test/render";

const nav = vi.hoisted(() => ({ pathname: "/" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => nav.pathname,
  useSearchParams: () => new URLSearchParams(),
}));

// The permissions of the mock "Sign in with Microsoft" user (restore_operator).
const READ_ONLY = ["application.read", "backup.read", "host.read", "policy.read", "repository.read", "restore.execute", "restore.read"];
const ALL = [...READ_ONLY, "audit.read", "user.read", "user.manage", "host.manage", "repository.manage"];

beforeEach(() => {
  nav.pathname = "/";
  window.localStorage.clear();
});

const labels = (perms: string[]) =>
  visibleNav(meWith(perms)).map((g) => [g.label, g.items.map((i) => i.label)] as const);

describe("navigation model", () => {
  it("groups the console like the roadmap", () => {
    expect(NAV_GROUPS.map((g) => g.label)).toEqual([null, "Docker", "Protection", "Recovery", "Storage", "System"]);
    expect(labels(ALL)).toEqual([
      [null, ["Dashboard"]],
      ["Docker", ["Hosts", "Applications", "Containers", "Volumes"]],
      ["Protection", ["Policies", "Contracts", "Jobs", "Recovery Points"]],
      ["Recovery", ["Restore", "Restore Testing"]],
      ["Storage", ["Repositories", "Usage"]],
      ["System", ["Agents", "Users", "Notifications", "Platform protection", "Audit Log", "Settings"]],
    ]);
  });

  it("hides items and empty groups the user may not see", () => {
    expect(labels(READ_ONLY).find(([g]) => g === "System")?.[1]).toEqual(["Agents", "Notifications", "Settings"]);
    expect(labels([])).toEqual([
      [null, ["Dashboard"]],
      ["System", ["Settings"]],
    ]);
  });

  it("marks the item of the current route (and its sub-routes) active", () => {
    const item = (href: string) => NAV_GROUPS.flatMap((g) => g.items).find((i) => i.href === href)!;
    expect(isActive(item("/"), "/")).toBe(true);
    expect(isActive(item("/"), "/hosts")).toBe(false);
    expect(isActive(item("/restores"), "/restores/new")).toBe(true);
    expect(isActive(item("/hosts"), "/hosts-other")).toBe(false);
    expect(isActive(item("/settings/security"), "/settings/security")).toBe(true);
  });
});

describe("AppShell", () => {
  it("renders the grouped, permission-gated sidebar with collapsible groups", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ items: [] })));
    nav.pathname = "/containers";
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(READ_ONLY)}>
        <AppShell>
          <p>content</p>
        </AppShell>
      </CurrentUserProvider>,
    );
    const main = screen.getAllByRole("navigation", { name: "Main" })[0]!;
    expect(within(main).getByRole("link", { name: "Containers" })).toHaveAttribute("aria-current", "page");
    expect(within(main).queryByRole("link", { name: "Users" })).toBeNull();
    expect(within(main).queryByRole("link", { name: "Audit Log" })).toBeNull();

    const storage = within(main).getByRole("button", { name: "Storage" });
    expect(storage).toHaveAttribute("aria-expanded", "true");
    await user.click(storage);
    expect(storage).toHaveAttribute("aria-expanded", "false");
    expect(within(main).queryByRole("link", { name: "Usage" })).toBeNull();
    expect(JSON.parse(window.localStorage.getItem("dbr2.nav.collapsed") ?? "[]")).toEqual(["storage"]);
    // The group holding the current page cannot be collapsed away.
    await user.click(within(main).getByRole("button", { name: "Docker" }));
    expect(within(main).getByRole("link", { name: "Containers" })).toBeInTheDocument();
  });

  it("shows the number of open alerts on Notifications", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        Response.json({
          items: [
            {
              id: 1,
              severity: "critical",
              type: "backup.failed",
              target_type: null,
              target_id: null,
              message: "x",
              details: {},
              created_at: "2026-09-25T10:00:00Z",
              acknowledged_at: null,
            },
          ],
        }),
      ),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <AppShell>
          <p>content</p>
        </AppShell>
      </CurrentUserProvider>,
    );
    expect(await screen.findByTestId("alerts-badge")).toHaveTextContent("1 open alert");
  });
});

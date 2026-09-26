// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { manualRoles, UsersView } from "@/components/users/users-view";
import { UserListSchema } from "@/lib/api/users-schemas";
import { meWith } from "@/test/fleet-fixtures";
import { GROUP_MAPPINGS, ROLES, USER_LINUS, USERS } from "@/test/phase6-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const routes = (extra: Parameters<typeof routeFetch>[0] = {}) =>
  routeFetch({
    "GET /api/v1/users": () => Response.json({ items: USERS }),
    "GET /api/v1/roles": () => Response.json({ items: ROLES }),
    "GET /api/v1/oidc/group-mappings": () => Response.json({ items: GROUP_MAPPINGS }),
    ...extra,
  });

describe("users schemas", () => {
  it("normalise null roles and keep only manual roles editable", () => {
    const users = UserListSchema.parse({ items: USERS }).items;
    expect(users[2]?.roles).toEqual([]);
    expect(manualRoles(users[1]!)).toEqual(["auditor"]);
  });
});

describe("UsersView", () => {
  it("lists users with status and role sources; read-only without user.manage", async () => {
    vi.stubGlobal("fetch", vi.fn(routes()));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["user.read"])}>
        <UsersView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Users" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    expect(within(rows[0]!).getByText("Master admin")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("Disabled")).toBeInTheDocument();
    expect(await within(rows[1]!).findByText("Backup Administrator")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("· group")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Edit roles/ })).toBeNull();
    expect(await screen.findByRole("table", { name: "Group mappings" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add mapping" })).toBeNull();
  });

  it("replaces the manual roles with a reason", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routes({
          [`PUT /api/v1/users/${USER_LINUS}/roles`]: (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return new Response(null, { status: 204 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["user.read", "user.manage"])}>
        <UsersView />
      </CurrentUserProvider>,
    );
    await user.click(await screen.findByRole("button", { name: "Edit roles of Linus Torvalds" }));
    const dialog = await screen.findByRole("dialog", { name: "Roles of Linus Torvalds" });
    expect(within(dialog).getByRole("checkbox", { name: /Auditor/ })).toBeChecked();
    expect(within(dialog).getByText("(also from a group)")).toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "Save roles" });
    expect(save).toBeDisabled();
    await user.click(within(dialog).getByRole("checkbox", { name: /Read Only/ }));
    await user.type(within(dialog).getByLabelText("Reason (audit log)"), "ticket OPS-12");
    await user.click(save);
    await waitFor(() => expect(bodies).toEqual([{ roles: ["auditor", "read_only"], reason: "ticket OPS-12" }]));
  });

  it("disables a user and never offers actions on the master admin", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routes({
          [`PUT /api/v1/users/${USER_LINUS}/status`]: (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return new Response(null, { status: 204 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["user.read", "user.manage"])}>
        <UsersView />
      </CurrentUserProvider>,
    );
    expect(await screen.findByText("Built-in; cannot be changed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enable Mallory Example" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Disable Linus Torvalds" }));
    const dialog = await screen.findByRole("dialog", { name: "Disable Linus Torvalds" });
    expect(dialog).toHaveTextContent("all sessions are revoked");
    await user.type(within(dialog).getByLabelText("Reason (audit log)"), "left the company");
    await user.click(within(dialog).getByRole("button", { name: "Disable user" }));
    await waitFor(() => expect(bodies).toEqual([{ disabled: true, reason: "left the company" }]));
  });

  it("requires user.read", () => {
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read"])}>
        <UsersView />
      </CurrentUserProvider>,
    );
    expect(screen.getByText("Access denied")).toBeInTheDocument();
  });
});

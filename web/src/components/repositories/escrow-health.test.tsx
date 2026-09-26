// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import {
  drillFilename,
  EscrowDrills,
  EscrowHealthPanel,
  RegenerateEscrowDialog,
  sortProblems,
} from "@/components/repositories/escrow-health";
import { EscrowHealthSchema, RepositorySchema } from "@/lib/api/protection-schemas";
import { downloadTextFile } from "@/lib/protection";
import { meWith } from "@/test/fleet-fixtures";
import { DRILL_ID, DRILL_NEW, ESCROW_HEALTH } from "@/test/phase7-fixtures";
import { REPO_AWAITING, REPO_ID, REPO_READY } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

vi.mock("@/lib/protection", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/protection")>()),
  downloadTextFile: vi.fn(),
}));

const MANAGE = ["repository.read", "repository.manage"];
const repos = [RepositorySchema.parse(REPO_READY), RepositorySchema.parse(REPO_AWAITING)];

describe("escrow health", () => {
  it("orders critical problems first", () => {
    const h = EscrowHealthSchema.parse(ESCROW_HEALTH);
    expect(sortProblems(h.problems).map((p) => p.code)).toEqual(["not_confirmed", "recipients_changed", "drill_due"]);
  });

  it("lists problems with severity and the fix for each", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/escrow/health": () => Response.json(ESCROW_HEALTH) })));
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <EscrowHealthPanel repositories={repos} canManage />
      </CurrentUserProvider>,
    );
    const list = await screen.findByRole("list", { name: "Escrow problems" });
    const items = within(list).getAllByRole("listitem");
    expect(items.map((i) => i.getAttribute("data-problem"))).toEqual(["not_confirmed", "recipients_changed", "drill_due"]);
    expect(items[1]).toHaveTextContent("Critical: Recipients changed: regenerate the package · nas01-backups");
    expect(within(items[1]!).getByRole("button", { name: "Regenerate the escrow package of nas01-backups" })).toBeInTheDocument();
    expect(within(items[0]!).getByRole("button", { name: "Confirm the escrow of lab-scratch" })).toBeInTheDocument();
    expect(within(items[2]!).getByRole("link", { name: "Run an escrow drill" })).toHaveAttribute("href", "#escrow-drills");
    expect(screen.getByText("3 problems")).toBeInTheDocument();
    expect(screen.getByText("Never")).toBeInTheDocument();
  });

  it("offers no fixes to read-only users", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/escrow/health": () => Response.json(ESCROW_HEALTH) })));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["repository.read"])}>
        <EscrowHealthPanel repositories={repos} canManage={false} />
      </CurrentUserProvider>,
    );
    await screen.findByRole("list", { name: "Escrow problems" });
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("regenerates a package: download, then confirm the new code", async () => {
    const confirms: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`POST /api/v1/repositories/${REPO_ID}/escrow/regenerate`]: () =>
            Response.json({
              repository: { ...REPO_READY, escrow_confirmed_at: null },
              escrow_package: "-----BEGIN AGE ENCRYPTED FILE-----\nx\n-----END AGE ENCRYPTED FILE-----\n",
              escrow_filename: "dbr2-escrow-nas01-backups-0b6f1d2e.age",
            }),
          [`POST /api/v1/repositories/${REPO_ID}/escrow/confirm`]: (init) => {
            confirms.push(JSON.parse(String(init?.body)));
            return Response.json(REPO_READY);
          },
          "GET /api/v1/repositories": () => Response.json({ items: [REPO_READY] }),
          "GET /api/v1/escrow/health": () => Response.json({ ...ESCROW_HEALTH, problems: [] }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <RegenerateEscrowDialog repo={repos[0]!} />
      </CurrentUserProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Regenerate the escrow package of nas01-backups" }));
    const dialog = await screen.findByRole("dialog", { name: /Regenerate the escrow package/ });
    await user.click(within(dialog).getByRole("button", { name: "Regenerate package" }));
    const next = await within(dialog).findByRole("button", { name: "I stored it offline: next" });
    expect(next).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: /^Download dbr2-escrow-nas01/ }));
    expect(downloadTextFile).toHaveBeenCalledWith("dbr2-escrow-nas01-backups-0b6f1d2e.age", expect.stringContaining("AGE"));
    await user.click(next);
    await user.type(within(dialog).getByLabelText("Confirmation code"), "k7qx m2da pl4w zt6r");
    await user.click(within(dialog).getByRole("button", { name: "Confirm escrow" }));
    await waitFor(() => expect(confirms).toEqual([{ confirmation_code: "K7QX-M2DA-PL4W-ZT6R" }]));
    expect(await within(dialog).findByText("Escrow of nas01-backups is confirmed")).toBeInTheDocument();
  });
});

describe("escrow drills", () => {
  it("starts a drill, downloads the package and completes it with the code", async () => {
    let drills: unknown[] = [];
    const completes: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/escrow/drills": () => Response.json({ items: drills }),
          "POST /api/v1/escrow/drills": () => {
            drills = [{ ...DRILL_NEW, package: undefined }];
            return Response.json(DRILL_NEW, { status: 201 });
          },
          [`POST /api/v1/escrow/drills/${DRILL_ID}/complete`]: (init) => {
            const body = JSON.parse(String(init?.body));
            completes.push(body);
            if (completes.length === 1) {
              return new Response(
                JSON.stringify({ title: "Bad Request", status: 400, detail: "the confirmation code does not match this drill package", code: "validation_failed" }),
                { status: 400, headers: { "content-type": "application/problem+json" } },
              );
            }
            drills = [{ ...DRILL_NEW, package: undefined, completed_at: "2026-09-25T09:10:00Z" }];
            return Response.json({ ...DRILL_NEW, completed_at: "2026-09-25T09:10:00Z" });
          },
          "GET /api/v1/escrow/health": () => Response.json(ESCROW_HEALTH),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <EscrowDrills canManage />
      </CurrentUserProvider>,
    );
    expect(await screen.findByText("No escrow drill yet.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start escrow drill" }));
    const dialog = await screen.findByRole("dialog", { name: "Escrow drill" });
    await user.click(within(dialog).getByRole("button", { name: "Create drill package" }));
    const filename = drillFilename(DRILL_NEW);
    expect(filename).toBe("dbr2-escrow-drill-2026-09-25-9d8c7b6a.age");
    await user.click(await within(dialog).findByRole("button", { name: `Download ${filename}` }));
    expect(downloadTextFile).toHaveBeenCalledWith(filename, DRILL_NEW.package);
    await user.click(within(dialog).getByRole("button", { name: "Next: enter the code" }));

    const code = within(dialog).getByLabelText("Drill confirmation code");
    await user.type(code, "nope");
    await user.click(within(dialog).getByRole("button", { name: "Complete drill" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("XXXX-XXXX-XXXX-XXXX");
    expect(completes).toHaveLength(0);
    await user.clear(code);
    await user.type(code, "abcd-efgh-ijkl-mnop");
    await user.click(within(dialog).getByRole("button", { name: "Complete drill" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("does not match");
    await user.click(within(dialog).getByRole("button", { name: "Complete drill" }));
    expect(await within(dialog).findByText("Drill passed")).toBeInTheDocument();
    expect(completes).toEqual([{ confirmation_code: "ABCD-EFGH-IJKL-MNOP" }, { confirmation_code: "ABCD-EFGH-IJKL-MNOP" }]);
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(await screen.findByText(/^Passed /)).toBeInTheDocument();
  });
});

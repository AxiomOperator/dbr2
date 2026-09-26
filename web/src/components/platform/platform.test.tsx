// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { PlatformView, summarizeBundle } from "@/components/platform/platform-view";
import { meWith } from "@/test/fleet-fixtures";
import { PLATFORM_BACKUPS } from "@/test/phase7-fixtures";
import { REPO_AWAITING, REPO_READY } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

describe("platform protection", () => {
  it("summarises the bundle manifest without secrets", () => {
    expect(summarizeBundle(PLATFORM_BACKUPS[0]!.manifest)).toBe("platform 0.1.0.0 · 2 tables, 412 rows · 2 reposervers (1 missing)");
    expect(summarizeBundle(null)).toBeNull();
    expect(summarizeBundle({ tables: "nope" })).toBeNull();
  });

  it("shows the runs, the System Repository and the runbook, and runs a backup now", async () => {
    let posts = 0;
    const other = { ...REPO_AWAITING, id: "0b6f1d2e-3c4a-4b5d-8e6f-7a8b9c0d1e09", name: "nas02", status: "ready", is_system: false };
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/platform/backups": () => Response.json({ items: PLATFORM_BACKUPS }),
          "GET /api/v1/repositories": () => Response.json({ items: [REPO_READY, other] }),
          "POST /api/v1/platform/backups": () => {
            posts++;
            return Response.json({ workflow_id: "platform/backup" }, { status: 202 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["repository.read", "repository.manage"])}>
        <PlatformView />
      </CurrentUserProvider>,
    );
    expect(await screen.findByTestId("system-repository")).toHaveTextContent("nas01-backups");
    expect(screen.getByText("docs/operations/platform-recovery.md")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Make nas02 the System Repository" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Platform backups" });
    const row = within(table).getAllByRole("row")[1]!;
    expect(row).toHaveAttribute("data-state", "partial");
    expect(row).toHaveTextContent("dbr2-platform-20260924T020000Z.tar.zst.age");
    expect(row).toHaveTextContent("sha256 6eee7256…20dd");
    expect(row).toHaveTextContent("state export failed");
    expect(row).toHaveTextContent("1 min 30 s");
    expect(screen.getByText("The latest platform self-backup is partial")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Run now" }));
    await waitFor(() => expect(posts).toBe(1));
    expect(await screen.findByText("Platform self-backup started")).toBeInTheDocument();
  });

  it("requires repository.manage", () => {
    renderWithQuery(
      <CurrentUserProvider me={meWith(["repository.read"])}>
        <PlatformView />
      </CurrentUserProvider>,
    );
    expect(screen.getByText(/requires the/)).toHaveTextContent("repository.manage");
  });
});

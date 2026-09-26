// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { RecoveryPointDetail } from "@/components/backups/recovery-point-detail";
import { deleteBlockers, graceRemaining } from "@/components/common/deletion";
import { RepositoryDetail } from "@/components/repositories/repository-detail";
import { deleteRequestSchema } from "@/lib/api/protection-schemas";
import { meWith } from "@/test/fleet-fixtures";
import { MANIFEST, REPO_ID, REPO_READY, RP_COMMITTED, RP_ID } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/",
  useSearchParams: () => new URLSearchParams(),
}));

/** Actions return the list shape (no manifest). */
function withoutManifest(rp: Record<string, unknown>) {
  const list = { ...rp };
  delete list.manifest;
  return list;
}

const DELETE = ["backup.read", "backup.delete", "restore.execute"];

describe("typed confirmation", () => {
  it("needs the exact name and a reason", () => {
    expect(deleteBlockers("Web shop", "web shop", "")).toEqual([
      "Type Web shop exactly to confirm.",
      "Enter a reason (at least 3 characters).",
    ]);
    expect(deleteBlockers("Web shop", " Web shop ", "old")).toEqual([]);
    expect(deleteRequestSchema("Web shop").parse({ confirmation: " Web shop ", reason: " dup " })).toEqual({
      confirmation: "Web shop",
      reason: "dup",
    });
    expect(deleteRequestSchema("Web shop").safeParse({ confirmation: "Web Shop", reason: "dup" }).success).toBe(false);
    const now = new Date("2026-09-25T10:00:00Z");
    expect(graceRemaining("2026-10-02T10:00:00Z", now)).toBe("in 7 days");
    expect(graceRemaining("2026-09-26T11:00:00Z", now)).toBe("in 1 day");
    expect(graceRemaining("2026-09-25T12:00:00Z", now)).toBe("within a day");
  });
});

describe("recovery point deletion with a grace period", () => {
  it("schedules the deletion after typing the application name, then undeletes it", async () => {
    let rp: Record<string, unknown> = { ...RP_COMMITTED, manifest: MANIFEST };
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () => Response.json(rp),
          [`POST /api/v1/recovery-points/${RP_ID}/delete`]: (init) => {
            const body = JSON.parse(String(init?.body));
            bodies.push(body);
            rp = { ...rp, delete_after: "2026-10-02T10:00:00Z", delete_reason: body.reason };
            return Response.json(withoutManifest(rp));
          },
          [`POST /api/v1/recovery-points/${RP_ID}/undelete`]: () => {
            rp = { ...rp, delete_after: null, delete_reason: null };
            return Response.json(withoutManifest(rp));
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(DELETE)}>
        <RecoveryPointDetail id={RP_ID} />
      </CurrentUserProvider>,
    );
    await user.click(await screen.findByRole("button", { name: "Delete this recovery point" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete this recovery point?" });
    expect(dialog).toHaveTextContent("can still be restored");
    const submit = within(dialog).getByRole("button", { name: "Schedule deletion" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/^Reason/), "Duplicate test backup");
    await user.type(within(dialog).getByLabelText(/to confirm/), "web shop");
    expect(within(dialog).getByLabelText(/to confirm/)).toHaveAttribute("aria-invalid", "true");
    expect(submit).toBeDisabled();
    await user.clear(within(dialog).getByLabelText(/to confirm/));
    await user.type(within(dialog).getByLabelText(/to confirm/), "Web shop");
    await user.click(submit);
    await waitFor(() => expect(bodies).toEqual([{ confirmation: "Web shop", reason: "Duplicate test backup" }]));

    const banner = await screen.findByTestId("scheduled-deletion");
    expect(banner).toHaveTextContent("Scheduled for deletion on");
    expect(banner).toHaveTextContent("Reason: Duplicate test backup");
    expect(banner).toHaveTextContent("stays restorable");
    // Still restorable meanwhile; the manifest is kept after the action.
    expect(screen.getByRole("link", { name: /Restore…/ })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Components" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete this recovery point" })).not.toBeInTheDocument();

    await user.click(within(banner).getByRole("button", { name: "Undelete" }));
    await waitFor(() => expect(screen.queryByTestId("scheduled-deletion")).not.toBeInTheDocument());
    expect(await screen.findByText("Deletion cancelled")).toBeInTheDocument();
  });

  it("hides Delete… and Undelete without backup.delete", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () =>
            Response.json({ ...RP_COMMITTED, delete_after: "2026-10-02T10:00:00Z", delete_reason: "old" }),
        }),
      ),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <RecoveryPointDetail id={RP_ID} />
      </CurrentUserProvider>,
    );
    const banner = await screen.findByTestId("scheduled-deletion");
    expect(within(banner).queryByRole("button", { name: "Undelete" })).not.toBeInTheDocument();
    expect(banner).toHaveTextContent("requires the backup.delete permission");
    expect(screen.queryByRole("button", { name: /Delete/ })).not.toBeInTheDocument();
  });

  it("explains deleted recovery points", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () =>
            Response.json({ ...RP_COMMITTED, state: "deleted", deleted_at: "2026-09-20T10:00:00Z", delete_reason: "test data" }),
        }),
      ),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(DELETE)}>
        <RecoveryPointDetail id={RP_ID} />
      </CurrentUserProvider>,
    );
    expect(await screen.findByText(/can no longer be restored/)).toHaveTextContent("Reason: test data");
    expect(screen.queryByRole("link", { name: /Restore…/ })).not.toBeInTheDocument();
  });
});

describe("Repository deletion with a grace period", () => {
  it("marks the Repository pending deletion and undeletes it", async () => {
    let repo: Record<string, unknown> = { ...REPO_READY };
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/repositories/${REPO_ID}`]: () => Response.json(repo),
          "GET /api/v1/repositories": () => Response.json({ items: [repo] }),
          [`POST /api/v1/repositories/${REPO_ID}/delete`]: (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            repo = { ...repo, status: "pending_deletion", is_default: false, delete_after: "2026-10-02T10:00:00Z" };
            return Response.json(repo);
          },
          [`POST /api/v1/repositories/${REPO_ID}/undelete`]: () => {
            repo = { ...repo, status: "ready", delete_after: null };
            return Response.json(repo);
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["repository.read", "repository.manage"])}>
        <RepositoryDetail id={REPO_ID} />
      </CurrentUserProvider>,
    );
    expect(await screen.findAllByText("System Repository")).not.toHaveLength(0);
    await user.click(await screen.findByRole("button", { name: "Delete Repository nas01-backups" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Repository nas01-backups?" });
    expect(dialog).toHaveTextContent("never erases the stored data");
    expect(dialog).toHaveTextContent("It is the System Repository");
    await user.type(within(dialog).getByLabelText(/^Reason/), "Replaced by nas02");
    await user.type(within(dialog).getByLabelText(/to confirm/), "nas01-backups");
    await user.click(within(dialog).getByRole("button", { name: "Schedule deletion" }));
    await waitFor(() => expect(bodies).toEqual([{ confirmation: "nas01-backups", reason: "Replaced by nas02" }]));
    const banner = await screen.findByTestId("scheduled-deletion");
    expect(screen.getAllByText("Pending deletion").length).toBeGreaterThan(0);
    await user.click(within(banner).getByRole("button", { name: "Undelete Repository" }));
    await waitFor(() => expect(screen.queryByTestId("scheduled-deletion")).not.toBeInTheDocument());
  });
});

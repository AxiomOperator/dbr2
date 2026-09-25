// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { CreateRepositoryDialog } from "@/components/repositories/create-repository-dialog";
import { EscrowRecipients, shortKey } from "@/components/repositories/escrow-recipients";
import { RepositoriesView } from "@/components/repositories/repositories-view";
import {
  CapacityBar,
  RepositoryStatusBadge,
  truncateFingerprint,
  usedPercent,
} from "@/components/repositories/repository-badges";
import { RepositoryDetail, sortUsage } from "@/components/repositories/repository-detail";
import { RepositorySchema } from "@/lib/api/protection-schemas";
import { downloadTextFile } from "@/lib/protection";
import { meWith } from "@/test/fleet-fixtures";
import {
  CERT,
  CREATE_REPO_RESPONSE,
  RECIPIENTS,
  REPO_AWAITING,
  REPO_AWAITING_ID,
  REPO_READY,
  REPO_UNAVAILABLE,
} from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

vi.mock("@/lib/protection", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/protection")>()),
  downloadTextFile: vi.fn(),
}));

const READ = ["repository.read"];
const MANAGE = ["repository.read", "repository.manage"];

function problem(status: number, code: string, detail: string) {
  return new Response(JSON.stringify({ title: "Error", status, detail, code }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });
}

describe("RepositoryStatusBadge", () => {
  it("labels each status in text", () => {
    const { rerender } = renderWithQuery(<RepositoryStatusBadge status="awaiting_escrow" />);
    expect(screen.getByText("Escrow not confirmed")).toHaveClass("text-amber-700");
    rerender(<RepositoryStatusBadge status="ready" />);
    expect(screen.getByText("Ready")).toHaveClass("text-emerald-700");
    rerender(<RepositoryStatusBadge status="unavailable" />);
    expect(screen.getByText("Unavailable")).toHaveAttribute("data-variant", "destructive");
  });
});

describe("capacity and fingerprint", () => {
  it("computes the used percentage and renders an accessible bar", () => {
    expect(usedPercent(50, 200)).toBe(25);
    expect(usedPercent(10, 0)).toBeNull();
    expect(usedPercent(300, 200)).toBe(100);
    renderWithQuery(<CapacityBar used={950} total={1000} />);
    const bar = screen.getByRole("progressbar", { name: "Storage used" });
    expect(bar).toHaveAttribute("aria-valuenow", "95");
    expect(bar.firstElementChild).toHaveClass("bg-destructive");
  });

  it("truncates long fingerprints", () => {
    expect(truncateFingerprint(CERT)).toBe("6eee7256…20dd");
    expect(truncateFingerprint("abcd")).toBe("abcd");
  });

  it("shortens keys for display", () => {
    expect(shortKey(RECIPIENTS[0]!.public_key)).toBe("age1ql3z7hjy…mcac8p");
    expect(shortKey(RECIPIENTS[1]!.public_key)).toMatch(/^ssh-ed25519 AAAAC3NzaC…/);
  });

  it("sorts host usage largest first", () => {
    const repo = RepositorySchema.parse(REPO_READY);
    expect(sortUsage([...repo.usage_by_host].reverse()).map((u) => u.hostname)).toEqual([
      "docker-prod-01",
      "docker-edge-02",
    ]);
  });
});

describe("RepositoriesView", () => {
  function stub() {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/repositories": () => Response.json({ items: [REPO_READY, REPO_AWAITING, REPO_UNAVAILABLE] }),
          "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }),
        }),
      ),
    );
  }

  it("shows status, storage and fingerprint, and hides manage actions for read-only users", async () => {
    stub();
    renderWithQuery(
      <CurrentUserProvider me={meWith(READ)}>
        <RepositoriesView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Repositories" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-status"))).toEqual(["ready", "awaiting_escrow", "unavailable"]);
    expect(within(rows[0]!).getByText("Default")).toBeInTheDocument();
    expect(within(rows[0]!).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40");
    expect(within(rows[0]!).getByText("6eee7256…20dd")).toHaveAttribute("title", CERT);
    expect(within(rows[1]!).getByText("Escrow not confirmed")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("Unreachable")).toBeInTheDocument();
    expect(within(rows[2]!).getByText(/connection refused/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create Repository" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reindex/ })).not.toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Escrow recipients" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add recipient" })).not.toBeInTheDocument();
  });

  it("offers create, reindex, escrow download and confirm with repository.manage", async () => {
    stub();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <RepositoriesView />
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Repositories" });
    expect(screen.getByRole("button", { name: "Create Repository" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reindex nas01-backups" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download escrow package of nas01-backups" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Confirm escrow of lab-scratch" })).toHaveAttribute(
      "href",
      `/repositories/${REPO_AWAITING_ID}#escrow`,
    );
  });

  it("denies access without repository.read", () => {
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read"])}>
        <RepositoriesView />
      </CurrentUserProvider>,
    );
    expect(screen.getByText("Access denied")).toBeInTheDocument();
  });

  it("confirms before starting a reindex", async () => {
    const posts: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/repositories": () => Response.json({ items: [REPO_READY] }),
          "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }),
          [`POST /api/v1/repositories/${REPO_READY.id}/reindex`]: () => {
            posts.push("reindex");
            return Response.json({ workflow_id: "reindex-wf-1" }, { status: 202 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <RepositoriesView />
      </CurrentUserProvider>,
    );
    await user.click(await screen.findByRole("button", { name: "Reindex nas01-backups" }));
    const dialog = await screen.findByRole("dialog", { name: "Reindex nas01-backups?" });
    expect(posts).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Start reindex" }));
    expect(await screen.findByText("reindex-wf-1")).toBeInTheDocument();
    expect(posts).toEqual(["reindex"]);
  });
});

describe("EscrowRecipients", () => {
  it("rejects a pasted private identity without sending or keeping it", async () => {
    const fetchMock = vi.fn(
      routeFetch({ "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }) }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithQuery(<EscrowRecipients canManage />);
    await user.click(await screen.findByRole("button", { name: "Add recipient" }));
    const dialog = await screen.findByRole("dialog", { name: "Add escrow recipient" });
    await user.type(within(dialog).getByLabelText("Key holder"), "Ada");
    const key = within(dialog).getByLabelText("Public key");
    await user.click(key);
    await user.paste("AGE-SECRET-KEY-1QQPQ9X0KZ5EXAMPLEEXAMPLEEXAMPLE");
    expect(key).toHaveValue("");
    expect(within(dialog).getByTestId("private-key-warning")).toHaveTextContent("Never paste private keys");
    await user.click(within(dialog).getByRole("button", { name: "Add recipient" }));
    expect(within(dialog).getAllByRole("alert").at(-1)).toHaveTextContent("Paste the public key");
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  });

  it("adds a public key", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/escrow/recipients": () => Response.json({ items: [] }),
          "POST /api/v1/escrow/recipients": (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return Response.json({ ...RECIPIENTS[0], name: "Ada" }, { status: 201 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<EscrowRecipients canManage />);
    expect(await screen.findByText("0 of 2 required recipients")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add recipient" }));
    const dialog = await screen.findByRole("dialog", { name: "Add escrow recipient" });
    await user.type(within(dialog).getByLabelText("Key holder"), "Ada");
    await user.type(within(dialog).getByLabelText("Public key"), `  ${RECIPIENTS[0]!.public_key}  `);
    await user.click(within(dialog).getByRole("button", { name: "Add recipient" }));
    await waitFor(() => expect(bodies).toEqual([{ name: "Ada", public_key: RECIPIENTS[0]!.public_key }]));
    expect(await screen.findByText("Escrow recipient Ada added")).toBeInTheDocument();
  });

  it("confirms before removing a recipient", async () => {
    const deleted: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }),
          [`DELETE /api/v1/escrow/recipients/${RECIPIENTS[1]!.id}`]: () => {
            deleted.push(RECIPIENTS[1]!.id);
            return new Response(null, { status: 204 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<EscrowRecipients canManage />);
    await user.click(await screen.findByRole("button", { name: "Remove escrow recipient Grace Hopper" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove Grace Hopper?" });
    expect(within(dialog).getByText(/Fewer than 2 recipients will remain/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Remove recipient" }));
    await waitFor(() => expect(deleted).toEqual([RECIPIENTS[1]!.id]));
  });
});

describe("CreateRepositoryDialog", () => {
  it("blocks creation while fewer than two escrow recipients exist", async () => {
    const fetchMock = vi.fn(
      routeFetch({ "GET /api/v1/escrow/recipients": () => Response.json({ items: [RECIPIENTS[0]] }) }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithQuery(<CreateRepositoryDialog />);
    await user.click(screen.getByRole("button", { name: "Create Repository" }));
    const dialog = await screen.findByRole("dialog", { name: "Create Repository" });
    const notice = await within(dialog).findByTestId("recipients-notice");
    expect(notice).toHaveTextContent("Only 1 escrow recipient is registered");
    expect(within(notice).getByRole("link", { name: "Add escrow recipients" })).toHaveAttribute(
      "href",
      "/repositories#escrow-recipients",
    );
    expect(within(dialog).getByRole("button", { name: "Create Repository" })).toBeDisabled();
  });

  it("walks through create → escrow package → confirmation code → ready", async () => {
    const bodies: Record<string, unknown[]> = { create: [], confirm: [] };
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }),
          "GET /api/v1/repositories": () => Response.json({ items: [] }),
          "POST /api/v1/repositories": (init) => {
            bodies.create!.push(JSON.parse(String(init?.body)));
            return Response.json(CREATE_REPO_RESPONSE, { status: 201 });
          },
          [`POST /api/v1/repositories/${REPO_AWAITING_ID}/escrow/confirm`]: (init) => {
            const body = JSON.parse(String(init?.body));
            bodies.confirm!.push(body);
            if (body.confirmation_code !== "K7QX-M2DA-PL4W-ZT6R") {
              return problem(400, "validation_failed", "the confirmation code does not match");
            }
            return Response.json({ ...REPO_AWAITING, status: "ready", escrow_confirmed_at: "2026-09-25T12:00:00Z" });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<CreateRepositoryDialog />);
    await user.click(screen.getByRole("button", { name: "Create Repository" }));
    const dialog = await screen.findByRole("dialog", { name: "Create Repository" });
    expect(within(dialog).getByText(/Step 1 of 3/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Management URL")).toHaveValue("http://dbr2-reposerver:8091");
    expect(within(dialog).getByLabelText(/Internal server URL/)).toHaveAttribute(
      "placeholder",
      "https://dbr2-reposerver:51515",
    );

    await user.type(within(dialog).getByLabelText("Name"), "lab-scratch");
    await user.type(within(dialog).getByLabelText("Server URL"), "https://lab-backup.example.lan:51515");
    await user.click(within(dialog).getByRole("button", { name: "Create Repository" }));

    // Step 2: not usable yet; the package is offered as a file.
    expect(await within(dialog).findByText(/Step 2 of 3/)).toBeInTheDocument();
    expect(within(dialog).getByText("lab-scratch is not usable yet")).toBeInTheDocument();
    expect(within(dialog).getByText(`age -d -i <identity-file> ${CREATE_REPO_RESPONSE.escrow_filename}`)).toBeInTheDocument();
    expect(bodies.create).toEqual([
      {
        name: "lab-scratch",
        backend: "nfs",
        management_url: "http://dbr2-reposerver:8091",
        server_url: "https://lab-backup.example.lan:51515",
        default: false,
      },
    ]);
    const next = within(dialog).getByRole("button", { name: /I stored it offline/ });
    expect(next).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: `Download ${CREATE_REPO_RESPONSE.escrow_filename}` }));
    expect(downloadTextFile).toHaveBeenCalledWith(
      CREATE_REPO_RESPONSE.escrow_filename,
      CREATE_REPO_RESPONSE.escrow_package,
    );
    await user.click(next);

    // Step 3: a malformed code is rejected client-side, a wrong one by the server.
    expect(await within(dialog).findByText(/Step 3 of 3/)).toBeInTheDocument();
    const code = within(dialog).getByLabelText("Confirmation code");
    await user.type(code, "abc");
    await user.click(within(dialog).getByRole("button", { name: "Confirm escrow" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("XXXX-XXXX-XXXX-XXXX");
    expect(bodies.confirm).toEqual([]);

    await user.clear(code);
    await user.type(code, "aaaa bbbb cccc dddd");
    await user.click(within(dialog).getByRole("button", { name: "Confirm escrow" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("does not match");

    await user.clear(code);
    await user.type(code, "k7qx m2da pl4w zt6r");
    await user.click(within(dialog).getByRole("button", { name: "Confirm escrow" }));
    expect(await within(dialog).findByText("lab-scratch is ready")).toBeInTheDocument();
    expect(bodies.confirm).toEqual([
      { confirmation_code: "AAAA-BBBB-CCCC-DDDD" },
      { confirmation_code: "K7QX-M2DA-PL4W-ZT6R" },
    ]);
  });

  it("shows the server's 409 when creation is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/escrow/recipients": () => Response.json({ items: RECIPIENTS }),
          "POST /api/v1/repositories": () => problem(409, "conflict", "the reposerver storage is not empty"),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<CreateRepositoryDialog />);
    await user.click(screen.getByRole("button", { name: "Create Repository" }));
    const dialog = await screen.findByRole("dialog", { name: "Create Repository" });
    await user.type(within(dialog).getByLabelText("Name"), "x");
    await user.type(within(dialog).getByLabelText("Server URL"), "https://x:51515");
    await user.click(within(dialog).getByRole("button", { name: "Create Repository" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("the reposerver storage is not empty");
    expect(within(dialog).getByText(/Step 1 of 3/)).toBeInTheDocument();
  });
});

describe("RepositoryDetail", () => {
  it("shows the escrow confirmation for managers of an awaiting Repository", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/repositories/${REPO_AWAITING_ID}`]: () => Response.json(REPO_AWAITING) })),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <RepositoryDetail id={REPO_AWAITING_ID} />
      </CurrentUserProvider>,
    );
    expect(await screen.findByRole("heading", { name: "Confirm key escrow" })).toBeInTheDocument();
    expect(screen.getByLabelText("Confirmation code")).toBeInTheDocument();
    expect(screen.getByText("Same as the server URL")).toBeInTheDocument();
    expect(screen.getByText("No recovery points in this Repository yet.")).toBeInTheDocument();
  });

  it("shows the internal URL and per-host usage, read-only without repository.manage", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/repositories/${REPO_READY.id}`]: () => Response.json(REPO_READY) })),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(READ)}>
        <RepositoryDetail id={REPO_READY.id} />
      </CurrentUserProvider>,
    );
    expect(await screen.findByText("https://dbr2-reposerver:51515")).toBeInTheDocument();
    const usage = screen.getByRole("list", { name: "Usage by host" });
    const items = within(usage).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent("docker-prod-01");
    expect(items[0]).toHaveTextContent("3 applications");
    expect(items[0]).toHaveTextContent("413 GiB");
    expect(screen.getAllByText(/cannot be attributed/).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: /Reindex/ })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Confirmation code")).not.toBeInTheDocument();
  });
});

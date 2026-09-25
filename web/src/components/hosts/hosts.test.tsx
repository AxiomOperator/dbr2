// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { AddHostDialog } from "@/components/hosts/add-host-dialog";
import { actionsFor, HostActions } from "@/components/hosts/host-actions";
import { AgentSchema } from "@/lib/api/fleet-schemas";
import { AGENT_ACTIVE, CREATE_TOKEN_RESPONSE, meWith } from "@/test/fleet-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const agent = AgentSchema.parse(AGENT_ACTIVE);

function problem(status: number, code: string, detail: string) {
  return new Response(JSON.stringify({ title: "Error", status, detail, code }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });
}

describe("actionsFor", () => {
  it("offers the transitions valid for each status", () => {
    expect(actionsFor("pending")).toEqual(["approve", "revoke"]);
    expect(actionsFor("active")).toEqual(["suspend", "revoke"]);
    expect(actionsFor("suspended")).toEqual(["resume", "revoke"]);
    expect(actionsFor("revoked")).toEqual([]);
  });
});

describe("Revoke dialog", () => {
  it("requires a reason and the typed hostname before revoking", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`POST /api/v1/agents/${agent.id}/revoke`]: (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return Response.json({ ...AGENT_ACTIVE, status: "revoked", connected: false });
          },
          "GET /api/v1/agents": () => Response.json({ items: [] }),
          "GET /api/v1/applications": () => Response.json({ items: [] }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read", "host.manage"])}>
        <HostActions agent={agent} />
      </CurrentUserProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Revoke sysadm" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke sysadm" });
    const submit = within(dialog).getByRole("button", { name: "Revoke host" });
    expect(submit).toBeDisabled();

    await user.type(within(dialog).getByLabelText(/Reason/), "host decommissioned");
    expect(submit).toBeDisabled();

    const confirm = within(dialog).getByLabelText(/to confirm/);
    await user.type(confirm, "sysadm-typo");
    expect(submit).toBeDisabled();

    await user.clear(confirm);
    await user.type(confirm, "sysadm");
    expect(submit).toBeEnabled();
    await user.click(submit);

    await waitFor(() => expect(bodies).toEqual([{ reason: "host decommissioned" }]));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("sysadm revoked")).toBeInTheDocument();
  });

  it("shows a conflict error and keeps the dialog open", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`POST /api/v1/agents/${agent.id}/suspend`]: () =>
            problem(409, "conflict", "cannot suspend an agent that is suspended"),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read", "host.manage"])}>
        <HostActions agent={agent} />
      </CurrentUserProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Suspend sysadm" }));
    const dialog = await screen.findByRole("dialog", { name: "Suspend sysadm" });
    expect(within(dialog).queryByLabelText(/to confirm/)).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText(/Reason/), "maintenance");
    await user.click(within(dialog).getByRole("button", { name: "Suspend host" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "cannot suspend an agent that is suspended The resource changed in the meantime",
    );
  });
});

describe("AddHostDialog", () => {
  it("shows the join command once and forgets it when closed", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "POST /api/v1/agents/registration-tokens": (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return Response.json(CREATE_TOKEN_RESPONSE, { status: 201 });
          },
          "GET /api/v1/agents/registration-tokens": () => Response.json({ items: [] }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<AddHostDialog />);

    await user.click(screen.getByRole("button", { name: "Add host" }));
    let dialog = await screen.findByRole("dialog", { name: "Add host" });
    expect(within(dialog).getByLabelText(/Valid for/)).toHaveValue(24);
    await user.type(within(dialog).getByLabelText("Description"), "docker-prod-01");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    expect(await within(dialog).findByText("Shown only once")).toBeInTheDocument();
    expect(within(dialog).getByTestId("join-command")).toHaveTextContent(CREATE_TOKEN_RESPONSE.join_command);
    expect(within(dialog).getByText(CREATE_TOKEN_RESPONSE.ca_sha256)).toBeInTheDocument();
    expect(within(dialog).getByText(CREATE_TOKEN_RESPONSE.gateway_address)).toBeInTheDocument();
    expect(bodies).toEqual([{ description: "docker-prod-01", expires_in_hours: 24 }]);

    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // Reopening starts a fresh form: the token is never shown again.
    await user.click(screen.getByRole("button", { name: "Add host" }));
    dialog = await screen.findByRole("dialog", { name: "Add host" });
    expect(within(dialog).queryByTestId("join-command")).not.toBeInTheDocument();
    expect(screen.queryByText(CREATE_TOKEN_RESPONSE.token, { exact: false })).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Create token" })).toBeInTheDocument();
  });

  it("validates the expiry range client-side", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithQuery(<AddHostDialog />);
    await user.click(screen.getByRole("button", { name: "Add host" }));
    const dialog = await screen.findByRole("dialog", { name: "Add host" });
    await user.type(within(dialog).getByLabelText("Description"), "x");
    const hours = within(dialog).getByLabelText(/Valid for/);
    await user.clear(hours);
    await user.type(hours, "200");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("at most 168 hours");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

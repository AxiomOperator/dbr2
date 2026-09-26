// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import {
  contractComponentOptions,
  contractDraftToRequest,
  ContractCard,
  contractToDraft,
} from "@/components/policies/contract-card";
import { ContractsView, sortContracts } from "@/components/policies/contracts-view";
import { PoliciesView } from "@/components/policies/policies-view";
import { newPolicyDraft, policyDraftToRequest, policyToDraft } from "@/components/policies/policy-form";
import { PolicyAssignmentCard } from "@/components/policies/policy-assignment";
import { ApplicationDetailSchema } from "@/lib/api/fleet-schemas";
import { ContractSchema, PolicySchema } from "@/lib/api/policy-schemas";
import { APP_DETAIL, meWith } from "@/test/fleet-fixtures";
import {
  CONTRACT_SATISFIED,
  CONTRACT_VIOLATED,
  POLICY,
  POLICY_DETAIL,
  POLICY_HOURLY,
  POLICY_ID,
} from "@/test/phase7-fixtures";
import { APP_ID } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const nav = vi.hoisted(() => ({ replace: vi.fn(), search: "" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace }),
  usePathname: () => "/contracts",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

beforeEach(() => {
  nav.replace.mockReset();
  nav.search = "";
});

const READ = ["policy.read", "repository.read", "application.read"];
const MANAGE = [...READ, "policy.manage"];

function problem(status: number, detail: string) {
  return new Response(JSON.stringify({ title: "Bad Request", status, detail, code: "validation_failed" }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });
}

describe("policy draft", () => {
  it("sends presets by name and round-trips a stored custom schedule", () => {
    const draft = { ...newPolicyDraft(), name: "Nightly", timezone: "UTC" };
    const res = policyDraftToRequest(draft);
    expect(res).toMatchObject({ ok: true, value: { name: "Nightly", schedule: "daily", timezone: "UTC", enabled: true } });
    expect(res.ok && res.value.retention).toEqual({ keep_last: 7, keep_hourly: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 12, keep_yearly: 0 });
    expect(res.ok && res.value.consistency_mode).toBeUndefined();

    const hourly = policyToDraft(PolicySchema.parse(POLICY_HOURLY));
    expect(hourly).toMatchObject({ schedule: "custom", cron: "15 * * * *", mode: "live" });
    expect(policyDraftToRequest(hourly)).toMatchObject({ ok: true, value: { schedule: "15 * * * *", consistency_mode: "live" } });
    expect(policyToDraft(PolicySchema.parse(POLICY)).schedule).toBe("daily");
  });

  it("rejects invalid cron, timezones and retention before the request", () => {
    const d = { ...newPolicyDraft(), name: "x", timezone: "UTC" };
    expect(policyDraftToRequest({ ...d, schedule: "custom", cron: "0 25 * * *" })).toEqual({
      ok: false,
      error: "Schedule: Hour: 25 is out of range 0–23.",
    });
    expect(policyDraftToRequest({ ...d, timezone: "Mars/Olympus" })).toMatchObject({ ok: false, error: /not a known IANA timezone/ });
    expect(policyDraftToRequest({ ...d, retention: { ...d.retention, keep_last: "0" } })).toMatchObject({
      ok: false,
      error: "Keep last must be at least 1.",
    });
    expect(policyDraftToRequest({ ...d, retention: { ...d.retention, keep_daily: "-1" } })).toMatchObject({
      ok: false,
      error: "Daily cannot be negative.",
    });
    expect(policyDraftToRequest({ ...d, name: "  " })).toMatchObject({ ok: false, error: "Enter a name." });
  });
});

describe("PoliciesView", () => {
  it("lists schedules in words, next run and retention; creates a policy with a live cron preview", async () => {
    const posts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/policies": () => Response.json({ items: [POLICY, { ...POLICY_HOURLY, enabled: false }] }),
          "GET /api/v1/repositories": () => Response.json({ items: [] }),
          "POST /api/v1/policies": async (init) => {
            const body = JSON.parse(String(init?.body));
            posts.push(body);
            if (posts.length === 1) return problem(400, "schedule must be hourly, daily, weekly, monthly or a 5-field cron expression: odd");
            return Response.json({ ...POLICY, id: "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e09", name: body.name, schedule: body.schedule }, { status: 201 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <PoliciesView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Policies" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows[0]).toHaveTextContent("Every day at 01:00");
    expect(rows[0]).toHaveTextContent("America/Chicago");
    expect(rows[0]).toHaveTextContent("Last 7 · 14 daily · 8 weekly · 12 monthly");
    expect(rows[0]).toHaveTextContent("Enabled");
    expect(rows[1]).toHaveTextContent("Every hour at minute 15");
    expect(within(rows[1]!).getAllByText("Disabled").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "Create policy" }));
    const dialog = await screen.findByRole("dialog", { name: "Create policy" });
    await user.type(within(dialog).getByLabelText("Name"), "Weekdays");
    await user.click(within(dialog).getByRole("combobox", { name: "Frequency" }));
    await user.click(await screen.findByRole("option", { name: "Custom (cron expression)" }));
    const cron = within(dialog).getByLabelText("Cron expression");
    await user.clear(cron);
    await user.type(cron, "30 22 * * 1-5");
    expect(within(dialog).getByTestId("schedule-preview")).toHaveTextContent("Every Monday–Friday at 22:30");
    const tz = within(dialog).getByLabelText("Timezone");
    await user.clear(tz);
    await user.type(tz, "UTC");
    const keepLast = within(dialog).getByLabelText(/^Keep last/);
    await user.clear(keepLast);
    await user.type(keepLast, "3");
    expect(within(dialog).getByTestId("retention-summary")).toHaveTextContent("Last 3 · 14 daily");

    // The server's validation message is shown when it disagrees.
    await user.click(within(dialog).getByRole("button", { name: "Create policy" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("5-field cron expression");
    await user.click(within(dialog).getByRole("button", { name: "Create policy" }));
    await waitFor(() => expect(posts).toHaveLength(2));
    expect(posts[1]).toMatchObject({ name: "Weekdays", schedule: "30 22 * * 1-5", timezone: "UTC", retention: { keep_last: 3 } });
    expect(await screen.findByText("Policy Weekdays created")).toBeInTheDocument();
  });

  it("is read-only without policy.manage", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/policies": () => Response.json({ items: [POLICY] }),
          "GET /api/v1/repositories": () => Response.json({ items: [] }),
        }),
      ),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(READ)}>
        <PoliciesView />
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Policies" });
    expect(screen.queryByRole("button", { name: "Create policy" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Delete policy/ })).not.toBeInTheDocument();
  });
});

describe("policy assignment", () => {
  it("shows the current policy and assigns another one", async () => {
    const puts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/policies": () => Response.json({ items: [POLICY, POLICY_HOURLY] }),
          [`GET /api/v1/policies/${POLICY_ID}`]: () => Response.json(POLICY_DETAIL),
          [`PUT /api/v1/applications/${APP_ID}/policy`]: (init) => {
            puts.push(JSON.parse(String(init?.body)));
            return new Response(null, { status: 204 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <PolicyAssignmentCard applicationId={APP_ID} applicationName="Web shop" />
      </CurrentUserProvider>,
    );
    const select = await screen.findByRole("combobox", { name: "Protection policy" });
    await waitFor(() => expect(select).toHaveTextContent("Nightly production"));
    expect(screen.getByTestId("policy-summary")).toHaveTextContent("Every day at 01:00 (America/Chicago)");
    expect(screen.getByRole("button", { name: "Save assignment" })).toBeDisabled();
    await user.click(select);
    await user.click(await screen.findByRole("option", { name: "No policy (on demand only)" }));
    await user.click(screen.getByRole("button", { name: "Save assignment" }));
    await waitFor(() => expect(puts).toEqual([{ policy_id: null }]));
    expect(await screen.findByText("Web shop has no policy")).toBeInTheDocument();
  });
});

describe("Recovery Contracts", () => {
  it("converts drafts with RPO units and requires something to promise", () => {
    const c = ContractSchema.parse(CONTRACT_SATISFIED);
    expect(contractToDraft(c)).toEqual({ rpoEnabled: true, rpoValue: "1", rpoUnit: "days", required: ["config", "volume:shop_pgdata"] });
    expect(contractToDraft({ ...c, max_rpo_minutes: 90 })).toMatchObject({ rpoValue: "90", rpoUnit: "minutes" });
    expect(contractDraftToRequest({ rpoEnabled: true, rpoValue: "26", rpoUnit: "hours", required: ["config", "config"] })).toEqual({
      ok: true,
      value: { max_rpo_minutes: 1560, required_components: ["config"] },
    });
    expect(contractDraftToRequest({ rpoEnabled: false, rpoValue: "", rpoUnit: "hours", required: ["config"] })).toEqual({
      ok: true,
      value: { required_components: ["config"] },
    });
    expect(contractDraftToRequest({ rpoEnabled: false, rpoValue: "", rpoUnit: "hours", required: [] })).toMatchObject({ ok: false });
    expect(contractDraftToRequest({ rpoEnabled: true, rpoValue: "0.5", rpoUnit: "minutes", required: [] })).toMatchObject({
      ok: false,
      error: /whole number of minutes/,
    });
  });

  it("offers the planned components plus required ones no longer planned", () => {
    const app = ApplicationDetailSchema.parse(APP_DETAIL);
    expect(contractComponentOptions(app, ["volume:gone"]).map((c) => [c.name, c.missing])).toEqual([
      ["config", false],
      ["volume:fbcad_miniodata", false],
      ["volume:gone", true],
    ]);
  });

  it("shows the state and reasons and saves the contract", async () => {
    const app = ApplicationDetailSchema.parse(APP_DETAIL);
    const puts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/applications/${app.id}/contract`]: () =>
            Response.json({ ...CONTRACT_VIOLATED, application_id: app.id, application_name: "fbcad" }),
          [`PUT /api/v1/applications/${app.id}/contract`]: (init) => {
            const body = JSON.parse(String(init?.body));
            puts.push(body);
            return Response.json({ ...CONTRACT_SATISFIED, ...body, application_id: app.id });
          },
          "GET /api/v1/contracts": () => Response.json({ items: [] }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <ContractCard app={app} />
      </CurrentUserProvider>,
    );
    const state = await screen.findByTestId("contract-state");
    expect(state).toHaveTextContent("Violated");
    expect(within(state).getByRole("list", { name: "Reasons" })).toHaveTextContent("no recovery point exists");
    await user.click(screen.getByRole("checkbox", { name: "volume:fbcad_miniodata" }));
    const rpo = screen.getByRole("spinbutton", { name: "Maximum RPO" });
    await user.clear(rpo);
    await user.type(rpo, "2");
    await user.click(screen.getByRole("button", { name: "Save contract" }));
    await waitFor(() => expect(puts).toEqual([{ max_rpo_minutes: 120, required_components: ["config", "volume:fbcad_miniodata"] }]));
    expect(await screen.findByText("Recovery Contract saved")).toBeInTheDocument();
  });

  it("lists contracts violated first and filters by state from the URL", async () => {
    expect(sortContracts([ContractSchema.parse(CONTRACT_SATISFIED), ContractSchema.parse(CONTRACT_VIOLATED)]).map((c) => c.state)).toEqual([
      "violated",
      "satisfied",
    ]);
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ "GET /api/v1/contracts": () => Response.json({ items: [CONTRACT_SATISFIED, CONTRACT_VIOLATED] }) })),
    );
    nav.search = "state=violated";
    renderWithQuery(
      <CurrentUserProvider me={meWith(READ)}>
        <ContractsView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Recovery Contracts" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-state", "violated");
    expect(rows[0]).toHaveTextContent("no recovery point exists");
    expect(rows[0]).toHaveTextContent("1 hour");
    expect(screen.getByText(/2 contracts, 1 violated/)).toBeInTheDocument();
  });
});

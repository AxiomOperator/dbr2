// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { draftToLimits, HostLimitsCard, limitsToDraft } from "@/components/hosts/host-limits-card";
import { HostSettingsSchema } from "@/lib/api/protection-schemas";
import { meWith } from "@/test/fleet-fixtures";
import { HOST_ID, HOST_SETTINGS } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

describe("host limits draft", () => {
  it("converts the window between minutes of the day and HH:MM", () => {
    const draft = limitsToDraft(HostSettingsSchema.parse(HOST_SETTINGS));
    expect(draft).toEqual({ jobs: "2", noWindow: false, start: "22:00", end: "05:30", timezone: "America/Chicago" });
    expect(draftToLimits(draft)).toEqual({ ok: true, value: HOST_SETTINGS });
  });

  it("sends null bounds for “no window”", () => {
    const draft = limitsToDraft({ ...HOST_SETTINGS, backup_window_start: null, backup_window_end: null });
    expect(draft.noWindow).toBe(true);
    expect(draftToLimits({ ...draft, jobs: "4" })).toEqual({
      ok: true,
      value: { max_concurrent_jobs: 4, backup_window_start: null, backup_window_end: null, backup_window_timezone: "America/Chicago" },
    });
  });

  it("rejects bad jobs, times and timezones", () => {
    const d = limitsToDraft(HostSettingsSchema.parse(HOST_SETTINGS));
    expect(draftToLimits({ ...d, jobs: "0" })).toMatchObject({ ok: false, error: /At least 1/ });
    expect(draftToLimits({ ...d, jobs: "17" })).toMatchObject({ ok: false, error: /At most 16/ });
    expect(draftToLimits({ ...d, jobs: "" })).toMatchObject({ ok: false });
    expect(draftToLimits({ ...d, start: "" })).toMatchObject({ ok: false, error: /HH:MM/ });
    expect(draftToLimits({ ...d, timezone: "Mars/Olympus" })).toMatchObject({ ok: false, error: /IANA/ });
  });
});

describe("HostLimitsCard", () => {
  it("is read-only without host.manage", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/agents/${HOST_ID}/settings`]: () => Response.json(HOST_SETTINGS) })),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read"])}>
        <HostLimitsCard agentId={HOST_ID} hostname="docker-prod-01" />
      </CurrentUserProvider>,
    );
    expect(await screen.findByLabelText("Maximum concurrent jobs")).toBeDisabled();
    expect(screen.getByLabelText("Starts")).toHaveValue("22:00");
    expect(screen.getByTestId("window-summary")).toHaveTextContent(
      "22:00–05:30 (overnight, wraps midnight) (America/Chicago)",
    );
    expect(screen.queryByRole("button", { name: "Save limits" })).not.toBeInTheDocument();
  });

  it("saves “no window” and warns that the agent reconnects", async () => {
    const puts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/agents/${HOST_ID}/settings`]: () => Response.json(HOST_SETTINGS),
          [`PUT /api/v1/agents/${HOST_ID}/settings`]: (init) => {
            const body = JSON.parse(String(init?.body));
            puts.push(body);
            return Response.json(body);
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["host.read", "host.manage"])}>
        <HostLimitsCard agentId={HOST_ID} hostname="docker-prod-01" />
      </CurrentUserProvider>,
    );
    const jobs = await screen.findByLabelText("Maximum concurrent jobs");
    expect(screen.getByText("Saving makes the agent reconnect.")).toBeInTheDocument();
    await user.clear(jobs);
    await user.type(jobs, "4");
    await user.click(screen.getByRole("checkbox", { name: /No window/ }));
    expect(screen.queryByLabelText("Starts")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    await waitFor(() =>
      expect(puts).toEqual([
        { max_concurrent_jobs: 4, backup_window_start: null, backup_window_end: null, backup_window_timezone: "America/Chicago" },
      ]),
    );
    expect(await screen.findByText("The agent reconnects to apply the new concurrency limit.")).toBeInTheDocument();
  });
});

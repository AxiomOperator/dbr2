// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import {
  ChannelsPanel,
  channelToDraft,
  draftToCreateRequest,
  draftToUpdateRequest,
  newChannelDraft,
} from "@/components/notifications/channels";
import {
  customPatterns,
  EventPicker,
  toggleWildcard,
} from "@/components/notifications/event-picker";
import { NotificationsView } from "@/components/notifications/notifications-view";
import {
  SmtpSettingsPanel,
  smtpDraftToRequest,
  smtpToDraft,
} from "@/components/notifications/smtp-settings";
import {
  NotificationChannelSchema,
  SmtpSettingsSchema,
} from "@/lib/api/policy-schemas";
import { meWith } from "@/test/fleet-fixtures";
import {
  CHANNEL_EMAIL,
  CHANNEL_WEBHOOK,
  DELIVERIES,
  SMTP,
} from "@/test/phase7-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const nav = vi.hoisted(() => ({ replace: vi.fn(), search: "" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace }),
  usePathname: () => "/notifications",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

beforeEach(() => {
  nav.replace.mockReset();
  nav.search = "";
});

const MANAGE = ["policy.read", "policy.manage", "backup.read"];
const webhook = () => NotificationChannelSchema.parse(CHANNEL_WEBHOOK);

describe("channel drafts: write-only secret semantics", () => {
  it("creates webhook channels with a secret only when one is set", () => {
    const d = {
      ...newChannelDraft("webhook"),
      name: "Hook",
      url: "https://hooks.example.com/x",
      events: [],
    };
    expect(draftToCreateRequest(d)).toEqual({
      ok: true,
      value: {
        kind: "webhook",
        name: "Hook",
        enabled: true,
        config: { url: "https://hooks.example.com/x" },
        events: [],
        min_severity: "warning",
      },
    });
    expect(
      draftToCreateRequest({ ...d, secretAction: "replace", secret: "short" }),
    ).toMatchObject({ ok: false, error: /at least 16/ });
    expect(
      draftToCreateRequest({
        ...d,
        secretAction: "replace",
        secret: "0123456789abcdef", // gitleaks:allow (test fixture)
      }),
    ).toMatchObject({
      // gitleaks:allow (test fixture)
      ok: true,
      value: { secret: "0123456789abcdef" }, // gitleaks:allow (test fixture)
    });
  });

  it("omits the secret to keep it, sends an empty one to remove it, a new one to replace it", () => {
    const d = channelToDraft(webhook());
    expect(d).toMatchObject({
      secretAction: "keep",
      url: CHANNEL_WEBHOOK.config.url,
      events: ["backup.failed", "contract.*"],
    });
    const keep = draftToUpdateRequest(d);
    expect(keep.ok && "secret" in keep.value).toBe(false);
    expect(
      draftToUpdateRequest({ ...d, secretAction: "remove" }),
    ).toMatchObject({ ok: true, value: { secret: "" } });
    expect(
      draftToUpdateRequest({
        ...d,
        secretAction: "replace",
        secret: "a-brand-new-secret-value",
      }),
    ).toMatchObject({
      ok: true,
      value: { secret: "a-brand-new-secret-value" },
    });
    const upd = draftToUpdateRequest(d);
    expect(upd.ok && "kind" in upd.value).toBe(false);
  });

  it("validates email recipients, webhook URLs and event patterns", () => {
    const email = { ...newChannelDraft("email"), name: "Mail" };
    expect(draftToCreateRequest({ ...email, recipients: "" })).toMatchObject({
      ok: false,
      error: /at least one recipient/,
    });
    expect(
      draftToCreateRequest({ ...email, recipients: "ops@example.com, nope" }),
    ).toMatchObject({ ok: false, error: /“nope”/ });
    expect(
      draftToCreateRequest({
        ...email,
        recipients: "ops@example.com\nb@example.com",
      }),
    ).toMatchObject({
      ok: true,
      value: { config: { to: ["ops@example.com", "b@example.com"] } },
    });
    const hook = { ...newChannelDraft("webhook"), name: "Hook" };
    expect(
      draftToCreateRequest({ ...hook, url: "http://example.com" }),
    ).toMatchObject({ ok: false, error: /https/ });
    expect(
      draftToCreateRequest({
        ...hook,
        url: "https://x.example.com",
        events: ["Backup!"],
      }),
    ).toMatchObject({
      ok: false,
      error: /not an event pattern/,
    });
  });
});

describe("EventPicker", () => {
  it("toggles group wildcards and keeps custom patterns", () => {
    expect(
      toggleWildcard(["backup.failed", "agent.offline"], "backup", true),
    ).toEqual(["agent.offline", "backup.*"]);
    expect(toggleWildcard(["backup.*"], "backup", false)).toEqual([]);
    expect(
      customPatterns(["backup.*", "repository.*", "backup.failed"]),
    ).toEqual(["repository.*"]);
  });

  it("covers a group with its wildcard and adds validated custom patterns", async () => {
    let latest: string[] = [];
    function Harness() {
      const [v, setV] = useState<string[]>(["backup.failed"]);
      latest = v;
      return <EventPicker value={v} onChange={setV} />;
    }
    const user = userEvent.setup();
    renderWithQuery(<Harness />);
    expect(screen.getByTestId("events-summary")).toHaveTextContent(
      "Delivers 1 of the 18 listed event types",
    );
    await user.click(screen.getByRole("checkbox", { name: "backup.*" }));
    expect(latest).toEqual(["backup.*"]);
    const failed = screen.getByRole("checkbox", { name: /^backup\.failed/ });
    expect(failed).toBeChecked();
    expect(failed).toBeDisabled();
    expect(screen.getByTestId("events-summary")).toHaveTextContent(
      "Delivers 3 of",
    );

    await user.type(
      screen.getByLabelText("Other event type or wildcard"),
      "Repository*{Enter}",
    );
    expect(
      screen.getByText(/prefix wildcard such as backup\.\*/),
    ).toBeInTheDocument();
    await user.clear(screen.getByLabelText("Other event type or wildcard"));
    await user.type(
      screen.getByLabelText("Other event type or wildcard"),
      "repository.*{Enter}",
    );
    expect(latest).toEqual(["backup.*", "repository.*"]);
    expect(screen.getByTestId("events-summary")).toHaveTextContent(
      "plus 1 other pattern",
    );
    await user.click(
      screen.getByRole("button", { name: "Remove repository.*" }),
    );
    expect(latest).toEqual(["backup.*"]);

    await user.click(screen.getByRole("checkbox", { name: /All events/ }));
    expect(latest).toEqual([]);
    expect(screen.getByTestId("events-summary")).toHaveTextContent(
      "Every event is delivered",
    );
  });
});

describe("ChannelsPanel", () => {
  it("lists channels with the last error, sends a test and shows the deliveries", async () => {
    let tests = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/notification-channels": () =>
            Response.json({ items: [CHANNEL_EMAIL, CHANNEL_WEBHOOK] }),
          [`POST /api/v1/notification-channels/${CHANNEL_WEBHOOK.id}/test`]:
            () => {
              tests++;
              return Response.json({
                delivered: false,
                duration_ms: 1200,
                error: "503 Service Unavailable",
              });
            },
          [`POST /api/v1/notification-channels/${CHANNEL_EMAIL.id}/test`]: () =>
            Response.json({ delivered: true, duration_ms: 84 }),
          [`GET /api/v1/notification-channels/${CHANNEL_WEBHOOK.id}/deliveries`]:
            () => Response.json({ items: DELIVERIES }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <ChannelsPanel />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", {
      name: "Notification channels",
    });
    const [email, hook] = within(table).getAllByRole("row").slice(1);
    expect(email).toHaveTextContent("ops@example.com, oncall@example.com");
    expect(email).toHaveTextContent("All events");
    expect(hook).toHaveTextContent("backup.failed, contract.*");
    expect(within(hook!).getByText("Signed")).toBeInTheDocument();
    expect(within(hook!).getByTestId("last-error")).toHaveTextContent(
      "503 Service Unavailable",
    );

    await user.click(
      within(email!).getByRole("button", {
        name: "Send a test message to Ops email",
      }),
    );
    expect(await within(email!).findByTestId("test-result")).toHaveTextContent(
      "Delivered in 84 ms",
    );
    await user.click(
      within(hook!).getByRole("button", {
        name: "Send a test message to PagerDuty webhook",
      }),
    );
    expect(
      await screen.findByText("Test message to PagerDuty webhook failed"),
    ).toBeInTheDocument();
    expect(tests).toBe(1);

    await user.click(
      within(hook!).getByRole("button", {
        name: "Deliveries of PagerDuty webhook",
      }),
    );
    const drawer = await screen.findByRole("dialog", {
      name: "Deliveries: PagerDuty webhook",
    });
    const rows = within(
      await within(drawer).findByRole("table", { name: "Deliveries" }),
    )
      .getAllByRole("row")
      .slice(1);
    expect(rows[0]).toHaveAttribute("data-state", "failed");
    expect(rows[1]).toHaveTextContent("Retrying");
    expect(rows[1]).toHaveTextContent("Next attempt");
  });

  it("offers keep / replace / remove for a stored secret when editing", async () => {
    const puts: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/notification-channels": () =>
            Response.json({ items: [CHANNEL_WEBHOOK] }),
          [`PUT /api/v1/notification-channels/${CHANNEL_WEBHOOK.id}`]: (
            init,
          ) => {
            const body = JSON.parse(String(init?.body));
            puts.push(body);
            return Response.json({ ...CHANNEL_WEBHOOK, secret_set: false });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <ChannelsPanel />
      </CurrentUserProvider>,
    );
    await user.click(
      await screen.findByRole("button", {
        name: "Edit channel PagerDuty webhook",
      }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit PagerDuty webhook",
    });
    expect(
      within(dialog).getByRole("combobox", { name: "Type" }),
    ).toBeDisabled();
    expect(
      within(dialog).getByRole("radio", { name: "Keep the stored secret" }),
    ).toBeChecked();
    await user.click(
      within(dialog).getByRole("radio", {
        name: "Remove it (unsigned requests)",
      }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Save channel" }),
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0]).toMatchObject({
      secret: "",
      config: { url: CHANNEL_WEBHOOK.config.url },
    });
  });

  it("keeps the selected tab in the URL", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/settings/smtp": () => Response.json(SMTP),
          "GET /api/v1/notification-channels": () =>
            Response.json({ items: [] }),
        }),
      ),
    );
    nav.search = "tab=smtp";
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["policy.read"])}>
        <NotificationsView />
      </CurrentUserProvider>,
    );
    expect(
      screen.queryByRole("tab", { name: "Alerts" }),
    ).not.toBeInTheDocument();
    expect(await screen.findByLabelText("SMTP host")).toHaveValue(
      "smtp.example.com",
    );
    await user.click(screen.getByRole("tab", { name: "Channels" }));
    // "channels" is this user's first tab, so it is the default and leaves the URL clean.
    expect(nav.replace).toHaveBeenCalledWith("/notifications", {
      scroll: false,
    });
  });
});

describe("SMTP settings", () => {
  it("keeps, removes or replaces the write-only password", () => {
    const d = smtpToDraft(SmtpSettingsSchema.parse(SMTP));
    const keep = smtpDraftToRequest(d);
    expect(keep).toEqual({
      ok: true,
      value: {
        host: "smtp.example.com",
        port: 587,
        tls: "starttls",
        username: "dbr2",
        from: "DBR2 <dbr2@example.com>",
      },
    });
    expect(
      smtpDraftToRequest({ ...d, passwordAction: "remove" }),
    ).toMatchObject({ ok: true, value: { password: "" } });
    expect(
      smtpDraftToRequest({ ...d, passwordAction: "replace", password: "" }),
    ).toMatchObject({ ok: false });
    expect(
      smtpDraftToRequest({
        ...d,
        passwordAction: "replace",
        password: "s3cret",
      }),
    ).toMatchObject({ ok: true, value: { password: "s3cret" } });
    expect(smtpDraftToRequest({ ...d, port: "70000" })).toMatchObject({
      ok: false,
      error: "The port is 1–65535.",
    });
  });

  it("saves without sending the password unless it changes", async () => {
    const puts: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/settings/smtp": () => Response.json(SMTP),
          "PUT /api/v1/settings/smtp": (init) => {
            const body = JSON.parse(String(init?.body));
            puts.push(body);
            return Response.json({
              ...SMTP,
              ...body,
              updated_at: "2026-09-25T10:00:00Z",
            });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(MANAGE)}>
        <SmtpSettingsPanel />
      </CurrentUserProvider>,
    );
    const host = await screen.findByLabelText("SMTP host");
    expect(
      screen.getByRole("radio", { name: "Keep the stored password" }),
    ).toBeChecked();
    await user.clear(host);
    await user.type(host, "relay.example.com");
    await user.click(
      screen.getByRole("button", { name: "Save SMTP settings" }),
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0]).toMatchObject({ host: "relay.example.com" });
    expect("password" in puts[0]!).toBe(false);
    expect(await screen.findByText("SMTP settings saved")).toBeInTheDocument();
  });
});

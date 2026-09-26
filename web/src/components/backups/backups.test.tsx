// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import {
  alertTargetHref,
  AlertsCard,
  AlertsView,
  countBySeverity,
  sortAlerts,
} from "@/components/backups/alerts";
import { ApplicationBackups } from "@/components/backups/application-backups";
import { BackUpNowButton, startBackupErrorMessage } from "@/components/backups/back-up-now";
import { RecoveryPointStateBadge, SeverityBadge } from "@/components/backups/backup-badges";
import {
  BackupSettingsCard,
  componentRows,
  draftToRequest,
  settingsToDraft,
} from "@/components/backups/backup-settings-card";
import { formatOwner, RecoveryPointDetail } from "@/components/backups/recovery-point-detail";
import { parseRecoveryPointFilters } from "@/components/backups/recovery-points-view";
import { ApiError } from "@/lib/api/client";
import { ApplicationDetailSchema } from "@/lib/api/fleet-schemas";
import { AlertListSchema, BackupSettingsSchema } from "@/lib/api/protection-schemas";
import { APP_DETAIL, meWith } from "@/test/fleet-fixtures";
import {
  ALERTS,
  APP_ID,
  BACKUP_SETTINGS,
  HOST_ID,
  MANIFEST,
  REPO_ID,
  REPO_READY,
  RP_COMMITTED,
  RP_FAILED,
  RP_ID,
  RP_PARTIAL,
  RP_PARTIAL_ID,
} from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const app = ApplicationDetailSchema.parse(APP_DETAIL);
const settings = BackupSettingsSchema.parse(BACKUP_SETTINGS);
const alerts = AlertListSchema.parse({ items: ALERTS }).items;

function problem(status: number, code: string, detail: string) {
  return new Response(JSON.stringify({ title: "Error", status, detail, code }), {
    status,
    headers: { "content-type": "application/problem+json" },
  });
}

describe("badges", () => {
  it("shows recovery point states and alert severities as text", () => {
    const { rerender } = renderWithQuery(<RecoveryPointStateBadge state="failed" />);
    expect(screen.getByText("Failed")).toHaveAttribute("data-variant", "destructive");
    rerender(<RecoveryPointStateBadge state="pending" />);
    expect(screen.getByText("In progress")).toBeInTheDocument();
    rerender(<SeverityBadge severity="critical" />);
    expect(screen.getByText("Critical")).toHaveAttribute("data-variant", "destructive");
    rerender(<SeverityBadge severity="warning" />);
    expect(screen.getByText("Warning")).toHaveClass("text-amber-700");
  });
});

describe("Back up now", () => {
  it("explains a 409 as an operation already running", () => {
    expect(startBackupErrorMessage(new ApiError({ status: 409, code: "conflict", detail: "busy" }))).toBe(
      "An operation is already running for this application. Wait for it to finish, then try again.",
    );
    expect(startBackupErrorMessage(new ApiError({ status: 403, code: "forbidden" }))).toMatch(/permission/);
  });

  it("starts a backup with the settings' mode and reports the workflow", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`POST /api/v1/applications/${APP_ID}/backups`]: (init) => {
            bodies.push(JSON.parse(String(init?.body)));
            return Response.json({ workflow_id: "backup-wf-7" }, { status: 202 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<BackUpNowButton applicationId={APP_ID} name="Web shop" effectiveMode="quiesced" />);
    await user.click(screen.getByRole("button", { name: "Back up now" }));
    const dialog = await screen.findByRole("dialog", { name: "Back up Web shop now" });
    expect(within(dialog).getByRole("combobox")).toHaveTextContent("Use the backup settings (Quiesced)");
    await user.click(within(dialog).getByRole("button", { name: "Start backup" }));
    // The dialog switches to the live progress view and a toast reports the workflow.
    expect(await screen.findByRole("dialog", { name: "Backing up Web shop" })).toBeInTheDocument();
    expect((await screen.findAllByText("backup-wf-7")).length).toBeGreaterThan(0);
    expect(screen.getByTestId("job-progress-waiting")).toHaveTextContent("Backup running");
    expect(bodies).toEqual([{}]);
  });

  it("keeps the dialog open with the conflict message on 409", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`POST /api/v1/applications/${APP_ID}/backups`]: () =>
            problem(409, "conflict", "an operation is already running for this application"),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<BackUpNowButton applicationId={APP_ID} name="Web shop" />);
    await user.click(screen.getByRole("button", { name: "Back up now" }));
    const dialog = await screen.findByRole("dialog", { name: "Back up Web shop now" });
    await user.click(within(dialog).getByRole("button", { name: "Start backup" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("An operation is already running");
  });
});

describe("backup settings draft", () => {
  it("round-trips the saved settings unchanged", () => {
    const draft = settingsToDraft(settings);
    expect(draft.mode).toBe("automatic");
    expect(draft.quiesceMinutes).toBe("30");
    expect(draft.preHooks[0]?.command).toBe(`sh -c 'psql -U shop -c "CHECKPOINT"'`);
    const res = draftToRequest(draft, settings);
    expect(res).toEqual({
      ok: true,
      value: {
        repository_id: null,
        consistency_mode: null,
        max_quiesce_seconds: 1800,
        pre_hooks: [{ container: "db", command: ["sh", "-c", 'psql -U shop -c "CHECKPOINT"'], timeout_seconds: 120 }],
        post_hooks: [],
        optional_components: ["bind:/srv/shop/uploads"],
        excluded_components: [],
      },
    });
  });

  it("keeps odd seconds when the minutes are unchanged and converts edited minutes", () => {
    const odd = { ...settings, max_quiesce_seconds: 1830 };
    const draft = settingsToDraft(odd);
    expect(draft.quiesceMinutes).toBe("31");
    expect(draftToRequest(draft, odd)).toMatchObject({ ok: true, value: { max_quiesce_seconds: 1830 } });
    expect(draftToRequest({ ...draft, quiesceMinutes: "90" }, odd)).toMatchObject({
      ok: true,
      value: { max_quiesce_seconds: 5400 },
    });
  });

  it("validates the quiesce range and hook commands", () => {
    const draft = settingsToDraft(settings);
    for (const m of ["0", "1441", "1.5", ""]) {
      expect(draftToRequest({ ...draft, quiesceMinutes: m }, settings)).toMatchObject({ ok: false, error: /1–1440/ });
    }
    const hook = draft.preHooks[0]!;
    expect(draftToRequest({ ...draft, preHooks: [{ ...hook, command: "sh -c 'oops" }] }, settings)).toEqual({
      ok: false,
      error: "Pre-hook 1: Unterminated single quote in the command.",
    });
    expect(draftToRequest({ ...draft, preHooks: [{ ...hook, container: " " }] }, settings)).toMatchObject({
      ok: false,
      error: /Pre-hook 1: choose the container/,
    });
    expect(draftToRequest({ ...draft, postHooks: [{ ...hook, command: "   " }] }, settings)).toMatchObject({
      ok: false,
      error: "Post-hook 1: enter the command.",
    });
    expect(draftToRequest({ ...draft, preHooks: [{ ...hook, timeout: "3601" }] }, settings)).toMatchObject({
      ok: false,
      error: /timeout/,
    });
  });

  it("never sends a component as both optional and excluded, nor config as optional", () => {
    const draft = { ...settingsToDraft(settings), optional: ["config", "volume:a", "volume:b"], excluded: ["volume:b"] };
    expect(draftToRequest(draft, settings)).toMatchObject({
      ok: true,
      value: { optional_components: ["volume:a"], excluded_components: ["volume:b"] },
    });
  });

  it("lists the API's planned components plus configured names no longer in the analysis", () => {
    const rows = componentRows(app, { optional: ["bind:/srv/shop/uploads"], excluded: [] });
    expect(rows.map((r) => [r.name, r.detail])).toEqual([
      ["config", "/home/garrettpost/Projects/fbcad"],
      ["volume:fbcad_miniodata", "/var/lib/docker/volumes/fbcad_miniodata/_data"],
      ["bind:/srv/shop/uploads", "Not in the latest analysis"],
    ]);
  });
});

describe("BackupSettingsCard", () => {
  function stub(onPut?: (body: unknown) => void) {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/applications/${app.id}/backup-settings`]: () => Response.json(BACKUP_SETTINGS),
          "GET /api/v1/repositories": () => Response.json({ items: [REPO_READY] }),
          [`PUT /api/v1/applications/${app.id}/backup-settings`]: (init) => {
            const body = JSON.parse(String(init?.body));
            onPut?.(body);
            return Response.json({ ...body, effective_mode: "quiesced", updated_at: "2026-09-25T12:00:00Z" });
          },
        }),
      ),
    );
  }

  it("is read-only with policy.read", async () => {
    stub();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["application.read", "policy.read"])}>
        <BackupSettingsCard app={app} />
      </CurrentUserProvider>,
    );
    const quiesce = await screen.findByLabelText("Maximum quiesce (minutes)");
    expect(quiesce).toHaveValue(30);
    expect(quiesce).toBeDisabled();
    expect(screen.getByText(/Read-only: editing requires/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add pre-hook/ })).not.toBeInTheDocument();
    expect(screen.getByTestId("mode-help")).toHaveTextContent("Quiesced when pre- or post-hooks are defined");
    expect(screen.getByText("Quiesced", { selector: "strong" })).toBeInTheDocument();
  });

  it("saves edited minutes, a new post-hook and an excluded component with policy.manage", async () => {
    const puts: unknown[] = [];
    stub((b) => puts.push(b));
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["application.read", "policy.read", "policy.manage", "repository.read"])}>
        <BackupSettingsCard app={app} />
      </CurrentUserProvider>,
    );
    const quiesce = await screen.findByLabelText("Maximum quiesce (minutes)");
    await user.clear(quiesce);
    await user.type(quiesce, "45");

    await user.click(screen.getByRole("button", { name: "Add post-hook" }));
    await user.type(screen.getByLabelText("Post-hook 1: container"), "fbcad-minio-1");
    await user.type(screen.getByLabelText("Post-hook 1: command"), "mc admin service unfreeze local");
    const args = screen.getAllByRole("list", { name: "Arguments" }).at(-1)!;
    expect(within(args).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "mc",
      "admin",
      "service",
      "unfreeze",
      "local",
    ]);

    await user.click(screen.getByRole("checkbox", { name: "volume:fbcad_miniodata excluded" }));
    expect(screen.getByRole("checkbox", { name: "volume:fbcad_miniodata optional" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "config optional" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Save settings" }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0]).toEqual({
      repository_id: null,
      consistency_mode: null,
      max_quiesce_seconds: 2700,
      pre_hooks: [{ container: "db", command: ["sh", "-c", 'psql -U shop -c "CHECKPOINT"'], timeout_seconds: 120 }],
      post_hooks: [{ container: "fbcad-minio-1", command: ["mc", "admin", "service", "unfreeze", "local"] }],
      optional_components: ["bind:/srv/shop/uploads"],
      excluded_components: ["volume:fbcad_miniodata"],
    });
    expect(await screen.findByText("Backup settings saved")).toBeInTheDocument();
  });
});

describe("recovery points", () => {
  it("parses URL filters and ignores unknown states", () => {
    expect(parseRecoveryPointFilters(new URLSearchParams("application=abc&state=failed"))).toEqual({
      application: "abc",
      state: "failed",
    });
    expect(parseRecoveryPointFilters(new URLSearchParams("state=bogus"))).toEqual({ application: "all", state: "all" });
  });

  it("lists an application's recovery points with state, status, mode and error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        expect(url).toContain(`application_id=${APP_ID}`);
        return Promise.resolve(Response.json({ items: [RP_COMMITTED, RP_PARTIAL, RP_FAILED] }));
      }),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "restore.execute"])}>
        <ApplicationBackups applicationId={APP_ID} />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Recovery points of this application" });
    const rows = within(table).getAllByRole("row").slice(1);
    // Restore… only for committed recovery points (restore.execute).
    expect(within(rows[0]!).getByRole("link", { name: `Restore ${RP_ID}` })).toHaveAttribute(
      "href",
      `/recovery-points/${RP_ID}/restore`,
    );
    expect(within(rows[2]!).queryByRole("link", { name: /^Restore / })).not.toBeInTheDocument();
    expect(rows.map((r) => r.getAttribute("data-rp-state"))).toEqual(["committed", "committed", "failed"]);
    expect(within(rows[0]!).getByRole("link", { name: RP_ID })).toHaveAttribute("href", `/recovery-points/${RP_ID}`);
    expect(within(rows[0]!).getByText("Quiesced")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Partial")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Crash-consistent")).toBeInTheDocument();
    expect(within(rows[2]!).getByText("pre-hook 1 (db) exited with status 2")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open in Recovery points" })).toHaveAttribute(
      "href",
      `/recovery-points?application=${APP_ID}`,
    );
  });

  it("hides Restore… without restore.execute", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(Response.json({ items: [RP_COMMITTED] }))));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <ApplicationBackups applicationId={APP_ID} />
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Recovery points of this application" });
    expect(screen.queryByRole("link", { name: /^Restore / })).not.toBeInTheDocument();
  });

  it("formats component ownership", () => {
    expect(formatOwner({ owner_uid: 999, owner_gid: 999, mode: "0700" })).toBe("999:999 · 0700");
    expect(formatOwner({})).toBe("—");
  });

  it("shows the manifest components and the raw JSON on demand", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () => Response.json({ ...RP_COMMITTED, manifest: MANIFEST }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "application.read"])}>
        <RecoveryPointDetail id={RP_ID} />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Components" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    expect(within(rows[1]!).getByText("volume:shop_pgdata")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("system_u:object_r:container_file_t:s0")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("999:999 · 0700")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("2143")).toBeInTheDocument();
    expect(within(rows[2]!).getByText(/Ownership, modes and SELinux labels of\s+volume:shop_pgdata/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Web shop" })).toHaveAttribute("href", `/applications/${APP_ID}`);
    // Repository link only with repository.read.
    expect(screen.queryByRole("link", { name: REPO_ID })).not.toBeInTheDocument();

    expect(screen.queryByTestId("manifest-json")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Raw manifest (JSON)" }));
    expect(await screen.findByTestId("manifest-json")).toHaveTextContent('"schema_version": 1');
  });

  it("explains a failed recovery point without a manifest", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ [`GET /api/v1/recovery-points/${RP_FAILED.id}`]: () => Response.json(RP_FAILED) })),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <RecoveryPointDetail id={RP_FAILED.id} />
      </CurrentUserProvider>,
    );
    expect(await screen.findByText("Backup failed")).toBeInTheDocument();
    expect(screen.getByText(/the backup failed before it was committed/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Raw manifest (JSON)" })).not.toBeInTheDocument();
  });
});

describe("alerts", () => {
  it("counts open alerts by severity and sorts critical first, newest first", () => {
    expect(countBySeverity(alerts)).toEqual({ critical: 2, warning: 1, info: 0 });
    expect(sortAlerts(alerts).map((a) => a.id)).toEqual([42, 44, 43, 41]);
  });

  it("links alerts to their target only when permitted", () => {
    const [partial, failed, agent, repo] = alerts;
    const all = meWith(["backup.read", "application.read", "host.read", "repository.read"]);
    expect(alertTargetHref(partial!, all)).toBe(`/recovery-points/${RP_PARTIAL_ID}`);
    expect(alertTargetHref(failed!, all)).toBe(`/applications/${APP_ID}`);
    expect(alertTargetHref(agent!, all)).toBe(`/hosts/${HOST_ID}`);
    expect(alertTargetHref(repo!, all)).toBe(`/repositories/${REPO_ID}`);
    expect(alertTargetHref(failed!, meWith(["backup.read"]))).toBeNull();
  });

  it("acknowledges an alert with backup.execute", async () => {
    const acked: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/alerts": () => Response.json({ items: ALERTS.filter((a) => !a.acknowledged_at) }),
          "POST /api/v1/alerts/44/acknowledge": () => {
            acked.push("44");
            return new Response(null, { status: 204 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "backup.execute"])}>
        <AlertsView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Alerts" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-severity"))).toEqual(["critical", "critical", "warning"]);
    await user.click(
      screen.getByRole("button", { name: "Acknowledge alert: Backup of Web shop failed: pre-hook 1 (db) exited with status 2" }),
    );
    await waitFor(() => expect(acked).toEqual(["44"]));
  });

  it("hides acknowledge without backup.execute", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/alerts": () => Response.json({ items: ALERTS }) })));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <AlertsView />
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Alerts" });
    expect(screen.queryByRole("button", { name: /Acknowledge/ })).not.toBeInTheDocument();
  });

  it("summarises open alerts on the dashboard card", async () => {
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/alerts": () => Response.json({ items: ALERTS }) })));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <AlertsCard />
      </CurrentUserProvider>,
    );
    const counts = await screen.findByRole("list", { name: "Open alerts by severity" });
    expect(within(counts).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "2Critical",
      "1Warning",
      "0Info",
    ]);
    const latest = screen.getByRole("list", { name: "Latest alerts" });
    expect(within(latest).getAllByRole("listitem")[0]).toHaveTextContent("docker-prod-01 resumed wiki");
    expect(screen.getByRole("link", { name: "View all alerts" })).toHaveAttribute("href", "/notifications");
  });
});

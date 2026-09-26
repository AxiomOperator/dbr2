// SPDX-License-Identifier: Apache-2.0
//
// Applications with protection status, the Topology tab, "Back up now" with
// live progress over SSE, the Jobs page, the recovery point browser, the
// restore wizard through to a succeeded restore, and the create-Repository
// wizard.

import { APPS, expect, loginAsAdmin, mainNav, test } from "./fixtures";

test("applications list → detail with protection card and topology", async ({ page }) => {
  await loginAsAdmin(page, "/applications");
  const table = page.getByRole("table").first();
  const shopRow = table.getByRole("row").filter({ has: page.getByRole("link", { name: "Web shop" }) });
  await expect(shopRow.getByText("Protected", { exact: true })).toBeVisible();
  await expect(shopRow.getByText(/\d+\/\d+/)).toBeVisible();

  // Filter by protection status (kept in the URL).
  await page.getByRole("combobox", { name: "Protection" }).click();
  await page.getByRole("option", { name: "Failed" }).click();
  await expect(page).toHaveURL(/status=failed/);
  await expect(table.getByRole("link", { name: "redis-cache" })).toBeVisible();
  await expect(table.getByRole("link", { name: "Web shop" })).toHaveCount(0);

  await page.goto(`/applications/${APPS.shop}`);
  const card = page.getByTestId("protection-card");
  await expect(card).toBeVisible();
  await expect(card.getByRole("table")).toContainText("volume:shop_pgdata");
  await expect(card).toContainText(/external dependencies are not protected/);

  await page.getByRole("tab", { name: "Topology" }).click();
  const graph = page.getByTestId("topology-graph");
  await expect(graph).toBeVisible();
  await expect(graph.locator('[data-kind="container"]')).toHaveCount(3);
  await expect(graph.locator('[data-kind="volume"][data-coverage="protected"]')).toContainText("shop_pgdata");
  await expect(graph.locator('[data-kind="bind"]')).toContainText("/srv/shop/uploads");
  await expect(graph.locator('[data-kind="network"]').filter({ hasText: "proxy" })).toContainText("external");
});

test("back up now shows live per-component progress and the job finishes", async ({ page }) => {
  await loginAsAdmin(page, `/applications/${APPS.mftPg}`);
  await expect(page.getByTestId("live-indicator")).toHaveAttribute("data-status", "live");
  await page.getByRole("button", { name: "Back up now" }).click();
  const dialog = page.getByRole("dialog", { name: "Back up mft-pg now" });
  await dialog.getByRole("button", { name: "Start backup" }).click();

  const running = page.getByRole("dialog", { name: "Backing up mft-pg" });
  await expect(running).toBeVisible();
  const progress = running.getByTestId("job-progress");
  await expect(progress).toContainText(/Component [12] of 2/);
  await expect(progress).toContainText("Hashed");
  await expect(progress.getByRole("progressbar", { name: "Components finished" })).toBeVisible();
  await expect(progress).toContainText(/Backup: finished/, { timeout: 20_000 });
  // The commit raises an alert, shown as a toast.
  await expect(page.getByText(/Backup of mft-pg committed/).first()).toBeVisible();
  await running.getByRole("button", { name: "Close" }).first().click();

  // The protection card re-reads the application after the backup.updated event.
  await expect(page.getByTestId("protection-card")).toContainText("Protected");

  await mainNav(page).getByRole("link", { name: "Jobs" }).click();
  const jobs = page.getByRole("table", { name: "Jobs" });
  const row = jobs.getByRole("row").filter({ hasText: "mft-pg" }).first();
  await expect(row).toHaveAttribute("data-state", "succeeded");
  await row.getByRole("link", { name: /Backup/ }).click();
  await expect(page).toHaveURL(/\/recovery-points\/rp_/);
  await expect(page.getByTestId("rp-status")).toContainText("Complete");
});

test("recovery point browser: status, nested fsmeta and topology", async ({ page }) => {
  await loginAsAdmin(page, `/recovery-points?application=${APPS.shop}&state=committed`);
  const table = page.getByRole("table", { name: "Recovery points" });
  const partial = table.getByRole("row").filter({ hasText: "Partial" }).first();
  await partial.getByRole("link").first().click();
  await expect(page.getByTestId("rp-status")).toContainText("Partial");
  await expect(page.getByText("Partial recovery point")).toBeVisible();
  const components = page.getByRole("table", { name: "Components" });
  const rows = components.getByRole("row");
  const names = await rows.locator("td:first-child .font-mono").allTextContents();
  const idx = names.indexOf("volume:shop_pgdata");
  expect(names[idx + 1]).toBe("fsmeta:volume:shop_pgdata");
  await expect(page.getByRole("table", { name: "Containers at capture time" })).toContainText("shop-db-1");
  await expect(page.getByRole("heading", { name: "Databases" })).toBeVisible();
});

test("restore wizard: preview, typed confirmation, then the restore succeeds live", async ({ page }) => {
  await loginAsAdmin(page, "/restores");
  await page.getByRole("link", { name: "Start a restore" }).click();
  await expect(page).toHaveURL(/\/restores\/new/);
  await page.getByRole("combobox", { name: "Application" }).click();
  await page.getByRole("option", { name: /Web shop/ }).click();
  const table = page.getByRole("table", { name: "Committed recovery points" });
  const complete = table.getByRole("row").filter({ hasText: "Complete" }).first();
  await complete.getByRole("link", { name: /^Restore / }).click();
  await expect(page).toHaveURL(/\/recovery-points\/rp_[^/]+\/restore$/);

  // Target: the source host (in place) by default.
  await page.getByRole("button", { name: "Preview impact" }).click();
  await expect(page.getByTestId("production-banner")).toBeVisible();
  await page.getByRole("button", { name: "Continue" }).click();

  const start = page.getByRole("button", { name: "Start restore" });
  await expect(start).toBeDisabled();
  await page.getByLabel(/^Reason/).fill("E2E-1: restore drill");
  await page.getByLabel(/to confirm/).fill("web shop");
  await expect(start).toBeDisabled();
  await page.getByLabel(/to confirm/).fill("Web shop");
  await expect(start).toBeEnabled();
  await start.click();

  await expect(page).toHaveURL(/\/restores\/rs_/);
  await expect(page.getByRole("heading", { name: /Restore of Web shop to docker-prod-01/ })).toBeVisible();
  await expect(page.getByText("Updates live while the restore runs.")).toBeVisible();
  await expect(page.getByText("Restore succeeded")).toBeVisible({ timeout: 30_000 });

  // The Jobs page lists it.
  await mainNav(page).getByRole("link", { name: "Jobs" }).click();
  await expect(
    page.getByRole("table", { name: "Jobs" }).getByRole("row").filter({ hasText: "Web shop" }).filter({ hasText: "Restore" }).first(),
  ).toHaveAttribute("data-state", "succeeded");
});

test("create Repository wizard: package, escrow confirmation, ready", async ({ page }) => {
  await loginAsAdmin(page, "/repositories");
  await page.getByRole("button", { name: "Create Repository" }).click();
  const dialog = page.getByRole("dialog", { name: "Create Repository" });
  await dialog.getByLabel("Name", { exact: true }).fill("e2e-repo");
  await dialog.getByLabel("Server URL", { exact: true }).fill("https://reposerver.example.com:51515");
  const created = page.waitForResponse((r) => r.url().endsWith("/api/v1/repositories") && r.request().method() === "POST");
  await dialog.getByRole("button", { name: "Create Repository" }).click();
  const body = (await (await created).json()) as { escrow_package: string };
  const code = /confirmation_code ([A-Z2-7-]+)/.exec(body.escrow_package)?.[1];
  expect(code).toBeTruthy();

  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: /^Download / }).click();
  expect((await download).suggestedFilename()).toMatch(/\.age$/);
  await dialog.getByRole("button", { name: "I stored it offline: next" }).click();
  await dialog.getByLabel("Confirmation code").fill(code!.toLowerCase());
  await dialog.getByRole("button", { name: "Confirm escrow" }).click();
  await expect(dialog.getByText(/Key escrow is confirmed/)).toBeVisible();
  await dialog.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("link", { name: "e2e-repo" })).toBeVisible();
});

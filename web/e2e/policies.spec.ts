// SPDX-License-Identifier: Apache-2.0
//
// Phases 7–9: create a Protection Policy and assign an application; delete a
// recovery point with the 7-day grace period and undelete it; add a webhook
// notification channel and send test messages; escrow health and drills.

import { APPS, expect, loginAsAdmin, test } from "./fixtures";

test("create a policy with a custom schedule, then assign an application", async ({ page }) => {
  await loginAsAdmin(page, "/policies");
  await page.getByRole("button", { name: "Create policy" }).click();
  const dialog = page.getByRole("dialog", { name: "Create policy" });
  await dialog.getByLabel("Name", { exact: true }).fill("E2E weekdays");
  await dialog.getByRole("combobox", { name: "Frequency" }).click();
  await page.getByRole("option", { name: "Custom (cron expression)" }).click();
  const cron = dialog.getByLabel("Cron expression");
  await cron.fill("61 22 * * 1-5");
  await expect(dialog.getByTestId("schedule-preview")).toContainText("Minute: 61 is out of range 0–59");
  await cron.fill("30 22 * * 1-5");
  await expect(dialog.getByTestId("schedule-preview")).toContainText("Every Monday–Friday at 22:30");
  await dialog.getByLabel("Timezone").fill("Europe/Berlin");
  await dialog.getByLabel(/^Keep last/).fill("10");
  await expect(dialog.getByTestId("retention-summary")).toContainText("Last 10 · 14 daily · 8 weekly · 12 monthly");
  await dialog.getByRole("button", { name: "Create policy" }).click();
  await expect(dialog).toBeHidden();

  const row = page.getByRole("table", { name: "Policies" }).getByRole("row").filter({ hasText: "E2E weekdays" });
  await expect(row).toContainText("Every Monday–Friday at 22:30");
  await expect(row).toContainText("Europe/Berlin");

  // Assign mft-pg (currently on "Nightly production") in its Backup settings tab.
  await page.goto(`/applications/${APPS.mftPg}`);
  await page.getByRole("tab", { name: "Backup settings" }).click();
  const card = page.getByTestId("policy-assignment");
  const select = card.getByRole("combobox", { name: "Protection policy" });
  await expect(select).toContainText("Nightly production");
  await select.click();
  await page.getByRole("option", { name: "E2E weekdays" }).click();
  await card.getByRole("button", { name: "Save assignment" }).click();
  await expect(page.getByText("mft-pg follows E2E weekdays")).toBeVisible();
  await card.getByRole("link", { name: "View policy E2E weekdays" }).click();
  await expect(page.getByRole("table", { name: "Assigned applications" })).toContainText("mft-pg");

  // The contract card and database strategy live in the same tab.
  await page.goto(`/applications/${APPS.shop}`);
  await page.getByRole("tab", { name: "Backup settings" }).click();
  await expect(page.getByTestId("contract-card")).toContainText("Satisfied");
  await expect(page.getByRole("combobox", { name: "Database strategy" })).toBeVisible();
});

test("delete a recovery point with the grace period, then undelete it", async ({ page }) => {
  await loginAsAdmin(page, `/recovery-points?application=${APPS.shop}&state=committed`);
  const table = page.getByRole("table", { name: "Recovery points" });
  // The seeded Partial one is already scheduled for deletion.
  await expect(table.getByRole("row").filter({ hasText: "Partial" }).first()).toHaveAttribute("data-scheduled-deletion", "true");
  await table.getByRole("row").filter({ hasText: "Complete" }).first().getByRole("link").first().click();
  await expect(page.getByTestId("verification-card")).toContainText("Verified");

  await page.getByRole("button", { name: "Delete this recovery point" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete this recovery point?" });
  await expect(dialog).toContainText("can still be restored");
  const submit = dialog.getByRole("button", { name: "Schedule deletion" });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel(/^Reason/).fill("E2E-2: duplicate test backup");
  await dialog.getByLabel(/to confirm/).fill("web shop");
  await expect(submit).toBeDisabled();
  await dialog.getByLabel(/to confirm/).fill("Web shop");
  await expect(submit).toBeEnabled();
  await submit.click();

  const banner = page.getByTestId("scheduled-deletion");
  await expect(banner).toContainText("Scheduled for deletion on");
  await expect(banner).toContainText("E2E-2: duplicate test backup");
  await expect(page.getByRole("link", { name: "Restore…" })).toBeVisible();
  await banner.getByRole("button", { name: "Undelete" }).click();
  await expect(page.getByText("Deletion cancelled")).toBeVisible();
  await expect(banner).toBeHidden();
  await expect(page.getByRole("button", { name: "Delete this recovery point" })).toBeVisible();
});

test("add a webhook channel, send test messages and read the deliveries", async ({ page }) => {
  await loginAsAdmin(page, "/notifications");
  await page.getByRole("tab", { name: "Channels" }).click();
  await expect(page).toHaveURL(/tab=channels/);
  const channels = page.getByRole("table", { name: "Notification channels" });
  await expect(channels.getByRole("row").filter({ hasText: "PagerDuty webhook" })).toContainText("503 Service Unavailable");

  await page.getByRole("button", { name: "Add channel" }).click();
  const dialog = page.getByRole("dialog", { name: "Add notification channel" });
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /^Webhook/ }).click();
  await dialog.getByLabel("Name", { exact: true }).fill("E2E hook");
  await dialog.getByLabel("Webhook URL").fill("http://hooks.example.com/dbr2");
  await dialog.getByRole("radio", { name: "Sign requests with a secret" }).check();
  await dialog.getByLabel("Secret", { exact: true }).fill("too-short");
  await dialog.getByRole("button", { name: "Add channel" }).click();
  await expect(dialog.getByRole("alert")).toContainText("https");
  await dialog.getByLabel("Webhook URL").fill("https://hooks.example.com/dbr2");
  await dialog.getByRole("button", { name: "Add channel" }).click();
  await expect(dialog.getByRole("alert")).toContainText("at least 16 characters");
  await dialog.getByLabel("Secret", { exact: true }).fill("e2e-signing-secret-0123456789");
  await dialog.getByLabel("restore.*", { exact: true }).check();
  await expect(dialog.getByTestId("events-summary")).toContainText("Delivers 6 of");
  await dialog.getByRole("button", { name: "Add channel" }).click();
  await expect(dialog).toBeHidden();

  const row = channels.getByRole("row").filter({ hasText: "E2E hook" });
  await expect(row).toContainText("Signed");
  await row.getByRole("button", { name: "Send a test message to E2E hook" }).click();
  await expect(page.getByText("Test message delivered to E2E hook")).toBeVisible();
  await expect(row.getByTestId("test-result")).toContainText(/Delivered in \d+ ms/);

  const pd = channels.getByRole("row").filter({ hasText: "PagerDuty webhook" });
  await pd.getByRole("button", { name: "Send a test message to PagerDuty webhook" }).click();
  await expect(page.getByText("Test message to PagerDuty webhook failed")).toBeVisible();
  await pd.getByRole("button", { name: "Deliveries of PagerDuty webhook" }).click();
  const drawer = page.getByRole("dialog", { name: "Deliveries: PagerDuty webhook" });
  await expect(drawer.getByRole("table", { name: "Deliveries" }).getByRole("row").filter({ hasText: "Failed" }).first()).toBeVisible();
  await drawer.getByRole("button", { name: "Close" }).first().click();

  // Editing keeps the stored secret unless asked otherwise.
  await row.getByRole("button", { name: "Edit channel E2E hook" }).click();
  const edit = page.getByRole("dialog", { name: "Edit E2E hook" });
  await expect(edit.getByRole("radio", { name: "Keep the stored secret" })).toBeChecked();
  await edit.getByRole("button", { name: "Cancel" }).click();
});

test("escrow health lists problems with fixes; an escrow drill completes with its code", async ({ page }) => {
  await loginAsAdmin(page, "/repositories");
  const health = page.getByTestId("escrow-health");
  await expect(health.locator('[data-problem="recipients_changed"]')).toContainText("nas01-backups");
  await expect(health.locator('[data-problem="drill_due"]')).toBeVisible();

  await page.getByRole("button", { name: "Start escrow drill" }).click();
  const dialog = page.getByRole("dialog", { name: "Escrow drill" });
  const created = page.waitForResponse((r) => r.url().endsWith("/api/v1/escrow/drills") && r.request().method() === "POST");
  await dialog.getByRole("button", { name: "Create drill package" }).click();
  const body = (await (await created).json()) as { package: string };
  const code = /confirmation_code ([A-Z2-7-]+)/.exec(body.package)?.[1];
  expect(code).toBeTruthy();
  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: /^Download / }).click();
  expect((await download).suggestedFilename()).toMatch(/^dbr2-escrow-drill-.*\.age$/);
  await dialog.getByRole("button", { name: "Next: enter the code" }).click();
  await dialog.getByLabel("Drill confirmation code").fill(code!.toLowerCase());
  await dialog.getByRole("button", { name: "Complete drill" }).click();
  await expect(dialog.getByText("Drill passed")).toBeVisible();
  await dialog.getByRole("button", { name: "Done" }).click();
  await expect(health.locator('[data-problem="drill_due"]')).toHaveCount(0);
});
